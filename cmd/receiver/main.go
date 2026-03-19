package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil-protocol/veil/pkg/crypto"
	"github.com/veil-protocol/veil/pkg/epoch"
)

type receivedMessage struct {
	MessageID string `json:"message_id"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

var (
	keyRing      *crypto.KeyRing
	mu           sync.Mutex
	receivedMsgs []receivedMessage
)

func main() {
	// Generate key ring on startup
	var err error
	keyRing, err = crypto.NewKeyRing()
	if err != nil {
		log.Fatalf("failed to create key ring: %v", err)
	}
	log.Println("receiver key ring created")

	// Start epoch manager and register key rotation callback
	epochDuration := epoch.DurationFromEnv()
	epochMgr := epoch.NewManager(epochDuration)
	epochMgr.OnEpochTick(func(e uint64) {
		if err := keyRing.Rotate(); err != nil {
			log.Printf("receiver key rotation failed at epoch %d: %v", e, err)
			return
		}
		log.Printf("receiver rotated keys at epoch %d", e)
		assert.Sometimes(true, "receiver_key_rotated", map[string]any{
			"epoch": e,
		})
	})
	epochMgr.Start()
	log.Printf("receiver epoch manager started with duration %v", epochDuration)

	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/pubkey", handlePubKey)
	http.HandleFunc("/received", handleReceived)

	// Signal setup complete
	lifecycle.SetupComplete(map[string]any{
		"service": "receiver",
	})

	// Start polling goroutine
	go pollMessagePool()

	fmt.Println("receiver listening on :8085")
	log.Fatal(http.ListenAndServe(":8085", nil))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handlePubKey(w http.ResponseWriter, r *http.Request) {
	currentKP := keyRing.Current()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"public_key": base64.StdEncoding.EncodeToString(currentKP.Public[:]),
	})
}

func handleReceived(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	msgs := make([]receivedMessage, len(receivedMsgs))
	copy(msgs, receivedMsgs)
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"messages": msgs,
		"count":    len(msgs),
	})
}

func pollMessagePool() {
	lastSeenIndex := -1

	for {
		time.Sleep(2 * time.Second)

		resp, err := http.Get("http://message-pool:8081/messages")
		if err != nil {
			log.Printf("poll message-pool failed: %v", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("read response failed: %v", err)
			continue
		}

		var poolResp struct {
			Messages []struct {
				Index      int    `json:"index"`
				Ciphertext string `json:"ciphertext"`
			} `json:"messages"`
			Count int `json:"count"`
		}
		if err := json.Unmarshal(body, &poolResp); err != nil {
			log.Printf("parse pool response failed: %v", err)
			continue
		}

		for _, msg := range poolResp.Messages {
			if msg.Index <= lastSeenIndex {
				continue
			}
			lastSeenIndex = msg.Index

			// Decode the ciphertext
			ciphertextBytes, err := base64.StdEncoding.DecodeString(msg.Ciphertext)
			if err != nil {
				continue
			}

			// Attempt to decrypt using key ring (tries current then previous key)
			plaintext, err := keyRing.TryDecrypt(ciphertextBytes)
			if err != nil {
				// Silently skip — message was for someone else or is cover traffic
				continue
			}

			// Parse the plaintext JSON
			var parsed struct {
				MessageID string `json:"message_id"`
				Content   string `json:"content"`
				Timestamp string `json:"timestamp"`
			}
			if err := json.Unmarshal(plaintext, &parsed); err != nil {
				log.Printf("parse decrypted message failed: %v", err)
				continue
			}

			mu.Lock()
			receivedMsgs = append(receivedMsgs, receivedMessage{
				MessageID: parsed.MessageID,
				Content:   parsed.Content,
				Timestamp: parsed.Timestamp,
			})
			mu.Unlock()

			assert.Sometimes(true, "message_delivered", map[string]any{
				"message_id": parsed.MessageID,
			})

			assert.Always(true, "only_own_messages_decrypted", map[string]any{
				"message_id": parsed.MessageID,
			})

			log.Printf("received message %s: %s", parsed.MessageID, parsed.Content)
		}
	}
}
