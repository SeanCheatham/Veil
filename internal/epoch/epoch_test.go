package epoch

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestCurrentEpoch(t *testing.T) {
	config := EpochConfig{
		DurationSeconds:    60,
		GracePeriodSeconds: 10,
	}
	em := NewEpochManager(config)

	// Set a fixed clock
	fixedTime := time.Unix(120, 0) // 2 minutes after epoch 0
	em.SetClock(func() time.Time { return fixedTime })

	epoch := em.CurrentEpoch()
	if epoch != 2 {
		t.Errorf("expected epoch 2, got %d", epoch)
	}

	// Test epoch 0
	em.SetClock(func() time.Time { return time.Unix(0, 0) })
	epoch = em.CurrentEpoch()
	if epoch != 0 {
		t.Errorf("expected epoch 0, got %d", epoch)
	}

	// Test epoch boundary
	em.SetClock(func() time.Time { return time.Unix(59, 0) })
	epoch = em.CurrentEpoch()
	if epoch != 0 {
		t.Errorf("expected epoch 0 at 59s, got %d", epoch)
	}

	em.SetClock(func() time.Time { return time.Unix(60, 0) })
	epoch = em.CurrentEpoch()
	if epoch != 1 {
		t.Errorf("expected epoch 1 at 60s, got %d", epoch)
	}
}

func TestPreviousEpoch(t *testing.T) {
	config := EpochConfig{
		DurationSeconds:    60,
		GracePeriodSeconds: 10,
	}
	em := NewEpochManager(config)

	// Epoch 2 -> previous is 1
	em.SetClock(func() time.Time { return time.Unix(120, 0) })
	if prev := em.PreviousEpoch(); prev != 1 {
		t.Errorf("expected previous epoch 1, got %d", prev)
	}

	// Epoch 0 -> previous is 0 (no negative epochs)
	em.SetClock(func() time.Time { return time.Unix(30, 0) })
	if prev := em.PreviousEpoch(); prev != 0 {
		t.Errorf("expected previous epoch 0 for epoch 0, got %d", prev)
	}
}

func TestIsInGracePeriod(t *testing.T) {
	config := EpochConfig{
		DurationSeconds:    60,
		GracePeriodSeconds: 10,
	}
	em := NewEpochManager(config)

	tests := []struct {
		timestamp int64
		expected  bool
		desc      string
	}{
		{60, true, "exactly at epoch boundary"},
		{61, true, "1s into epoch"},
		{69, true, "9s into epoch (last second of grace)"},
		{70, false, "10s into epoch (just past grace)"},
		{90, false, "30s into epoch"},
		{119, false, "end of epoch"},
		{120, true, "next epoch boundary"},
	}

	for _, tc := range tests {
		em.SetClock(func() time.Time { return time.Unix(tc.timestamp, 0) })
		result := em.IsInGracePeriod()
		if result != tc.expected {
			t.Errorf("%s (t=%d): expected grace=%v, got %v", tc.desc, tc.timestamp, tc.expected, result)
		}
	}
}

func TestGetValidEpochs(t *testing.T) {
	config := EpochConfig{
		DurationSeconds:    60,
		GracePeriodSeconds: 10,
	}
	em := NewEpochManager(config)

	// Not in grace period - only current epoch valid
	em.SetClock(func() time.Time { return time.Unix(90, 0) }) // epoch 1, 30s in
	valid := em.GetValidEpochs()
	if len(valid) != 1 || valid[0] != 1 {
		t.Errorf("expected [1], got %v", valid)
	}

	// In grace period - both current and previous valid
	em.SetClock(func() time.Time { return time.Unix(65, 0) }) // epoch 1, 5s in (grace)
	valid = em.GetValidEpochs()
	if len(valid) != 2 || valid[0] != 1 || valid[1] != 0 {
		t.Errorf("expected [1, 0], got %v", valid)
	}

	// Epoch 0 in grace period - can't have previous
	em.SetClock(func() time.Time { return time.Unix(5, 0) }) // epoch 0, 5s in
	valid = em.GetValidEpochs()
	if len(valid) != 1 || valid[0] != 0 {
		t.Errorf("expected [0] for epoch 0 in grace, got %v", valid)
	}
}

