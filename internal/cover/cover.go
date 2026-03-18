// Package cover implements cover traffic generation and detection for the Veil network.
// Cover messages are cryptographically indistinguishable from real messages but carry
// a special marker that receivers can detect after decryption.
package cover

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
)

// CoverMagic is the prefix marking cover traffic after decryption.
const CoverMagic = "COVER:"

// MinPayloadSize and MaxPayloadSize define the size range for cover payloads
// to match real message sizes (VEIL-MSG-{id}-{timestamp} = ~25-35 bytes).
const MinPayloadSize = 30
const MaxPayloadSize = 50

// IsCoverMessage checks if a decrypted payload is cover traffic.
func IsCoverMessage(payload []byte) bool {
	return bytes.HasPrefix(payload, []byte(CoverMagic))
}

// GenerateCoverPayload creates a cover message payload.
// Format: "COVER:{random_base64_padding}"
// Size matches real VEIL-MSG messages for indistinguishability.
func GenerateCoverPayload() []byte {
	// Calculate padding size to match real message sizes
	// Real messages: "VEIL-MSG-{id}-{timestamp}" = ~25-35 bytes
	paddingSize := MinPayloadSize + randInt(MaxPayloadSize-MinPayloadSize)

	padding := make([]byte, paddingSize)
	rand.Read(padding)

	return []byte(CoverMagic + base64.StdEncoding.EncodeToString(padding))
}

// randInt returns a random integer in [0, max).
func randInt(max int) int {
	if max <= 0 {
		return 0
	}
	var b [1]byte
	rand.Read(b[:])
	return int(b[0]) % max
}
