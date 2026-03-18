// Package main implements the receiver-workload test driver.
// This workload polls the message pool and verifies message delivery.
package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil/veil/internal/workload"
)

func main() {
	log.Println("receiver-workload starting...")

	// Get message pool URL from environment
	messagePoolURL := os.Getenv("MESSAGE_POOL_URL")
	if messagePoolURL == "" {
		messagePoolURL = "http://message-pool:8082"
	}

	// Get poll interval from environment
	pollIntervalMS := 500
	if intervalStr := os.Getenv("POLL_INTERVAL_MS"); intervalStr != "" {
		var err error
		pollIntervalMS, err = strconv.Atoi(intervalStr)
		if err != nil {
			log.Printf("Invalid POLL_INTERVAL_MS %q, using default 500ms", intervalStr)
			pollIntervalMS = 500
		}
	}

	// Initialize the receiver
	receiver := workload.NewReceiver(messagePoolURL)

	log.Printf("Receiver initialized with messagePoolURL=%s, pollInterval=%dms", messagePoolURL, pollIntervalMS)

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service":          "receiver-workload",
		"message_pool_url": messagePoolURL,
		"poll_interval_ms": pollIntervalMS,
		"cover_count":      0,
	})

	log.Println("receiver-workload ready, starting polling loop")

	// Run continuous polling loop
	var lastSeenIndex uint64 = 0
	pollInterval := time.Duration(pollIntervalMS) * time.Millisecond

	for {
		messages, nextIndex, err := receiver.PollMessages(lastSeenIndex)
		if err != nil {
			// Log error but continue - Antithesis injects faults
			log.Printf("Failed to poll messages: %v", err)
		} else {
			// Process received messages
			for _, msg := range messages {
				log.Printf("Received message: id=%s, sequence=%d, payload=%s",
					msg.ID, msg.Sequence, string(msg.Payload))

				// Check for cover messages first - they should be discarded
				if receiver.IsCoverMessage(msg.Payload) {
					receiver.TrackCoverMessage()
					log.Printf("Discarded cover message: id=%s (total cover: %d)",
						msg.ID, receiver.GetCoverCount())
					continue
				}

				// Track the message for duplicate detection
				isDuplicate := receiver.TrackMessage(msg.ID)
				if isDuplicate {
					log.Printf("WARNING: Duplicate message detected: id=%s", msg.ID)
				}

				// Verify message format
				if receiver.VerifyMessage(msg.Payload) {
					log.Printf("Message verified: id=%s", msg.ID)
				} else {
					log.Printf("WARNING: Message failed verification: id=%s, payload=%s",
						msg.ID, string(msg.Payload))
				}
			}

			if len(messages) > 0 {
				log.Printf("Processed %d messages, total received: %d, cover discarded: %d, next index: %d",
					len(messages), receiver.GetReceivedCount(), receiver.GetCoverCount(), nextIndex)
			}

			lastSeenIndex = nextIndex
		}

		time.Sleep(pollInterval)
	}
}
