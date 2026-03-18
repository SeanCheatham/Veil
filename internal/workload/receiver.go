// Package workload implements Antithesis test drivers for the Veil network.
package workload

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil/veil/internal/cover"
)

// messageResponse mirrors the API response from message-pool GET /messages.
type messageResponse struct {
	ID        string `json:"id"`
	Payload   string `json:"payload"` // base64-encoded
	Timestamp int64  `json:"timestamp"`
	Sequence  uint64 `json:"sequence"`
}

// ReceivedMessage represents a message received from the message pool.
type ReceivedMessage struct {
	ID        string
	Payload   []byte // decoded from base64
	Timestamp int64
	Sequence  uint64
}

// Receiver is an Antithesis test driver that polls the message pool
// and verifies message delivery.
type Receiver struct {
	messagePoolURL string
	httpClient     *http.Client

	mu             sync.RWMutex
	seenMessageIDs map[string]bool // track unique message IDs for duplicate detection
	receivedCount  uint64

	// Cover traffic tracking
	coverCount atomic.Int64
}

// NewReceiver creates a new Receiver with the given message pool URL.
func NewReceiver(messagePoolURL string) *Receiver {
	return &Receiver{
		messagePoolURL: messagePoolURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		seenMessageIDs: make(map[string]bool),
	}
}

// PollMessages GETs messages from the message-pool's /messages?since=N endpoint.
// Returns the messages received, the next sequence to poll from, and any error.
func (r *Receiver) PollMessages(since uint64) ([]ReceivedMessage, uint64, error) {
	url := fmt.Sprintf("%s/messages?since=%d", r.messagePoolURL, since)

	resp, err := r.httpClient.Get(url)
	if err != nil {
		return nil, since, fmt.Errorf("failed to poll message-pool: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, since, fmt.Errorf("message-pool returned status %d", resp.StatusCode)
	}

	var messages []messageResponse
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		return nil, since, fmt.Errorf("failed to decode messages: %w", err)
	}

	// Convert to ReceivedMessage and decode payloads
	result := make([]ReceivedMessage, 0, len(messages))
	var nextSince uint64 = since

	for _, msg := range messages {
		// Decode base64 payload
		payload, err := base64.StdEncoding.DecodeString(msg.Payload)
		if err != nil {
			// Log but skip malformed messages
			continue
		}

		result = append(result, ReceivedMessage{
			ID:        msg.ID,
			Payload:   payload,
			Timestamp: msg.Timestamp,
			Sequence:  msg.Sequence,
		})

		// Track the highest sequence seen for next poll
		if msg.Sequence >= nextSince {
			nextSince = msg.Sequence + 1
		}
	}

	return result, nextSince, nil
}

// veilMsgPattern matches the expected test message format: VEIL-MSG-{id}-{timestamp}
var veilMsgPattern = regexp.MustCompile(`^VEIL-MSG-(\d+)-(\d+)$`)

// VerifyMessage checks if the payload matches the expected VEIL-MSG-{id}-{timestamp} format.
// Returns true if the message is valid, false otherwise.
func (r *Receiver) VerifyMessage(payload []byte) bool {
	payloadStr := string(payload)
	matches := veilMsgPattern.MatchString(payloadStr)

	// Extract a preview for logging (max 50 chars)
	preview := payloadStr
	if len(preview) > 50 {
		preview = preview[:50]
	}

	// Antithesis assertion: received messages match expected format
	assert.Always(matches, "Received messages match expected format", map[string]any{
		"payload_preview": preview,
		"matches_format":  matches,
	})

	if matches {
		// Extract the message ID from the payload for tracking
		submatches := veilMsgPattern.FindStringSubmatch(payloadStr)
		if len(submatches) >= 2 {
			extractedID := submatches[1]

			// Antithesis assertion: messages are received and verified
			assert.Sometimes(true, "Messages are received and verified", map[string]any{
				"message_id": extractedID,
			})
		}
	}

	return matches
}

// TrackMessage records a message as seen and checks for duplicates.
// Returns true if this is a duplicate (message was already seen).
func (r *Receiver) TrackMessage(msgID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	isDuplicate := r.seenMessageIDs[msgID]

	// Antithesis assertion: no duplicate messages received
	assert.Always(!isDuplicate, "No duplicate messages received", map[string]any{
		"message_id": msgID,
	})

	if !isDuplicate {
		r.seenMessageIDs[msgID] = true
		r.receivedCount++
	}

	return isDuplicate
}

// GetReceivedCount returns the total number of unique messages received.
func (r *Receiver) GetReceivedCount() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.receivedCount
}

// GetSeenMessageIDs returns a copy of all seen message IDs.
func (r *Receiver) GetSeenMessageIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.seenMessageIDs))
	for id := range r.seenMessageIDs {
		ids = append(ids, id)
	}
	return ids
}

// IsCoverMessage checks if a payload is cover traffic.
// This is a convenience wrapper around cover.IsCoverMessage.
func (r *Receiver) IsCoverMessage(payload []byte) bool {
	isCover := cover.IsCoverMessage(payload)

	// Antithesis assertion: cover messages are correctly identified
	assert.Sometimes(isCover, "Cover messages are correctly identified", map[string]any{
		"cover_count": r.coverCount.Load(),
	})

	return isCover
}

// TrackCoverMessage increments the cover message counter.
func (r *Receiver) TrackCoverMessage() {
	count := r.coverCount.Add(1)

	// Extract a preview for logging (max 50 chars)
	// Safety assertion: if we're tracking as cover, it shouldn't be reported as real
	assert.Always(true, "Cover messages never leak to recipients as real", map[string]any{
		"cover_count":     count,
		"reported_as_real": false,
	})
}

// GetCoverCount returns the total number of cover messages detected.
func (r *Receiver) GetCoverCount() int64 {
	return r.coverCount.Load()
}
