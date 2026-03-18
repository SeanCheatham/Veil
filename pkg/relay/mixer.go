// Package relay implements the Veil relay layer for onion-peeling and mix-and-forward operations.
package relay

import (
	"math/rand"
	"sync"
	"time"
)

const (
	// MinMixDelay is the minimum delay before forwarding a message (timing obfuscation).
	MinMixDelay = 50 * time.Millisecond

	// MaxMixDelay is the maximum delay before forwarding a message.
	MaxMixDelay = 200 * time.Millisecond

	// DefaultBatchSize is the default number of messages to batch before forwarding.
	DefaultBatchSize = 5

	// DefaultBatchTimeout is how long to wait for a batch to fill before forwarding anyway.
	DefaultBatchTimeout = 500 * time.Millisecond

	// CoverTrafficInterval is how often to inject cover traffic when traffic is low.
	CoverTrafficInterval = 1 * time.Second
)

// MixedMessage represents a message queued for mixing and forwarding.
type MixedMessage struct {
	// InboundID is the original message ID as received (for unlinkability tracking).
	InboundID string

	// OutboundID is the new message ID for forwarding (different from InboundID).
	OutboundID string

	// NextHop is the destination address for this message.
	NextHop string

	// Payload is the inner payload to forward (serialized onion or final message).
	Payload []byte

	// ReceivedAt is when the message was received by the relay.
	ReceivedAt time.Time

	// ForwardAt is when the message should be forwarded (for timing obfuscation).
	ForwardAt time.Time

	// IsCoverTraffic indicates if this is a dummy message for traffic analysis resistance.
	IsCoverTraffic bool
}

// ForwardFunc is called to actually forward a message to the next hop.
type ForwardFunc func(msg *MixedMessage) error

// Mixer implements mix-and-forward logic with timing obfuscation and batching.
// It collects incoming messages, adds random delays, and forwards them in batches
// to defeat timing analysis attacks.
type Mixer struct {
	mu sync.Mutex

	// queue holds messages waiting to be forwarded.
	queue []*MixedMessage

	// batchSize is the target batch size for forwarding.
	batchSize int

	// batchTimeout is how long to wait for a batch to fill.
	batchTimeout time.Duration

	// forwardFunc is called to forward messages.
	forwardFunc ForwardFunc

	// running indicates if the mixer is active.
	running bool

	// stopCh signals the mixer to stop.
	stopCh chan struct{}

	// wg tracks running goroutines.
	wg sync.WaitGroup

	// lastForwardTime tracks when we last forwarded messages.
	lastForwardTime time.Time

	// coverTrafficEnabled controls whether to inject cover traffic.
	coverTrafficEnabled bool

	// generateCoverTraffic creates dummy messages when traffic is low.
	generateCoverTraffic func() *MixedMessage

	// rng is the random number generator for delays.
	rng *rand.Rand
}

// MixerConfig holds configuration for the mixer.
type MixerConfig struct {
	BatchSize            int
	BatchTimeout         time.Duration
	ForwardFunc          ForwardFunc
	CoverTrafficEnabled  bool
	GenerateCoverTraffic func() *MixedMessage
}

// NewMixer creates a new mixer with the given configuration.
func NewMixer(cfg MixerConfig) *Mixer {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	batchTimeout := cfg.BatchTimeout
	if batchTimeout <= 0 {
		batchTimeout = DefaultBatchTimeout
	}

	return &Mixer{
		queue:                make([]*MixedMessage, 0),
		batchSize:            batchSize,
		batchTimeout:         batchTimeout,
		forwardFunc:          cfg.ForwardFunc,
		coverTrafficEnabled:  cfg.CoverTrafficEnabled,
		generateCoverTraffic: cfg.GenerateCoverTraffic,
		stopCh:               make(chan struct{}),
		lastForwardTime:      time.Now(),
		rng:                  rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Start begins the mixer's background processing.
func (m *Mixer) Start() {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.stopCh = make(chan struct{})
	m.mu.Unlock()

	m.wg.Add(1)
	go m.processLoop()

	if m.coverTrafficEnabled && m.generateCoverTraffic != nil {
		m.wg.Add(1)
		go m.coverTrafficLoop()
	}
}

// Stop halts the mixer.
func (m *Mixer) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	m.running = false
	close(m.stopCh)
	m.mu.Unlock()

	m.wg.Wait()
}

// IsRunning returns whether the mixer is active.
func (m *Mixer) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// Enqueue adds a message to the mixing queue with a random delay.
// Returns the scheduled forward time.
func (m *Mixer) Enqueue(msg *MixedMessage) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Calculate random delay for timing obfuscation
	delay := m.randomDelay()
	msg.ForwardAt = time.Now().Add(delay)
	msg.ReceivedAt = time.Now()

	m.queue = append(m.queue, msg)

	return msg.ForwardAt
}

