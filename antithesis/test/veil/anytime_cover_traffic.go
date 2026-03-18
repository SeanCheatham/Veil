// Command anytime_cover_traffic is an Antithesis test command that verifies
// cover traffic is being generated, sent, and correctly identified by receivers.
// It can run at any point including during fault injection as an "anytime_" prefixed command.
package main

import (
	"log"
	"os"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil/veil/internal/cover"
	"github.com/veil/veil/internal/workload"
)

func main() {
	log.Println("anytime_cover_traffic: checking cover traffic status")

	// Get message pool URL from environment
	messagePoolURL := os.Getenv("MESSAGE_POOL_URL")
	if messagePoolURL == "" {
		messagePoolURL = "http://message-pool:8082"
	}

	// Create receiver to poll messages
	receiver := workload.NewReceiver(messagePoolURL)

	// Poll all messages from the beginning
	messages, _, err := receiver.PollMessages(0)
	if err != nil {
		// Network errors are expected during fault injection
		log.Printf("anytime_cover_traffic: could not poll messages: %v", err)
		log.Println("anytime_cover_traffic: skipping check due to network error")
		os.Exit(0)
	}

	log.Printf("anytime_cover_traffic: checking %d messages for cover traffic", len(messages))

	// Count cover messages and real messages
	coverCount := 0
	realCount := 0
	var coverPayloads []string
	var realPayloads []string

	for _, msg := range messages {
		if cover.IsCoverMessage(msg.Payload) {
			coverCount++
			preview := string(msg.Payload)
			if len(preview) > 50 {
				preview = preview[:50] + "..."
			}
			coverPayloads = append(coverPayloads, preview)
		} else {
			realCount++
			preview := string(msg.Payload)
			if len(preview) > 50 {
				preview = preview[:50] + "..."
			}
			realPayloads = append(realPayloads, preview)
		}
	}

	// Antithesis assertion: cover traffic is being generated
	// This is a "sometimes" assertion - we expect to see cover traffic at some point
	hasCover := coverCount > 0
	assert.Sometimes(hasCover, "Cover traffic is being generated and received", map[string]any{
		"cover_count": coverCount,
		"real_count":  realCount,
		"total":       len(messages),
	})

	// Antithesis assertion: cover messages don't appear as verified real messages
	// Check that none of the cover messages would pass real message verification
	coverLeaked := false
	for _, msg := range messages {
		if cover.IsCoverMessage(msg.Payload) {
			// If this is cover, it should NOT verify as a real VEIL-MSG
			if receiver.VerifyMessage(msg.Payload) {
				coverLeaked = true
				log.Printf("anytime_cover_traffic: ERROR - cover message verified as real: %s",
					string(msg.Payload))
			}
		}
	}

	assert.Always(!coverLeaked, "Cover messages never leak to recipients as real", map[string]any{
		"cover_count":   coverCount,
		"cover_leaked":  coverLeaked,
	})

	// Log sample cover payloads for debugging
	if len(coverPayloads) > 0 {
		log.Printf("anytime_cover_traffic: sample cover payloads (%d total):", coverCount)
		for i, preview := range coverPayloads {
			if i >= 3 {
				log.Printf("  ... and %d more", len(coverPayloads)-3)
				break
			}
			log.Printf("  Cover: %s", preview)
		}
	}

	// Log sample real payloads for comparison
	if len(realPayloads) > 0 {
		log.Printf("anytime_cover_traffic: sample real payloads (%d total):", realCount)
		for i, preview := range realPayloads {
			if i >= 3 {
				log.Printf("  ... and %d more", len(realPayloads)-3)
				break
			}
			log.Printf("  Real: %s", preview)
		}
	}

	log.Printf("anytime_cover_traffic: completed. Total=%d, Cover=%d, Real=%d, Leaked=%t",
		len(messages), coverCount, realCount, coverLeaked)

	os.Exit(0)
}
