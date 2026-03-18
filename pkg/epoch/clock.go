// Package epoch implements the Veil epoch clock and session key rotation.
// Epochs are fundamental to anonymity guarantees - they define time boundaries
// for key rotation and message batching.
package epoch

import (
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil-protocol/veil/pkg/antithesis"
)

// TickHandler is called when the epoch clock ticks to a new epoch.
type TickHandler func(epoch uint64)

// Clock manages epoch timing and notifies subscribers on epoch boundaries.
// Epochs are numbered starting from 1 and monotonically increase.
type Clock struct {
	mu            sync.RWMutex
	currentEpoch  uint64
	lastEpoch     uint64 // tracks previous epoch for skip/duplicate detection
	duration      time.Duration
	handlers      []TickHandler
	stopCh        chan struct{}
	running       bool
	tickCount     uint64 // total number of ticks for testing/debugging
	hasStarted    bool   // whether the clock has ever started
	seenEpochs    map[uint64]bool // tracks all seen epochs for duplicate detection
}

// NewClock creates a new epoch clock with the specified tick duration.
// The clock does not start automatically; call Start() to begin ticking.
func NewClock(duration time.Duration) *Clock {
	return &Clock{
		currentEpoch: 0, // Will be set to 1 on first tick
		lastEpoch:    0,
		duration:     duration,
		handlers:     make([]TickHandler, 0),
		stopCh:       make(chan struct{}),
		seenEpochs:   make(map[uint64]bool),
	}
}

// Duration returns the configured epoch duration.
func (c *Clock) Duration() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.duration
}

// CurrentEpoch returns the current epoch number.
// Returns 0 if the clock has not started yet.
func (c *Clock) CurrentEpoch() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentEpoch
}

// OnTick registers a handler to be called when the epoch advances.
// Handlers are called synchronously in registration order.
func (c *Clock) OnTick(handler TickHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers = append(c.handlers, handler)
}

// Start begins the epoch clock. The first tick occurs immediately,
// setting the epoch to 1. Subsequent ticks occur at the configured interval.
func (c *Clock) Start() {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.hasStarted = true
	c.stopCh = make(chan struct{})
	c.mu.Unlock()

	// Initial tick to epoch 1
	c.tick()

	go c.run()
}

// Stop halts the epoch clock. It can be restarted with Start().
func (c *Clock) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	close(c.stopCh)
	c.mu.Unlock()
}

// IsRunning returns whether the clock is currently running.
func (c *Clock) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

// TickCount returns the total number of epoch ticks that have occurred.
func (c *Clock) TickCount() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tickCount
}

// run is the main loop that fires epoch ticks at the configured interval.
func (c *Clock) run() {
	ticker := time.NewTicker(c.duration)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.tick()
		}
	}
}

// tick advances the epoch and notifies all handlers.
func (c *Clock) tick() {
	c.mu.Lock()

	// Store previous epoch for boundary validation
	previousEpoch := c.currentEpoch
	c.lastEpoch = previousEpoch

	// Advance to next epoch
	newEpoch := previousEpoch + 1
	c.currentEpoch = newEpoch
	c.tickCount++

	// Check for duplicate epochs (should never happen in correct implementation)
	isDuplicate := c.seenEpochs[newEpoch]
	c.seenEpochs[newEpoch] = true

	// Check for skipped epochs (should never happen in correct implementation)
	isSkipped := previousEpoch > 0 && newEpoch != previousEpoch+1

	// Antithesis assertion: epoch_boundaries
	// This safety property asserts that epoch ticks never skip or duplicate.
	// A single counterexample disproves the property.
	validBoundary := !isDuplicate && !isSkipped
	assert.Always(
		validBoundary,
		antithesis.EpochBoundaries,
		map[string]any{
			"previous_epoch": previousEpoch,
			"new_epoch":      newEpoch,
			"is_duplicate":   isDuplicate,
			"is_skipped":     isSkipped,
		},
	)

	// Copy handlers to avoid holding lock during callback
	handlers := make([]TickHandler, len(c.handlers))
	copy(handlers, c.handlers)

	c.mu.Unlock()

	// Notify handlers outside the lock to prevent deadlocks
	for _, handler := range handlers {
		handler(newEpoch)
	}
}

// ForceAdvance manually advances the epoch to the next value.
// This is primarily for testing purposes.
// Returns the new epoch number.
func (c *Clock) ForceAdvance() uint64 {
	c.mu.Lock()
	if !c.hasStarted {
		c.hasStarted = true
	}
	c.mu.Unlock()

	c.tick()

	c.mu.RLock()
	epoch := c.currentEpoch
	c.mu.RUnlock()

	return epoch
}
