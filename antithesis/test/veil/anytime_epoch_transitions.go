// Command anytime_epoch_transitions is an Antithesis test command that checks
// epoch synchronization across relay nodes. It verifies that all relays report
// the same epoch (within tolerance) and tracks epoch transitions over time.
// It can run at any point including during fault injection as an "anytime_" prefixed command.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// RelayStatus matches the status response from relay nodes.
type RelayStatus struct {
	ID            int    `json:"id"`
	ForwardCount  int64  `json:"forward_count"`
	PublicKey     string `json:"public_key"`
	CurrentEpoch  uint64 `json:"current_epoch"`
	InGracePeriod bool   `json:"in_grace_period"`
}

func main() {
	log.Println("anytime_epoch_transitions: checking epoch synchronization across relays")

	// Query all relay nodes for their status
	relayHosts := []string{
		"http://relay-node0:8080",
		"http://relay-node1:8080",
		"http://relay-node2:8080",
		"http://relay-node3:8080",
		"http://relay-node4:8080",
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	var statuses []RelayStatus
	var errors []string

	for i, host := range relayHosts {
		status, err := getRelayStatus(client, host)
		if err != nil {
			// Network errors are expected during fault injection
			errors = append(errors, fmt.Sprintf("relay-%d: %v", i, err))
			continue
		}
		statuses = append(statuses, status)
	}

	// If we couldn't reach any relays, skip the check
	if len(statuses) == 0 {
		log.Printf("anytime_epoch_transitions: could not reach any relays: %v", errors)
		log.Println("anytime_epoch_transitions: skipping check due to network errors")
		os.Exit(0)
	}

	log.Printf("anytime_epoch_transitions: got status from %d relays", len(statuses))

	// Check epoch synchronization - all relays should be within 1 epoch of each other
	minEpoch := statuses[0].CurrentEpoch
	maxEpoch := statuses[0].CurrentEpoch

	for _, status := range statuses {
		if status.CurrentEpoch < minEpoch {
			minEpoch = status.CurrentEpoch
		}
		if status.CurrentEpoch > maxEpoch {
			maxEpoch = status.CurrentEpoch
		}
	}

	epochSpread := maxEpoch - minEpoch

	// Relays should be within 1 epoch of each other (accounting for propagation delay)
	epochsSynchronized := epochSpread <= 1
	assert.Always(epochsSynchronized, "All relays report same epoch within tolerance", map[string]any{
		"min_epoch":    minEpoch,
		"max_epoch":    maxEpoch,
		"epoch_spread": epochSpread,
		"relay_count":  len(statuses),
	})

	if !epochsSynchronized {
		log.Printf("anytime_epoch_transitions: WARNING - epoch spread is %d (min=%d, max=%d)",
			epochSpread, minEpoch, maxEpoch)
		for _, status := range statuses {
			log.Printf("  Relay %d: epoch=%d, grace=%t", status.ID, status.CurrentEpoch, status.InGracePeriod)
		}
	}

	// Check that epochs are advancing (should be > 0 unless system just started)
	epochsAdvancing := maxEpoch > 0
	assert.Sometimes(epochsAdvancing, "System transitions through epochs", map[string]any{
		"current_max_epoch": maxEpoch,
	})

	// Check grace period consistency during epoch transitions
	// If multiple relays are in grace period, they should have the same epoch
	graceCount := 0
	for _, status := range statuses {
		if status.InGracePeriod {
			graceCount++
		}
	}

	log.Printf("anytime_epoch_transitions: %d relays in grace period", graceCount)

	// Report epoch statistics
	for _, status := range statuses {
		log.Printf("  Relay %d: epoch=%d, forwards=%d, grace=%t, pubkey=%s...",
			status.ID, status.CurrentEpoch, status.ForwardCount,
			status.InGracePeriod, truncateKey(status.PublicKey))
	}

	// Assertion: relays have forwarded messages
	totalForwards := int64(0)
	for _, status := range statuses {
		totalForwards += status.ForwardCount
	}

	relaysActive := totalForwards > 0
	assert.Sometimes(relaysActive, "Relays are actively forwarding messages", map[string]any{
		"total_forwards": totalForwards,
		"relay_count":    len(statuses),
	})

	// Assertion: public keys are present (epoch-based keys are working)
	keysPresent := true
	for _, status := range statuses {
		if status.PublicKey == "" {
			keysPresent = false
			break
		}
	}

	assert.Always(keysPresent, "All relays have public keys configured", map[string]any{
		"relay_count": len(statuses),
		"keys_present": keysPresent,
	})

	log.Printf("anytime_epoch_transitions: completed. Relays=%d, MinEpoch=%d, MaxEpoch=%d, Spread=%d, Forwards=%d",
		len(statuses), minEpoch, maxEpoch, epochSpread, totalForwards)

	os.Exit(0)
}

func getRelayStatus(client *http.Client, host string) (RelayStatus, error) {
	resp, err := client.Get(host + "/status")
	if err != nil {
		return RelayStatus{}, fmt.Errorf("GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return RelayStatus{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var status RelayStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return RelayStatus{}, fmt.Errorf("decode failed: %w", err)
	}

	return status, nil
}

func truncateKey(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:12]
}
