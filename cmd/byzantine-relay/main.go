// Package main implements the byzantine-relay service for adversarial testing.
// This relay variant exhibits malicious behaviors to test system fault tolerance.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	mathrand "math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/veil-protocol/veil/internal/properties"
	"github.com/veil-protocol/veil/internal/relay"
)

// ByzantineBehavior defines the interface for malicious relay behaviors.
type ByzantineBehavior interface {
	// Name returns the name of this behavior for logging/observability.
	Name() string
	// Execute performs the byzantine action. Returns modified blob, outboundID, and whether to forward.
	Execute(inboundID string, blob []byte, epoch uint64, r *relay.Relay) (modifiedBlob []byte, outboundID string, shouldForward bool)
}

// DropMessage silently drops 100% of messages it handles (selected 10% of the time).
type DropMessage struct{}

func (d *DropMessage) Name() string {
	return "drop_message"
}

func (d *DropMessage) Execute(inboundID string, blob []byte, epoch uint64, r *relay.Relay) ([]byte, string, bool) {
	log.Printf("[BYZANTINE-%s] Dropping message %s", r.RelayID(), inboundID)
	return nil, "", false // Do not forward
}

// CorruptPayload flips random bits in the blob before forwarding.
type CorruptPayload struct{}

func (c *CorruptPayload) Name() string {
	return "corrupt_payload"
}

func (c *CorruptPayload) Execute(inboundID string, blob []byte, epoch uint64, r *relay.Relay) ([]byte, string, bool) {
	if len(blob) == 0 {
		return blob, inboundID, true
	}

	// Make a copy to corrupt
	corrupted := make([]byte, len(blob))
	copy(corrupted, blob)

	// Flip 1-5 random bits
	numBits, _ := rand.Int(rand.Reader, big.NewInt(5))
	flips := int(numBits.Int64()) + 1

	for i := 0; i < flips; i++ {
		byteIdx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(corrupted))))
		bitIdx, _ := rand.Int(rand.Reader, big.NewInt(8))
		corrupted[byteIdx.Int64()] ^= 1 << bitIdx.Int64()
	}

	log.Printf("[BYZANTINE-%s] Corrupted %d bits in message %s", r.RelayID(), flips, inboundID)
	return corrupted, inboundID, true
}

// ReplayMessage forwards the same message twice with different outbound IDs.
type ReplayMessage struct{}

func (r *ReplayMessage) Name() string {
	return "replay_message"
}

func (rm *ReplayMessage) Execute(inboundID string, blob []byte, epoch uint64, r *relay.Relay) ([]byte, string, bool) {
	log.Printf("[BYZANTINE-%s] Will replay message %s", r.RelayID(), inboundID)
	// Return special marker - the handler will detect this and send twice
	return blob, "REPLAY:" + inboundID, true
}

// DelayMessage holds messages for 5-10 seconds before forwarding.
type DelayMessage struct{}

func (d *DelayMessage) Name() string {
	return "delay_message"
}

func (d *DelayMessage) Execute(inboundID string, blob []byte, epoch uint64, r *relay.Relay) ([]byte, string, bool) {
	// Random delay between 5-10 seconds
	delayMs, _ := rand.Int(rand.Reader, big.NewInt(5000))
	delay := time.Duration(5000+delayMs.Int64()) * time.Millisecond

	log.Printf("[BYZANTINE-%s] Delaying message %s for %v", r.RelayID(), inboundID, delay)
	time.Sleep(delay)

	return blob, inboundID, true
}

// HonestBehavior is the default non-malicious behavior.
type HonestBehavior struct{}

func (h *HonestBehavior) Name() string {
	return "honest"
}

func (h *HonestBehavior) Execute(inboundID string, blob []byte, epoch uint64, r *relay.Relay) ([]byte, string, bool) {
	// Normal honest relay behavior - just forward as-is
	return blob, inboundID, true
}

// ByzantineRelay wraps a regular relay with byzantine behavior injection.
type ByzantineRelay struct {
	relay     *relay.Relay
	behaviors []ByzantineBehavior
	rng       *mathrand.Rand
}