func TestTimeUntilNextEpoch(t *testing.T) {
	config := EpochConfig{
		DurationSeconds:    60,
		GracePeriodSeconds: 10,
	}
	em := NewEpochManager(config)

	em.SetClock(func() time.Time { return time.Unix(90, 0) }) // 30s into epoch 1
	remaining := em.TimeUntilNextEpoch()
	expected := 30 * time.Second
	if remaining != expected {
		t.Errorf("expected %v, got %v", expected, remaining)
	}

	em.SetClock(func() time.Time { return time.Unix(60, 0) }) // exactly at epoch 1 start
	remaining = em.TimeUntilNextEpoch()
	expected = 60 * time.Second
	if remaining != expected {
		t.Errorf("expected %v, got %v", expected, remaining)
	}
}

func TestDeriveEpochKeyPair(t *testing.T) {
	// Use a test master seed
	masterSeed := make([]byte, 32)
	for i := range masterSeed {
		masterSeed[i] = byte(i)
	}

	// Derive key pair for relay 0, epoch 1
	kp1, err := DeriveEpochKeyPair(masterSeed, 0, 1)
	if err != nil {
		t.Fatalf("failed to derive key pair: %v", err)
	}

	// Verify key pair is valid
	if len(kp1.Public) != 32 {
		t.Errorf("expected 32-byte public key, got %d", len(kp1.Public))
	}
	if len(kp1.Private) != 32 {
		t.Errorf("expected 32-byte private key, got %d", len(kp1.Private))
	}

	// Derive same key pair again - should be identical (deterministic)
	kp2, err := DeriveEpochKeyPair(masterSeed, 0, 1)
	if err != nil {
		t.Fatalf("failed to derive key pair second time: %v", err)
	}

	if kp1.Public.Base64() != kp2.Public.Base64() {
		t.Error("key derivation is not deterministic - public keys differ")
	}
	if kp1.Private.Base64() != kp2.Private.Base64() {
		t.Error("key derivation is not deterministic - private keys differ")
	}
}

func TestDeriveEpochKeyPairDifferentEpochs(t *testing.T) {
	masterSeed := make([]byte, 32)
	for i := range masterSeed {
		masterSeed[i] = byte(i)
	}

	// Different epochs should produce different keys
	kp1, _ := DeriveEpochKeyPair(masterSeed, 0, 1)
	kp2, _ := DeriveEpochKeyPair(masterSeed, 0, 2)

	if kp1.Public.Base64() == kp2.Public.Base64() {
		t.Error("different epochs should produce different public keys")
	}
	if kp1.Private.Base64() == kp2.Private.Base64() {
		t.Error("different epochs should produce different private keys")
	}
}

func TestDeriveEpochKeyPairDifferentRelays(t *testing.T) {
	masterSeed := make([]byte, 32)
	for i := range masterSeed {
		masterSeed[i] = byte(i)
	}

	// Different relays should produce different keys (even with same seed)
	kp1, _ := DeriveEpochKeyPair(masterSeed, 0, 1)
	kp2, _ := DeriveEpochKeyPair(masterSeed, 1, 1)

	if kp1.Public.Base64() == kp2.Public.Base64() {
		t.Error("different relays should produce different public keys")
	}
}

