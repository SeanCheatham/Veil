package cover

import (
	"crypto/rand"
	"io"

	"github.com/veil-protocol/veil/pkg/crypto"
)

// GenerateCoverMessage creates a dummy onion-wrapped message that is
// structurally identical to a real message but encrypted to a throwaway
// recipient key. No one can decrypt the innermost layer, so the receiver
// silently skips it.
func GenerateCoverMessage(relayPubKeys []crypto.PublicKey, relayHosts []string) ([]byte, error) {
	// Generate a throwaway recipient keypair — immediately discard the private key.
	nullRecipient, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, err
	}

	// Random payload of 128 bytes (similar size to real JSON messages).
	payload := make([]byte, 128)
	if _, err := io.ReadFull(rand.Reader, payload); err != nil {
		return nil, err
	}

	// Wrap using the same onion encryption as real messages.
	wrapped, err := crypto.WrapMessage(payload, nullRecipient.Public, relayPubKeys, relayHosts)
	if err != nil {
		return nil, err
	}

	return wrapped, nil
}
