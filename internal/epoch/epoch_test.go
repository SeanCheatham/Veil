package epoch

import (
	"testing"
	"time"
)

func TestNewEpochClock(t *testing.T) {
	ec := NewEpochClock()
	if ec.Current() != 1 {
		t.Errorf("Expected initial epoch to be 1, got %d", ec.Current())
	}
}

func TestEpochClockTick(t *testing.T) {
	ec := NewEpochClock()

	// Subscribe to events
	eventCh := ec.Subscribe()

	// Start with a very fast interval for testing
	ec.Start(10 * time.Millisecond)
	defer ec.Stop()

	// Wait for at least one tick
	select {
	case event := <-eventCh:
		if event.PreviousEpoch != 1 {
			t.Errorf("Expected previous epoch 1, got %d", event.PreviousEpoch)
		}
		if event.CurrentEpoch != 2 {
			t.Errorf("Expected current epoch 2, got %d", event.CurrentEpoch)
		}
		if ec.Current() != 2 {
			t.Errorf("Expected clock to show epoch 2, got %d", ec.Current())
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Timeout waiting for epoch event")
	}
}

func TestEpochClockMultipleTicks(t *testing.T) {
	ec := NewEpochClock()

	// Subscribe to events
	eventCh := ec.Subscribe()

	// Start with a very fast interval for testing
	ec.Start(10 * time.Millisecond)
	defer ec.Stop()

	// Wait for 3 ticks
	for i := 0; i < 3; i++ {
		select {
		case event := <-eventCh:
			expectedPrev := uint64(i + 1)
			expectedCurr := uint64(i + 2)
			if event.PreviousEpoch != expectedPrev {
				t.Errorf("Tick %d: expected previous epoch %d, got %d", i, expectedPrev, event.PreviousEpoch)
			}
			if event.CurrentEpoch != expectedCurr {
				t.Errorf("Tick %d: expected current epoch %d, got %d", i, expectedCurr, event.CurrentEpoch)
			}
			// Verify no skips: current should always be previous + 1
			if event.CurrentEpoch != event.PreviousEpoch+1 {
				t.Errorf("Tick %d: epoch skip detected! prev=%d, curr=%d", i, event.PreviousEpoch, event.CurrentEpoch)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("Timeout waiting for epoch event %d", i)
			return
		}
	}

	// Verify final epoch
	if ec.Current() != 4 {
		t.Errorf("Expected clock to show epoch 4, got %d", ec.Current())
	}
}

func TestEpochClockSubscribeUnsubscribe(t *testing.T) {
	ec := NewEpochClock()

	// Subscribe
	ch := ec.Subscribe()

	// Unsubscribe
	ec.Unsubscribe(ch)

	// Channel should be closed
	_, ok := <-ch
	if ok {
		t.Error("Expected channel to be closed after unsubscribe")
	}
}

func TestEpochClockMultipleSubscribers(t *testing.T) {
	ec := NewEpochClock()

	// Create multiple subscribers
	ch1 := ec.Subscribe()
	ch2 := ec.Subscribe()
	ch3 := ec.Subscribe()

	// Start with a fast interval
	ec.Start(10 * time.Millisecond)
	defer ec.Stop()

	// All subscribers should receive the event
	for _, ch := range []chan EpochEvent{ch1, ch2, ch3} {
		select {
		case event := <-ch:
			if event.CurrentEpoch != 2 {
				t.Errorf("Expected epoch 2, got %d", event.CurrentEpoch)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Timeout waiting for epoch event on subscriber")
		}
	}
}

func TestEpochClockDoubleStart(t *testing.T) {
	ec := NewEpochClock()

	// Start twice should not panic or create multiple tickers
	ec.Start(50 * time.Millisecond)
	ec.Start(50 * time.Millisecond)

	defer ec.Stop()

	// Should still work normally
	if ec.Current() != 1 {
		t.Errorf("Expected epoch 1, got %d", ec.Current())
	}
}

func TestEpochClockStop(t *testing.T) {
	ec := NewEpochClock()

	eventCh := ec.Subscribe()

	// Start and immediately stop
	ec.Start(10 * time.Millisecond)
	time.Sleep(5 * time.Millisecond) // Let it start
	ec.Stop()

	// Wait a bit to ensure no more ticks
	initialEpoch := ec.Current()
	time.Sleep(50 * time.Millisecond)

	// Epoch should not have changed after stop
	// (or only changed by 1 if a tick was in progress)
	finalEpoch := ec.Current()
	if finalEpoch > initialEpoch+1 {
		t.Errorf("Epoch continued ticking after stop: initial=%d, final=%d", initialEpoch, finalEpoch)
	}

	// Clean up subscriber
	ec.Unsubscribe(eventCh)
}
