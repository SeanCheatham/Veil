// Command first_setup_check is an Antithesis test command that verifies
// the relay network is reachable before other tests run.
// It runs once at the start as a "first_" prefixed command.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

func main() {
	log.Println("first_setup_check: verifying relay network is reachable")

	// Get relay URL from environment
	relayURL := os.Getenv("RELAY_URL")
	if relayURL == "" {
		relayURL = "http://relay-node0:8080"
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Check relay-node0 health
	healthURL := relayURL + "/health"
	log.Printf("Checking relay health at %s", healthURL)

	var lastErr error
	maxRetries := 30
	retryDelay := time.Second

	for i := 1; i <= maxRetries; i++ {
		resp, err := client.Get(healthURL)
		if err != nil {
			lastErr = err
			log.Printf("Attempt %d/%d: relay not ready: %v", i, maxRetries, err)
			time.Sleep(retryDelay)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			log.Printf("Relay is healthy after %d attempts", i)

			// Antithesis assertion: relay network becomes reachable
			assert.Always(true, "Relay network is reachable at startup", map[string]any{
				"relay_url": relayURL,
				"attempts":  i,
			})

			os.Exit(0)
		}

		lastErr = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Printf("Attempt %d/%d: relay returned status %d", i, maxRetries, resp.StatusCode)
		time.Sleep(retryDelay)
	}

	// Failed to connect after all retries
	log.Printf("first_setup_check: FAILED - relay not reachable after %d attempts: %v", maxRetries, lastErr)

	// Antithesis assertion: this should eventually succeed
	assert.Sometimes(false, "Relay network becomes reachable", map[string]any{
		"relay_url":   relayURL,
		"max_retries": maxRetries,
		"last_error":  lastErr.Error(),
	})

	os.Exit(1)
}