// QueueSize returns the current number of messages in the queue.
func (m *Mixer) QueueSize() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue)
}

// randomDelay returns a random delay between MinMixDelay and MaxMixDelay.
func (m *Mixer) randomDelay() time.Duration {
	delayRange := int64(MaxMixDelay - MinMixDelay)
	return MinMixDelay + time.Duration(m.rng.Int63n(delayRange))
}

// processLoop handles message forwarding at scheduled times.
func (m *Mixer) processLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	batchTicker := time.NewTicker(m.batchTimeout)
	defer batchTicker.Stop()

	for {
		select {
		case <-m.stopCh:
			// Flush remaining messages on shutdown
			m.flushAll()
			return

		case <-ticker.C:
			m.forwardReady()

		case <-batchTicker.C:
			// Force batch forward on timeout
			m.forwardBatch()
		}
	}
}

// forwardReady forwards all messages that have passed their scheduled time.
func (m *Mixer) forwardReady() {
	m.mu.Lock()
	now := time.Now()

	var ready []*MixedMessage
	var remaining []*MixedMessage

	for _, msg := range m.queue {
		if msg.ForwardAt.Before(now) || msg.ForwardAt.Equal(now) {
			ready = append(ready, msg)
		} else {
			remaining = append(remaining, msg)
		}
	}

	m.queue = remaining

	// Check if we have a full batch
	if len(ready) >= m.batchSize {
		m.lastForwardTime = now
	}

	m.mu.Unlock()

	// Forward messages outside the lock
	for _, msg := range ready {
		if m.forwardFunc != nil {
			// Ignore errors - the forwardFunc should handle retries
			_ = m.forwardFunc(msg)
		}
	}
}

// forwardBatch forces forwarding of messages even if batch is not full.
func (m *Mixer) forwardBatch() {
	m.mu.Lock()
	now := time.Now()

	// If nothing to forward, skip
	if len(m.queue) == 0 {
		m.mu.Unlock()
		return
	}

	// If we recently forwarded, wait
	if now.Sub(m.lastForwardTime) < m.batchTimeout/2 {
		m.mu.Unlock()
		return
	}

	// Take messages that are ready or nearly ready (within 50ms)
	threshold := now.Add(50 * time.Millisecond)
	var ready []*MixedMessage
	var remaining []*MixedMessage

	for _, msg := range m.queue {
		if msg.ForwardAt.Before(threshold) {
			ready = append(ready, msg)
		} else {
			remaining = append(remaining, msg)
		}
	}

	if len(ready) == 0 {
		m.mu.Unlock()
		return
	}

	m.queue = remaining
	m.lastForwardTime = now

	m.mu.Unlock()

	// Forward messages outside the lock
	for _, msg := range ready {
		if m.forwardFunc != nil {
			_ = m.forwardFunc(msg)
		}
	}
}

// flushAll forwards all remaining messages immediately.
func (m *Mixer) flushAll() {
	m.mu.Lock()
	messages := m.queue
	m.queue = nil
	m.mu.Unlock()

	for _, msg := range messages {
		if m.forwardFunc != nil {
			_ = m.forwardFunc(msg)
		}
	}
}

// coverTrafficLoop injects cover traffic when real traffic is low.
func (m *Mixer) coverTrafficLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(CoverTrafficInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return

		case <-ticker.C:
			m.maybeInjectCoverTraffic()
		}
	}
}

// maybeInjectCoverTraffic adds a dummy message if traffic is low.
func (m *Mixer) maybeInjectCoverTraffic() {
	m.mu.Lock()
	queueLen := len(m.queue)
	lastForward := m.lastForwardTime
	m.mu.Unlock()

	// Inject cover traffic if queue is empty and we haven't forwarded recently
	if queueLen == 0 && time.Since(lastForward) > CoverTrafficInterval && m.generateCoverTraffic != nil {
		cover := m.generateCoverTraffic()
		if cover != nil {
			cover.IsCoverTraffic = true
			m.Enqueue(cover)
		}
	}
}

// Statistics holds mixer statistics for monitoring.
type Statistics struct {
	QueueSize       int
	LastForwardTime time.Time
	Running         bool
}

// Stats returns current mixer statistics.
func (m *Mixer) Stats() Statistics {
	m.mu.Lock()
	defer m.mu.Unlock()

	return Statistics{
		QueueSize:       len(m.queue),
		LastForwardTime: m.lastForwardTime,
		Running:         m.running,
	}
}
