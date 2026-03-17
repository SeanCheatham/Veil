// Package epoch implements the epoch clock and key rotation logic.
package epoch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// EpochClient subscribes to epoch events from an epoch-clock service via SSE.
type EpochClient struct {
	url        string
	httpClient *http.Client
	cancel     context.CancelFunc
}

// NewEpochClient creates a new client that subscribes to the epoch-clock service
// at the given URL (e.g., "http://epoch-clock:8083").
func NewEpochClient(epochClockURL string) *EpochClient {
	return &EpochClient{
		url: strings.TrimSuffix(epochClockURL, "/"),
		httpClient: &http.Client{
			Timeout: 0, // No timeout for SSE stream
		},
	}
}

// CurrentEpochResponse is the response from GET /epoch.
type CurrentEpochResponse struct {
	Epoch     uint64    `json:"epoch"`
	Timestamp time.Time `json:"timestamp"`
}

// GetCurrentEpoch fetches the current epoch from the epoch-clock service.
func (c *EpochClient) GetCurrentEpoch(ctx context.Context) (uint64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/epoch", nil)
	if err != nil {
		return 0, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetching current epoch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result CurrentEpochResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decoding response: %w", err)
	}

	return result.Epoch, nil
}

// Subscribe connects to the SSE stream at /subscribe and returns a channel
// that receives EpochEvent values. The channel is closed when the connection
// ends or when Close() is called.
//
// The returned channel is buffered (capacity 10) to prevent blocking the
// SSE parsing goroutine.
func (c *EpochClient) Subscribe(ctx context.Context) (<-chan EpochEvent, error) {
	// Create a cancellable context for the SSE connection
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/subscribe", nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("connecting to SSE stream: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	eventCh := make(chan EpochEvent, 10)

	// Start goroutine to parse SSE events
	go c.parseSSEStream(ctx, resp, eventCh)

	return eventCh, nil
}

// parseSSEStream reads the SSE stream and sends epoch events to the channel.
// SSE format:
//
//	event: epoch
//	data: {"previous_epoch":1,"current_epoch":2,"timestamp":"..."}
func (c *EpochClient) parseSSEStream(ctx context.Context, resp *http.Response, eventCh chan<- EpochEvent) {
	defer resp.Body.Close()
	defer close(eventCh)

	scanner := bufio.NewScanner(resp.Body)
	var eventType string
	var dataLine string

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()

		// Parse SSE format
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
		} else if line == "" {
			// Empty line signals end of event
			if eventType == "epoch" && dataLine != "" {
				var event EpochEvent
				if err := json.Unmarshal([]byte(dataLine), &event); err == nil {
					select {
					case eventCh <- event:
					case <-ctx.Done():
						return
					}
				}
			}
			// Reset for next event
			eventType = ""
			dataLine = ""
		}
	}
}

// Close stops the SSE subscription and closes the connection.
func (c *EpochClient) Close() {
	if c.cancel != nil {
		c.cancel()
	}
}
