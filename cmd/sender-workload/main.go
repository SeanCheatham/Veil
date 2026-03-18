// Package main implements the sender-workload test driver.
// This workload generates and sends test messages through the Veil network.
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
	log.Println("sender-workload starting...")

	// Get relay URL from environment
	relayURL := os.Getenv("RELAY_URL")
	if relayURL == "" {
		relayURL = "http://relay-node0:8080"
	}

	// Get send interval from environment
	sendIntervalMS := 1000
	if intervalStr := os.Getenv("SEND_INTERVAL_MS"); intervalStr != "" {
		var err error
		sendIntervalMS, err = strconv.Atoi(intervalStr)
		if err != nil {
			log.Printf("Invalid SEND_INTERVAL_MS %q, using default 1000ms", intervalStr)
			sendIntervalMS = 1000
		}
	}

	// Initialize the sender
	sender := workload.NewSender(relayURL)

	log.Printf("Sender initialized with relayURL=%s, sendInterval=%dms", relayURL, sendIntervalMS)

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service":          "sender-workload",
		"relay_url":        relayURL,
		"send_interval_ms": sendIntervalMS,
	})

	log.Println("sender-workload ready, starting message loop")

	// Run continuous loop sending messages
	messageID := 0
	sendInterval := time.Duration(sendIntervalMS) * time.Millisecond

	for {
		messageID++
		payload := sender.GenerateTestMessage(messageID)

		log.Printf("Sending message %d: %s", messageID, string(payload))

		err := sender.SendMessage(payload)
		if err != nil {
			// Log error but continue - Antithesis injects faults
			log.Printf("Failed to send message %d: %v", messageID, err)
		} else {
			log.Printf("Successfully sent message %d", messageID)
		}

		time.Sleep(sendInterval)
	}
}
