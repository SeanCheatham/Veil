// Package cover tests
package cover

import (
	"strings"
	"testing"

	"github.com/veil-protocol/veil/internal/pool"
)

func TestNewCoverTrafficGenerator(t *testing.T) {
	gen := NewCoverTrafficGenerator()
	if gen == nil {
		t.Fatal("NewCoverTrafficGenerator returned nil")
	}
	if gen.counter != 0 {
		t.Errorf("expected counter to be 0, got %d", gen.counter)
	}
}

func TestGenerateCoverMessage(t *testing.T) {
	gen := NewCoverTrafficGenerator()

	msg, err := gen.GenerateCoverMessage()
	if err != nil {
		t.Fatalf("GenerateCoverMessage failed: %v", err)
	}

	// Check ID has cover prefix
	if !strings.HasPrefix(msg.ID, coverMessageIDPrefix) {
		t.Errorf("message ID should have prefix %q, got %q", coverMessageIDPrefix, msg.ID)
	}

	// Check ciphertext is not empty
	if len(msg.Ciphertext) == 0 {
		t.Error("ciphertext should not be empty")
	}

	// Check hash is not empty
	if msg.Hash == "" {
		t.Error("hash should not be empty")
	}

	// Ciphertext should be at least the minimum onion layer size
	// (pubkey 32 + nonce 12 + nextHop length 1 + tag 16 = 61 bytes minimum)
	minSize := 32 + 12 + 1 + 16
	if len(msg.Ciphertext) < minSize {
		t.Errorf("ciphertext too short: got %d, expected at least %d", len(msg.Ciphertext), minSize)
	}
}

func TestGenerateCoverMessageUniqueIDs(t *testing.T) {
	gen := NewCoverTrafficGenerator()
	ids := make(map[string]bool)

	// Generate multiple messages and verify unique IDs
	for i := 0; i < 100; i++ {
		msg, err := gen.GenerateCoverMessage()
		if err != nil {
			t.Fatalf("GenerateCoverMessage failed on iteration %d: %v", i, err)
		}
		if ids[msg.ID] {
			t.Errorf("duplicate ID generated: %s", msg.ID)
		}
		ids[msg.ID] = true
	}
}

func TestGenerateCoverMessagePayloadSize(t *testing.T) {
	gen := NewCoverTrafficGenerator()

	// Generate multiple messages and check payload sizes fall in expected range
	// Since we encrypt the payload, we can't easily check the plaintext size,
	// but we can verify the ciphertext is in a reasonable range
	for i := 0; i < 50; i++ {
		msg, err := gen.GenerateCoverMessage()
		if err != nil {
			t.Fatalf("GenerateCoverMessage failed: %v", err)
		}

		// Ciphertext should have:
		// - pubkey (32) + nonce (12) + nextHop len (1) + nextHop (0 for empty) + payload (32-64) + tag (16)
		// Total: 32 + 12 + 1 + 0 + 32-64 + 16 = 93-125 bytes
		minExpected := 32 + 12 + 1 + 0 + 32 + 16 // 93
		maxExpected := 32 + 12 + 1 + 0 + 64 + 16 // 125

		if len(msg.Ciphertext) < minExpected || len(msg.Ciphertext) > maxExpected {
			t.Errorf("ciphertext size %d outside expected range [%d, %d]",
				len(msg.Ciphertext), minExpected, maxExpected)
		}
	}
}

func TestIsCoverTraffic(t *testing.T) {
	tests := []struct {
		msgID    string
		expected bool
	}{
		{"cover-abc123-1", true},
		{"cover-", true},
		{"cover-xyz", true},
		{"msg-1", false},
		{"abc123", false},
		{"", false},
		{"cove-123", false},
		{"COVER-123", false}, // case sensitive
	}

	for _, tt := range tests {
		t.Run(tt.msgID, func(t *testing.T) {
			result := IsCoverTraffic(tt.msgID)
			if result != tt.expected {
				t.Errorf("IsCoverTraffic(%q) = %v, expected %v", tt.msgID, result, tt.expected)
			}
		})
	}
}

