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
