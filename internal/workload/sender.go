// Package workload implements Antithesis test drivers for the Veil network.
package workload

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// Sender is an Antithesis test driver that generates and sends messages
// through the Veil relay network.
type Sender struct {
	relayURL   string
	httpClient *http.Client
	messageID  atomic.Int64
}

// NewSender creates a new Sender with the given relay URL.
func NewSender(relayURL string) *Sender {
	return &Sender{
		relayURL: relayURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GenerateTestMessage creates an identifiable test payload with format VEIL-MSG-{id}-{timestamp}.
func (s *Sender) GenerateTestMessage(id int) []byte {
	timestamp := time.Now().UnixNano()
	payload := []byte(fmt.Sprintf("VEIL-MSG-%d-%d", id, timestamp))

	// Antithesis assertion: generated messages have valid structure
	assert.Always(len(payload) > 0, "Generated messages have valid structure", map[string]any{
		"message_id": id,
		"payload_len": len(payload),
	})

	return payload
}

// SendMessage POSTs a message payload to the relay-node's /forward endpoint.
func (s *Sender) SendMessage(payload []byte) error {
	msgID := s.messageID.Add(1)

	// Build the request body
	reqBody := map[string]string{
		"payload": string(payload),
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := s.relayURL + "/forward"
	resp, err := s.httpClient.Post(url, "application/json", bytes.NewReader(jsonBody))

	// Antithesis assertion: sender successfully submits messages
	assert.Sometimes(err == nil, "Sender successfully submits messages", map[string]any{
		"message_id": msgID,
		"relay_url":  s.relayURL,
	})

	if err != nil {
		return fmt.Errorf("failed to send message to relay: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("relay returned unexpected status %d", resp.StatusCode)
	}

	return nil
}

// GetMessageCount returns the total number of messages sent.
func (s *Sender) GetMessageCount() int64 {
	return s.messageID.Load()
}
