// Package main implements the Veil workload driver.
// This service generates test traffic and runs as part of
// the Antithesis test harness.
package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/veil-protocol/veil/pkg/client"
	"github.com/veil-protocol/veil/pkg/relay"
)

const (
	// DefaultSendInterval is the default interval between sending messages.
	DefaultSendInterval = 500 * time.Millisecond

	// DefaultPollInterval is the default interval between polling the pool.
	DefaultPollInterval = 1 * time.Second

	// DefaultCoverInterval is the default interval between cover traffic messages.
	DefaultCoverInterval = 2 * time.Second

	// DefaultKeyRefreshInterval is how often to refresh relay keys.
	DefaultKeyRefreshInterval = 30 * time.Second

	// SharedKeyFile is the path to the shared receiver public key file.
	SharedKeyFile = "/shared/receiver_pubkey"
)

func main() {
	// Parse command line flags
	mode := flag.String("mode", "", "Workload mode: sender, receiver, or both")
	sendInterval := flag.Duration("send-interval", DefaultSendInterval, "Interval between sending messages")
	pollInterval := flag.Duration("poll-interval", DefaultPollInterval, "Interval between polling the pool")
	coverInterval := flag.Duration("cover-interval", DefaultCoverInterval, "Interval between cover traffic messages")
	flag.Parse()

	// Get mode from environment if not set via flag
	if *mode == "" {
		*mode = os.Getenv("VEIL_WORKLOAD_MODE")
	}
	if *mode == "" {
		*mode = "both" // Default to both sender and receiver
	}

	// Parse relay addresses
	relayAddrsStr := os.Getenv("VEIL_RELAY_ADDRS")
	if relayAddrsStr == "" {
		relayAddrsStr = "relay-1:7000,relay-2:7000,relay-3:7000,relay-4:7000,relay-5:7000"
	}
	relayAddrs := strings.Split(relayAddrsStr, ",")
	for i := range relayAddrs {
		relayAddrs[i] = strings.TrimSpace(relayAddrs[i])
	}

	// Parse pool address
	poolAddr := os.Getenv("VEIL_POOL_ADDR")
	if poolAddr == "" {
		poolAddr = "message-pool:8080"
	}

	// Parse validator addresses (derive from relay addresses)
	validatorAddrs := []string{"validator-1:9000", "validator-2:9000", "validator-3:9000"}

	log.Printf("veil-workload: starting in mode=%s", *mode)
	log.Printf("veil-workload: relays=%v", relayAddrs)
	log.Printf("veil-workload: pool=%s", poolAddr)

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	stopCh := make(chan struct{})

	switch *mode {
	case "sender":
		go runSender(relayAddrs, validatorAddrs, poolAddr, *sendInterval, *coverInterval, stopCh)
	case "receiver":
		go runReceiver(poolAddr, *pollInterval, stopCh)
	case "both":
		go runReceiver(poolAddr, *pollInterval, stopCh)
		go runSender(relayAddrs, validatorAddrs, poolAddr, *sendInterval, *coverInterval, stopCh)
	default:
		log.Fatalf("veil-workload: invalid mode: %s (expected: sender, receiver, or both)", *mode)
	}

	<-sigCh
	close(stopCh)
	log.Println("veil-workload: shutting down")
}

