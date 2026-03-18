package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
)

// Message represents a stored message in the pool
type Message struct {
	ID        int       `json:"id"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// MessagePool is a thread-safe append-only message store
type MessagePool struct {
	mu       sync.RWMutex
	messages []Message
}

// NewMessagePool creates a new message pool
func NewMessagePool() *MessagePool {
	return &MessagePool{
		messages: make([]Message, 0),
	}
}

// Append adds a message to the pool and returns its index
func (mp *MessagePool) Append(content string) Message {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	msg := Message{
		ID:        len(mp.messages),
		Content:   content,
		Timestamp: time.Now(),
	}
	mp.messages = append(mp.messages, msg)
	return msg
}

// Get retrieves a message by index
func (mp *MessagePool) Get(index int) (Message, bool) {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	if index < 0 || index >= len(mp.messages) {
		return Message{}, false
	}
	return mp.messages[index], true
}

// GetAll returns all messages
func (mp *MessagePool) GetAll() []Message {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	// Return a copy to prevent race conditions
	result := make([]Message, len(mp.messages))
	copy(result, mp.messages)
	return result
}

// Count returns the number of messages
func (mp *MessagePool) Count() int {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return len(mp.messages)
}

var pool = NewMessagePool()

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/messages", messagesHandler)
	mux.HandleFunc("/messages/", messageByIndexHandler)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service": "message-pool",
		"port":    port,
	})

	log.Printf("Message-pool service starting on port %s", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]string{
		"status":  "healthy",
		"service": "message-pool",
	}
	json.NewEncoder(w).Encode(response)
}

func messagesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		getMessagesHandler(w, r)
	case http.MethodPost:
		postMessageHandler(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func getMessagesHandler(w http.ResponseWriter, r *http.Request) {
	messages := pool.GetAll()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(messages)
}

type postMessageRequest struct {
	Content string `json:"content"`
}

func postMessageHandler(w http.ResponseWriter, r *http.Request) {
	var req postMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Content == "" {
		http.Error(w, "Content is required", http.StatusBadRequest)
		return
	}

	msg := pool.Append(req.Content)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(msg)
}

func messageByIndexHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract index from path: /messages/{index}
	path := strings.TrimPrefix(r.URL.Path, "/messages/")
	index, err := strconv.Atoi(path)
	if err != nil {
		http.Error(w, "Invalid message index", http.StatusBadRequest)
		return
	}

	msg, found := pool.Get(index)
	if !found {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(msg)
}
