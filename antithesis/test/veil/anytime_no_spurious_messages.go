// Command anytime_no_spurious_messages is an Antithesis test command that checks
// message integrity and ensures no unexpected messages appear in the pool.
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
	log.Println("anytime_no_spurious_messages: checking message integrity")

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
		// Network errors are expected during fault injection
		log.Printf("anytime_no_spurious_messages: could not poll messages: %v", err)
		log.Println("anytime_no_spurious_messages: skipping check due to network error")
		os.Exit(0)
	}

	log.Printf("anytime_no_spurious_messages: checking %d messages", len(messages))

	spuriousCount := 0
	var spuriousPayloads []string

	coverCount := 0
	for _, msg := range messages {
		// Cover messages are valid, not spurious
		if cover.IsCoverMessage(msg.Payload) {
			coverCount++
			continue
		}

		// A message is spurious if it doesn't match our expected format
		// (i.e., it wasn't sent by our sender-workload and is not cover traffic)
		if !receiver.VerifyMessage(msg.Payload) {
			spuriousCount++
			preview := string(msg.Payload)
			if len(preview) > 50 {
				preview = preview[:50] + "..."
			}
			spuriousPayloads = append(spuriousPayloads, preview)
		}
	}

	// Antithesis assertion: no spurious (unexpected) messages in the pool
	noSpurious := spuriousCount == 0
	assert.Always(noSpurious, "No spurious messages appear in pool", map[string]any{
		"spurious_count": spuriousCount,
		"cover_count":    coverCount,
		"total_messages": len(messages),
	})

	if spuriousCount > 0 {
		log.Printf("anytime_no_spurious_messages: WARNING - found %d spurious messages", spuriousCount)
		for i, preview := range spuriousPayloads {
			if i >= 5 {
				log.Printf("  ... and %d more", len(spuriousPayloads)-5)
				break
			}
			log.Printf("  Spurious payload: %s", preview)
		}
	}

	// Check message ordering - sequences should be strictly increasing
	orderingValid := true
	for i := 1; i < len(messages); i++ {
		if messages[i].Sequence <= messages[i-1].Sequence {
			orderingValid = false
			log.Printf("anytime_no_spurious_messages: ordering violation at index %d: seq %d <= %d",
				i, messages[i].Sequence, messages[i-1].Sequence)
			break
		}
	}

	// Antithesis assertion: message ordering is maintained
	assert.Always(orderingValid, "Message ordering is strictly increasing", map[string]any{
		"ordering_valid": orderingValid,
		"message_count":  len(messages),
	})

	log.Printf("anytime_no_spurious_messages: completed. Total=%d, Cover=%d, Spurious=%d, OrderingValid=%t",
		len(messages), coverCount, spuriousCount, orderingValid)

	os.Exit(0)
}
