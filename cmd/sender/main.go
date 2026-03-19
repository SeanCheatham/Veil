package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil-protocol/veil/pkg/cover"
	"github.com/veil-protocol/veil/pkg/crypto"
	"github.com/veil-protocol/veil/pkg/epoch"
	"github.com/veil-protocol/veil/pkg/routing"
)

type sentMessage struct {
	MessageID string `json:"message_id"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

var (
	mu       sync.Mutex
	sentMsgs []sentMessage

	// keysMu protects receiverPubKey and relays which are updated by epoch callback
	keysMu        sync.RWMutex
	receiverPubKey crypto.PublicKey
	relays        []routing.RelayInfo
)

func generateUUID() string {
	var buf [16]byte
	_, _ = io.ReadFull(rand.Reader, buf[:])
	// Set version 4 and variant bits
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

func discoverReceiverPubKey(host, port string) (crypto.PublicKey, bool) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s:%s/pubkey", host, port))
	if err != nil {
		return crypto.PublicKey{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return crypto.PublicKey{}, false
	}
	var result struct {
		PublicKey string `json:"public_key"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	decoded, err := base64.StdEncoding.DecodeString(result.PublicKey)
	if err != nil || len(decoded) != 32 {
		return crypto.PublicKey{}, false
	}
	var pk crypto.PublicKey
	copy(pk[:], decoded)
	return pk, true
}

