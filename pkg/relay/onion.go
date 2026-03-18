// Package relay implements the Veil relay layer for onion-peeling and mix-and-forward operations.
// Relays ensure sender anonymity by peeling one encryption layer and forwarding to the next hop.
package relay

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"golang.org/x/crypto/nacl/box"
)

// KeySize is the size of NaCl public/private keys in bytes.
const KeySize = 32

// NonceSize is the size of NaCl nonces in bytes.
const NonceSize = 24

// OverheadSize is the size of the NaCl box overhead (authentication tag).
const OverheadSize = box.Overhead

// MaxNextHopLen is the maximum length of a next hop address.
const MaxNextHopLen = 128

// ErrInvalidOnionMessage is returned when the onion message format is invalid.
var ErrInvalidOnionMessage = errors.New("invalid onion message format")

// ErrDecryptionFailed is returned when onion layer decryption fails.
var ErrDecryptionFailed = errors.New("onion layer decryption failed")

// ErrInvalidKeyPair is returned when the key pair is invalid.
var ErrInvalidKeyPair = errors.New("invalid key pair")

// OnionLayer represents a single encryption layer in the onion message.
// Each relay peels one layer to reveal the next hop and inner payload.
type OnionLayer struct {
	// NextHop is the address of the next hop (e.g., "relay-3:7000" or "validator-1:9000").
	NextHop string

	// InnerPayload is the encrypted inner payload (next layer or final message).
	InnerPayload []byte
}

// OnionMessage represents an onion-encrypted message with a unique hop-specific ID.
type OnionMessage struct {
	// ID is the unique identifier for this message at this hop.
	// Each hop generates a new ID to maintain unlinkability.
	ID string

	// Nonce is the NaCl nonce used for this encryption layer.
	Nonce [NonceSize]byte

	// SenderPubKey is the ephemeral public key for this layer.
	SenderPubKey [KeySize]byte

	// Ciphertext is the encrypted layer containing NextHop + InnerPayload.
	Ciphertext []byte
}

// RelayKeyPair represents a relay's NaCl key pair for onion decryption.
type RelayKeyPair struct {
	PublicKey  [KeySize]byte
	PrivateKey [KeySize]byte
}

// GenerateKeyPair generates a new NaCl key pair for a relay.
func GenerateKeyPair() (*RelayKeyPair, error) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %w", err)
	}
	return &RelayKeyPair{
		PublicKey:  *pub,
		PrivateKey: *priv,
	}, nil
}

// GenerateMessageID generates a new unique message ID.
func GenerateMessageID() (string, error) {
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", fmt.Errorf("failed to generate message ID: %w", err)
	}
	return hex.EncodeToString(idBytes), nil
}

// GenerateNonce generates a new random nonce for NaCl encryption.
func GenerateNonce() ([NonceSize]byte, error) {
	var nonce [NonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nonce, fmt.Errorf("failed to generate nonce: %w", err)
	}
	return nonce, nil
}

// encodeLayer encodes a layer's next hop and inner payload for encryption.
// Format: [1 byte: nextHopLen][nextHop bytes][inner payload bytes]
func encodeLayer(nextHop string, innerPayload []byte) ([]byte, error) {
	if len(nextHop) > MaxNextHopLen {
		return nil, fmt.Errorf("next hop address too long: %d > %d", len(nextHop), MaxNextHopLen)
	}

	// Format: [nextHopLen:1][nextHop:n][innerPayload:rest]
	data := make([]byte, 1+len(nextHop)+len(innerPayload))
	data[0] = byte(len(nextHop))
	copy(data[1:1+len(nextHop)], nextHop)
	copy(data[1+len(nextHop):], innerPayload)

	return data, nil
}

// decodeLayer decodes a layer's next hop and inner payload.
func decodeLayer(data []byte) (*OnionLayer, error) {
	if len(data) < 1 {
		return nil, ErrInvalidOnionMessage
	}

	nextHopLen := int(data[0])
	if nextHopLen > MaxNextHopLen || len(data) < 1+nextHopLen {
		return nil, ErrInvalidOnionMessage
	}

	return &OnionLayer{
		NextHop:      string(data[1 : 1+nextHopLen]),
		InnerPayload: data[1+nextHopLen:],
	}, nil
}

