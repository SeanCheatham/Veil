package epoch

import (
	"sync"
	"testing"
	"time"
)

func TestEpochAdvancesAfterDuration(t *testing.T) {
	m := NewManager(50 * time.Millisecond)
	m.Start()

	time.Sleep(180 * time.Millisecond)

	got := m.GetCurrentEpoch()
	if got < 2 {
		t.Fatalf("expected epoch >= 2 after ~180ms with 50ms ticks, got %d", got)
	}
}

func TestCallbacksFireOnTick(t *testing.T) {
	m := NewManager(50 * time.Millisecond)

	var mu sync.Mutex
	var fired []uint64
	m.OnEpochTick(func(epoch uint64) {
		mu.Lock()
		fired = append(fired, epoch)
		mu.Unlock()
	})

	m.Start()
	time.Sleep(180 * time.Millisecond)

	mu.Lock()
	count := len(fired)
	mu.Unlock()

	if count < 2 {
		t.Fatalf("expected callback to fire >= 2 times, got %d", count)
	}
}

func TestGetCurrentEpochReturnsCorrectValue(t *testing.T) {
	m := NewManager(50 * time.Millisecond)
	if m.GetCurrentEpoch() != 0 {
		t.Fatal("expected initial epoch to be 0")
	}

	m.Start()
	time.Sleep(80 * time.Millisecond)

	got := m.GetCurrentEpoch()
	if got < 1 {
		t.Fatalf("expected epoch >= 1 after one tick, got %d", got)
	}
}

func TestConcurrentReadsAreSafe(t *testing.T) {
	m := NewManager(10 * time.Millisecond)
	m.Start()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.GetCurrentEpoch()
		}()
	}
	wg.Wait()
}