func discoverRelayPubKeys() ([]routing.RelayInfo, bool) {
	client := &http.Client{Timeout: 5 * time.Second}
	discovered := make([]routing.RelayInfo, 5)
	for n := 1; n <= 5; n++ {
		host := fmt.Sprintf("relay-%d", n)
		resp, err := client.Get(fmt.Sprintf("http://%s:8083/pubkey", host))
		if err != nil {
			return nil, false
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, false
		}
		var result struct {
			RelayID   string `json:"relay_id"`
			PublicKey string `json:"public_key"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		decoded, err := base64.StdEncoding.DecodeString(result.PublicKey)
		if err != nil || len(decoded) != 32 {
			return nil, false
		}
		var pk crypto.PublicKey
		copy(pk[:], decoded)
		discovered[n-1] = routing.RelayInfo{
			ID:     result.RelayID,
			Host:   fmt.Sprintf("%s:8083", host),
			PubKey: pk,
		}
	}
	return discovered, true
}

func main() {
	receiverHost := os.Getenv("RECEIVER_HOST")
	if receiverHost == "" {
		receiverHost = "receiver"
	}
	receiverPort := os.Getenv("RECEIVER_PORT")
	if receiverPort == "" {
		receiverPort = "8085"
	}

	// Discover receiver public key with backoff
	log.Println("discovering receiver public key...")
	for attempt := 1; ; attempt++ {
		pk, ok := discoverReceiverPubKey(receiverHost, receiverPort)
		if ok {
			keysMu.Lock()
			receiverPubKey = pk
			keysMu.Unlock()
			log.Printf("discovered receiver pubkey on attempt %d", attempt)
			break
		}
		wait := time.Duration(attempt) * time.Second
		if wait > 10*time.Second {
			wait = 10 * time.Second
		}
		log.Printf("attempt %d: receiver pubkey not available, retrying in %v...", attempt, wait)
		time.Sleep(wait)
	}

	// Discover relay public keys with backoff
	log.Println("discovering relay public keys...")
	for attempt := 1; ; attempt++ {
		discovered, ok := discoverRelayPubKeys()
		if ok {
			keysMu.Lock()
			relays = discovered
			keysMu.Unlock()
			log.Printf("discovered all relay pubkeys on attempt %d", attempt)
			break
		}
		wait := time.Duration(attempt) * time.Second
		if wait > 10*time.Second {
			wait = 10 * time.Second
		}
		log.Printf("attempt %d: relay pubkeys not available, retrying in %v...", attempt, wait)
		time.Sleep(wait)
	}

	// Start epoch manager
	epochDuration := epoch.DurationFromEnv()
	epochMgr := epoch.NewManager(epochDuration)

	// Register epoch callback to re-discover keys
	epochMgr.OnEpochTick(func(e uint64) {
		// Re-discover receiver pubkey
		pk, ok := discoverReceiverPubKey(receiverHost, receiverPort)
		if ok {
			keysMu.Lock()
			receiverPubKey = pk
			keysMu.Unlock()
			log.Printf("epoch %d: re-discovered receiver pubkey", e)
		} else {
			log.Printf("epoch %d: receiver pubkey re-discovery failed, keeping old key", e)
		}

		// Re-discover relay pubkeys
		discovered, ok := discoverRelayPubKeys()
		if ok {
			keysMu.Lock()
			relays = discovered
			keysMu.Unlock()
			log.Printf("epoch %d: re-discovered all relay pubkeys", e)
		} else {
			log.Printf("epoch %d: relay pubkey re-discovery failed, keeping old keys", e)
		}

		assert.Sometimes(true, "sender_rediscovered_keys", map[string]any{
			"epoch": e,
		})
	})

	epochMgr.Start()
	log.Printf("epoch manager started with duration %v", epochDuration)

	// Signal setup complete after discovery
	lifecycle.SetupComplete(map[string]any{
		"service": "sender",
	})
	log.Println("sender setup complete, starting message loop")

	// Start HTTP server in background
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/sent", handleSent)
	go func() {
		log.Fatal(http.ListenAndServe(":8084", nil))
	}()

	// Message sending loop
	msgCount := 0
	for {
		time.Sleep(2 * time.Second)
		msgCount++

		msgID := generateUUID()
		ts := time.Now().UTC().Format(time.RFC3339)
		content := fmt.Sprintf("test message %d", msgCount)

		plaintext, _ := json.Marshal(map[string]string{
			"message_id": msgID,
			"content":    content,
			"timestamp":  ts,
		})

		// Read keys under lock
		keysMu.RLock()
		currentReceiverPubKey := receiverPubKey
		currentRelays := make([]routing.RelayInfo, len(relays))
		copy(currentRelays, relays)
		keysMu.RUnlock()

		// Select random route of 3 relays
		route, err := routing.SelectRoute(currentRelays, 3, 3)
		if err != nil {
			log.Printf("route selection failed: %v", err)
			continue
		}

		relayPubKeys := make([]crypto.PublicKey, len(route))
		relayHosts := make([]string, len(route))
		for i, r := range route {
			relayPubKeys[i] = r.PubKey
			relayHosts[i] = r.Host
		}

		wrapped, err := crypto.WrapMessage(plaintext, currentReceiverPubKey, relayPubKeys, relayHosts)
		if err != nil {
			log.Printf("wrap message failed: %v", err)
			continue
		}

		ciphertextB64 := base64.StdEncoding.EncodeToString(wrapped)
		forwardBody, _ := json.Marshal(map[string]string{"ciphertext": ciphertextB64})

		firstRelayHost := route[0].Host
		forwardURL := fmt.Sprintf("http://%s/forward", firstRelayHost)

		resp, err := http.Post(forwardURL, "application/json", bytes.NewReader(forwardBody))
		if err != nil {
			log.Printf("forward to %s failed: %v", firstRelayHost, err)
			continue
		}
		resp.Body.Close()

		mu.Lock()
		sentMsgs = append(sentMsgs, sentMessage{
			MessageID: msgID,
			Content:   content,
			Timestamp: ts,
		})
		mu.Unlock()

		assert.Sometimes(true, "sender_sent_message", map[string]any{
			"message_id": msgID,
		})

		log.Printf("sent message %s via %s", msgID, firstRelayHost)

		// Send 1-2 cover messages after each real message
		coverCount := 1
		if msgCount%2 == 0 {
			coverCount = 2
		}
		for c := 0; c < coverCount; c++ {
			coverRoute, err := routing.SelectRoute(currentRelays, 3, 3)
			if err != nil {
				log.Printf("cover route selection failed: %v", err)
				continue
			}
			coverPubKeys := make([]crypto.PublicKey, len(coverRoute))
			coverHosts := make([]string, len(coverRoute))
			for i, r := range coverRoute {
				coverPubKeys[i] = r.PubKey
				coverHosts[i] = r.Host
			}
			coverMsg, err := cover.GenerateCoverMessage(coverPubKeys, coverHosts)
			if err != nil {
				log.Printf("cover message generation failed: %v", err)
				continue
			}
			coverB64 := base64.StdEncoding.EncodeToString(coverMsg)
			coverBody, _ := json.Marshal(map[string]string{"ciphertext": coverB64})
			coverRelayHost := coverRoute[0].Host
			coverURL := fmt.Sprintf("http://%s/forward", coverRelayHost)
			coverResp, err := http.Post(coverURL, "application/json", bytes.NewReader(coverBody))
			if err != nil {
				log.Printf("cover forward to %s failed: %v", coverRelayHost, err)
				continue
			}
			coverResp.Body.Close()

			assert.Sometimes(true, "cover_traffic_sent", map[string]any{
				"epoch": epochMgr.GetCurrentEpoch(),
			})
			log.Printf("sent cover message via %s (epoch %d)", coverRelayHost, epochMgr.GetCurrentEpoch())
		}
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handleSent(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	msgs := make([]sentMessage, len(sentMsgs))
	copy(msgs, sentMsgs)
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"messages": msgs,
		"count":    len(msgs),
	})
}
