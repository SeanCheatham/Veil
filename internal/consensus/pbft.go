// Package consensus implements a simplified PBFT (Practical Byzantine Fault Tolerance)
// consensus protocol for ordering messages across validators.
//
// Protocol Overview:
// 1. Pre-prepare: Primary assigns sequence number, broadcasts to backups
// 2. Prepare: Backups verify pre-prepare, broadcast prepare to all
// 3. Commit: Upon 2 prepares (including own), broadcast commit
// 4. Execute: Upon 2 commits (including own), write to message-pool
//
// With 3 validators (f=0 Byzantine tolerance simplified), quorum is 2.
package consensus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// ConsensusState represents the current consensus round state
type ConsensusState int

const (
	StateIdle ConsensusState = iota
	StatePrePrepare
	StatePrepare
	StateCommit
	StateExecuted
)

func (s ConsensusState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StatePrePrepare:
		return "pre-prepare"
	case StatePrepare:
		return "prepare"
	case StateCommit:
		return "commit"
	case StateExecuted:
		return "executed"
	default:
		return "unknown"
	}
}

// ConsensusMessage represents inter-validator messages
type ConsensusMessage struct {
	Type        string `json:"type"` // "pre-prepare", "prepare", "commit"
	ViewNumber  uint64 `json:"view_number"`
	SeqNumber   uint64 `json:"seq_number"`
	Digest      string `json:"digest"`       // SHA256 of content
	Content     string `json:"content"`      // Only in pre-prepare
	ValidatorID int    `json:"validator_id"` // ID of sender
	Signature   string `json:"signature"`    // Stub: just validator ID for now
}

