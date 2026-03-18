package epoch

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewClock(t *testing.T) {
	clock := NewClock(100 * time.Millisecond)
	if clock == nil {
		t.Fatal("NewClock returned nil")
	}
	if clock.CurrentEpoch() != 0 {
		t.Errorf("expected epoch 0 before start, got %d", clock.CurrentEpoch())
	}
	if clock.IsRunning() {
		t.Error("clock should not be running before Start()")
	}
}

func TestClockStart(t *testing.T) {
	clock := NewClock(50 * time.Millisecond)

	clock.Start()
	defer clock.Stop()

	// First tick should happen immediately
	if clock.CurrentEpoch() != 1 {
		t.Errorf("expected epoch 1 after start, got %d", clock.CurrentEpoch())
	}
	if !clock.IsRunning() {
		t.Error("clock should be running after Start()")
	}
}

func TestClockStop(t *testing.T) {
	clock := NewClock(10 * time.Millisecond)

	clock.Start()
	initialEpoch := clock.CurrentEpoch()

	clock.Stop()

	if clock.IsRunning() {
		t.Error("clock should not be running after Stop()")
	}

	// Wait a bit and verify epoch doesn't advance
	time.Sleep(30 * time.Millisecond)
	if clock.CurrentEpoch() != initialEpoch {
		t.Errorf("epoch should not advance after stop: expected %d, got %d",
			initialEpoch, clock.CurrentEpoch())
	}
}

func TestClockTicks(t *testing.T) {
	clock := NewClock(20 * time.Millisecond)

	clock.Start()
	defer clock.Stop()

	// Wait for a few ticks
	time.Sleep(70 * time.Millisecond)

	epoch := clock.CurrentEpoch()
	if epoch < 3 {
		t.Errorf("expected at least 3 epochs after 70ms with 20ms interval, got %d", epoch)
	}
}

func TestClockOnTick(t *testing.T) {
	clock := NewClock(20 * time.Millisecond)

	var mu sync.Mutex
	var epochs []uint64

	clock.OnTick(func(epoch uint64) {
		mu.Lock()
		epochs = append(epochs, epoch)
		mu.Unlock()
	})

	clock.Start()
	time.Sleep(70 * time.Millisecond)
	clock.Stop()

	mu.Lock()
	defer mu.Unlock()

	if len(epochs) < 3 {
		t.Errorf("expected at least 3 tick callbacks, got %d", len(epochs))
	}

	// Verify monotonically increasing
	for i := 1; i < len(epochs); i++ {
		if epochs[i] != epochs[i-1]+1 {
			t.Errorf("epochs should be monotonically increasing: %v", epochs)
			break
		}
	}
}

func TestClockMultipleHandlers(t *testing.T) {
	clock := NewClock(20 * time.Millisecond)

	var count1, count2 int32

	clock.OnTick(func(epoch uint64) {
		atomic.AddInt32(&count1, 1)
	})

	clock.OnTick(func(epoch uint64) {
		atomic.AddInt32(&count2, 1)
	})

	clock.Start()
	time.Sleep(50 * time.Millisecond)
	clock.Stop()

	c1 := atomic.LoadInt32(&count1)
	c2 := atomic.LoadInt32(&count2)

	if c1 != c2 {
		t.Errorf("both handlers should be called same number of times: %d != %d", c1, c2)
	}
	if c1 < 2 {
		t.Errorf("handlers should be called at least twice, got %d", c1)
	}
}

func TestClockStartIdempotent(t *testing.T) {
	clock := NewClock(50 * time.Millisecond)

	clock.Start()
	defer clock.Stop()

	epoch1 := clock.CurrentEpoch()

	// Starting again should be a no-op
	clock.Start()

	epoch2 := clock.CurrentEpoch()

	if epoch2 != epoch1 {
		t.Error("double start should not cause extra tick")
	}
}

func TestClockStopIdempotent(t *testing.T) {
	clock := NewClock(50 * time.Millisecond)

	clock.Start()
	clock.Stop()
	clock.Stop() // Should not panic

	if clock.IsRunning() {
		t.Error("clock should not be running after Stop()")
	}
}

func TestClockForceAdvance(t *testing.T) {
	clock := NewClock(1 * time.Hour) // Long duration so no natural ticks

	// Force advance without starting
	epoch1 := clock.ForceAdvance()
	if epoch1 != 1 {
		t.Errorf("expected epoch 1 after first force advance, got %d", epoch1)
	}

	epoch2 := clock.ForceAdvance()
	if epoch2 != 2 {
		t.Errorf("expected epoch 2 after second force advance, got %d", epoch2)
	}
}

func TestClockTickCount(t *testing.T) {
	clock := NewClock(20 * time.Millisecond)

	if clock.TickCount() != 0 {
		t.Errorf("expected tick count 0 before start, got %d", clock.TickCount())
	}

	clock.Start()
	time.Sleep(50 * time.Millisecond)
	clock.Stop()

	if clock.TickCount() < 2 {
		t.Errorf("expected at least 2 ticks, got %d", clock.TickCount())
	}
}

func TestClockDuration(t *testing.T) {
	duration := 123 * time.Millisecond
	clock := NewClock(duration)

	if clock.Duration() != duration {
		t.Errorf("expected duration %v, got %v", duration, clock.Duration())
	}
}

func TestClockEpochsNeverSkip(t *testing.T) {
	clock := NewClock(5 * time.Millisecond)

	var mu sync.Mutex
	var epochs []uint64

	clock.OnTick(func(epoch uint64) {
		mu.Lock()
		epochs = append(epochs, epoch)
		mu.Unlock()
	})

	clock.Start()
	time.Sleep(100 * time.Millisecond)
	clock.Stop()

	mu.Lock()
	defer mu.Unlock()

	// Verify no skips
	for i := 0; i < len(epochs); i++ {
		expected := uint64(i + 1)
		if epochs[i] != expected {
			t.Errorf("expected epoch %d at position %d, got %d (epochs: %v)",
				expected, i, epochs[i], epochs)
			break
		}
	}
}

func TestClockConcurrentAccess(t *testing.T) {
	clock := NewClock(5 * time.Millisecond)
	clock.Start()
	defer clock.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = clock.CurrentEpoch()
				_ = clock.IsRunning()
				_ = clock.TickCount()
			}
		}()
	}

	wg.Wait()
}