// runSender runs the sender workload loop.
func runSender(relayAddrs, validatorAddrs []string, poolAddr string, sendInterval, coverInterval time.Duration, stopCh <-chan struct{}) {
	log.Println("veil-workload: initializing sender...")

	// Create sender
	sender := client.NewSender(client.SenderConfig{
		RelayAddresses: relayAddrs,
		ValidatorAddrs: validatorAddrs,
	})

	// Wait for relays to be ready and fetch keys
	var fetchErr error
	for retries := 0; retries < 30; retries++ {
		_, fetchErr = sender.FetchRelayKeys()
		if fetchErr == nil {
			break
		}
		log.Printf("veil-workload: waiting for relays... (%v)", fetchErr)
		time.Sleep(2 * time.Second)
	}
	if fetchErr != nil {
		log.Printf("veil-workload: failed to fetch relay keys after retries: %v", fetchErr)
		return
	}

	// Create cover traffic generator
	coverGen, err := client.NewCoverTrafficGenerator(client.CoverTrafficConfig{
		Sender:   sender,
		PoolAddr: poolAddr,
	})
	if err != nil {
		log.Printf("veil-workload: failed to create cover traffic generator: %v", err)
		return
	}

	// Load or generate receiver public key for encrypting messages
	receiverPubKey, err := loadOrWaitForReceiverKey()
	if err != nil {
		log.Printf("veil-workload: failed to get receiver key: %v", err)
		// Generate our own key for testing
		keyPair, _ := relay.GenerateKeyPair()
		receiverPubKey = &keyPair.PublicKey
	}

	log.Println("veil-workload: sender initialized, starting send loop...")

	// Start sending messages
	sendTicker := time.NewTicker(sendInterval)
	coverTicker := time.NewTicker(coverInterval)
	keyRefreshTicker := time.NewTicker(DefaultKeyRefreshInterval)
	defer sendTicker.Stop()
	defer coverTicker.Stop()
	defer keyRefreshTicker.Stop()

	messageNum := 0
	for {
		select {
		case <-stopCh:
			log.Printf("veil-workload: sender stopping, sent %d messages", sender.SentCount())
			return

		case <-sendTicker.C:
			messageNum++
			// Generate message payload
			payload := []byte(fmt.Sprintf("veil-message-%d-%d", messageNum, time.Now().UnixNano()))

			// Send message
			msgID, err := sender.Send(payload, receiverPubKey)
			if err != nil {
				log.Printf("veil-workload: failed to send message: %v", err)
				continue
			}

			log.Printf("veil-workload: sent message %d (id=%s)", messageNum, msgID[:16])

		case <-coverTicker.C:
			// Send cover traffic
			coverID, err := coverGen.SendCoverTraffic()
			if err != nil {
				log.Printf("veil-workload: failed to send cover traffic: %v", err)
				continue
			}
			log.Printf("veil-workload: sent cover traffic (id=%s)", coverID[:16])

		case <-keyRefreshTicker.C:
			// Refresh relay keys periodically
			if _, err := sender.FetchRelayKeys(); err != nil {
				log.Printf("veil-workload: failed to refresh relay keys: %v", err)
			}
		}
	}
}

// runReceiver runs the receiver workload loop.
func runReceiver(poolAddr string, pollInterval time.Duration, stopCh <-chan struct{}) {
	log.Println("veil-workload: initializing receiver...")

	// Create receiver
	receiver, err := client.NewReceiver(client.ReceiverConfig{
		PoolAddr: poolAddr,
	})
	if err != nil {
		log.Fatalf("veil-workload: failed to create receiver: %v", err)
	}

	// Share our public key via shared volume
	if err := shareReceiverKey(receiver.GetPublicKey()); err != nil {
		log.Printf("veil-workload: failed to share receiver key: %v", err)
	}

	log.Println("veil-workload: receiver initialized, starting poll loop...")

	// Wait a bit for the network to stabilize before polling
	time.Sleep(5 * time.Second)

	// Start polling for messages
	pollTicker := time.NewTicker(pollInterval)
	defer pollTicker.Stop()

	for {
		select {
		case <-stopCh:
			log.Printf("veil-workload: receiver stopping, received %d messages", receiver.ReceivedCount())
			return

		case <-pollTicker.C:
			newCount, decrypted, err := receiver.Poll()
			if err != nil {
				log.Printf("veil-workload: poll error: %v", err)
				continue
			}

			if newCount > 0 {
				log.Printf("veil-workload: polled %d new messages, decrypted %d", newCount, len(decrypted))
				for _, msg := range decrypted {
					log.Printf("veil-workload: received message %s (latency=%dms)", msg.ID[:16], msg.LatencyMs)
				}
			}
		}
	}
}

// shareReceiverKey writes the receiver's public key to a shared file.
func shareReceiverKey(pubKey [relay.KeySize]byte) error {
	encoded := base64.StdEncoding.EncodeToString(pubKey[:])
	return os.WriteFile(SharedKeyFile, []byte(encoded), 0644)
}

// loadOrWaitForReceiverKey loads the receiver's public key from the shared file.
// If not found, waits and retries.
func loadOrWaitForReceiverKey() (*[relay.KeySize]byte, error) {
	for retries := 0; retries < 10; retries++ {
		data, err := os.ReadFile(SharedKeyFile)
		if err == nil {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
			if err == nil && len(decoded) == relay.KeySize {
				var pubKey [relay.KeySize]byte
				copy(pubKey[:], decoded)
				log.Printf("veil-workload: loaded receiver public key from %s", SharedKeyFile)
				return &pubKey, nil
			}
		}

		log.Printf("veil-workload: waiting for receiver key (%d/10)...", retries+1)
		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("receiver key not available at %s", SharedKeyFile)
}
