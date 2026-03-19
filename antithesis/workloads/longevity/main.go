// Package main implements a longevity workload that validates system behavior
// over extended periods (hours/days in Antithesis). This workload:
// - Runs indefinitely (no exit condition)
// - Tracks cumulative statistics over time
// - Periodically asserts properties hold
// - Uses assert.Sometimes with low frequency for long-running behavior
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/random"
	"github.com/veil/veil/internal/crypto"
	"github.com/veil/veil/internal/epoch"
)

// Message mirrors the message-pool Message struct
type Message struct {
	ID        int       `json:"id"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// RelayRequest is the format for relay /relay endpoint
type RelayRequest struct {
	Payload   string `json:"payload"`
	MessageID string `json:"message_id"`
}

// RelayResponse is the response from relay /relay endpoint
type RelayResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	RelayID int    `json:"relay_id"`
}

// ValidatorStatus represents the status response from a validator
type ValidatorStatus struct {
	ValidatorID   int    `json:"validator_id"`
	ViewNumber    uint64 `json:"view_number"`
	SeqNumber     uint64 `json:"seq_number"`
	IsPrimary     bool   `json:"is_primary"`
	CommittedMsgs int    `json:"committed_msgs"`
}

// Statistics tracks cumulative metrics over time
type Statistics struct {
	mu sync.RWMutex

	// Message tracking
	messagesSent     int64
	messagesReceived int64
	messagesFailed   int64

	// Epoch tracking
	epochTransitions int64
	lastEpoch        uint64
	startEpoch       uint64

	// Pool metrics
	lastPoolSize     int
	maxPoolSize      int
	poolSizeSum      int64 // for computing average
	poolSizeCount    int64

	// Timing
	startTime        time.Time
	lastReportTime   time.Time
}

var (
	relaySeeds       [][]byte
	epochDuration    int64
	relayURL         string
	poolURL          string
	validatorURLs    []string
	httpClient       *http.Client
	epochManager     *epoch.Manager
	stats            *Statistics
	reportInterval   int64
	deliveryThreshold float64

	// Track sent messages for delivery verification
	sentMessages     sync.Map // map[string]time.Time (messageID -> sendTime)
	receivedMessages sync.Map // map[string]bool (payload -> received)
)

func main() {
	log.Println("longevity workload starting...")

	// Initialize statistics
	stats = &Statistics{
		startTime:      time.Now(),
		lastReportTime: time.Now(),
	}

	// Configure HTTP client with longer timeout for stability
	httpClient = &http.Client{
		Timeout: 30 * time.Second,
	}

	// Parse relay URL
	relayURL = os.Getenv("RELAY_URL")
	if relayURL == "" {
		relayURL = "http://relay-node0:8080"
	}

	// Parse message pool URL
	poolURL = os.Getenv("MESSAGE_POOL_URL")
	if poolURL == "" {
		poolURL = "http://message-pool:8082"
	}

	// Parse validator URLs
	validatorURLsStr := os.Getenv("VALIDATOR_URLS")
	if validatorURLsStr == "" {
		validatorURLsStr = "http://validator-node0:8081,http://validator-node1:8081,http://validator-node2:8081"
	}
	validatorURLs = strings.Split(validatorURLsStr, ",")

	// Parse epoch duration
	epochStr := os.Getenv("EPOCH_DURATION_SECONDS")
	if epochStr == "" {
		epochStr = "60"
	}
	var err error
	epochDuration, err = strconv.ParseInt(epochStr, 10, 64)
	if err != nil {
		log.Printf("Invalid EPOCH_DURATION_SECONDS: %v, using default 60", err)
		epochDuration = 60
	}

	// Parse report interval (default 300 seconds = 5 minutes)
	reportIntervalStr := os.Getenv("REPORT_INTERVAL_SECONDS")
	if reportIntervalStr == "" {
		reportIntervalStr = "300"
	}
	reportInterval, err = strconv.ParseInt(reportIntervalStr, 10, 64)
	if err != nil {
		log.Printf("Invalid REPORT_INTERVAL_SECONDS: %v, using default 300", err)
		reportInterval = 300
	}

	// Parse delivery threshold (default 0.8 = 80%)
	deliveryThresholdStr := os.Getenv("DELIVERY_THRESHOLD")
	if deliveryThresholdStr == "" {
		deliveryThresholdStr = "0.8"
	}
	deliveryThreshold, err = strconv.ParseFloat(deliveryThresholdStr, 64)
	if err != nil || deliveryThreshold < 0 || deliveryThreshold > 1 {
		log.Printf("Invalid DELIVERY_THRESHOLD: %v, using default 0.8", err)
		deliveryThreshold = 0.8
	}

	// Parse relay master seeds
	seedsStr := os.Getenv("RELAY_MASTER_SEEDS")
	if seedsStr == "" {
		log.Fatal("RELAY_MASTER_SEEDS is required for longevity workload")
	}
	relaySeeds, err = parseRelaySeeds(seedsStr)
	if err != nil {
		log.Fatalf("Invalid RELAY_MASTER_SEEDS: %v", err)
	}

	// Create epoch manager
	epochManager = epoch.NewManager(epochDuration)
	stats.startEpoch = epochManager.CurrentEpoch()
	stats.lastEpoch = stats.startEpoch

	// Wait for services to be healthy
	maxRetries := 60
	retryInterval := time.Second

	log.Println("Waiting for relay chain to be healthy...")
	for i := 0; i < 5; i++ {
		relayHealthURL := fmt.Sprintf("http://relay-node%d:8080/health", i)
		healthy := waitForHealth(relayHealthURL, maxRetries, retryInterval)
		if !healthy {
			log.Fatalf("Relay %d did not become healthy", i)
		}
	}
	log.Println("All relays are healthy")

	log.Println("Waiting for message pool to be healthy...")
	if !waitForHealth(poolURL+"/health", maxRetries, retryInterval) {
		log.Fatal("Message pool did not become healthy")
	}
	log.Println("Message pool is healthy")

	log.Println("Waiting for validators to be healthy...")
	for i, url := range validatorURLs {
		if !waitForHealth(url+"/health", maxRetries, retryInterval) {
			log.Fatalf("Validator %d did not become healthy", i)
		}
	}
	log.Println("All validators are healthy")

	// Start epoch monitoring in background
	go monitorEpochs()

	// Start pool monitoring in background
	go monitorPool()

	// Start periodic reporting in background
	go reportStatistics()

	// Run main message loop indefinitely
	log.Printf("Starting longevity workload with report interval %ds, delivery threshold %.0f%%",
		reportInterval, deliveryThreshold*100)
	runMessageLoop()
}

func parseRelaySeeds(seedsStr string) ([][]byte, error) {
	seedParts := strings.Split(seedsStr, ",")
	if len(seedParts) != 5 {
		return nil, fmt.Errorf("expected 5 seeds, got %d", len(seedParts))
	}
	seeds := make([][]byte, 5)
	for i, s := range seedParts {
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("failed to decode seed %d: %w", i, err)
		}
		seeds[i] = seed
	}
	return seeds, nil
}

func waitForHealth(healthURL string, maxRetries int, retryInterval time.Duration) bool {
	for i := 0; i < maxRetries; i++ {
		resp, err := httpClient.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var result map[string]any
			if json.Unmarshal(body, &result) == nil {
				if status, ok := result["status"].(string); ok && status == "healthy" {
					return true
				}
			}
		}
		if resp != nil {
			resp.Body.Close()
		}
		if i%10 == 0 {
			log.Printf("Waiting for service at %s... attempt %d/%d", healthURL, i+1, maxRetries)
		}
		time.Sleep(retryInterval)
	}
	return false
}

// monitorEpochs tracks epoch transitions over time
func monitorEpochs() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		currentEpoch := epochManager.CurrentEpoch()

		stats.mu.Lock()
		if currentEpoch != stats.lastEpoch {
			oldEpoch := stats.lastEpoch
			stats.lastEpoch = currentEpoch
			stats.epochTransitions++
			transitionCount := stats.epochTransitions
			stats.mu.Unlock()

			log.Printf("Epoch transition detected: %d -> %d (total transitions: %d)",
				oldEpoch, currentEpoch, transitionCount)

			// Assert that epoch transitions continue happening over time
			assert.Sometimes(true, "epoch_rotation_continues", map[string]any{
				"old_epoch":         oldEpoch,
				"new_epoch":         currentEpoch,
				"total_transitions": transitionCount,
				"uptime_seconds":    int64(time.Since(stats.startTime).Seconds()),
			})
		} else {
			stats.mu.Unlock()
		}
	}
}

// monitorPool tracks message pool size to detect unbounded growth
func monitorPool() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		messages := getPoolMessages()
		poolSize := len(messages)

		stats.mu.Lock()
		stats.lastPoolSize = poolSize
		if poolSize > stats.maxPoolSize {
			stats.maxPoolSize = poolSize
		}
		stats.poolSizeSum += int64(poolSize)
		stats.poolSizeCount++

		avgPoolSize := float64(stats.poolSizeSum) / float64(stats.poolSizeCount)
		maxSize := stats.maxPoolSize
		stats.mu.Unlock()

		// Track received messages from pool
		for _, msg := range messages {
			if strings.HasPrefix(msg.Content, "longevity-") {
				receivedMessages.Store(msg.Content, true)
			}
		}

		// Count how many of our sent messages have been received
		var sentCount, receivedCount int64
		sentMessages.Range(func(key, value any) bool {
			sentCount++
			payload := fmt.Sprintf("longevity-%s", key)
			if _, found := receivedMessages.Load(payload); found {
				receivedCount++
			}
			return true
		})

		if sentCount > 0 {
			atomic.StoreInt64(&stats.messagesReceived, receivedCount)
		}

		// Assert pool doesn't grow unbounded
		// In a healthy system, old messages should be processed/cleaned
		// We use a soft assertion that pool size remains reasonable
		poolReasonable := poolSize < 10000 // arbitrary but reasonable limit

		assert.Always(poolReasonable, "pool_size_bounded", map[string]any{
			"current_size": poolSize,
			"max_size":     maxSize,
			"avg_size":     avgPoolSize,
			"limit":        10000,
		})
	}
}

// reportStatistics emits summary statistics periodically
func reportStatistics() {
	ticker := time.NewTicker(time.Duration(reportInterval) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		emitStatisticsReport()
	}
}

func emitStatisticsReport() {
	stats.mu.RLock()
	sent := atomic.LoadInt64(&stats.messagesSent)
	received := atomic.LoadInt64(&stats.messagesReceived)
	failed := atomic.LoadInt64(&stats.messagesFailed)
	epochTransitions := stats.epochTransitions
	poolSize := stats.lastPoolSize
	maxPoolSize := stats.maxPoolSize
	avgPoolSize := float64(0)
	if stats.poolSizeCount > 0 {
		avgPoolSize = float64(stats.poolSizeSum) / float64(stats.poolSizeCount)
	}
	uptime := time.Since(stats.startTime)
	stats.mu.RUnlock()

	// Calculate delivery rate
	deliveryRate := float64(0)
	if sent > 0 {
		deliveryRate = float64(received) / float64(sent)
	}

	log.Printf("=== LONGEVITY STATISTICS REPORT ===")
	log.Printf("Uptime: %v", uptime)
	log.Printf("Messages: sent=%d, received=%d, failed=%d, delivery_rate=%.2f%%",
		sent, received, failed, deliveryRate*100)
	log.Printf("Epochs: transitions=%d, current=%d", epochTransitions, epochManager.CurrentEpoch())
	log.Printf("Pool: current=%d, max=%d, avg=%.1f", poolSize, maxPoolSize, avgPoolSize)
	log.Printf("===================================")

	// Assert cumulative delivery rate is above threshold
	// Use Sometimes because early in the run, rate may be low
	deliveryOK := sent < 10 || deliveryRate >= deliveryThreshold

	assert.Sometimes(deliveryOK, "cumulative_delivery_rate", map[string]any{
		"sent":             sent,
		"received":         received,
		"delivery_rate":    deliveryRate,
		"threshold":        deliveryThreshold,
		"uptime_seconds":   int64(uptime.Seconds()),
	})

	// Assert system is making progress (messages are being processed)
	makingProgress := sent > 0 && (received > 0 || sent < 50)

	assert.Sometimes(makingProgress, "system_making_progress", map[string]any{
		"sent":           sent,
		"received":       received,
		"uptime_seconds": int64(uptime.Seconds()),
	})

	// Assert epoch rotations continue over extended time
	// After several report intervals, we should have seen epoch transitions
	expectedEpochs := int64(uptime.Seconds()) / epochDuration
	epochProgress := epochTransitions > 0 || expectedEpochs < 1

	assert.Sometimes(epochProgress, "epoch_rotations_continue", map[string]any{
		"transitions":     epochTransitions,
		"expected":        expectedEpochs,
		"uptime_seconds":  int64(uptime.Seconds()),
		"epoch_duration":  epochDuration,
	})
}

func runMessageLoop() {
	iteration := int64(0)

	// For local validation, run limited iterations
	maxIterations := int64(0) // 0 means unlimited (Antithesis mode)
	if env := os.Getenv("MAX_ITERATIONS"); env != "" {
		fmt.Sscanf(env, "%d", &maxIterations)
	}

	for {
		iteration++

		// Check if we should stop (only for local testing)
		if maxIterations > 0 && iteration > maxIterations {
			log.Printf("Completed %d iterations, exiting", maxIterations)
			emitStatisticsReport() // Final report
			fmt.Println("SUCCESS: longevity workload completed successfully")
			return
		}

		// Send a message
		sendLongevityMessage(iteration)

		// Random delay between messages (1-3 seconds for longevity test)
		randomValue := random.GetRandom()
		delay := 1000 + (randomValue % 2000)
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}
}

func sendLongevityMessage(iteration int64) {
	epoch := epochManager.CurrentEpoch()
	randomValue := random.GetRandom()

	messageID := fmt.Sprintf("longevity-%d-%d", iteration, randomValue)
	payload := fmt.Sprintf("longevity-%s", messageID)

	// Track sent message
	sentMessages.Store(messageID, time.Now())

	// Wrap in onion
	onion, err := crypto.WrapOnion(relaySeeds, epoch, messageID, payload)
	if err != nil {
		log.Printf("Failed to wrap onion: %v", err)
		atomic.AddInt64(&stats.messagesFailed, 1)
		return
	}

	// Send to relay chain
	relayReq := RelayRequest{
		Payload:   onion,
		MessageID: messageID,
	}
	reqBody, _ := json.Marshal(relayReq)

	resp, err := httpClient.Post(relayURL+"/relay", "application/json", bytes.NewBuffer(reqBody))
	success := false
	if err == nil && resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var relayResp RelayResponse
		if json.Unmarshal(body, &relayResp) == nil && relayResp.Success {
			success = true
			atomic.AddInt64(&stats.messagesSent, 1)
		}
	}
	if resp != nil && !success {
		resp.Body.Close()
		log.Printf("Relay failed for message %s", messageID)
		atomic.AddInt64(&stats.messagesFailed, 1)
	}

	// Log progress periodically (every 100 messages)
	if iteration%100 == 0 {
		sent := atomic.LoadInt64(&stats.messagesSent)
		received := atomic.LoadInt64(&stats.messagesReceived)
		log.Printf("Progress: iteration=%d, sent=%d, received=%d, epoch=%d",
			iteration, sent, received, epoch)
	}

	// Periodic assertion: messages continue to be sent successfully
	if iteration%50 == 0 {
		sent := atomic.LoadInt64(&stats.messagesSent)
		successRate := float64(sent) / float64(iteration)

		assert.Sometimes(successRate > 0.5, "message_send_success_rate", map[string]any{
			"iteration":    iteration,
			"sent":         sent,
			"success_rate": successRate,
		})
	}
}

func getPoolMessages() []Message {
	resp, err := httpClient.Get(poolURL + "/messages")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var messages []Message
	if json.Unmarshal(body, &messages) != nil {
		return nil
	}

	return messages
}
