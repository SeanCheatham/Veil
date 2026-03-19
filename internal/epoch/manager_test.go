package epoch

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	m := NewManager(60)
	if m.Duration() != 60 {
		t.Errorf("Expected duration 60, got %d", m.Duration())
	}

	// Test default duration for invalid input
	m2 := NewManager(0)
	if m2.Duration() != 60 {
		t.Errorf("Expected default duration 60 for zero input, got %d", m2.Duration())
	}

	m3 := NewManager(-10)
	if m3.Duration() != 60 {
		t.Errorf("Expected default duration 60 for negative input, got %d", m3.Duration())
	}
}

func TestCurrentEpoch(t *testing.T) {
	m := NewManager(60)
	epoch := m.CurrentEpoch()

	// Verify epoch is reasonable (should be time.Now() / 60)
	expected := uint64(time.Now().Unix() / 60)
	if epoch != expected && epoch != expected+1 {
		t.Errorf("CurrentEpoch returned %d, expected ~%d", epoch, expected)
	}
}

func TestCachedEpoch(t *testing.T) {
	m := NewManager(60)
	cached := m.CachedEpoch()
	current := m.CurrentEpoch()

	// Cached should equal current initially (within same second)
	if cached != current && cached != current-1 {
		t.Errorf("CachedEpoch %d not close to CurrentEpoch %d", cached, current)
	}
}

func TestEpochAtTime(t *testing.T) {
	m := NewManager(60)

	// Test specific timestamp
	testTime := time.Unix(120, 0) // 2 minutes after epoch 0
	epoch := m.EpochAtTime(testTime)
	if epoch != 2 {
		t.Errorf("Expected epoch 2 for time 120, got %d", epoch)
	}

	testTime2 := time.Unix(59, 0) // Just before first minute boundary
	epoch2 := m.EpochAtTime(testTime2)
	if epoch2 != 0 {
		t.Errorf("Expected epoch 0 for time 59, got %d", epoch2)
	}
}

func TestTimeUntilNextEpoch(t *testing.T) {
	m := NewManager(60)
	duration := m.TimeUntilNextEpoch()

	// Should be between 0 and 60 seconds
	if duration < 0 || duration > 60*time.Second {
		t.Errorf("TimeUntilNextEpoch returned %v, expected 0-60s", duration)
	}
}

func TestStartAndStop(t *testing.T) {
	// Use a very short duration for testing
	m := NewManager(1)

	var transitionCount int32

	// Start monitoring in background
	go m.Start(func(oldEpoch, newEpoch uint64) {
		atomic.AddInt32(&transitionCount, 1)
	})

	// Wait for at least one transition (with buffer)
	time.Sleep(2500 * time.Millisecond)

	m.Stop()

	count := atomic.LoadInt32(&transitionCount)
	if count < 1 {
		t.Errorf("Expected at least 1 transition, got %d", count)
	}
}

func TestWaitForRotation(t *testing.T) {
	// Use short epoch for faster test
	m := NewManager(2)

	startEpoch := m.CurrentEpoch()
	newEpoch := m.WaitForRotation()

	if newEpoch <= startEpoch {
		t.Errorf("WaitForRotation returned %d, expected > %d", newEpoch, startEpoch)
	}
}
