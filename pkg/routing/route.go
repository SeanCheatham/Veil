package routing

import (
	"crypto/rand"
	"errors"
	"math/big"

	"github.com/veil-protocol/veil/pkg/crypto"
)

// RelayInfo holds metadata about a relay node.
type RelayInfo struct {
	ID     string
	Host   string
	PubKey crypto.PublicKey
}

// SelectRoute selects a random subset of relays (between minHops and maxHops)
// and shuffles their order. Returns the selected relays in route order.
func SelectRoute(relays []RelayInfo, minHops, maxHops int) ([]RelayInfo, error) {
	if minHops < 1 {
		return nil, errors.New("minHops must be at least 1")
	}
	if maxHops < minHops {
		return nil, errors.New("maxHops must be >= minHops")
	}
	if len(relays) < minHops {
		return nil, errors.New("not enough relays for minHops")
	}

	// Clamp maxHops to available relays
	if maxHops > len(relays) {
		maxHops = len(relays)
	}

	// Pick a random hop count in [minHops, maxHops]
	rangeSize := maxHops - minHops + 1
	n, err := rand.Int(rand.Reader, big.NewInt(int64(rangeSize)))
	if err != nil {
		return nil, err
	}
	hopCount := minHops + int(n.Int64())

	// Fisher-Yates shuffle on a copy, then take first hopCount
	shuffled := make([]RelayInfo, len(relays))
	copy(shuffled, relays)

	for i := len(shuffled) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return nil, err
		}
		shuffled[i], shuffled[int(j.Int64())] = shuffled[int(j.Int64())], shuffled[i]
	}

	return shuffled[:hopCount], nil
}