func TestInjectCoverTraffic(t *testing.T) {
	gen := NewCoverTrafficGenerator()

	// Create a batch of real messages
	realMessages := []pool.Message{
		{ID: "msg-1", Ciphertext: []byte("data1"), Hash: "hash1"},
		{ID: "msg-2", Ciphertext: []byte("data2"), Hash: "hash2"},
	}

	// Run injection many times to test probability
	injectedCount := 0
	iterations := 1000

	for i := 0; i < iterations; i++ {
		result := gen.InjectCoverTraffic(realMessages)

		// Should have at least the original messages
		if len(result) < len(realMessages) {
			t.Errorf("injection removed messages: got %d, expected at least %d",
				len(result), len(realMessages))
		}

		// Verify original messages are preserved at the beginning
		for j := range realMessages {
			if result[j].ID != realMessages[j].ID {
				t.Errorf("original message %d modified", j)
			}
		}

		// Count injections
		if len(result) > len(realMessages) {
			injectedCount++

			// Verify injected messages have cover prefix
			for j := len(realMessages); j < len(result); j++ {
				if !IsCoverTraffic(result[j].ID) {
					t.Errorf("injected message doesn't have cover prefix: %s", result[j].ID)
				}
			}
		}
	}

	// With 30% probability, we should see some injections
	// Allow for statistical variance: expect roughly 20-40%
	injectionRate := float64(injectedCount) / float64(iterations) * 100
	if injectionRate < 15 || injectionRate > 45 {
		t.Errorf("injection rate %0.1f%% outside expected range [15%%, 45%%]", injectionRate)
	}
}

func TestInjectCoverTrafficEmptyBatch(t *testing.T) {
	gen := NewCoverTrafficGenerator()

	// Empty batch should still work
	emptyBatch := []pool.Message{}

	// Run multiple times to ensure no crashes
	for i := 0; i < 100; i++ {
		result := gen.InjectCoverTraffic(emptyBatch)
		// Result can be empty or have cover messages only
		for _, msg := range result {
			if !IsCoverTraffic(msg.ID) {
				t.Errorf("non-cover message in empty batch result: %s", msg.ID)
			}
		}
	}
}

func TestInjectCoverTrafficMessageCount(t *testing.T) {
	gen := NewCoverTrafficGenerator()

	batch := []pool.Message{
		{ID: "msg-1", Ciphertext: []byte("data"), Hash: "hash"},
	}

	// Run many iterations and verify cover message count is 1-3 when injection happens
	for i := 0; i < 500; i++ {
		result := gen.InjectCoverTraffic(batch)
		coverCount := len(result) - len(batch)

		if coverCount > 0 && (coverCount < 1 || coverCount > 3) {
			t.Errorf("cover message count %d outside expected range [1, 3]", coverCount)
		}
	}
}

func TestRandomInt(t *testing.T) {
	tests := []struct {
		min, max int
		valid    bool
	}{
		{1, 10, true},
		{5, 5, true},   // min == max should return that value
		{10, 5, false}, // min > max is invalid
		{0, 100, true},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result, err := randomInt(tt.min, tt.max)
			if tt.valid {
				if err != nil {
					t.Errorf("randomInt(%d, %d) failed: %v", tt.min, tt.max, err)
				}
				if result < tt.min || result > tt.max {
					t.Errorf("randomInt(%d, %d) = %d, outside range", tt.min, tt.max, result)
				}
			} else {
				if err == nil {
					t.Errorf("randomInt(%d, %d) should have failed", tt.min, tt.max)
				}
			}
		})
	}
}

func TestRandomIntDistribution(t *testing.T) {
	// Test that random values are reasonably distributed
	counts := make(map[int]int)
	iterations := 10000
	min, max := 1, 5

	for i := 0; i < iterations; i++ {
		val, err := randomInt(min, max)
		if err != nil {
			t.Fatalf("randomInt failed: %v", err)
		}
		counts[val]++
	}

	// Each value should appear roughly equally (20% each for 5 values)
	// Allow for statistical variance: expect 15-25%
	for i := min; i <= max; i++ {
		pct := float64(counts[i]) / float64(iterations) * 100
		if pct < 12 || pct > 28 {
			t.Errorf("value %d appeared %0.1f%% of the time, expected ~20%%", i, pct)
		}
	}
}