// WrapLayer encrypts a layer using the recipient's public key.
// This creates one onion layer with a new ephemeral key pair.
func WrapLayer(nextHop string, innerPayload []byte, recipientPubKey *[KeySize]byte) (*OnionMessage, error) {
	// Generate ephemeral key pair for this layer
	ephPub, ephPriv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ephemeral key: %w", err)
	}

	// Generate nonce
	nonce, err := GenerateNonce()
	if err != nil {
		return nil, err
	}

	// Encode the layer data
	plaintext, err := encodeLayer(nextHop, innerPayload)
	if err != nil {
		return nil, err
	}

	// Encrypt with NaCl box
	ciphertext := box.Seal(nil, plaintext, &nonce, recipientPubKey, ephPriv)

	// Generate message ID
	id, err := GenerateMessageID()
	if err != nil {
		return nil, err
	}

	return &OnionMessage{
		ID:           id,
		Nonce:        nonce,
		SenderPubKey: *ephPub,
		Ciphertext:   ciphertext,
	}, nil
}

// PeelLayer decrypts one onion layer using the relay's private key.
// Returns the peeled layer data (next hop and inner payload).
// The relay generates a NEW message ID for the outbound message.
func PeelLayer(msg *OnionMessage, privateKey *[KeySize]byte) (*OnionLayer, string, error) {
	// Decrypt the layer
	plaintext, ok := box.Open(nil, msg.Ciphertext, &msg.Nonce, &msg.SenderPubKey, privateKey)
	if !ok {
		return nil, "", ErrDecryptionFailed
	}

	// Decode the layer
	layer, err := decodeLayer(plaintext)
	if err != nil {
		return nil, "", err
	}

	// Generate new message ID for the outbound message
	newID, err := GenerateMessageID()
	if err != nil {
		return nil, "", err
	}

	return layer, newID, nil
}

// CreateOnion creates a multi-layer onion message for a given path.
// The path is a list of (relay address, relay public key) pairs, with the final hop last.
// The payload is the message to deliver to the final destination.
func CreateOnion(path []PathHop, payload []byte) (*OnionMessage, error) {
	if len(path) == 0 {
		return nil, errors.New("path cannot be empty")
	}

	// Build onion from inside out (start with innermost layer)
	currentPayload := payload

	// Process path in reverse order
	for i := len(path) - 1; i >= 0; i-- {
		hop := path[i]

		// The "next hop" for this layer is the address of the NEXT relay in the path,
		// or for the final layer, it's the final destination
		var nextHop string
		if i == len(path)-1 {
			nextHop = hop.Address // Final destination
		} else {
			nextHop = path[i+1].Address
		}

		msg, err := WrapLayer(nextHop, currentPayload, &hop.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("failed to wrap layer %d: %w", i, err)
		}

		// Serialize this layer as the inner payload for the next (outer) layer
		currentPayload = SerializeOnionMessage(msg)
	}

	// The final currentPayload is the complete onion; deserialize it
	return DeserializeOnionMessage(currentPayload)
}

// PathHop represents a single hop in the onion routing path.
type PathHop struct {
	Address   string
	PublicKey [KeySize]byte
}

// SerializeOnionMessage serializes an OnionMessage to bytes.
// Format: [ID:32][Nonce:24][SenderPubKey:32][Ciphertext:rest]
func SerializeOnionMessage(msg *OnionMessage) []byte {
	idBytes, _ := hex.DecodeString(msg.ID)
	if len(idBytes) != 16 {
		idBytes = make([]byte, 16)
	}

	result := make([]byte, 16+NonceSize+KeySize+len(msg.Ciphertext))
	copy(result[0:16], idBytes)
	copy(result[16:16+NonceSize], msg.Nonce[:])
	copy(result[16+NonceSize:16+NonceSize+KeySize], msg.SenderPubKey[:])
	copy(result[16+NonceSize+KeySize:], msg.Ciphertext)

	return result
}

// DeserializeOnionMessage deserializes bytes to an OnionMessage.
func DeserializeOnionMessage(data []byte) (*OnionMessage, error) {
	minLen := 16 + NonceSize + KeySize + OverheadSize + 1 // Minimum: header + at least 1 byte encrypted
	if len(data) < minLen {
		return nil, ErrInvalidOnionMessage
	}

	msg := &OnionMessage{
		ID: hex.EncodeToString(data[0:16]),
	}
	copy(msg.Nonce[:], data[16:16+NonceSize])
	copy(msg.SenderPubKey[:], data[16+NonceSize:16+NonceSize+KeySize])
	msg.Ciphertext = make([]byte, len(data)-(16+NonceSize+KeySize))
	copy(msg.Ciphertext, data[16+NonceSize+KeySize:])

	return msg, nil
}
