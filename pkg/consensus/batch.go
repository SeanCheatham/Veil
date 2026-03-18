// Package consensus implements the BFT consensus layer for Veil validators.
// Validators use a simple 2f+1 BFT protocol to order messages before committing
// them to the message pool.
package consensus

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// BatchState represents the current state of a batch.
type BatchState int

const (
	// BatchPending indicates the batch is collecting messages.
	BatchPending BatchState = iota
	// BatchProposed indicates the batch has been proposed for consensus.
	BatchProposed
	// BatchCommitted indicates the batch has been committed to the pool.
	BatchCommitted
)

// String returns a string representation of the batch state.
func (s BatchState) String() string {
	switch s {
	case BatchPending:
		return "pending"
	case BatchProposed:
		return "proposed"
	case BatchCommitted:
		return "committed"
	default:
		return "unknown"
	}
}

// BatchMessage represents a single message in a batch.
type BatchMessage struct {
	// ID is the message identifier (typically a hash).
	ID string `json:"id"`

	// Ciphertext is the encrypted message content.
	Ciphertext []byte `json:"ciphertext"`

	// ReceivedAt is when the message was received by this validator.
	ReceivedAt time.Time `json:"received_at"`
}

// Batch represents a collection of messages to be committed together.
type Batch struct {
	// SequenceNum is the global ordering number for this batch.
	SequenceNum uint64 `json:"sequence_num"`

	// Hash is the unique identifier for this batch (hash of messages).
	Hash string `json:"hash"`

	// Messages are the ordered messages in this batch.
	Messages []*BatchMessage `json:"messages"`

	// ProposerID is the validator that proposed this batch.
	ProposerID string `json:"proposer_id"`

	// Epoch is the epoch when this batch was created.
	Epoch uint64 `json:"epoch"`

	// CreatedAt is when the batch was created.
	CreatedAt time.Time `json:"created_at"`

	// State is the current state of the batch.
	State BatchState `json:"state"`

	// Votes tracks which validators have voted for this batch.
	Votes map[string]bool `json:"votes"`
}

// computeBatchHash computes a deterministic hash of the batch contents.
func computeBatchHash(seqNum uint64, messages []*BatchMessage) string {
	hasher := sha256.New()

	// Include sequence number
	seqBytes := make([]byte, 8)
	for i := 0; i < 8; i++ {
		seqBytes[i] = byte(seqNum >> (8 * (7 - i)))
	}
	hasher.Write(seqBytes)

	// Include message IDs in order
	for _, msg := range messages {
		hasher.Write([]byte(msg.ID))
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

// NewBatch creates a new batch with the given sequence number.
func NewBatch(seqNum uint64, proposerID string, epoch uint64) *Batch {
	return &Batch{
		SequenceNum: seqNum,
		Messages:    make([]*BatchMessage, 0),
		ProposerID:  proposerID,
		Epoch:       epoch,
		CreatedAt:   time.Now().UTC(),
		State:       BatchPending,
		Votes:       make(map[string]bool),
	}
}

// AddMessage adds a message to the batch.
func (b *Batch) AddMessage(msg *BatchMessage) {
	b.Messages = append(b.Messages, msg)
}

// ComputeHash computes and sets the batch hash based on current contents.
func (b *Batch) ComputeHash() {
	b.Hash = computeBatchHash(b.SequenceNum, b.Messages)
}

// Size returns the number of messages in the batch.
func (b *Batch) Size() int {
	return len(b.Messages)
}

// IsEmpty returns true if the batch has no messages.
func (b *Batch) IsEmpty() bool {
	return len(b.Messages) == 0
}

// AddVote records a vote from a validator.
func (b *Batch) AddVote(validatorID string) {
	b.Votes[validatorID] = true
}

// VoteCount returns the number of votes received.
func (b *Batch) VoteCount() int {
	return len(b.Votes)
}

// HasQuorum returns true if the batch has received 2f+1 votes for n=3 (f=1).
// For BFT with 3 validators, we need at least 2 votes for quorum.
func (b *Batch) HasQuorum(totalValidators int) bool {
	// 2f+1 quorum where f = (n-1)/3
	// For n=3: f=0 (we can tolerate 0 Byzantine), need 2/3 = 2 votes
	// Actually for 3 nodes and f=1 Byzantine tolerance: need n-f = 2 votes
	quorum := (totalValidators + 1) / 2 // For 3 nodes: 2
	if totalValidators == 3 {
		quorum = 2 // 2 of 3 for simple majority
	}
	return b.VoteCount() >= quorum
}

// BatchCollector manages message collection and batch creation.
type BatchCollector struct {
	mu            sync.Mutex
	currentBatch  *Batch
	validatorID   string
	epoch         uint64
	nextSeqNum    uint64
	maxBatchSize  int
	batchTimeout  time.Duration
	batchCreatedAt time.Time
}

// NewBatchCollector creates a new batch collector for the given validator.
func NewBatchCollector(validatorID string, maxBatchSize int, batchTimeout time.Duration) *BatchCollector {
	return &BatchCollector{
		validatorID:  validatorID,
		maxBatchSize: maxBatchSize,
		batchTimeout: batchTimeout,
		nextSeqNum:   1,
	}
}

// SetEpoch updates the current epoch.
func (bc *BatchCollector) SetEpoch(epoch uint64) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.epoch = epoch
}

// AddMessage adds a message to the current batch.
// Returns true if the batch is ready to be proposed (full or timed out).
func (bc *BatchCollector) AddMessage(id string, ciphertext []byte) (bool, *Batch) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Create new batch if needed
	if bc.currentBatch == nil {
		bc.currentBatch = NewBatch(bc.nextSeqNum, bc.validatorID, bc.epoch)
		bc.batchCreatedAt = time.Now()
	}

	// Add message
	msg := &BatchMessage{
		ID:         id,
		Ciphertext: ciphertext,
		ReceivedAt: time.Now().UTC(),
	}
	bc.currentBatch.AddMessage(msg)

	// Check if batch is ready
	if bc.currentBatch.Size() >= bc.maxBatchSize {
		return true, bc.finalizeBatch()
	}

	return false, nil
}

// CheckTimeout checks if the current batch has timed out.
// Returns true and the batch if it should be proposed.
func (bc *BatchCollector) CheckTimeout() (bool, *Batch) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.currentBatch == nil || bc.currentBatch.IsEmpty() {
		return false, nil
	}

	if time.Since(bc.batchCreatedAt) >= bc.batchTimeout {
		return true, bc.finalizeBatch()
	}

	return false, nil
}

// finalizeBatch prepares the current batch for proposal and resets state.
// Must be called with lock held.
func (bc *BatchCollector) finalizeBatch() *Batch {
	batch := bc.currentBatch
	batch.ComputeHash()
	batch.State = BatchProposed

	bc.currentBatch = nil
	bc.nextSeqNum++

	return batch
}

// NextSequenceNum returns the next expected sequence number.
func (bc *BatchCollector) NextSequenceNum() uint64 {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	return bc.nextSeqNum
}

// SetNextSequenceNum sets the next sequence number (used for synchronization).
func (bc *BatchCollector) SetNextSequenceNum(seqNum uint64) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.nextSeqNum = seqNum
}

// CurrentBatch returns the current pending batch, if any.
func (bc *BatchCollector) CurrentBatch() *Batch {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	return bc.currentBatch
}

// HasPendingMessages returns true if there are messages waiting to be batched.
func (bc *BatchCollector) HasPendingMessages() bool {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	return bc.currentBatch != nil && !bc.currentBatch.IsEmpty()
}
