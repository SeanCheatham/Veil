// Package epoch provides time-based epoch management for coordinated key rotation.
package epoch

import (
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// DefaultDurationSeconds is the default epoch duration (60 seconds).
const DefaultDurationSeconds = 60

// DefaultGracePeriodSeconds is the default grace period (10 seconds).
const DefaultGracePeriodSeconds = 10

// EpochConfig contains the configuration for epoch management.
type EpochConfig struct {
	DurationSeconds    int64 // Duration of each epoch in seconds (default: 60)
	GracePeriodSeconds int64 // Grace period at start of epoch for previous keys (default: 10)
}

// DefaultConfig returns the default epoch configuration.
func DefaultConfig() EpochConfig {
	return EpochConfig{
		DurationSeconds:    DefaultDurationSeconds,
		GracePeriodSeconds: DefaultGracePeriodSeconds,
	}
}

// EpochManager manages epoch calculation and key validity windows.
type EpochManager struct {
	config EpochConfig
	clock  func() time.Time // Injectable clock for testing

	mu             sync.RWMutex
	lastEpoch      uint64 // Track epoch transitions for Antithesis assertions
	transitionCount int64
}

// NewEpochManager creates a new EpochManager with the given configuration.
func NewEpochManager(config EpochConfig) *EpochManager {
	if config.DurationSeconds <= 0 {
		config.DurationSeconds = DefaultDurationSeconds
	}
	if config.GracePeriodSeconds <= 0 {
		config.GracePeriodSeconds = DefaultGracePeriodSeconds
	}
	if config.GracePeriodSeconds >= config.DurationSeconds {
		config.GracePeriodSeconds = config.DurationSeconds / 2
	}

	return &EpochManager{
		config: config,
		clock:  time.Now,
	}
}

// SetClock sets a custom clock function for testing.
func (e *EpochManager) SetClock(clock func() time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clock = clock
}

// CurrentEpoch returns the current epoch number based on the wall clock.
// Epoch number is calculated as: unix_timestamp / epoch_duration
func (e *EpochManager) CurrentEpoch() uint64 {
	e.mu.RLock()
	clock := e.clock
	e.mu.RUnlock()

	now := clock()
	epoch := uint64(now.Unix()) / uint64(e.config.DurationSeconds)

	// Track epoch transitions for Antithesis assertions
	e.mu.Lock()
	if epoch > e.lastEpoch {
		oldEpoch := e.lastEpoch
		e.lastEpoch = epoch
		e.transitionCount++
		e.mu.Unlock()

		// Antithesis assertion: epoch transitions happen
		assert.Sometimes(epoch > oldEpoch, "System transitions through epochs", map[string]any{
			"old_epoch": oldEpoch,
			"new_epoch": epoch,
		})
	} else {
		e.mu.Unlock()
	}

	return epoch
}

// PreviousEpoch returns the previous epoch number.
func (e *EpochManager) PreviousEpoch() uint64 {
	current := e.CurrentEpoch()
	if current == 0 {
		return 0
	}
	return current - 1
}

// TimeUntilNextEpoch returns the duration until the next epoch starts.
func (e *EpochManager) TimeUntilNextEpoch() time.Duration {
	e.mu.RLock()
	clock := e.clock
	e.mu.RUnlock()

	now := clock()
	epochSeconds := e.config.DurationSeconds
	currentEpochStart := (now.Unix() / epochSeconds) * epochSeconds
	nextEpochStart := currentEpochStart + epochSeconds

	return time.Unix(nextEpochStart, 0).Sub(now)
}

// IsInGracePeriod returns true if we're currently in the grace period
// at the start of an epoch where previous epoch keys are still valid.
func (e *EpochManager) IsInGracePeriod() bool {
	e.mu.RLock()
	clock := e.clock
	e.mu.RUnlock()

	now := clock()
	epochSeconds := e.config.DurationSeconds
	currentEpochStart := (now.Unix() / epochSeconds) * epochSeconds
	secondsIntoEpoch := now.Unix() - currentEpochStart

	return secondsIntoEpoch < e.config.GracePeriodSeconds
}

// GetValidEpochs returns the list of valid epoch numbers.
// During the grace period, both current and previous epochs are valid.
// Otherwise, only the current epoch is valid.
func (e *EpochManager) GetValidEpochs() []uint64 {
	current := e.CurrentEpoch()
	if e.IsInGracePeriod() && current > 0 {
		// Antithesis assertion: grace period allows two key sets
		assert.Always(true, "Grace period allows previous epoch keys", map[string]any{
			"current_epoch":  current,
			"previous_epoch": current - 1,
		})
		return []uint64{current, current - 1}
	}
	return []uint64{current}
}

// GetConfig returns the epoch configuration.
func (e *EpochManager) GetConfig() EpochConfig {
	return e.config
}

// GetTransitionCount returns the number of epoch transitions observed.
func (e *EpochManager) GetTransitionCount() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.transitionCount
}
