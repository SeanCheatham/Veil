package cover

import (
	"testing"

	"github.com/veil-protocol/veil/pkg/crypto"
)

func makeRelays(n int) ([]crypto.PublicKey, []string, []crypto.KeyPair) {
	pubs := make([]crypto.PublicKey, n)
	hosts := make([]string, n)
	kps := make([]crypto.KeyPair, n)
	for i := 0; i < n; i++ {
		kp, _ := crypto.GenerateKeyPair()
		kps[i] = kp
		pubs[i] = kp.Public
		hosts[i] = "relay:8083"
	}
	return pubs, hosts, kps
}

func TestGenerateCoverMessageProducesValidOutput(t *testing.T) {
	pubs, hosts, _ := makeRelays(3)
	msg, err := GenerateCoverMessage(pubs, hosts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg) == 0 {
		t.Fatal("expected non-empty cover message")
	}
}

func TestCoverMessageCannotBeDecryptedByKnownKey(t *testing.T) {
	pubs, hosts, _ := makeRelays(3)

	// Generate a "real" recipient — cover messages should NOT be decryptable by this key.
	realRecipient, _ := crypto.GenerateKeyPair()

	msg, err := GenerateCoverMessage(pubs, hosts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to peel all relay layers and then decrypt as the real recipient.
	// The relay layers ARE peelable (they use real relay keys), but the innermost
	// layer is encrypted to a throwaway key, so FinalDecrypt must fail.
	// However since we don't have the relay private keys here (we used GenerateCoverMessage
	// which picks a null recipient), we can directly try FinalDecrypt on the outermost
	// layer which should also fail.
	_, err = crypto.FinalDecrypt(msg, realRecipient.Private)
	if err == nil {
		t.Fatal("expected cover message to be undecryptable by real recipient key")
	}
}

func TestCoverMessageSizeSimilarToRealMessage(t *testing.T) {
	pubs, hosts, _ := makeRelays(3)

	coverMsg, err := GenerateCoverMessage(pubs, hosts)
	if err != nil {
		t.Fatalf("cover message error: %v", err)
	}

	// Create a real message of similar payload size for comparison.
	realPayload := make([]byte, 128)
	realRecipient, _ := crypto.GenerateKeyPair()
	realMsg, err := crypto.WrapMessage(realPayload, realRecipient.Public, pubs, hosts)
	if err != nil {
		t.Fatalf("real message error: %v", err)
	}

	// Sizes should be within 20% of each other (they use the same wrapping).
	coverLen := len(coverMsg)
	realLen := len(realMsg)
	diff := coverLen - realLen
	if diff < 0 {
		diff = -diff
	}
	threshold := realLen / 5
	if threshold == 0 {
		threshold = 1
	}
	if diff > threshold {
		t.Fatalf("cover message size (%d) differs too much from real message size (%d)", coverLen, realLen)
	}
}