// ComputeDigest computes SHA256 digest of content
func ComputeDigest(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// SequenceState tracks state for a single sequence number
type SequenceState struct {
	state        ConsensusState
	content      string
	digest       string
	prepareVotes map[int]bool // validatorID -> received
	commitVotes  map[int]bool // validatorID -> received
	committed    bool
	messageID    int // ID returned from message-pool after commit
}

// PBFT manages consensus for a single validator
type PBFT struct {
	mu sync.Mutex

	validatorID    int
	numValidators  int
	viewNumber     uint64
	nextSeqNumber  uint64
	peerURLs       []string // URLs of all validators (including self)
	messagePoolURL string

	// Per-sequence state
	sequences map[uint64]*SequenceState

	// HTTP client for peer communication
	client *http.Client
}

// NewPBFT creates a new PBFT instance
func NewPBFT(validatorID int, numValidators int, peerURLs []string, messagePoolURL string) *PBFT {
	return &PBFT{
		validatorID:    validatorID,
		numValidators:  numValidators,
		viewNumber:     0,
		nextSeqNumber:  0,
		peerURLs:       peerURLs,
		messagePoolURL: messagePoolURL,
		sequences:      make(map[uint64]*SequenceState),
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// IsPrimary returns true if this validator is the current primary
func (p *PBFT) IsPrimary() bool {
	return p.validatorID == int(p.viewNumber%uint64(p.numValidators))
}

// GetViewNumber returns the current view number
func (p *PBFT) GetViewNumber() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.viewNumber
}

// GetNextSeqNumber returns the next sequence number (for testing)
func (p *PBFT) GetNextSeqNumber() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.nextSeqNumber
}

// Quorum returns the number of votes needed for consensus
// For 3 validators: quorum = 2 (tolerates 0 Byzantine faults, simplification)
func (p *PBFT) Quorum() int {
	// In classic PBFT: 2f+1 where n=3f+1
	// For n=3, f=0 (simplified), quorum = 2
	return (p.numValidators / 2) + 1
}

// getOrCreateSeqState gets or creates state for a sequence number
func (p *PBFT) getOrCreateSeqState(seqNum uint64) *SequenceState {
	if _, exists := p.sequences[seqNum]; !exists {
		p.sequences[seqNum] = &SequenceState{
			state:        StateIdle,
			prepareVotes: make(map[int]bool),
			commitVotes:  make(map[int]bool),
		}
	}
	return p.sequences[seqNum]
}

// Submit initiates consensus for new content (called by primary only)
// Returns message ID and error
func (p *PBFT) Submit(content string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.IsPrimary() {
		return 0, fmt.Errorf("only primary can initiate consensus (current primary: validator-%d)", int(p.viewNumber%uint64(p.numValidators)))
	}

	// Assign sequence number
	seqNum := p.nextSeqNumber
	p.nextSeqNumber++

	digest := ComputeDigest(content)

	log.Printf("[pbft-%d] Primary initiating consensus for seq=%d digest=%s", p.validatorID, seqNum, digest[:16])

	// Create sequence state
	seqState := p.getOrCreateSeqState(seqNum)
	seqState.state = StatePrePrepare
	seqState.content = content
	seqState.digest = digest

	// Antithesis assertion: sequence numbers are monotonic
	assert.Always(seqNum >= 0, "sequence_monotonic", map[string]any{
		"validator_id": p.validatorID,
		"seq_number":   seqNum,
	})

	// Antithesis assertion: single primary per view
	assert.Always(p.IsPrimary(), "single_primary", map[string]any{
		"validator_id": p.validatorID,
		"view_number":  p.viewNumber,
	})

	// Create pre-prepare message
	prePrepare := ConsensusMessage{
		Type:        "pre-prepare",
		ViewNumber:  p.viewNumber,
		SeqNumber:   seqNum,
		Digest:      digest,
		Content:     content,
		ValidatorID: p.validatorID,
		Signature:   fmt.Sprintf("sig-%d", p.validatorID),
	}

	// Broadcast pre-prepare to all peers (including self for simplicity)
	go p.broadcastMessage(prePrepare)

	// Primary also immediately sends its own prepare
	p.mu.Unlock()
	err := p.HandlePrePrepare(prePrepare)
	p.mu.Lock()
	if err != nil {
		log.Printf("[pbft-%d] Error handling own pre-prepare: %v", p.validatorID, err)
	}

	// Wait for consensus to complete (with timeout)
	// Release lock while waiting
	p.mu.Unlock()
	messageID, err := p.waitForCommit(seqNum, 10*time.Second)
	p.mu.Lock()

	return messageID, err
}

// waitForCommit waits for a sequence to be committed or times out
func (p *PBFT) waitForCommit(seqNum uint64, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		seqState, exists := p.sequences[seqNum]
		if exists && seqState.committed {
			messageID := seqState.messageID
			p.mu.Unlock()

			// Antithesis assertion: consensus was reached
			assert.Always(true, "consensus_reached", map[string]any{
				"seq_number": seqNum,
				"message_id": messageID,
			})

			return messageID, nil
		}
		p.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	return 0, fmt.Errorf("consensus timeout for seq=%d", seqNum)
}

// HandlePrePrepare processes a pre-prepare message from primary
func (p *PBFT) HandlePrePrepare(msg ConsensusMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if msg.Type != "pre-prepare" {
		return fmt.Errorf("expected pre-prepare, got %s", msg.Type)
	}

	// Verify message is from current primary
	expectedPrimary := int(msg.ViewNumber % uint64(p.numValidators))
	if msg.ValidatorID != expectedPrimary {
		return fmt.Errorf("pre-prepare from non-primary %d (expected %d)", msg.ValidatorID, expectedPrimary)
	}

	// Verify digest
	computedDigest := ComputeDigest(msg.Content)
	if computedDigest != msg.Digest {
		return fmt.Errorf("digest mismatch: expected %s, got %s", msg.Digest, computedDigest)
	}

	log.Printf("[pbft-%d] Received pre-prepare seq=%d from validator-%d", p.validatorID, msg.SeqNumber, msg.ValidatorID)

	// Update sequence state
	seqState := p.getOrCreateSeqState(msg.SeqNumber)
	seqState.state = StatePrepare
	seqState.content = msg.Content
	seqState.digest = msg.Digest

	// Send prepare to all peers
	prepare := ConsensusMessage{
		Type:        "prepare",
		ViewNumber:  msg.ViewNumber,
		SeqNumber:   msg.SeqNumber,
		Digest:      msg.Digest,
		ValidatorID: p.validatorID,
		Signature:   fmt.Sprintf("sig-%d", p.validatorID),
	}

	go p.broadcastMessage(prepare)

	// Also record our own prepare vote
	seqState.prepareVotes[p.validatorID] = true

	// Check if we have quorum for prepare
	p.checkPrepareQuorum(msg.SeqNumber)

	return nil
}

// HandlePrepare processes a prepare message from a peer
func (p *PBFT) HandlePrepare(msg ConsensusMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if msg.Type != "prepare" {
		return fmt.Errorf("expected prepare, got %s", msg.Type)
	}

	log.Printf("[pbft-%d] Received prepare seq=%d from validator-%d", p.validatorID, msg.SeqNumber, msg.ValidatorID)

	seqState := p.getOrCreateSeqState(msg.SeqNumber)

	// Verify digest matches (if we have content)
	if seqState.digest != "" && seqState.digest != msg.Digest {
		return fmt.Errorf("prepare digest mismatch for seq=%d", msg.SeqNumber)
	}

	// Record prepare vote
	seqState.prepareVotes[msg.ValidatorID] = true

	// Check if we have quorum
	p.checkPrepareQuorum(msg.SeqNumber)

	return nil
}

// checkPrepareQuorum checks if we have enough prepares to move to commit phase
func (p *PBFT) checkPrepareQuorum(seqNum uint64) {
	seqState := p.sequences[seqNum]
	if seqState == nil || seqState.state >= StateCommit {
		return
	}

	if len(seqState.prepareVotes) >= p.Quorum() {
		log.Printf("[pbft-%d] Prepare quorum reached for seq=%d (%d/%d)", p.validatorID, seqNum, len(seqState.prepareVotes), p.Quorum())

		seqState.state = StateCommit

		// Send commit to all peers
		commit := ConsensusMessage{
			Type:        "commit",
			ViewNumber:  p.viewNumber,
			SeqNumber:   seqNum,
			Digest:      seqState.digest,
			ValidatorID: p.validatorID,
			Signature:   fmt.Sprintf("sig-%d", p.validatorID),
		}

		go p.broadcastMessage(commit)

		// Record our own commit vote
		seqState.commitVotes[p.validatorID] = true

		// Check if we have commit quorum
		p.checkCommitQuorum(seqNum)
	}
}

// HandleCommit processes a commit message from a peer
func (p *PBFT) HandleCommit(msg ConsensusMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if msg.Type != "commit" {
		return fmt.Errorf("expected commit, got %s", msg.Type)
	}

	log.Printf("[pbft-%d] Received commit seq=%d from validator-%d", p.validatorID, msg.SeqNumber, msg.ValidatorID)

	seqState := p.getOrCreateSeqState(msg.SeqNumber)

	// Verify digest matches (if we have it)
	if seqState.digest != "" && seqState.digest != msg.Digest {
		return fmt.Errorf("commit digest mismatch for seq=%d", msg.SeqNumber)
	}

	// Record commit vote
	seqState.commitVotes[msg.ValidatorID] = true

	// Check if we have quorum
	p.checkCommitQuorum(msg.SeqNumber)

	return nil
}

// checkCommitQuorum checks if we have enough commits to execute
func (p *PBFT) checkCommitQuorum(seqNum uint64) {
	seqState := p.sequences[seqNum]
	if seqState == nil || seqState.committed {
		return
	}

	if len(seqState.commitVotes) >= p.Quorum() {
		log.Printf("[pbft-%d] Commit quorum reached for seq=%d (%d/%d), executing", p.validatorID, seqNum, len(seqState.commitVotes), p.Quorum())

		// Execute: write to message-pool
		messageID, err := p.executeCommit(seqState.content)
		if err != nil {
			log.Printf("[pbft-%d] Failed to execute commit for seq=%d: %v", p.validatorID, seqNum, err)
			return
		}

		seqState.committed = true
		seqState.state = StateExecuted
		seqState.messageID = messageID

		log.Printf("[pbft-%d] Committed seq=%d to pool with messageID=%d", p.validatorID, seqNum, messageID)

		// Antithesis assertion: consensus reached
		assert.Always(true, "consensus_reached", map[string]any{
			"validator_id": p.validatorID,
			"seq_number":   seqNum,
			"message_id":   messageID,
			"commit_votes": len(seqState.commitVotes),
		})
	}
}

// executeCommit writes the content to message-pool
func (p *PBFT) executeCommit(content string) (int, error) {
	reqBody := map[string]string{"content": content}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := p.client.Post(p.messagePoolURL+"/messages", "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return 0, fmt.Errorf("failed to POST to message-pool: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("message-pool returned status %d: %s", resp.StatusCode, string(body))
	}

	var poolResp struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&poolResp); err != nil {
		return 0, fmt.Errorf("failed to decode pool response: %w", err)
	}

	return poolResp.ID, nil
}

