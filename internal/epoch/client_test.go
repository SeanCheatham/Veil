package epoch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEpochClientGetCurrentEpoch(t *testing.T) {
	// Create a test server that returns a fixed epoch
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/epoch" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"epoch":     42,
			"timestamp": time.Now().UTC(),
		})
	}))
	defer server.Close()

	client := NewEpochClient(server.URL)

	epoch, err := client.GetCurrentEpoch(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentEpoch failed: %v", err)
	}

	if epoch != 42 {
		t.Errorf("epoch = %d, want 42", epoch)
	}
}

func TestEpochClientGetCurrentEpochError(t *testing.T) {
	// Create a test server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Internal error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewEpochClient(server.URL)

	_, err := client.GetCurrentEpoch(context.Background())
	if err == nil {
		t.Error("Expected error, got nil")
	}
}

func TestEpochClientSubscribe(t *testing.T) {
	// Track when connected event was sent
	var connectedSent bool

	// Create a test server that sends SSE events
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/subscribe" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "SSE not supported", http.StatusInternalServerError)
			return
		}

		// Send connected event
		data, _ := json.Marshal(map[string]interface{}{
			"type":  "connected",
			"epoch": 1,
		})
		fmt.Fprintf(w, "event: connected\ndata: %s\n\n", data)
		flusher.Flush()
		connectedSent = true

		// Send epoch events
		for i := 1; i <= 3; i++ {
			event := EpochEvent{
				PreviousEpoch: uint64(i),
				CurrentEpoch:  uint64(i + 1),
				Timestamp:     time.Now(),
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: epoch\ndata: %s\n\n", data)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer server.Close()

	client := NewEpochClient(server.URL)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	eventCh, err := client.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Collect events
	var events []EpochEvent
	for event := range eventCh {
		events = append(events, event)
		if len(events) >= 3 {
			break
		}
	}

	if !connectedSent {
		t.Error("Connected event was not sent")
	}

	if len(events) != 3 {
		t.Errorf("Received %d events, want 3", len(events))
	}

	// Verify event sequence
	for i, event := range events {
		expectedPrev := uint64(i + 1)
		expectedCurr := uint64(i + 2)
		if event.PreviousEpoch != expectedPrev {
			t.Errorf("Event %d: PreviousEpoch = %d, want %d", i, event.PreviousEpoch, expectedPrev)
		}
		if event.CurrentEpoch != expectedCurr {
			t.Errorf("Event %d: CurrentEpoch = %d, want %d", i, event.CurrentEpoch, expectedCurr)
		}
	}
}

func TestEpochClientSubscribeContextCancel(t *testing.T) {
	// Create a server that never closes
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		flusher.Flush()

		// Block until client disconnects
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewEpochClient(server.URL)

	ctx, cancel := context.WithCancel(context.Background())

	eventCh, err := client.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Cancel the context
	cancel()

	// Channel should close
	select {
	case _, ok := <-eventCh:
		if ok {
			// Got an event, wait for close
			<-eventCh
		}
	case <-time.After(time.Second):
		t.Error("Channel did not close after context cancel")
	}
}

func TestEpochClientClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		flusher.Flush()

		// Block until client disconnects
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewEpochClient(server.URL)

	ctx := context.Background()
	eventCh, err := client.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Close the client
	client.Close()

	// Channel should close
	select {
	case _, ok := <-eventCh:
		if ok {
			// Got an event, wait for close
			<-eventCh
		}
	case <-time.After(time.Second):
		t.Error("Channel did not close after client.Close()")
	}
}

func TestNewEpochClient(t *testing.T) {
	// Test URL normalization (trailing slash removal)
	client1 := NewEpochClient("http://localhost:8083/")
	client2 := NewEpochClient("http://localhost:8083")

	if client1.url != "http://localhost:8083" {
		t.Errorf("URL with trailing slash not normalized: %s", client1.url)
	}
	if client2.url != "http://localhost:8083" {
		t.Errorf("URL without trailing slash changed: %s", client2.url)
	}
}
