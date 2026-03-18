package crypto

import "encoding/base64"

// Pre-generated relay keys for the 5-relay network.
// These are static keys used for the initial implementation.
// In production, keys would be generated per-deployment and distributed securely.

// Relay private keys (base64-encoded, 32 bytes each)
// These should be set via RELAY_PRIVATE_KEY environment variable for each relay.
const (
	RelayPrivateKey0 = "sebD3Vkt3XmFh7LA/YkymGSSFFsoK5o4FcwCmTO0EGs="
	RelayPrivateKey1 = "2YloYa2SmfK2Th7zGmElRQgrkP4sqor8IKbw66g3H+E="
	RelayPrivateKey2 = "mQr/x27q7i1WaoguIJb7X7WGrcPyhVje8XKV84bLRUk="
	RelayPrivateKey3 = "TaNa45Kl8VyFzColaqh++hjuF+hUFbwSLM+AnTEibgo="
	RelayPrivateKey4 = "jTv+HtjcrBTAf01EIhQzrE5seoTPuxycRdc4VgoPaDg="
)

// Master seeds for epoch-based key derivation (base64-encoded, 32 bytes each).
// These are used with HKDF to derive deterministic epoch-specific keys.
// Each relay has a unique master seed. In production, these would be securely generated
// and distributed during deployment.
const (
	RelayMasterSeed0 = "KzNhvFWQe7yR8pBwXdC4TmJgUoHaLsYx1q9nI3rE6Mk="
	RelayMasterSeed1 = "VpQcGtYw2hXjNsKmL8bFrDe5oU7iA0nZ4xWvJz3C1Hg="
	RelayMasterSeed2 = "BwS9fXkMnYz1pTqR7vO2hJc4uE6gLaI8xKdNmW0sAVe="
	RelayMasterSeed3 = "DqH4mZy5oP1tVw8nKjR3bF6iA9xSgLcE7uN2aWsXkMf="
	RelayMasterSeed4 = "FzK7pYn5wT0rMeB8jC1hXgU3vI6aLdS4oQsN9xWmEqH="
)

// Relay public keys (base64-encoded, 32 bytes each)
const (
	RelayPublicKey0 = "tsIGSq6BFL56VyPsPsCoX+K1FpKoe1FEoWNo5vvFRxo="
	RelayPublicKey1 = "pAh/LB2r0gZJ1/nXeGynOx/OEz3dhOtYA3d4zXrPa0c="
	RelayPublicKey2 = "rdvQe9YGOMeKC0rFptSWBwyg1tgmj6aVd6RWXok3+TQ="
	RelayPublicKey3 = "ZHm28MHqLpt/HbYpcM599XXRtbg23l/6yMTwJpyI1CQ="
	RelayPublicKey4 = "aMhoaB6eiA1Bffosi2f/rzjhjh/27hSL7kGyA5olRWA="
)

// Relay hostnames in order (for building onion layers)
const (
	RelayHost0 = "relay-node0:8080"
	RelayHost1 = "relay-node1:8080"
	RelayHost2 = "relay-node2:8080"
	RelayHost3 = "relay-node3:8080"
	RelayHost4 = "relay-node4:8080"
)

// GetRelayPublicKeys returns the ordered list of relay public keys.
// Keys are returned in order from first relay (0) to last relay (4).
func GetRelayPublicKeys() []PublicKey {
	keys := make([]PublicKey, 5)

	// Parse each key (these are known-good values, so we ignore errors)
	keys[0], _ = PublicKeyFromBase64(RelayPublicKey0)
	keys[1], _ = PublicKeyFromBase64(RelayPublicKey1)
	keys[2], _ = PublicKeyFromBase64(RelayPublicKey2)
	keys[3], _ = PublicKeyFromBase64(RelayPublicKey3)
	keys[4], _ = PublicKeyFromBase64(RelayPublicKey4)

	return keys
}

// GetRelayHops returns the ordered list of relay hostnames.
// Hops are returned in order from first relay (0) to last relay (4).
// The last hop is empty string because relay4 forwards to the validator, not another relay.
func GetRelayHops() []string {
	return []string{
		RelayHost1, // relay0 forwards to relay1
		RelayHost2, // relay1 forwards to relay2
		RelayHost3, // relay2 forwards to relay3
		RelayHost4, // relay3 forwards to relay4
		"",         // relay4 forwards to validator (indicated by empty next hop)
	}
}

// GetRelayPrivateKeyByID returns the private key for a specific relay.
// This is used for loading keys in development/testing when RELAY_PRIVATE_KEY is not set.
func GetRelayPrivateKeyByID(id int) string {
	switch id {
	case 0:
		return RelayPrivateKey0
	case 1:
		return RelayPrivateKey1
	case 2:
		return RelayPrivateKey2
	case 3:
		return RelayPrivateKey3
	case 4:
		return RelayPrivateKey4
	default:
		return ""
	}
}

// GetRelayMasterSeedByID returns the master seed for a specific relay.
// This is used for epoch-based key derivation.
func GetRelayMasterSeedByID(id int) string {
	switch id {
	case 0:
		return RelayMasterSeed0
	case 1:
		return RelayMasterSeed1
	case 2:
		return RelayMasterSeed2
	case 3:
		return RelayMasterSeed3
	case 4:
		return RelayMasterSeed4
	default:
		return ""
	}
}

// GetRelayMasterSeeds returns all relay master seeds as byte slices.
// Seeds are returned in order from relay 0 to relay 4.
func GetRelayMasterSeeds() [][]byte {
	seeds := make([][]byte, 5)
	seedStrings := []string{
		RelayMasterSeed0,
		RelayMasterSeed1,
		RelayMasterSeed2,
		RelayMasterSeed3,
		RelayMasterSeed4,
	}

	for i, s := range seedStrings {
		seed, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			// Static seeds should always be valid
			panic("invalid relay master seed: " + err.Error())
		}
		seeds[i] = seed
	}

	return seeds
}
