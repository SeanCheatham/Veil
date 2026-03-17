// Package pool tests
package pool

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestComputeHash(t *testing.T) {
	ciphertext := []byte("test ciphertext data")
	expected := sha256.Sum256(ciphertext)
	expectedHex := hex.EncodeToString(expected[:])

	actual := computeHash(ciphertext)
	if actual != expectedHex {
		t.Errorf("computeHash mismatch: expected %s, got %s", expectedHex, actual)
	}
}

func TestNewMessagePool(t *testing.T) {
	pool := NewMessagePool()
	if pool == nil {
		t.Fatal("NewMessagePool returned nil")
	}
	if pool.Len() != 0 {
		t.Errorf("New pool should be empty, got %d", pool.Len())
	}
}

func TestSubmitAndGetAll(t *testing.T) {
	pool := NewMessagePool()

	// Create a message with correct hash
	ciphertext := []byte("encrypted message data")
	hash := computeHash(ciphertext)

	batch := []Message{
		{
			ID:         "msg-1",
			Ciphertext: ciphertext,
			Hash:       hash,
		},
	}

	pool.Submit(batch)

	if pool.Len() != 1 {
		t.Errorf("Pool should have 1 message, got %d", pool.Len())
	}

	messages := pool.GetAll()
	if len(messages) != 1 {
		t.Fatalf("GetAll should return 1 message, got %d", len(messages))
	}

	msg := messages[0]
	if msg.ID != "msg-1" {
		t.Errorf("Message ID mismatch: expected msg-1, got %s", msg.ID)
	}
	if string(msg.Ciphertext) != string(ciphertext) {
		t.Error("Ciphertext mismatch")
	}
	if msg.Hash != hash {
		t.Errorf("Hash mismatch: expected %s, got %s", hash, msg.Hash)
	}
}

func TestSubmitMultipleBatches(t *testing.T) {
	pool := NewMessagePool()

	// First batch
	ct1 := []byte("message 1")
	batch1 := []Message{
		{ID: "msg-1", Ciphertext: ct1, Hash: computeHash(ct1)},
	}
	pool.Submit(batch1)

	// Second batch
	ct2 := []byte("message 2")
	ct3 := []byte("message 3")
	batch2 := []Message{
		{ID: "msg-2", Ciphertext: ct2, Hash: computeHash(ct2)},
		{ID: "msg-3", Ciphertext: ct3, Hash: computeHash(ct3)},
	}
	pool.Submit(batch2)

	if pool.Len() != 3 {
		t.Errorf("Pool should have 3 messages, got %d", pool.Len())
	}

	messages := pool.GetAll()
	if len(messages) != 3 {
		t.Fatalf("GetAll should return 3 messages, got %d", len(messages))
	}
}

func TestConcurrentAccess(t *testing.T) {
	pool := NewMessagePool()
	done := make(chan bool)

	// Concurrent writes
	for i := 0; i < 10; i++ {
		go func(n int) {
			ct := []byte("concurrent message")
			batch := []Message{
				{ID: "msg", Ciphertext: ct, Hash: computeHash(ct)},
			}
			pool.Submit(batch)
			done <- true
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		go func() {
			_ = pool.GetAll()
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	if pool.Len() != 10 {
		t.Errorf("Pool should have 10 messages after concurrent submits, got %d", pool.Len())
	}
}
