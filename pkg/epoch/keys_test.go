package epoch

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewKeyManager(t *testing.T) {
	clock := NewClock(100 * time.Millisecond)
	km := NewKeyManager(clock)

	if km == nil {
		t.Fatal("NewKeyManager returned nil")
	}
	if km.CurrentKey() != nil {
		t.Error("CurrentKey should be nil before clock starts")
	}
	if km.RotationCount() != 0 {
		t.Errorf("expected rotation count 0, got %d", km.RotationCount())
	}
}

func TestKeyManagerRotatesOnTick(t *testing.T) {
	clock := NewClock(30 * time.Millisecond)
	km := NewKeyManager(clock)

	clock.Start()
	defer clock.Stop()

	// Wait for initial tick
	time.Sleep(10 * time.Millisecond)

	key := km.CurrentKey()
	if key == nil {
		t.Fatal("CurrentKey should not be nil after clock starts")
	}
	if key.Epoch != 1 {
		t.Errorf("expected key epoch 1, got %d", key.Epoch)
	}
	if len(key.Key) != KeySize {
		t.Errorf("expected key size %d, got %d", KeySize, len(key.Key))
	}
	if key.ID == "" {
		t.Error("key ID should not be empty")
	}
}

func TestKeyManagerRotationCount(t *testing.T) {
	clock := NewClock(20 * time.Millisecond)
	km := NewKeyManager(clock)

	clock.Start()
	time.Sleep(70 * time.Millisecond)
	clock.Stop()

	if km.RotationCount() < 3 {
		t.Errorf("expected at least 3 rotations, got %d", km.RotationCount())
	}
}

func TestKeyManagerPreviousKey(t *testing.T) {
	clock := NewClock(30 * time.Millisecond)
	km := NewKeyManager(clock)

	clock.Start()
	time.Sleep(10 * time.Millisecond)

	// After first tick, there's no previous key yet
	if km.PreviousKey() != nil {
		t.Error("PreviousKey should be nil after first tick")
	}

	firstKey := km.CurrentKey()

	// Wait for second tick
	time.Sleep(40 * time.Millisecond)
	clock.Stop()

	// Now previous key should be the first key
	prevKey := km.PreviousKey()
	if prevKey == nil {
		t.Fatal("PreviousKey should not be nil after second tick")
	}
	if prevKey.ID != firstKey.ID {
		t.Errorf("PreviousKey should match first key: %s != %s", prevKey.ID, firstKey.ID)
	}
}

func TestKeyManagerOnRotate(t *testing.T) {
	clock := NewClock(30 * time.Millisecond)
	km := NewKeyManager(clock)

	var mu sync.Mutex
	var keys []*SessionKey

	km.OnRotate(func(key *SessionKey) {
		mu.Lock()
		keys = append(keys, key)
		mu.Unlock()
	})

	clock.Start()
	time.Sleep(80 * time.Millisecond)
	clock.Stop()

	mu.Lock()
	defer mu.Unlock()

	if len(keys) < 2 {
		t.Errorf("expected at least 2 rotation callbacks, got %d", len(keys))
	}

	// Verify each key has increasing epoch
	for i := 1; i < len(keys); i++ {
		if keys[i].Epoch != keys[i-1].Epoch+1 {
			t.Errorf("epochs should increase by 1: %d -> %d", keys[i-1].Epoch, keys[i].Epoch)
		}
	}
}

func TestKeyManagerForceRotate(t *testing.T) {
	clock := NewClock(1 * time.Hour) // Long duration
	km := NewKeyManager(clock)

	err := km.ForceRotate(5)
	if err != nil {
		t.Fatalf("ForceRotate failed: %v", err)
	}

	key := km.CurrentKey()
	if key == nil {
		t.Fatal("CurrentKey should not be nil after ForceRotate")
	}
	if key.Epoch != 5 {
		t.Errorf("expected epoch 5, got %d", key.Epoch)
	}
}

func TestKeyManagerMultipleHandlers(t *testing.T) {
	clock := NewClock(30 * time.Millisecond)
	km := NewKeyManager(clock)

	var count1, count2 int32

	km.OnRotate(func(key *SessionKey) {
		atomic.AddInt32(&count1, 1)
	})

	km.OnRotate(func(key *SessionKey) {
		atomic.AddInt32(&count2, 1)
	})

	clock.Start()
	time.Sleep(70 * time.Millisecond)
	clock.Stop()

	c1 := atomic.LoadInt32(&count1)
	c2 := atomic.LoadInt32(&count2)

	if c1 != c2 {
		t.Errorf("both handlers should be called same number of times: %d != %d", c1, c2)
	}
}

func TestKeyManagerHasRotatedOnce(t *testing.T) {
	clock := NewClock(30 * time.Millisecond)
	km := NewKeyManager(clock)

	if km.HasRotatedOnce() {
		t.Error("HasRotatedOnce should be false before any rotations")
	}

	clock.Start()
	time.Sleep(10 * time.Millisecond)

	// After first tick, we have a key but no rotation yet
	if km.HasRotatedOnce() {
		t.Error("HasRotatedOnce should be false after first tick (no prior key to rotate from)")
	}

	// Wait for second tick
	time.Sleep(40 * time.Millisecond)
	clock.Stop()

	if !km.HasRotatedOnce() {
		t.Error("HasRotatedOnce should be true after second tick")
	}
}

func TestKeyManagerValidateKeyForEpoch(t *testing.T) {
	clock := NewClock(1 * time.Hour)
	km := NewKeyManager(clock)

	// Generate keys for epochs 1, 2, 3
	km.ForceRotate(1)
	key1 := km.CurrentKey()

	km.ForceRotate(2)
	key2 := km.CurrentKey()

	km.ForceRotate(3)
	key3 := km.CurrentKey()

	// Current key (epoch 3) should be valid for epoch 3
	if !km.ValidateKeyForEpoch(key3.ID, 3) {
		t.Error("current key should be valid for current epoch")
	}

	// Previous key (epoch 2) should be valid in current epoch (grace period)
	if !km.ValidateKeyForEpoch(key2.ID, 3) {
		t.Error("previous key should be valid in current epoch (grace period)")
	}

	// Old key (epoch 1) should not be valid
	if km.ValidateKeyForEpoch(key1.ID, 3) {
		t.Error("old key should not be valid")
	}

	// Nonexistent key should not be valid
	if km.ValidateKeyForEpoch("nonexistent", 3) {
		t.Error("nonexistent key should not be valid")
	}
}

func TestGenerateSessionKey(t *testing.T) {
	key1, err := generateSessionKey(1)
	if err != nil {
		t.Fatalf("generateSessionKey failed: %v", err)
	}

	if len(key1.Key) != KeySize {
		t.Errorf("expected key size %d, got %d", KeySize, len(key1.Key))
	}
	if key1.Epoch != 1 {
		t.Errorf("expected epoch 1, got %d", key1.Epoch)
	}
	if key1.ID == "" {
		t.Error("key ID should not be empty")
	}

	// Two keys should be different
	key2, _ := generateSessionKey(1)
	if key1.ID == key2.ID {
		t.Error("two generated keys should have different IDs")
	}
}

func TestKeyManagerConcurrentAccess(t *testing.T) {
	clock := NewClock(10 * time.Millisecond)
	km := NewKeyManager(clock)

	clock.Start()
	defer clock.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = km.CurrentKey()
				_ = km.PreviousKey()
				_ = km.RotationCount()
				_ = km.HasRotatedOnce()
			}
		}()
	}

	wg.Wait()
}
