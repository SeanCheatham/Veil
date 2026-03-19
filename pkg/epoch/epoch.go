package epoch

import (
	"os"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// Manager tracks epoch numbers and notifies callbacks on each tick.
type Manager struct {
	currentEpoch uint64
	duration     time.Duration
	mu           sync.RWMutex
	callbacks    []func(epoch uint64)
}

// NewManager creates a new epoch manager with the given tick duration.
func NewManager(duration time.Duration) *Manager {
	return &Manager{
		duration: duration,
	}
}

// Start begins the epoch ticker goroutine.
func (m *Manager) Start() {
	go func() {
		ticker := time.NewTicker(m.duration)
		defer ticker.Stop()
		for range ticker.C {
			m.mu.Lock()
			m.currentEpoch++
			newEpoch := m.currentEpoch
			cbs := make([]func(uint64), len(m.callbacks))
			copy(cbs, m.callbacks)
			m.mu.Unlock()

			assert.Sometimes(true, "epoch_advanced", map[string]any{"epoch": newEpoch})

			for _, cb := range cbs {
				cb(newEpoch)
			}
		}
	}()
}

// GetCurrentEpoch returns the current epoch number (thread-safe).
func (m *Manager) GetCurrentEpoch() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentEpoch
}

// OnEpochTick registers a callback for epoch transitions.
func (m *Manager) OnEpochTick(callback func(epoch uint64)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks = append(m.callbacks, callback)
}

// DurationFromEnv reads the EPOCH_DURATION env var and parses it as a Go
// duration string. Returns the default of 30s if unset or unparseable.
func DurationFromEnv() time.Duration {
	s := os.Getenv("EPOCH_DURATION")
	if s == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 30 * time.Second
	}
	return d
}