// NewByzantineRelay creates a new byzantine relay that wraps a regular relay.
func NewByzantineRelay(nodeID, epochClockURL, relayPeers, validatorEndpoints string) *ByzantineRelay {
	return &ByzantineRelay{
		relay: relay.NewRelay(nodeID, epochClockURL, relayPeers, validatorEndpoints),
		behaviors: []ByzantineBehavior{
			&DropMessage{},
			&CorruptPayload{},
			&ReplayMessage{},
			&DelayMessage{},
		},
		rng: mathrand.New(mathrand.NewSource(time.Now().UnixNano())),
	}
}

// selectBehavior randomly selects a behavior with 20% chance of byzantine action.
// Returns the selected behavior (byzantine or honest).
func (br *ByzantineRelay) selectBehavior() ByzantineBehavior {
	// 20% chance of any byzantine action (5% each for 4 behaviors)
	roll := br.rng.Intn(100)
	if roll < 20 {
		// Select one of the 4 byzantine behaviors (5% each)
		behaviorIdx := roll / 5
		if behaviorIdx >= len(br.behaviors) {
			behaviorIdx = len(br.behaviors) - 1
		}
		return br.behaviors[behaviorIdx]
	}
	return &HonestBehavior{}
}

var byzantineRelay *ByzantineRelay

// handleMessage handles POST /message with potential byzantine behavior.
func handleMessage(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg relay.MessageRequest
	if err := json.NewDecoder(req.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if msg.ID == "" {
		http.Error(w, "Missing message ID", http.StatusBadRequest)
		return
	}
	if len(msg.Blob) == 0 {
		http.Error(w, "Missing message blob", http.StatusBadRequest)
		return
	}

	// Select behavior randomly
	behavior := byzantineRelay.selectBehavior()
	behaviorName := behavior.Name()

	// Execute the behavior
	modifiedBlob, outboundID, shouldForward := behavior.Execute(msg.ID, msg.Blob, msg.Epoch, byzantineRelay.relay)

	// Report byzantine input to Antithesis if non-honest behavior was selected
	if behaviorName != "honest" {
		properties.ObserveByzantineInput(true, byzantineRelay.relay.RelayID(), behaviorName)
		log.Printf("[BYZANTINE-%s] Applied behavior: %s to message %s", byzantineRelay.relay.RelayID(), behaviorName, msg.ID)
	}

	if !shouldForward {
		// Drop the message - still return success to sender
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(relay.MessageResponse{
			Status: "accepted",
			MsgID:  msg.ID,
		})
		return
	}

	// Check for replay marker
	if strings.HasPrefix(outboundID, "REPLAY:") {
		originalID := strings.TrimPrefix(outboundID, "REPLAY:")
		// Forward twice - first with modified ID
		if err := byzantineRelay.relay.OnMessage(originalID+"-replay-1", modifiedBlob, msg.Epoch); err != nil {
			log.Printf("[BYZANTINE-%s] First replay forward failed for %s: %v", byzantineRelay.relay.RelayID(), msg.ID, err)
		}
		// Second with different ID
		if err := byzantineRelay.relay.OnMessage(originalID+"-replay-2", modifiedBlob, msg.Epoch); err != nil {
			log.Printf("[BYZANTINE-%s] Second replay forward failed for %s: %v", byzantineRelay.relay.RelayID(), msg.ID, err)
		}
	} else {
		// Normal forward through the relay (with potentially modified blob)
		if err := byzantineRelay.relay.OnMessage(msg.ID, modifiedBlob, msg.Epoch); err != nil {
			log.Printf("[BYZANTINE-%s] Failed to process message %s: %v", byzantineRelay.relay.RelayID(), msg.ID, err)
			http.Error(w, "Failed to process message: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(relay.MessageResponse{
		Status: "accepted",
		MsgID:  msg.ID,
	})
}

// handleStatus handles GET /status to return relay status information.
func handleStatus(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := byzantineRelay.relay.Status()

	// Add byzantine indicator
	response := map[string]interface{}{
		"node_id":        status.NodeID,
		"inbound_count":  status.InboundCount,
		"outbound_count": status.OutboundCount,
		"peers":          status.Peers,
		"validator_count": status.ValidatorCount,
		"byzantine":      true,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleHealth handles GET /health for health checks.
func handleHealth(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
	})
}

// handlePubkey handles GET /pubkey/:epoch to return the public key for an epoch.
func handlePubkey(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(req.URL.Path, "/pubkey/")
	if path == "" || path == req.URL.Path {
		http.Error(w, "Missing epoch in URL path", http.StatusBadRequest)
		return
	}

	var epoch uint64
	_, err := json.Number(path).Int64()
	if err != nil {
		var e int
		if _, scanErr := json.Number(path).Int64(); scanErr != nil {
			http.Error(w, "Invalid epoch number", http.StatusBadRequest)
			return
		}
		epoch = uint64(e)
	} else {
		n, _ := json.Number(path).Int64()
		epoch = uint64(n)
	}

	pubKey := byzantineRelay.relay.PublicKey(epoch)
	if pubKey == nil {
		http.Error(w, "No public key available for epoch", http.StatusNotFound)
		return
	}

	pubKeyBytes := pubKey.Bytes()
	pubKeyBase64 := base64.StdEncoding.EncodeToString(pubKeyBytes)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"relay_id":   byzantineRelay.relay.RelayID(),
		"epoch":      epoch,
		"public_key": pubKeyBase64,
	})
}

