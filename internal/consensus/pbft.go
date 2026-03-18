// Package consensus implements the PBFT consensus protocol for ordering messages.
package consensus

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// PBFTConsensus implements a simplified PBFT consensus protocol.
type PBFTConsensus struct {
	mu              sync.RWMutex
	validatorID     int
	validators      []string                   // peer URLs (not including self)
	validatorCount  int                        // total number of validators (including self)
	pendingMessages map[uint64]*ConsensusState // sequence -> state
	nextSequence    uint64                     // next sequence number to assign
	committedSeq    uint64                     // highest committed sequence
	messagePoolURL  string
	httpClient      *http.Client
}

// NewPBFTConsensus creates a new PBFT consensus instance.
func NewPBFTConsensus(id int, peers []string, messagePoolURL string) *PBFTConsensus {
	return &PBFTConsensus{
		validatorID:     id,
		validators:      peers,
		validatorCount:  len(peers) + 1, // peers + self
		pendingMessages: make(map[uint64]*ConsensusState),
		nextSequence:    0,
		committedSeq:    0,
		messagePoolURL:  messagePoolURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Propose initiates consensus for a new message.
// Called when a relay submits a proposal to this validator.
func (p *PBFTConsensus) Propose(payload []byte) error {
	p.mu.Lock()
	seq := p.nextSequence
	p.nextSequence++

	// Create consensus state for this message
	state := NewConsensusState(seq, payload)
	p.pendingMessages[seq] = state
	p.mu.Unlock()

	log.Printf("[Validator %d] Proposing message with sequence %d", p.validatorID, seq)

	// Antithesis assertion: proposals are initiated by validators
	assert.Sometimes(true, "Proposals are initiated by validators", map[string]any{
		"validator_id": p.validatorID,
		"sequence":     seq,
	})

	// Broadcast PREPARE to all validators (including self)
	prepareMsg := ConsensusMessage{
		Type:        "prepare",
		Sequence:    seq,
		Payload:     payload,
		ValidatorID: p.validatorID,
		Signature:   ComputeSignature("prepare", seq, payload, p.validatorID),
	}

	// Handle own prepare first
	if err := p.HandlePrepare(prepareMsg); err != nil {
		log.Printf("[Validator %d] Error handling own prepare: %v", p.validatorID, err)
	}

	// Broadcast to peers
	if err := p.broadcastToValidators(prepareMsg); err != nil {
		log.Printf("[Validator %d] Error broadcasting prepare: %v", p.validatorID, err)
		// Don't fail - peers may still send their prepares
	}

	return nil
}

// HandlePrepare processes a PREPARE message from any validator.
func (p *PBFTConsensus) HandlePrepare(msg ConsensusMessage) error {
	// Verify signature
	if !VerifySignature(msg) {
		log.Printf("[Validator %d] Invalid signature on prepare from validator %d", p.validatorID, msg.ValidatorID)
		return fmt.Errorf("invalid signature")
	}

	p.mu.Lock()

	log.Printf("[Validator %d] Received PREPARE for seq %d from validator %d", p.validatorID, msg.Sequence, msg.ValidatorID)

	// Get or create consensus state
	state, exists := p.pendingMessages[msg.Sequence]
	isNew := !exists
	if !exists {
		// Create state if we're seeing this sequence for the first time from a peer
		state = NewConsensusState(msg.Sequence, msg.Payload)
		p.pendingMessages[msg.Sequence] = state
		// Update nextSequence if needed
		if msg.Sequence >= p.nextSequence {
			p.nextSequence = msg.Sequence + 1
		}
	}

	// Record prepare vote
	state.PrepareVotes[msg.ValidatorID] = true

	// If this is a new message and we haven't voted yet, send our own prepare
	// This ensures all validators participate in the prepare phase
	shouldBroadcastOwnPrepare := isNew && !state.PrepareVotes[p.validatorID]
	if shouldBroadcastOwnPrepare {
		state.PrepareVotes[p.validatorID] = true
	}

	prepareCount := state.PrepareCount()
	quorum := Quorum(p.validatorCount)

	log.Printf("[Validator %d] Prepare count for seq %d: %d/%d", p.validatorID, msg.Sequence, prepareCount, quorum)

	// Check if we have quorum of prepares
	shouldCommit := prepareCount >= quorum && state.Phase == PhasePrepare
	if shouldCommit {
		state.Phase = PhaseCommit
	}

	// Unlock before network operations
	p.mu.Unlock()

	// Broadcast our own prepare if we just learned about this message
	if shouldBroadcastOwnPrepare {
		ownPrepare := ConsensusMessage{
			Type:        "prepare",
			Sequence:    msg.Sequence,
			Payload:     msg.Payload,
			ValidatorID: p.validatorID,
			Signature:   ComputeSignature("prepare", msg.Sequence, msg.Payload, p.validatorID),
		}
		if err := p.broadcastToValidators(ownPrepare); err != nil {
			log.Printf("[Validator %d] Error broadcasting own prepare: %v", p.validatorID, err)
		}
	}

	// If we reached quorum, broadcast COMMIT
	if shouldCommit {
		commitMsg := ConsensusMessage{
			Type:        "commit",
			Sequence:    msg.Sequence,
			Payload:     state.Payload,
			ValidatorID: p.validatorID,
			Signature:   ComputeSignature("commit", msg.Sequence, state.Payload, p.validatorID),
		}

		if err := p.HandleCommit(commitMsg); err != nil {
			log.Printf("[Validator %d] Error handling own commit: %v", p.validatorID, err)
		}
		if err := p.broadcastToValidators(commitMsg); err != nil {
			log.Printf("[Validator %d] Error broadcasting commit: %v", p.validatorID, err)
		}
	}

	return nil
}

// HandleCommit processes a COMMIT message from any validator.
func (p *PBFTConsensus) HandleCommit(msg ConsensusMessage) error {
	// Verify signature
	if !VerifySignature(msg) {
		log.Printf("[Validator %d] Invalid signature on commit from validator %d", p.validatorID, msg.ValidatorID)
		return fmt.Errorf("invalid signature")
	}

	p.mu.Lock()

	log.Printf("[Validator %d] Received COMMIT for seq %d from validator %d", p.validatorID, msg.Sequence, msg.ValidatorID)

	// Get or create consensus state
	state, exists := p.pendingMessages[msg.Sequence]
	if !exists {
		// Create state if we're seeing this sequence for the first time
		state = NewConsensusState(msg.Sequence, msg.Payload)
		p.pendingMessages[msg.Sequence] = state
		// Update nextSequence if needed
		if msg.Sequence >= p.nextSequence {
			p.nextSequence = msg.Sequence + 1
		}
	}

	// Record commit vote
	state.CommitVotes[msg.ValidatorID] = true

	commitCount := state.CommitCount()
	quorum := Quorum(p.validatorCount)

	log.Printf("[Validator %d] Commit count for seq %d: %d/%d", p.validatorID, msg.Sequence, commitCount, quorum)

	// Check if we have quorum of commits
	if commitCount >= quorum && state.Phase != PhaseCommitted {
		state.Phase = PhaseCommitted

		// Check ordering invariant - committed sequence should be in order
		expectedNext := p.committedSeq
		inOrder := msg.Sequence == expectedNext || p.committedSeq == 0

		// Antithesis assertion: messages are committed in sequence order
		assert.Always(inOrder, "Messages are committed in sequence order", map[string]any{
			"validator_id": p.validatorID,
			"expected":     expectedNext,
			"actual":       msg.Sequence,
		})

		if msg.Sequence == p.committedSeq || p.committedSeq == 0 {
			p.committedSeq = msg.Sequence + 1
		}

		log.Printf("[Validator %d] Message seq %d COMMITTED, forwarding to message-pool", p.validatorID, msg.Sequence)

		// Forward to message-pool (unlock first to avoid holding lock during HTTP call)
		payload := state.Payload
		sequence := msg.Sequence
		p.mu.Unlock()

		err := p.forwardToMessagePool(payload, sequence)

		// Antithesis assertion: consensus completes and messages reach pool
		assert.Sometimes(err == nil, "Consensus completes and messages reach pool", map[string]any{
			"validator_id": p.validatorID,
			"sequence":     sequence,
		})

		if err != nil {
			log.Printf("[Validator %d] Error forwarding to message-pool: %v", p.validatorID, err)
			return err
		}

		return nil
	}

	p.mu.Unlock()
	return nil
}

// broadcastToValidators sends a consensus message to all peer validators.
func (p *PBFTConsensus) broadcastToValidators(msg ConsensusMessage) error {
	p.mu.RLock()
	peers := make([]string, len(p.validators))
	copy(peers, p.validators)
	p.mu.RUnlock()

	var lastErr error
	for _, peerURL := range peers {
		endpoint := fmt.Sprintf("%s/consensus/%s", peerURL, msg.Type)

		jsonBody, err := json.Marshal(msg)
		if err != nil {
			log.Printf("[Validator %d] Error marshaling %s message: %v", p.validatorID, msg.Type, err)
			lastErr = err
			continue
		}

		resp, err := p.httpClient.Post(endpoint, "application/json", bytes.NewReader(jsonBody))
		if err != nil {
			log.Printf("[Validator %d] Error sending %s to %s: %v", p.validatorID, msg.Type, peerURL, err)
			lastErr = err
			continue
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Printf("[Validator %d] Peer %s returned status %d for %s", p.validatorID, peerURL, resp.StatusCode, msg.Type)
			lastErr = fmt.Errorf("peer returned status %d", resp.StatusCode)
		}
	}

	return lastErr
}

// forwardToMessagePool sends the committed message to the message-pool service.
func (p *PBFTConsensus) forwardToMessagePool(payload []byte, consensusSequence uint64) error {
	// The message-pool expects base64-encoded payload in JSON
	reqBody := map[string]any{
		"payload":            base64.StdEncoding.EncodeToString(payload),
		"consensus_sequence": consensusSequence,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := p.messagePoolURL + "/messages"
	resp, err := p.httpClient.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to forward to message-pool: %w", err)
	}
	defer resp.Body.Close()

	// Accept 201 (Created) or 200 (OK, if duplicate)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("message-pool returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetStatus returns the current consensus state for debugging.
func (p *PBFTConsensus) GetStatus() map[string]any {
	p.mu.RLock()
	defer p.mu.RUnlock()

	pendingCount := len(p.pendingMessages)
	pendingSeqs := make([]uint64, 0, pendingCount)
	for seq := range p.pendingMessages {
		pendingSeqs = append(pendingSeqs, seq)
	}

	return map[string]any{
		"validator_id":    p.validatorID,
		"validator_count": p.validatorCount,
		"next_sequence":   p.nextSequence,
		"committed_seq":   p.committedSeq,
		"pending_count":   pendingCount,
		"pending_seqs":    pendingSeqs,
	}
}
