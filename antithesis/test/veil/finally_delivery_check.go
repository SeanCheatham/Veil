// Command finally_delivery_check is an Antithesis test command that runs at the end
// to validate that messages were delivered to the message pool.
// It runs once at the end as a "finally_" prefixed command.
package main

import (
	"log"
	"os"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil/veil/internal/workload"
)

func main() {
	log.Println("finally_delivery_check: starting final delivery validation")

	// Get message pool URL from environment
	messagePoolURL := os.Getenv("MESSAGE_POOL_URL")
	if messagePoolURL == "" {
		messagePoolURL = "http://message-pool:8082"
	}

	// Create receiver
	receiver := workload.NewReceiver(messagePoolURL)

	// Poll all messages from the beginning
	messages, _, err := receiver.PollMessages(0)
	if err != nil {
		log.Printf("finally_delivery_check: FAILED - could not poll messages: %v", err)

		// Antithesis assertion: message pool should be readable at end
		assert.Sometimes(false, "Message pool is readable at test end", map[string]any{
			"error": err.Error(),
		})

		os.Exit(1)
	}

	log.Printf("finally_delivery_check: found %d messages in pool", len(messages))

	// Verify all messages
	validCount := 0
	invalidCount := 0
	var invalidPayloads []string

	for _, msg := range messages {
		if receiver.VerifyMessage(msg.Payload) {
			validCount++
		} else {
			invalidCount++
			// Collect previews of invalid payloads for debugging
			preview := string(msg.Payload)
			if len(preview) > 50 {
				preview = preview[:50] + "..."
			}
			invalidPayloads = append(invalidPayloads, preview)
		}
	}

	// Antithesis assertions

	// At least some messages should be in the pool at the end of the test
	assert.Sometimes(len(messages) > 0, "Messages are delivered to pool by test end", map[string]any{
		"message_count": len(messages),
	})

	// All messages in the pool should be valid format
	allValid := invalidCount == 0
	assert.Always(allValid, "All delivered messages have valid format", map[string]any{
		"valid_count":   validCount,
		"invalid_count": invalidCount,
	})

	if !allValid {
		log.Printf("finally_delivery_check: WARNING - %d messages have invalid format", invalidCount)
		for i, preview := range invalidPayloads {
			if i >= 5 {
				log.Printf("  ... and %d more", len(invalidPayloads)-5)
				break
			}
			log.Printf("  Invalid payload: %s", preview)
		}
	}

	log.Printf("finally_delivery_check: completed. Total=%d, Valid=%d, Invalid=%d",
		len(messages), validCount, invalidCount)

	// Success if we can read the pool (even if empty - faults may have prevented delivery)
	os.Exit(0)
}
