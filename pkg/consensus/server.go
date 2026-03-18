// Package consensus implements the BFT consensus layer for Veil validators.
package consensus

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
)

// Server provides an HTTP API for the validator.
type Server struct {
	validator *Validator
	mux       *http.ServeMux
	server    *http.Server
}

// NewServer creates a new HTTP server for the validator.
func NewServer(validator *Validator, addr string) *Server {
	s := &Server{
		validator: validator,
		mux:       http.NewServeMux(),
	}

	// External API for relays/clients
	s.mux.HandleFunc("POST /submit", s.handleSubmit)
	s.mux.HandleFunc("GET /status", s.handleStatus)

	// Internal API for peer communication
	s.mux.HandleFunc("POST /propose", s.handlePropose)
	s.mux.HandleFunc("POST /vote", s.handleVote)

	// Health check
	s.mux.HandleFunc("GET /health", s.handleHealth)

	s.server = &http.Server{
		Addr:    addr,
		Handler: s.mux,
	}

	return s
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	log.Printf("validator %s: server listening on %s", s.validator.ID, s.server.Addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() error {
	return s.server.Close()
}

// handleSubmit handles POST /submit - receives messages from relays for ordering.
func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req SubmitRequest
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

	// Submit for consensus
	id, err := s.validator.SubmitMessage(ciphertext)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp := SubmitResponse{ID: id}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(resp)
}

// handleStatus handles GET /status - returns validator status.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := s.validator.GetStatus()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handlePropose handles POST /propose - receives batch proposals from leader.
func (s *Server) handlePropose(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var proposal ProposalRequest
	if err := json.Unmarshal(body, &proposal); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if proposal.Hash == "" {
		http.Error(w, "hash is required", http.StatusBadRequest)
		return
	}

	if proposal.ProposerID == "" {
		http.Error(w, "proposer_id is required", http.StatusBadRequest)
		return
	}

	// Process proposal and vote
	voted := s.validator.HandleProposal(&proposal)

	if !voted {
		http.Error(w, "proposal rejected", http.StatusConflict)
		return
	}

	resp := VoteResponse{
		VoterID: s.validator.ID,
		Voted:   true,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleVote handles POST /vote - receives votes from peers (for explicit voting).
// In our simple BFT, votes are sent as part of the proposal response,
// but this endpoint allows for explicit vote messages.
func (s *Server) handleVote(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var vote VoteRequest
	if err := json.Unmarshal(body, &vote); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Record vote for the batch
	s.validator.RecordVote(vote.BatchHash, vote.VoterID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"accepted": true})
}

// handleHealth handles GET /health - returns health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := s.validator.GetStatus()

	resp := HealthResponse{
		Status:    "healthy",
		ID:        s.validator.ID,
		IsLeader:  status.IsLeader,
		Epoch:     status.CurrentEpoch,
		LastSeq:   status.LastCommittedSeq,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// VoteRequest is the request body for POST /vote.
type VoteRequest struct {
	BatchHash string `json:"batch_hash"`
	VoterID   string `json:"voter_id"`
}

// HealthResponse is the response body for GET /health.
type HealthResponse struct {
	Status   string `json:"status"`
	ID       string `json:"id"`
	IsLeader bool   `json:"is_leader"`
	Epoch    uint64 `json:"epoch"`
	LastSeq  uint64 `json:"last_seq"`
}
