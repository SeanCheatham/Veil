package pool

import (
	"bytes"
	"testing"
)

func TestNew(t *testing.T) {
	p := New()
	if p == nil {
		t.Fatal("New() returned nil")
	}
	if p.Count() != 0 {
		t.Errorf("expected count 0, got %d", p.Count())
	}
}

func TestAdd(t *testing.T) {
	p := New()
	ciphertext := []byte("test message content")

	id, err := p.Add(ciphertext)
	if err != nil {
		t.Fatalf("Add() failed: %v", err)
	}
	if id == "" {
		t.Fatal("Add() returned empty ID")
	}
	if p.Count() != 1 {
		t.Errorf("expected count 1, got %d", p.Count())
	}
}

func TestAddEmpty(t *testing.T) {
	p := New()
	_, err := p.Add([]byte{})
	if err == nil {
		t.Fatal("Add() should fail for empty ciphertext")
	}
}

func TestAddIdempotent(t *testing.T) {
	p := New()
	ciphertext := []byte("test message")

	id1, _ := p.Add(ciphertext)
	id2, _ := p.Add(ciphertext)

	if id1 != id2 {
		t.Errorf("idempotent add should return same ID: %s != %s", id1, id2)
	}
	if p.Count() != 1 {
		t.Errorf("expected count 1 after duplicate add, got %d", p.Count())
	}
}

func TestGet(t *testing.T) {
	p := New()
	ciphertext := []byte("secret message")

	id, _ := p.Add(ciphertext)

	msg, integrityOK, err := p.Get(id)
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if !integrityOK {
		t.Error("integrity check should pass")
	}
	if msg.ID != id {
		t.Errorf("expected ID %s, got %s", id, msg.ID)
	}
	if !bytes.Equal(msg.Ciphertext, ciphertext) {
		t.Errorf("ciphertext mismatch")
	}
}

func TestGetNotFound(t *testing.T) {
	p := New()
	_, _, err := p.Get("nonexistent")
	if err != ErrMessageNotFound {
		t.Errorf("expected ErrMessageNotFound, got %v", err)
	}
}

func TestList(t *testing.T) {
	p := New()

	// Add messages in order
	id1, _ := p.Add([]byte("message 1"))
	id2, _ := p.Add([]byte("message 2"))
	id3, _ := p.Add([]byte("message 3"))

	list := p.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(list))
	}

	// Check insertion order
	expected := []string{id1, id2, id3}
	for i, id := range expected {
		if list[i] != id {
			t.Errorf("order mismatch at %d: expected %s, got %s", i, id, list[i])
		}
	}
}

func TestComputeHash(t *testing.T) {
	// Known SHA-256 hash of "test"
	input := []byte("test")
	expected := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

	hash := computeHash(input)
	if hash != expected {
		t.Errorf("hash mismatch: expected %s, got %s", expected, hash)
	}
}

func TestMessageCopyIsolation(t *testing.T) {
	p := New()
	ciphertext := []byte("original")
	id, _ := p.Add(ciphertext)

	msg1, _, _ := p.Get(id)
	// Modify the returned copy
	msg1.Ciphertext[0] = 'X'

	// Get again and verify original is unchanged
	msg2, integrityOK, _ := p.Get(id)
	if !integrityOK {
		t.Error("integrity should pass - modification should not affect stored message")
	}
	if msg2.Ciphertext[0] != 'o' {
		t.Error("stored message should not be modified")
	}
}

// mockEpochProvider implements EpochProvider for testing
type mockEpochProvider struct {
	epoch uint64
}

func (m *mockEpochProvider) CurrentEpoch() uint64 {
	return m.epoch
}

func TestNewWithEpoch(t *testing.T) {
	provider := &mockEpochProvider{epoch: 5}
	p := NewWithEpoch(provider)

	if p == nil {
		t.Fatal("NewWithEpoch returned nil")
	}

	id, _ := p.Add([]byte("test message"))
	msg, _, _ := p.Get(id)

	if msg.Epoch != 5 {
		t.Errorf("expected epoch 5, got %d", msg.Epoch)
	}
}

func TestSetEpochProvider(t *testing.T) {
	p := New()

	// Add without provider - epoch should be 0
	id1, _ := p.Add([]byte("message 1"))
	msg1, _, _ := p.Get(id1)
	if msg1.Epoch != 0 {
		t.Errorf("expected epoch 0 without provider, got %d", msg1.Epoch)
	}

	// Set provider
	provider := &mockEpochProvider{epoch: 10}
	p.SetEpochProvider(provider)

	// Add with provider - epoch should be 10
	id2, _ := p.Add([]byte("message 2"))
	msg2, _, _ := p.Get(id2)
	if msg2.Epoch != 10 {
		t.Errorf("expected epoch 10, got %d", msg2.Epoch)
	}
}

func TestListByEpoch(t *testing.T) {
	provider := &mockEpochProvider{epoch: 1}
	p := NewWithEpoch(provider)

	// Add messages in epoch 1
	id1, _ := p.Add([]byte("epoch 1 message 1"))
	id2, _ := p.Add([]byte("epoch 1 message 2"))

	// Change to epoch 2
	provider.epoch = 2
	id3, _ := p.Add([]byte("epoch 2 message 1"))

	// Change to epoch 3
	provider.epoch = 3
	_, _ = p.Add([]byte("epoch 3 message 1"))

	// List epoch 1 messages
	epoch1 := p.ListByEpoch(1)
	if len(epoch1) != 2 {
		t.Errorf("expected 2 messages in epoch 1, got %d", len(epoch1))
	}
	if epoch1[0] != id1 || epoch1[1] != id2 {
		t.Errorf("unexpected messages in epoch 1: %v", epoch1)
	}

	// List epoch 2 messages
	epoch2 := p.ListByEpoch(2)
	if len(epoch2) != 1 {
		t.Errorf("expected 1 message in epoch 2, got %d", len(epoch2))
	}
	if epoch2[0] != id3 {
		t.Errorf("unexpected message in epoch 2: %v", epoch2)
	}

	// List nonexistent epoch
	epoch99 := p.ListByEpoch(99)
	if len(epoch99) != 0 {
		t.Errorf("expected 0 messages in epoch 99, got %d", len(epoch99))
	}
}

func TestPoolCurrentEpoch(t *testing.T) {
	// Without provider
	p := New()
	if p.CurrentEpoch() != 0 {
		t.Errorf("expected epoch 0 without provider, got %d", p.CurrentEpoch())
	}

	// With provider
	provider := &mockEpochProvider{epoch: 42}
	p.SetEpochProvider(provider)
	if p.CurrentEpoch() != 42 {
		t.Errorf("expected epoch 42, got %d", p.CurrentEpoch())
	}
}
