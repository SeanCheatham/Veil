package client

import (
	"testing"

	"github.com/veil-protocol/veil/pkg/relay"
)

func TestNewCoverTrafficGenerator(t *testing.T) {
	sender := NewSender(SenderConfig{
		RelayAddresses: []string{"relay-1:7000", "relay-2:7000", "relay-3:7000"},
		ValidatorAddrs: []string{"validator-1:9000"},
	})

	gen, err := NewCoverTrafficGenerator(CoverTrafficConfig{
		Sender:   sender,
		PoolAddr: "message-pool:8080",
	})
	if err != nil {
		t.Fatalf("NewCoverTrafficGenerator failed: %v", err)
	}

	if gen == nil {
		t.Fatal("generator is nil")
	}

	if gen.CoverReceiverKey == nil {
		t.Error("cover receiver key not generated")
	}

	if gen.Sender != sender {
		t.Error("sender not set correctly")
	}
}

func TestGenerateCoverMessage(t *testing.T) {
	sender := NewSender(SenderConfig{
		RelayAddresses: []string{"relay-1:7000", "relay-2:7000", "relay-3:7000"},
	})

	gen, err := NewCoverTrafficGenerator(CoverTrafficConfig{
		Sender:   sender,
		PoolAddr: "message-pool:8080",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Generate a cover message
	msg, err := gen.GenerateCoverMessage()
	if err != nil {
		t.Fatalf("GenerateCoverMessage failed: %v", err)
	}

	// Check minimum size
	minLen := relay.KeySize + relay.NonceSize + relay.OverheadSize + MinCoverPayloadSize
	if len(msg) < minLen {
		t.Errorf("cover message too short: %d < %d", len(msg), minLen)
	}

	// Check stats updated
	stats := gen.GetStats()
	if stats.GeneratedCount != 1 {
		t.Errorf("expected GeneratedCount=1, got %d", stats.GeneratedCount)
	}

	// Generate another
	_, err = gen.GenerateCoverMessage()
	if err != nil {
		t.Fatal(err)
	}

	stats = gen.GetStats()
	if stats.GeneratedCount != 2 {
		t.Errorf("expected GeneratedCount=2, got %d", stats.GeneratedCount)
	}
}

func TestTryDecryptCover(t *testing.T) {
	sender := NewSender(SenderConfig{
		RelayAddresses: []string{"relay-1:7000", "relay-2:7000", "relay-3:7000"},
	})

	gen, err := NewCoverTrafficGenerator(CoverTrafficConfig{
		Sender:   sender,
		PoolAddr: "message-pool:8080",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Generate a cover message
	msg, err := gen.GenerateCoverMessage()
	if err != nil {
		t.Fatal(err)
	}

	// Should be able to decrypt with cover key
	if !gen.tryDecryptCover(msg) {
		t.Error("failed to decrypt cover message with cover key")
	}

	// Generate a different message
	gen2, _ := NewCoverTrafficGenerator(CoverTrafficConfig{
		Sender:   sender,
		PoolAddr: "message-pool:8080",
	})
	msg2, _ := gen2.GenerateCoverMessage()

	// Should NOT be able to decrypt with original cover key
	if gen.tryDecryptCover(msg2) {
		t.Error("should not decrypt message from different generator")
	}
}

func TestCreateMixerCoverTrafficGenerator(t *testing.T) {
	sender := NewSender(SenderConfig{
		RelayAddresses: []string{"relay-1:7000", "relay-2:7000", "relay-3:7000"},
		ValidatorAddrs: []string{"validator-1:9000", "validator-2:9000"},
	})

	gen, err := NewCoverTrafficGenerator(CoverTrafficConfig{
		Sender:   sender,
		PoolAddr: "message-pool:8080",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get the mixer callback
	callback := gen.CreateMixerCoverTrafficGenerator()
	if callback == nil {
		t.Fatal("callback is nil")
	}

	// Call it to generate a mixed message
	mixedMsg := callback()
	if mixedMsg == nil {
		t.Fatal("mixed message is nil")
	}

	if mixedMsg.InboundID == "" {
		t.Error("inbound ID is empty")
	}

	if mixedMsg.OutboundID == "" {
		t.Error("outbound ID is empty")
	}

	if !mixedMsg.IsCoverTraffic {
		t.Error("IsCoverTraffic should be true")
	}

	if len(mixedMsg.Payload) == 0 {
		t.Error("payload is empty")
	}
}

func TestCoverTrafficStats(t *testing.T) {
	sender := NewSender(SenderConfig{
		RelayAddresses: []string{"relay-1:7000"},
	})

	gen, _ := NewCoverTrafficGenerator(CoverTrafficConfig{
		Sender:   sender,
		PoolAddr: "message-pool:8080",
	})

	// Initial stats
	stats := gen.GetStats()
	if stats.GeneratedCount != 0 {
		t.Errorf("expected GeneratedCount=0, got %d", stats.GeneratedCount)
	}
	if stats.ReachedPoolCount != 0 {
		t.Errorf("expected ReachedPoolCount=0, got %d", stats.ReachedPoolCount)
	}
}
