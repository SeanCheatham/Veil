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
	var receiverPubKey crypto.PublicKey
	log.Println("discovering receiver public key...")
	for attempt := 1; ; attempt++ {
		resp, err := http.Get(fmt.Sprintf("http://%s:%s/pubkey", receiverHost, receiverPort))
		if err == nil && resp.StatusCode == 200 {
			var result struct {
				PublicKey string `json:"public_key"`
			}
			json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()
			decoded, err := base64.StdEncoding.DecodeString(result.PublicKey)
			if err == nil && len(decoded) == 32 {
				copy(receiverPubKey[:], decoded)
				log.Printf("discovered receiver pubkey on attempt %d", attempt)
				break
			}
		}
		if resp != nil {
			resp.Body.Close()
		}
		wait := time.Duration(attempt) * time.Second
		if wait > 10*time.Second {
			wait = 10 * time.Second
		}
		log.Printf("attempt %d: receiver pubkey not available, retrying in %v...", attempt, wait)
		time.Sleep(wait)
	}

	// Discover relay public keys with backoff
	type relayDiscovery struct {
		ID     string
		Host   string
		PubKey crypto.PublicKey
	}
	relays := make([]routing.RelayInfo, 5)
	for n := 1; n <= 5; n++ {
		host := fmt.Sprintf("relay-%d", n)
		log.Printf("discovering relay %s public key...", host)
		for attempt := 1; ; attempt++ {
			resp, err := http.Get(fmt.Sprintf("http://%s:8083/pubkey", host))
			if err == nil && resp.StatusCode == 200 {
				var result struct {
					RelayID   string `json:"relay_id"`
					PublicKey string `json:"public_key"`
				}
				json.NewDecoder(resp.Body).Decode(&result)
				resp.Body.Close()
				decoded, err := base64.StdEncoding.DecodeString(result.PublicKey)
				if err == nil && len(decoded) == 32 {
					var pk crypto.PublicKey
					copy(pk[:], decoded)
					relays[n-1] = routing.RelayInfo{
						ID:     result.RelayID,
						Host:   fmt.Sprintf("%s:8083", host),
						PubKey: pk,
					}
					log.Printf("discovered relay %s pubkey on attempt %d", host, attempt)
					break
				}
			}
			if resp != nil {
				resp.Body.Close()
			}
			wait := time.Duration(attempt) * time.Second
			if wait > 10*time.Second {
				wait = 10 * time.Second
			}
			log.Printf("attempt %d: relay %s not available, retrying in %v...", attempt, host, wait)
			time.Sleep(wait)
		}
	}

	// Start epoch manager
	epochDuration := epoch.DurationFromEnv()
	epochMgr := epoch.NewManager(epochDuration)
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

		// Select random route of 3 relays
		route, err := routing.SelectRoute(relays, 3, 3)
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

		wrapped, err := crypto.WrapMessage(plaintext, receiverPubKey, relayPubKeys, relayHosts)
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
			coverRoute, err := routing.SelectRoute(relays, 3, 3)
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
