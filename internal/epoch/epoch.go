// Package epoch implements the epoch clock and key rotation logic.
package epoch

import (
	"sync"
	"time"

	"github.com/veil-protocol/veil/internal/properties"
)

// EpochEvent represents an epoch transition event.
type EpochEvent struct {
	PreviousEpoch uint64    `json:"previous_epoch"`
	CurrentEpoch  uint64    `json:"current_epoch"`
	Timestamp     time.Time `json:"timestamp"`
}

// EpochClock tracks the current epoch number and notifies subscribers on transitions.
// Epochs start at 1 and must increment by exactly 1 (no skips, no duplicates).
type EpochClock struct {
	mu          sync.RWMutex
	current     uint64
	ticker      *time.Ticker
	stop        chan struct{}
	subscribers []chan EpochEvent
	subMu       sync.Mutex
	running     bool
}

// NewEpochClock creates a new epoch clock starting at epoch 1.
func NewEpochClock() *EpochClock {
	return &EpochClock{
		current:     1,
		subscribers: make([]chan EpochEvent, 0),
		stop:        make(chan struct{}),
	}
}

// Current returns the current epoch number.
func (ec *EpochClock) Current() uint64 {
	ec.mu.RLock()
	defer ec.mu.RUnlock()
	return ec.current
}

// Start begins the epoch ticker with the given interval.
// Each tick increments the epoch by 1 and notifies all subscribers.
func (ec *EpochClock) Start(interval time.Duration) {
	ec.mu.Lock()
	if ec.running {
		ec.mu.Unlock()
		return
	}
	ec.running = true
	ec.ticker = time.NewTicker(interval)
	ec.mu.Unlock()

	go ec.run()
}

// run is the main ticker goroutine.
func (ec *EpochClock) run() {
	for {
		select {
		case <-ec.stop:
			ec.ticker.Stop()
			return
		case <-ec.ticker.C:
			ec.tick()
		}
	}
}

// tick performs a single epoch transition.
func (ec *EpochClock) tick() {
	ec.mu.Lock()
	previous := ec.current
	next := previous + 1

	// Validate the transition: next must be exactly previous + 1
	// This ensures no skips (prev+2, prev+3, ...) and no duplicates (prev)
	valid := next == previous+1

	// Call Antithesis property assertion
	properties.AssertEpochBoundaries(valid, previous, next)

	ec.current = next
	ec.mu.Unlock()

	// Create the event
	event := EpochEvent{
		PreviousEpoch: previous,
		CurrentEpoch:  next,
		Timestamp:     time.Now(),
	}

	// Notify all subscribers
	ec.notifySubscribers(event)
}

// notifySubscribers sends the epoch event to all registered subscribers.
// It removes closed channels from the subscriber list.
func (ec *EpochClock) notifySubscribers(event EpochEvent) {
	ec.subMu.Lock()
	defer ec.subMu.Unlock()

	// Filter out any closed channels
	activeSubscribers := make([]chan EpochEvent, 0, len(ec.subscribers))
	for _, ch := range ec.subscribers {
		select {
		case ch <- event:
			activeSubscribers = append(activeSubscribers, ch)
		default:
			// Channel is full or closed, skip it
			// but keep trying - might be slow consumer
			activeSubscribers = append(activeSubscribers, ch)
		}
	}
	ec.subscribers = activeSubscribers
}

// Subscribe returns a channel that receives epoch events.
// The channel is buffered to prevent blocking the clock.
func (ec *EpochClock) Subscribe() chan EpochEvent {
	ec.subMu.Lock()
	defer ec.subMu.Unlock()

	ch := make(chan EpochEvent, 10)
	ec.subscribers = append(ec.subscribers, ch)
	return ch
}

// Unsubscribe removes a subscriber channel.
func (ec *EpochClock) Unsubscribe(ch chan EpochEvent) {
	ec.subMu.Lock()
	defer ec.subMu.Unlock()

	for i, sub := range ec.subscribers {
		if sub == ch {
			ec.subscribers = append(ec.subscribers[:i], ec.subscribers[i+1:]...)
			close(ch)
			return
		}
	}
}

// Stop halts the epoch clock.
func (ec *EpochClock) Stop() {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	if !ec.running {
		return
	}
	ec.running = false
	close(ec.stop)
}
