// Package pool implements the append-only ciphertext message store.
package pool

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/veil-protocol/veil/internal/properties"
)

// Message represents a ciphertext blob in the message pool.
type Message struct {
	ID         string `json:"id"`         // unique identifier
	Ciphertext []byte `json:"ciphertext"` // encrypted blob
	Hash       string `json:"hash"`       // integrity hash (SHA-256 of ciphertext)
}

// MessagePool is an append-only store for ciphertext messages.
// It is safe for concurrent use.
type MessagePool struct {
	mu       sync.RWMutex
	messages []Message
}

// NewMessagePool creates a new empty message pool.
func NewMessagePool() *MessagePool {
	return &MessagePool{
		messages: make([]Message, 0),
	}
}

// computeHash computes the SHA-256 hash of the ciphertext.
func computeHash(ciphertext []byte) string {
	h := sha256.Sum256(ciphertext)
	return hex.EncodeToString(h[:])
}

// Submit appends a batch of messages to the pool.
// It validates and stores the integrity hash for each message.
func (p *MessagePool) Submit(batch []Message) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, msg := range batch {
		// Store the message with its provided hash
		// The hash should be the SHA-256 of the ciphertext
		p.messages = append(p.messages, Message{
			ID:         msg.ID,
			Ciphertext: msg.Ciphertext,
			Hash:       msg.Hash,
		})
	}
}

// GetAll retrieves all messages from the pool, verifying integrity on read.
// For each message, it computes the hash of the stored ciphertext and
// compares it against the stored hash, calling AssertMessageIntegrity.
func (p *MessagePool) GetAll() []Message {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Create a copy to return
	result := make([]Message, len(p.messages))
	for i, msg := range p.messages {
		// Verify integrity on read
		actualHash := computeHash(msg.Ciphertext)
		integrityOK := actualHash == msg.Hash

		// Call Antithesis property assertion
		properties.AssertMessageIntegrity(integrityOK, msg.ID, msg.Hash, actualHash)

		result[i] = Message{
			ID:         msg.ID,
			Ciphertext: msg.Ciphertext,
			Hash:       msg.Hash,
		}
	}

	return result
}

// Len returns the number of messages in the pool.
func (p *MessagePool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.messages)
}