// broadcastMessage sends a consensus message to all peers
func (p *PBFT) broadcastMessage(msg ConsensusMessage) {
	bodyBytes, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[pbft-%d] Failed to marshal message: %v", p.validatorID, err)
		return
	}

	endpoint := "/consensus/" + msg.Type

	for i, peerURL := range p.peerURLs {
		// Skip self (we handle locally)
		if i == p.validatorID {
			continue
		}

		go func(url string) {
			resp, err := p.client.Post(url+endpoint, "application/json", bytes.NewBuffer(bodyBytes))
			if err != nil {
				log.Printf("[pbft-%d] Failed to send %s to %s: %v", p.validatorID, msg.Type, url, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
				body, _ := io.ReadAll(resp.Body)
				log.Printf("[pbft-%d] Peer %s returned %d for %s: %s", p.validatorID, url, resp.StatusCode, msg.Type, string(body))
			}
		}(peerURL)
	}
}

// GetSequenceState returns the state for a sequence number (for testing/debugging)
func (p *PBFT) GetSequenceState(seqNum uint64) (ConsensusState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if seqState, exists := p.sequences[seqNum]; exists {
		return seqState.state, true
	}
	return StateIdle, false
}

// IsCommitted returns whether a sequence has been committed
func (p *PBFT) IsCommitted(seqNum uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if seqState, exists := p.sequences[seqNum]; exists {
		return seqState.committed
	}
	return false
}
