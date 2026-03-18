// Command parallel_driver_verify_messages is an Antithesis test command that polls
// the message pool and verifies messages are well-formed.
// It runs concurrently and repeatedly as a parallel driver.
package main

import (
	"log"
	"os"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil/veil/internal/workload"
)

func main() {
	log.Println("parallel_driver_verify_messages: starting message verification")

	// Get message pool URL from environment
	messagePoolURL := os.Getenv("MESSAGE_POOL_URL")
	if messagePoolURL == "" {
		messagePoolURL = "http://message-pool:8082"
	}

	// Create receiver
	receiver := workload.NewReceiver(messagePoolURL)

	// Poll for messages starting from the beginning
	var since uint64 = 0
	maxPolls := 10
	pollDelay := 200 * time.Millisecond

	totalVerified := 0
	totalFailed := 0

	for poll := 1; poll <= maxPolls; poll++ {
		messages, nextSince, err := receiver.PollMessages(since)
		if err != nil {
			log.Printf("Poll %d/%d: failed to get messages: %v", poll, maxPolls, err)
			time.Sleep(pollDelay)
			continue
		}

		if len(messages) == 0 {
			log.Printf("Poll %d/%d: no new messages (since=%d)", poll, maxPolls, since)
			time.Sleep(pollDelay)
			continue
		}

		log.Printf("Poll %d/%d: received %d messages", poll, maxPolls, len(messages))

		for _, msg := range messages {
			// Track for duplicate detection
			isDuplicate := receiver.TrackMessage(msg.ID)
			if isDuplicate {
				log.Printf("WARNING: Duplicate message: id=%s", msg.ID)
			}

			// Verify message format
			if receiver.VerifyMessage(msg.Payload) {
				totalVerified++
				log.Printf("Verified message: id=%s, seq=%d, payload=%s",
					msg.ID, msg.Sequence, string(msg.Payload))
			} else {
				totalFailed++
				log.Printf("FAILED verification: id=%s, seq=%d, payload=%s",
					msg.ID, msg.Sequence, string(msg.Payload))
			}
		}

		since = nextSince
		time.Sleep(pollDelay)
	}

	// Antithesis assertion: some messages should be verified
	assert.Sometimes(totalVerified > 0, "Receiver verifies messages from pool", map[string]any{
		"verified_count": totalVerified,
		"failed_count":   totalFailed,
		"total_received": receiver.GetReceivedCount(),
	})

	log.Printf("parallel_driver_verify_messages: completed. Verified=%d, Failed=%d, Total=%d",
		totalVerified, totalFailed, receiver.GetReceivedCount())

	os.Exit(0)
}
