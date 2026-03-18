package cover

import (
	"strings"
	"testing"
)

func TestIsCoverMessage(t *testing.T) {
	tests := []struct {
		name     string
		payload  []byte
		expected bool
	}{
		{
			name:     "valid cover message",
			payload:  []byte("COVER:abc123xyz"),
			expected: true,
		},
		{
			name:     "real VEIL message",
			payload:  []byte("VEIL-MSG-1-1234567890"),
			expected: false,
		},
		{
			name:     "empty payload",
			payload:  []byte{},
			expected: false,
		},
		{
			name:     "partial prefix",
			payload:  []byte("COVE"),
			expected: false,
		},
		{
			name:     "just the magic",
			payload:  []byte("COVER:"),
			expected: true,
		},
		{
			name:     "lowercase cover",
			payload:  []byte("cover:data"),
			expected: false,
		},
		{
			name:     "similar but not cover",
			payload:  []byte("COVER-something"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCoverMessage(tt.payload)
			if result != tt.expected {
				t.Errorf("IsCoverMessage(%q) = %v, want %v", tt.payload, result, tt.expected)
			}
		})
	}
}

func TestGenerateCoverPayload(t *testing.T) {
	// Generate multiple payloads to test randomness
	payloads := make([][]byte, 10)
	for i := range payloads {
		payloads[i] = GenerateCoverPayload()
	}

	for i, payload := range payloads {
		// Check that all generated payloads are valid cover messages
		if !IsCoverMessage(payload) {
			t.Errorf("Generated payload %d is not a valid cover message: %s", i, string(payload))
		}

		// Check that the payload starts with the magic
		if !strings.HasPrefix(string(payload), CoverMagic) {
			t.Errorf("Payload %d does not start with COVER: magic: %s", i, string(payload))
		}

		// Check that there is padding after the magic
		padding := string(payload)[len(CoverMagic):]
		if len(padding) == 0 {
			t.Errorf("Payload %d has no padding after magic", i)
		}
	}

	// Check that payloads have some variance (not all identical)
	allSame := true
	for i := 1; i < len(payloads); i++ {
		if string(payloads[i]) != string(payloads[0]) {
			allSame = false
			break
		}
	}
	if allSame {
		t.Error("All generated payloads are identical, randomness may be broken")
	}
}

func TestGenerateCoverPayloadSize(t *testing.T) {
	// Generate many payloads to check size distribution
	for i := 0; i < 100; i++ {
		payload := GenerateCoverPayload()

		// The payload should be larger than just the magic prefix
		// Since we add MinPayloadSize to MaxPayloadSize bytes of random data (base64 encoded),
		// the minimum size would be: len(CoverMagic) + base64(MinPayloadSize) bytes
		// base64 encoding expands data by ~4/3
		expectedMinSize := len(CoverMagic) + ((MinPayloadSize + 2) / 3 * 4)
		expectedMaxSize := len(CoverMagic) + ((MaxPayloadSize + 2) / 3 * 4)

		if len(payload) < expectedMinSize {
			t.Errorf("Payload too small: got %d, expected at least %d", len(payload), expectedMinSize)
		}

		if len(payload) > expectedMaxSize {
			t.Errorf("Payload too large: got %d, expected at most %d", len(payload), expectedMaxSize)
		}
	}
}

func TestRandInt(t *testing.T) {
	// Test that randInt returns values in the expected range
	for i := 0; i < 100; i++ {
		val := randInt(20)
		if val < 0 || val >= 20 {
			t.Errorf("randInt(20) = %d, want value in [0, 20)", val)
		}
	}

	// Test edge case with 0
	if val := randInt(0); val != 0 {
		t.Errorf("randInt(0) = %d, want 0", val)
	}

	// Test edge case with negative (should return 0)
	if val := randInt(-5); val != 0 {
		t.Errorf("randInt(-5) = %d, want 0", val)
	}
}
