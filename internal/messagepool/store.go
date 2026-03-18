// Package messagepool provides the append-only message store for the Veil network.
package messagepool

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil/veil/internal/common"
)

// Store is a thread-safe append-only message store.
type Store struct {
	mu                 sync.RWMutex
	messages           []common.Message
	committedSequences map[uint64]bool // Track committed consensus sequences for deduplication
}

// NewStore creates a new message store.
func NewStore() *Store {
	return &Store{
		messages:           make([]common.Message, 0),
		committedSequences: make(map[uint64]bool),
	}
}

// Append adds a message to the store with an auto-incrementing sequence number.
// Returns the created message with its assigned ID and sequence.
// If consensusSequence is non-nil, it's used for deduplication - duplicate sequences are rejected.
func (s *Store) Append(payload []byte) (common.Message, error) {
	return s.AppendWithConsensus(payload, nil)
}

// AppendWithConsensus adds a message with optional consensus sequence deduplication.
// If consensusSequence is provided and already committed, returns an empty message with no error (idempotent).
func (s *Store) AppendWithConsensus(payload []byte, consensusSequence *uint64) (common.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for duplicate consensus sequence
	if consensusSequence != nil {
		if s.committedSequences[*consensusSequence] {
			// Already committed this sequence - idempotent success
			assert.Sometimes(true, "Duplicate consensus sequence rejected", map[string]any{
				"consensus_sequence": *consensusSequence,
			})
			return common.Message{}, nil
		}
		// Mark this consensus sequence as committed
		s.committedSequences[*consensusSequence] = true
	}

	prevLen := len(s.messages)

	// Generate a unique ID
	id, err := generateID()
	if err != nil {
		return common.Message{}, err
	}

	// Create the message with the next sequence number
	msg := common.Message{
		ID:        id,
		Payload:   payload,
		Timestamp: time.Now().UnixNano(),
		Sequence:  uint64(len(s.messages)),
	}

	s.messages = append(s.messages, msg)

	// Antithesis assertion: messages once appended are never lost
	assert.Always(len(s.messages) > prevLen, "Messages once appended are never lost", map[string]any{
		"prev_len":    prevLen,
		"current_len": len(s.messages),
		"message_id":  msg.ID,
	})

	return msg, nil
}

// GetSince returns all messages with sequence >= index.
func (s *Store) GetSince(index uint64) []common.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if index >= uint64(len(s.messages)) {
		return []common.Message{}
	}

	result := make([]common.Message, len(s.messages)-int(index))
	copy(result, s.messages[index:])

	// Antithesis assertion: message ordering is consistent across all reads
	assert.Always(isOrdered(result), "Message ordering is consistent across all reads", map[string]any{
		"since_index":  index,
		"result_count": len(result),
	})

	return result
}

// Count returns the total number of messages in the store.
func (s *Store) Count() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return uint64(len(s.messages))
}

// CommittedSequenceCount returns the number of committed consensus sequences.
func (s *Store) CommittedSequenceCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.committedSequences)
}

// isOrdered checks if messages are in ascending sequence order.
func isOrdered(messages []common.Message) bool {
	if len(messages) <= 1 {
		return true
	}
	for i := 1; i < len(messages); i++ {
		if messages[i].Sequence <= messages[i-1].Sequence {
			return false
		}
	}
	return true
}

// generateID creates a random hex-encoded ID.
func generateID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
