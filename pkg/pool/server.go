// Package pool implements the Veil message pool HTTP server.
package pool

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil-protocol/veil/pkg/antithesis"
)

// Server provides an HTTP API for the message pool.
type Server struct {
	pool   *Pool
	mux    *http.ServeMux
	server *http.Server
}

// NewServer creates a new HTTP server for the message pool.
func NewServer(pool *Pool, addr string) *Server {
	s := &Server{
		pool: pool,
		mux:  http.NewServeMux(),
	}

	s.mux.HandleFunc("POST /messages", s.handlePostMessage)
	s.mux.HandleFunc("GET /messages", s.handleListMessages)
	s.mux.HandleFunc("GET /messages/{id}", s.handleGetMessage)
	s.mux.HandleFunc("GET /health", s.handleHealth)

	s.server = &http.Server{
		Addr:    addr,
		Handler: s.mux,
	}

	return s
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	log.Printf("pool server listening on %s", s.server.Addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() error {
	return s.server.Close()
}

// PostMessageRequest is the request body for POST /messages.
type PostMessageRequest struct {
	// Ciphertext is the base64-encoded encrypted message.
	Ciphertext string `json:"ciphertext"`
}

// PostMessageResponse is the response body for POST /messages.
type PostMessageResponse struct {
	ID string `json:"id"`
}

// handlePostMessage handles POST /messages - adds a new message to the pool.
func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req PostMessageRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Ciphertext == "" {
		http.Error(w, "ciphertext is required", http.StatusBadRequest)
		return
	}

	// Decode base64 ciphertext
	ciphertext, err := base64.StdEncoding.DecodeString(req.Ciphertext)
	if err != nil {
		http.Error(w, "invalid base64 ciphertext", http.StatusBadRequest)
		return
	}

	id, err := s.pool.Add(ciphertext)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("added message: %s", id)

	resp := PostMessageResponse{ID: id}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// GetMessageResponse is the response body for GET /messages/{id}.
type GetMessageResponse struct {
	ID         string `json:"id"`
	Ciphertext string `json:"ciphertext"` // base64 encoded
	Timestamp  string `json:"timestamp"`
}

// handleGetMessage handles GET /messages/{id} - retrieves a message by ID.
// This handler includes the message_integrity Antithesis assertion.
func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		// Fallback for older Go versions or manual path parsing
		path := r.URL.Path
		prefix := "/messages/"
		if strings.HasPrefix(path, prefix) {
			id = strings.TrimPrefix(path, prefix)
		}
	}

	if id == "" {
		http.Error(w, "message ID is required", http.StatusBadRequest)
		return
	}

	msg, integrityOK, err := s.pool.Get(id)
	if err == ErrMessageNotFound {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Antithesis assertion: message_integrity
	// This safety property asserts that no message is ever modified in transit.
	// A single counterexample (integrityOK == false) disproves the property.
	assert.Always(
		integrityOK,
		antithesis.MessageIntegrity,
		map[string]any{
			"message_id":   id,
			"integrity_ok": integrityOK,
		},
	)

	// Even if integrity check fails in Antithesis testing, we still return the message
	// so the test can observe the failure. In production, you might want to error here.
	if !integrityOK {
		log.Printf("INTEGRITY VIOLATION: message %s content does not match hash", id)
	}

	resp := GetMessageResponse{
		ID:         msg.ID,
		Ciphertext: base64.StdEncoding.EncodeToString(msg.Ciphertext),
		Timestamp:  msg.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ListMessagesResponse is the response body for GET /messages.
type ListMessagesResponse struct {
	Messages []string `json:"messages"`
	Count    int      `json:"count"`
}

// handleListMessages handles GET /messages - lists all message IDs.
func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	ids := s.pool.List()

	resp := ListMessagesResponse{
		Messages: ids,
		Count:    len(ids),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HealthResponse is the response body for GET /health.
type HealthResponse struct {
	Status       string `json:"status"`
	MessageCount int    `json:"message_count"`
}

// handleHealth handles GET /health - returns pool health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:       "healthy",
		MessageCount: s.pool.Count(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
