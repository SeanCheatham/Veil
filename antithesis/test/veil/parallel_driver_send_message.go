// Command parallel_driver_send_message is an Antithesis test command that sends
// a batch of test messages through the relay network.
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
	log.Println("parallel_driver_send_message: starting batch send")

	// Get relay URL from environment
	relayURL := os.Getenv("RELAY_URL")
	if relayURL == "" {
		relayURL = "http://relay-node0:8080"
	}

	// Create sender
	sender := workload.NewSender(relayURL)

	// Send a batch of 10 messages
	batchSize := 10
	successCount := 0
	failCount := 0

	for i := 1; i <= batchSize; i++ {
		payload := sender.GenerateTestMessage(i)
		log.Printf("Sending message %d/%d: %s", i, batchSize, string(payload))

		err := sender.SendMessage(payload)
		if err != nil {
			log.Printf("Failed to send message %d: %v", i, err)
			failCount++
		} else {
			log.Printf("Successfully sent message %d", i)
			successCount++
		}

		// Small delay between messages
		time.Sleep(100 * time.Millisecond)
	}

	// Antithesis assertion: at least some messages should succeed
	assert.Sometimes(successCount > 0, "Batch send has some successful messages", map[string]any{
		"success_count": successCount,
		"fail_count":    failCount,
		"batch_size":    batchSize,
	})

	log.Printf("parallel_driver_send_message: completed batch. Success=%d, Failed=%d", successCount, failCount)

	// Exit successfully - even partial success is OK during fault injection
	os.Exit(0)
}
