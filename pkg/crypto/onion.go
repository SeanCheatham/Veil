package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/nacl/box"
)

// PublicKey is a 32-byte NaCl public key.
type PublicKey [32]byte

// PrivateKey is a 32-byte NaCl private key.
type PrivateKey [32]byte

// KeyPair holds a NaCl box keypair.
type KeyPair struct {
	Public  PublicKey
	Private PrivateKey
}

// layerPayload is the JSON structure inside each onion layer.
type layerPayload struct {
	NextHop string `json:"next_hop"`
	Data    string `json:"data"` // base64-encoded inner ciphertext
}

const nonceSize = 24

// GenerateKeyPair generates a new NaCl box keypair.
func GenerateKeyPair() (KeyPair, error) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate keypair: %w", err)
	}
	var kp KeyPair
	copy(kp.Public[:], pub[:])
	copy(kp.Private[:], priv[:])
	return kp, nil
}

// WrapMessage wraps plaintext in N+1 layers of onion encryption.
// The innermost layer is encrypted to recipientPubKey with an empty next_hop.
// Each subsequent layer is encrypted to the corresponding relay's public key and
// contains the next hop address.
//
// relayPubKeys and relayHosts must have the same length. The relays are ordered
// from first (outermost) to last (innermost, closest to recipient).
func WrapMessage(plaintext []byte, recipientPubKey PublicKey, relayPubKeys []PublicKey, relayHosts []string) ([]byte, error) {
	if len(relayPubKeys) != len(relayHosts) {
		return nil, errors.New("relayPubKeys and relayHosts must have the same length")
	}
	if len(relayPubKeys) == 0 {
		return nil, errors.New("at least one relay is required")
	}

	// Innermost layer: encrypt plaintext to recipient with empty next_hop
	inner, err := encryptLayer(plaintext, "", &recipientPubKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt innermost layer: %w", err)
	}

	// Wrap from innermost relay to outermost
	// relayPubKeys[len-1] is the last relay (closest to recipient), wraps around inner
	// relayPubKeys[0] is the first relay (outermost layer)
	current := inner
	for i := len(relayPubKeys) - 1; i >= 0; i-- {
		nextHop := ""
		if i < len(relayPubKeys)-1 {
			// This relay forwards to the next relay
			nextHop = relayHosts[i+1]
		}
		// For the last relay (i == len-1), nextHop is empty => isFinal=true,
		// meaning it forwards to a validator.

		current, err = encryptLayer(current, nextHop, &relayPubKeys[i])
		if err != nil {
			return nil, fmt.Errorf("encrypt layer %d: %w", i, err)
		}
	}

	return current, nil
}

// encryptLayer encrypts data as a single onion layer to the given public key.
// The wire format is: nonce (24 bytes) || box.Seal(jsonPayload).
func encryptLayer(data []byte, nextHop string, recipientPub *PublicKey) ([]byte, error) {
	payload := layerPayload{
		NextHop: nextHop,
		Data:    base64.StdEncoding.EncodeToString(data),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal layer payload: %w", err)
	}

	// Generate ephemeral keypair for this layer
	ephPub, ephPriv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ephemeral key: %w", err)
	}

	// Generate random nonce
	var nonce [nonceSize]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	// Encrypt: nonce || ephemeralPub || box.Seal(payload)
	recipientPubArr := (*[32]byte)(recipientPub)
	sealed := box.Seal(nil, payloadJSON, &nonce, recipientPubArr, ephPriv)

	// Wire format: nonce (24) || ephemeralPub (32) || sealed
	out := make([]byte, 0, nonceSize+32+len(sealed))
	out = append(out, nonce[:]...)
	out = append(out, ephPub[:]...)
	out = append(out, sealed...)
	return out, nil
}

// PeelLayer strips one onion layer using the relay's private key.
// Returns the inner ciphertext, the next hop address, and whether this is the
// final relay (nextHop is empty, meaning forward to a validator).
func PeelLayer(ciphertext []byte, relayPrivKey PrivateKey) (innerCiphertext []byte, nextHop string, isFinal bool, err error) {
	if len(ciphertext) < nonceSize+32+box.Overhead {
		return nil, "", false, errors.New("ciphertext too short")
	}

	var nonce [nonceSize]byte
	copy(nonce[:], ciphertext[:nonceSize])

	var ephPub [32]byte
	copy(ephPub[:], ciphertext[nonceSize:nonceSize+32])

	sealed := ciphertext[nonceSize+32:]

	privArr := (*[32]byte)(&relayPrivKey)
	plainJSON, ok := box.Open(nil, sealed, &nonce, &ephPub, privArr)
	if !ok {
		return nil, "", false, errors.New("decryption failed: wrong key or corrupted data")
	}

	var payload layerPayload
	if err := json.Unmarshal(plainJSON, &payload); err != nil {
		return nil, "", false, fmt.Errorf("unmarshal layer payload: %w", err)
	}

	inner, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil {
		return nil, "", false, fmt.Errorf("decode inner data: %w", err)
	}

	return inner, payload.NextHop, payload.NextHop == "", nil
}

// FinalDecrypt decrypts the innermost onion layer using the recipient's private key.
// This is the same operation as PeelLayer but returns only the plaintext.
func FinalDecrypt(ciphertext []byte, recipientPrivKey PrivateKey) ([]byte, error) {
	inner, _, _, err := PeelLayer(ciphertext, recipientPrivKey)
	if err != nil {
		return nil, fmt.Errorf("final decrypt: %w", err)
	}
	return inner, nil
}
