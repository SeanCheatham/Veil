// Package epoch provides epoch management and tracking for key rotation.
// An epoch is a fixed time period during which encryption keys remain constant.
// At epoch boundaries, keys are rotated by deriving new keys with the new epoch number.
package epoch

import (
	"sync"
	"time"
)

// Manager tracks epoch state and transitions.
// It provides thread-safe access to the current epoch and notifies
// callbacks when epoch transitions occur.
type Manager struct {
	mu           sync.RWMutex
	duration     int64  // epoch duration in seconds
	currentEpoch uint64 // cached current epoch
	stopCh       chan struct{}
	running      bool
}

// NewManager creates a new epoch manager with the specified duration.
// durationSeconds specifies how long each epoch lasts.
func NewManager(durationSeconds int64) *Manager {
	if durationSeconds <= 0 {
		durationSeconds = 60 // default to 60 seconds
	}
	return &Manager{
		duration:     durationSeconds,
		currentEpoch: uint64(time.Now().Unix() / durationSeconds),
		stopCh:       make(chan struct{}),
	}
}

// Duration returns the epoch duration in seconds.
func (m *Manager) Duration() int64 {
	return m.duration
}

// CurrentEpoch returns the current epoch number based on wall clock time.
func (m *Manager) CurrentEpoch() uint64 {
	return uint64(time.Now().Unix() / m.duration)
}

// CachedEpoch returns the last known epoch (may be slightly stale).
// Use CurrentEpoch() for accurate values.
func (m *Manager) CachedEpoch() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentEpoch
}

// Start begins epoch monitoring, calling onRotate at each transition.
// The callback receives (oldEpoch, newEpoch) when a transition is detected.
// This function blocks and should be run in a goroutine.
func (m *Manager) Start(onRotate func(oldEpoch, newEpoch uint64)) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.currentEpoch = m.CurrentEpoch()
	m.mu.Unlock()

	// Check every second for epoch transitions
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			newEpoch := m.CurrentEpoch()

			m.mu.Lock()
			if newEpoch != m.currentEpoch {
				oldEpoch := m.currentEpoch
				m.currentEpoch = newEpoch
				m.mu.Unlock()

				if onRotate != nil {
					onRotate(oldEpoch, newEpoch)
				}
			} else {
				m.mu.Unlock()
			}
		}
	}
}

// Stop stops the epoch monitoring goroutine.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		close(m.stopCh)
		m.running = false
	}
}

// WaitForRotation blocks until the next epoch transition and returns the new epoch.
// Returns the new epoch number after the transition.
func (m *Manager) WaitForRotation() uint64 {
	startEpoch := m.CurrentEpoch()

	// Calculate time until next epoch
	now := time.Now().Unix()
	nextEpochStart := ((now / m.duration) + 1) * m.duration
	waitTime := time.Duration(nextEpochStart-now) * time.Second

	// Add a small buffer to ensure we're past the boundary
	time.Sleep(waitTime + 100*time.Millisecond)

	newEpoch := m.CurrentEpoch()

	// In case of timing edge cases, wait a bit more if needed
	for newEpoch == startEpoch {
		time.Sleep(100 * time.Millisecond)
		newEpoch = m.CurrentEpoch()
	}

	return newEpoch
}

// TimeUntilNextEpoch returns the duration until the next epoch boundary.
func (m *Manager) TimeUntilNextEpoch() time.Duration {
	now := time.Now().Unix()
	nextEpochStart := ((now / m.duration) + 1) * m.duration
	return time.Duration(nextEpochStart-now) * time.Second
}

// EpochAtTime returns the epoch number for a given timestamp.
func (m *Manager) EpochAtTime(t time.Time) uint64 {
	return uint64(t.Unix() / m.duration)
}
