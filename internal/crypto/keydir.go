package crypto

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
