// Package pool implements the Veil message pool, an append-only ciphertext store.
// The pool receives messages from the relay network after BFT consensus ordering
// and stores them with hash-based integrity verification.
package pool

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// ErrMessageNotFound is returned when a requested message ID does not exist.
var ErrMessageNotFound = errors.New("message not found")

// ErrIntegrityViolation is returned when a message's content does not match its stored hash.
var ErrIntegrityViolation = errors.New("message integrity violation: content does not match hash")

// Message represents a ciphertext message stored in the pool.
type Message struct {
	// ID is the unique identifier for the message (SHA-256 hash of ciphertext).
	ID string `json:"id"`

	// Ciphertext is the encrypted message content.
	Ciphertext []byte `json:"ciphertext"`

	// StoredHash is the SHA-256 hash computed at storage time for integrity verification.
	StoredHash string `json:"stored_hash"`

	// Timestamp is when the message was added to the pool.
	Timestamp time.Time `json:"timestamp"`
}

// Pool is an append-only store for encrypted messages.
// It provides hash-based integrity verification on every retrieval.
type Pool struct {
	mu       sync.RWMutex
	messages map[string]*Message
	order    []string // maintains insertion order for listing
}

// New creates a new empty message pool.
func New() *Pool {
	return &Pool{
		messages: make(map[string]*Message),
		order:    make([]string, 0),
	}
}

// computeHash computes the SHA-256 hash of the given data and returns it as a hex string.
func computeHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// Add stores a new message in the pool.
// The message ID is computed as the SHA-256 hash of the ciphertext.
// Returns the message ID on success.
func (p *Pool) Add(ciphertext []byte) (string, error) {
	if len(ciphertext) == 0 {
		return "", errors.New("ciphertext cannot be empty")
	}

	hash := computeHash(ciphertext)

	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if message already exists (idempotent)
	if _, exists := p.messages[hash]; exists {
		return hash, nil
	}

	msg := &Message{
		ID:         hash,
		Ciphertext: make([]byte, len(ciphertext)),
		StoredHash: hash,
		Timestamp:  time.Now().UTC(),
	}
	copy(msg.Ciphertext, ciphertext)

	p.messages[hash] = msg
	p.order = append(p.order, hash)

	return hash, nil
}

// Get retrieves a message by ID and verifies its integrity.
// Returns ErrMessageNotFound if the message does not exist.
// Returns ErrIntegrityViolation if the message's content does not match its hash.
// The integrityOK return value indicates whether the integrity check passed.
func (p *Pool) Get(id string) (*Message, bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	msg, exists := p.messages[id]
	if !exists {
		return nil, false, ErrMessageNotFound
	}

	// Verify integrity by recomputing the hash
	currentHash := computeHash(msg.Ciphertext)
	integrityOK := currentHash == msg.StoredHash

	// Return a copy to prevent external modification
	msgCopy := &Message{
		ID:         msg.ID,
		Ciphertext: make([]byte, len(msg.Ciphertext)),
		StoredHash: msg.StoredHash,
		Timestamp:  msg.Timestamp,
	}
	copy(msgCopy.Ciphertext, msg.Ciphertext)

	return msgCopy, integrityOK, nil
}

// List returns all message IDs in insertion order.
func (p *Pool) List() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]string, len(p.order))
	copy(result, p.order)
	return result
}

// Count returns the number of messages in the pool.
func (p *Pool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.messages)
}