func TestDeriveEpochKeyPairInvalidSeed(t *testing.T) {
	// Too short
	_, err := DeriveEpochKeyPair(make([]byte, 16), 0, 1)
	if err == nil {
		t.Error("expected error for short seed")
	}

	// Too long
	_, err = DeriveEpochKeyPair(make([]byte, 64), 0, 1)
	if err == nil {
		t.Error("expected error for long seed")
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	if config.DurationSeconds != DefaultDurationSeconds {
		t.Errorf("expected duration %d, got %d", DefaultDurationSeconds, config.DurationSeconds)
	}
	if config.GracePeriodSeconds != DefaultGracePeriodSeconds {
		t.Errorf("expected grace period %d, got %d", DefaultGracePeriodSeconds, config.GracePeriodSeconds)
	}
}

func TestNewEpochManagerDefaults(t *testing.T) {
	// Test with zero config - should use defaults
	em := NewEpochManager(EpochConfig{})
	config := em.GetConfig()
	if config.DurationSeconds != DefaultDurationSeconds {
		t.Errorf("expected default duration, got %d", config.DurationSeconds)
	}
	if config.GracePeriodSeconds != DefaultGracePeriodSeconds {
		t.Errorf("expected default grace period, got %d", config.GracePeriodSeconds)
	}

	// Test with grace period >= duration - should be clamped
	em = NewEpochManager(EpochConfig{
		DurationSeconds:    60,
		GracePeriodSeconds: 60,
	})
	config = em.GetConfig()
	if config.GracePeriodSeconds >= config.DurationSeconds {
		t.Errorf("grace period should be less than duration: grace=%d, duration=%d",
			config.GracePeriodSeconds, config.DurationSeconds)
	}
}

func TestBuildHKDFInfo(t *testing.T) {
	info := buildHKDFInfo(0, 1)
	expected := "veil-relay-0-epoch-1"
	if string(info) != expected {
		t.Errorf("expected %q, got %q", expected, string(info))
	}

	info = buildHKDFInfo(4, 12345)
	expected = "veil-relay-4-epoch-12345"
	if string(info) != expected {
		t.Errorf("expected %q, got %q", expected, string(info))
	}
}

func TestEpochKeyPairFromSeed(t *testing.T) {
	// Create a known seed
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i * 7)
	}
	seedB64 := base64.StdEncoding.EncodeToString(seed)

	kp, err := EpochKeyPairFromSeed(seedB64, 2, 5)
	if err != nil {
		t.Fatalf("failed to derive key from base64 seed: %v", err)
	}

	// Verify it matches direct derivation
	kpDirect, _ := DeriveEpochKeyPair(seed, 2, 5)
	if kp.Public.Base64() != kpDirect.Public.Base64() {
		t.Error("base64 derivation should match direct derivation")
	}
}

func TestGetTransitionCount(t *testing.T) {
	config := EpochConfig{
		DurationSeconds:    60,
		GracePeriodSeconds: 10,
	}
	em := NewEpochManager(config)

	// Initial count should be 0
	if count := em.GetTransitionCount(); count != 0 {
		t.Errorf("expected initial transition count 0, got %d", count)
	}

	// First epoch access doesn't count as transition (from 0 to 0)
	em.SetClock(func() time.Time { return time.Unix(30, 0) })
	em.CurrentEpoch()
	// Note: lastEpoch starts at 0, and we're in epoch 0, so no transition

	// Move to epoch 1 - should count as transition
	em.SetClock(func() time.Time { return time.Unix(90, 0) })
	em.CurrentEpoch()
	if count := em.GetTransitionCount(); count != 1 {
		t.Errorf("expected transition count 1 after moving to epoch 1, got %d", count)
	}

	// Move to epoch 2 - should increment
	em.SetClock(func() time.Time { return time.Unix(150, 0) })
	em.CurrentEpoch()
	if count := em.GetTransitionCount(); count != 2 {
		t.Errorf("expected transition count 2 after moving to epoch 2, got %d", count)
	}

	// Stay in epoch 2 - should not increment
	em.CurrentEpoch()
	if count := em.GetTransitionCount(); count != 2 {
		t.Errorf("expected transition count to stay at 2, got %d", count)
	}
}