func main() {
	// Configuration from environment variables
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	nodeID := os.Getenv("NODE_ID")
	if nodeID == "" {
		nodeID = "byzantine-relay-1"
	}

	epochClockURL := os.Getenv("EPOCH_CLOCK_URL")
	if epochClockURL == "" {
		epochClockURL = "http://epoch-clock:8083"
	}

	relayPeers := os.Getenv("RELAY_PEERS")
	if relayPeers == "" {
		relayPeers = "http://relay-node-1:8081,http://relay-node-2:8081,http://relay-node-3:8081,http://relay-node-4:8081,http://relay-node-5:8081"
	}

	validatorEndpoints := os.Getenv("VALIDATOR_ENDPOINTS")
	if validatorEndpoints == "" {
		validatorEndpoints = "http://validator-node-1:8082,http://validator-node-2:8082,http://validator-node-3:8082"
	}

	// Create byzantine relay instance
	byzantineRelay = NewByzantineRelay(nodeID, epochClockURL, relayPeers, validatorEndpoints)

	log.Printf("Byzantine Relay %s starting on port %s", nodeID, port)
	log.Printf("Epoch clock: %s", epochClockURL)
	log.Printf("Relay peers: %s", relayPeers)
	log.Printf("Validator endpoints: %s", validatorEndpoints)
	log.Printf("Byzantine behaviors enabled: DropMessage (5%%), CorruptPayload (5%%), ReplayMessage (5%%), DelayMessage (5%%)")

	// Set up HTTP handlers
	http.HandleFunc("/message", handleMessage)
	http.HandleFunc("/status", handleStatus)
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/pubkey/", handlePubkey)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the relay's session key manager
	go func() {
		maxRetries := 10
		for i := 0; i < maxRetries; i++ {
			if err := byzantineRelay.relay.Start(ctx); err != nil {
				log.Printf("Failed to start byzantine relay (attempt %d/%d): %v", i+1, maxRetries, err)
				if i < maxRetries-1 {
					time.Sleep(time.Duration(i+1) * time.Second)
					continue
				}
				log.Printf("Warning: Running without epoch clock connection")
			}
			break
		}
	}()

	// Set up graceful shutdown
	server := &http.Server{Addr: ":" + port}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		log.Println("Shutting down...")
		cancel()
		byzantineRelay.relay.Stop()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
	}()

	log.Printf("Byzantine relay node service starting on port %s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}

	log.Println("Byzantine relay node stopped")
}
