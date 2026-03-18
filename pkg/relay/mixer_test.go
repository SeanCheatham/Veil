package relay

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMixerEnqueue(t *testing.T) {
	var forwardedCount int32

	mixer := NewMixer(MixerConfig{
		BatchSize:    3,
		BatchTimeout: 100 * time.Millisecond,
		ForwardFunc: func(msg *MixedMessage) error {
			atomic.AddInt32(&forwardedCount, 1)
			return nil
		},
	})

	// Enqueue a message
	msg := &MixedMessage{
		InboundID:  "inbound-1",
		OutboundID: "outbound-1",
		NextHop:    "relay-2:7000",
		Payload:    []byte("test"),
	}

	forwardAt := mixer.Enqueue(msg)

	// Forward time should be in the future
	if !forwardAt.After(time.Now()) {
		t.Error("Forward time should be in the future")
	}

	// Queue size should be 1
	if mixer.QueueSize() != 1 {
		t.Errorf("Expected queue size 1, got %d", mixer.QueueSize())
	}
}

func TestMixerStartStop(t *testing.T) {
	mixer := NewMixer(MixerConfig{})

	if mixer.IsRunning() {
		t.Error("Mixer should not be running initially")
	}

	mixer.Start()

	if !mixer.IsRunning() {
		t.Error("Mixer should be running after Start")
	}

	// Starting again should be no-op
	mixer.Start()

	if !mixer.IsRunning() {
		t.Error("Mixer should still be running")
	}

	mixer.Stop()

	if mixer.IsRunning() {
		t.Error("Mixer should not be running after Stop")
	}
}

func TestMixerForwarding(t *testing.T) {
	var mu sync.Mutex
	forwarded := make([]*MixedMessage, 0)

	mixer := NewMixer(MixerConfig{
		BatchSize:    5,
		BatchTimeout: 50 * time.Millisecond,
		ForwardFunc: func(msg *MixedMessage) error {
			mu.Lock()
			forwarded = append(forwarded, msg)
			mu.Unlock()
			return nil
		},
	})

	mixer.Start()
	defer mixer.Stop()

	// Enqueue a message
	msg := &MixedMessage{
		InboundID:  "in-1",
		OutboundID: "out-1",
		NextHop:    "relay:7000",
		Payload:    []byte("test"),
	}
	mixer.Enqueue(msg)

	// Wait for the message to be forwarded (max delay + processing)
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	count := len(forwarded)
	mu.Unlock()

	if count != 1 {
		t.Errorf("Expected 1 message forwarded, got %d", count)
	}
}

func TestMixerBatching(t *testing.T) {
	var mu sync.Mutex
	forwardedIDs := make([]string, 0)

	mixer := NewMixer(MixerConfig{
		BatchSize:    3,
		BatchTimeout: 500 * time.Millisecond,
		ForwardFunc: func(msg *MixedMessage) error {
			mu.Lock()
			forwardedIDs = append(forwardedIDs, msg.OutboundID)
			mu.Unlock()
			return nil
		},
	})

	mixer.Start()
	defer mixer.Stop()

	// Enqueue 3 messages
	for i := 0; i < 3; i++ {
		msg := &MixedMessage{
			InboundID:  "in",
			OutboundID: string(rune('a' + i)),
			NextHop:    "relay:7000",
			Payload:    []byte("test"),
		}
		mixer.Enqueue(msg)
	}

	// Wait for all messages to be forwarded
	time.Sleep(400 * time.Millisecond)

	mu.Lock()
	count := len(forwardedIDs)
	mu.Unlock()

	if count != 3 {
		t.Errorf("Expected 3 messages forwarded, got %d", count)
	}
}

func TestMixerTimingObfuscation(t *testing.T) {
	// Test that messages are delayed with random timing
	mixer := NewMixer(MixerConfig{
		BatchSize:    10,
		BatchTimeout: 1 * time.Second,
	})

	var delays []time.Duration
	now := time.Now()

	// Enqueue several messages and record their scheduled forward times
	for i := 0; i < 10; i++ {
		msg := &MixedMessage{
			InboundID:  "in",
			OutboundID: "out",
			NextHop:    "relay:7000",
			Payload:    []byte("test"),
		}
		forwardAt := mixer.Enqueue(msg)
		delays = append(delays, forwardAt.Sub(now))
	}

	// Check that delays are within expected range
	for _, delay := range delays {
		if delay < MinMixDelay || delay > MaxMixDelay {
			t.Errorf("Delay %v is outside expected range [%v, %v]", delay, MinMixDelay, MaxMixDelay)
		}
	}

	// Check that delays vary (not all the same)
	allSame := true
	for i := 1; i < len(delays); i++ {
		if delays[i] != delays[0] {
			allSame = false
			break
		}
	}

	if allSame && len(delays) > 1 {
		t.Error("All delays are the same - timing obfuscation may not be working")
	}
}

func TestMixerStats(t *testing.T) {
	mixer := NewMixer(MixerConfig{})

	stats := mixer.Stats()
	if stats.QueueSize != 0 {
		t.Errorf("Expected queue size 0, got %d", stats.QueueSize)
	}
	if stats.Running {
		t.Error("Expected not running")
	}

	mixer.Start()
	defer mixer.Stop()

	stats = mixer.Stats()
	if !stats.Running {
		t.Error("Expected running")
	}

	// Enqueue a message
	mixer.Enqueue(&MixedMessage{
		OutboundID: "test",
		NextHop:    "relay:7000",
		Payload:    []byte("test"),
	})

	stats = mixer.Stats()
	if stats.QueueSize != 1 {
		t.Errorf("Expected queue size 1, got %d", stats.QueueSize)
	}
}

func TestMixerFlushOnStop(t *testing.T) {
	var mu sync.Mutex
	forwardedCount := 0

	mixer := NewMixer(MixerConfig{
		BatchSize:    100, // Large batch size so it won't trigger normally
		BatchTimeout: 10 * time.Second, // Long timeout
		ForwardFunc: func(msg *MixedMessage) error {
			mu.Lock()
			forwardedCount++
			mu.Unlock()
			return nil
		},
	})

	mixer.Start()

	// Enqueue several messages
	for i := 0; i < 5; i++ {
		mixer.Enqueue(&MixedMessage{
			OutboundID: string(rune('a' + i)),
			NextHop:    "relay:7000",
			Payload:    []byte("test"),
		})
	}

	// Stop should flush all messages
	mixer.Stop()

	mu.Lock()
	count := forwardedCount
	mu.Unlock()

	if count != 5 {
		t.Errorf("Expected 5 messages flushed on stop, got %d", count)
	}
}

func TestMixedMessageFields(t *testing.T) {
	msg := &MixedMessage{
		InboundID:      "inbound-123",
		OutboundID:     "outbound-456",
		NextHop:        "relay-2:7000",
		Payload:        []byte("test payload"),
		IsCoverTraffic: false,
	}

	// Verify inbound and outbound IDs are different
	if msg.InboundID == msg.OutboundID {
		t.Error("Inbound and outbound IDs should be different for unlinkability")
	}

	// Test cover traffic flag
	if msg.IsCoverTraffic {
		t.Error("Should not be cover traffic by default")
	}

	msg.IsCoverTraffic = true
	if !msg.IsCoverTraffic {
		t.Error("Cover traffic flag should be set")
	}
}
