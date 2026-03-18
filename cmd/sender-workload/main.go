// Package main implements the sender-workload test driver.
// This workload generates and sends test messages through the Veil network.
package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil/veil/internal/crypto"
	"github.com/veil/veil/internal/epoch"
	"github.com/veil/veil/internal/workload"
)

// Default cover traffic rate (messages per second)
const defaultCoverTrafficRate = 0.5

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

	// Check if epoch-based key management is enabled
	epochDurationStr := os.Getenv("EPOCH_DURATION_SECONDS")
	if epochDurationStr != "" {
		epochDuration, err := strconv.ParseInt(epochDurationStr, 10, 64)
		if err != nil {
			log.Printf("Invalid EPOCH_DURATION_SECONDS %q, using default %d", epochDurationStr, epoch.DefaultDurationSeconds)
			epochDuration = int64(epoch.DefaultDurationSeconds)
		}

		// Create epoch manager
		em := epoch.NewEpochManager(epoch.EpochConfig{
			DurationSeconds:    epochDuration,
			GracePeriodSeconds: epoch.DefaultGracePeriodSeconds,
		})

		// Get relay master seeds for key derivation
		relayMasterSeeds := crypto.GetRelayMasterSeeds()

		// Configure sender for epoch-based keys
		sender.SetEpochManager(em, relayMasterSeeds)

		log.Printf("Sender configured with epoch-based keys, epoch duration: %ds", epochDuration)
		log.Printf("Current epoch: %d", em.CurrentEpoch())
	} else {
		log.Println("Sender using static relay keys (epoch mode disabled)")
	}

	// Get cover traffic rate from environment
	coverRate := defaultCoverTrafficRate
	if coverRateStr := os.Getenv("COVER_TRAFFIC_RATE"); coverRateStr != "" {
		var err error
		coverRate, err = strconv.ParseFloat(coverRateStr, 64)
		if err != nil {
			log.Printf("Invalid COVER_TRAFFIC_RATE %q, using default %.2f", coverRateStr, defaultCoverTrafficRate)
			coverRate = defaultCoverTrafficRate
		}
	}

	log.Printf("Sender initialized with relayURL=%s, sendInterval=%dms, coverRate=%.2f msg/sec",
		relayURL, sendIntervalMS, coverRate)

	// Signal to Antithesis that setup is complete
	setupInfo := map[string]any{
		"service":            "sender-workload",
		"relay_url":          relayURL,
		"send_interval_ms":   sendIntervalMS,
		"cover_traffic_rate": coverRate,
	}
	if currentEpoch, ok := sender.GetCurrentEpoch(); ok {
		setupInfo["current_epoch"] = currentEpoch
	}
	lifecycle.SetupComplete(setupInfo)

	log.Println("sender-workload ready, starting message loop")

	// Start cover traffic generator in background
	if coverRate > 0 {
		go func() {
			interval := time.Duration(float64(time.Second) / coverRate)
			log.Printf("Cover traffic generator started with interval %v", interval)
			for {
				if err := sender.SendCoverMessage(); err != nil {
					log.Printf("Failed to send cover message: %v", err)
				} else {
					log.Printf("Sent cover message (total: %d)", sender.GetCoverMessageCount())
				}
				time.Sleep(interval)
			}
		}()
	} else {
		log.Println("Cover traffic disabled (rate=0)")
	}

	// Run continuous loop sending messages
	messageID := 0
	sendInterval := time.Duration(sendIntervalMS) * time.Millisecond

	// Track epoch transitions for logging
	var lastLoggedEpoch uint64 = 0

	for {
		messageID++
		payload := sender.GenerateTestMessage(messageID)

		// Log epoch transitions
		if currentEpoch, ok := sender.GetCurrentEpoch(); ok && currentEpoch != lastLoggedEpoch {
			log.Printf("Epoch transition: %d -> %d", lastLoggedEpoch, currentEpoch)
			lastLoggedEpoch = currentEpoch
		}

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
