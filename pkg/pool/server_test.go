package pool

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlePostMessage(t *testing.T) {
	p := New()
	server := NewServer(p, ":8080")

	ciphertext := []byte("encrypted message")
	reqBody := PostMessageRequest{
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", w.Code)
	}

	var resp PostMessageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.ID == "" {
		t.Error("response ID should not be empty")
	}
}

func TestHandlePostMessageInvalidJSON(t *testing.T) {
	p := New()
	server := NewServer(p, ":8080")

	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestHandlePostMessageEmptyCiphertext(t *testing.T) {
	p := New()
	server := NewServer(p, ":8080")

	reqBody := PostMessageRequest{Ciphertext: ""}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestHandleGetMessage(t *testing.T) {
	p := New()
	server := NewServer(p, ":8080")

	// First, add a message
	ciphertext := []byte("secret data")
	id, _ := p.Add(ciphertext)

	req := httptest.NewRequest(http.MethodGet, "/messages/"+id, nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp GetMessageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.ID != id {
		t.Errorf("expected ID %s, got %s", id, resp.ID)
	}

	decoded, _ := base64.StdEncoding.DecodeString(resp.Ciphertext)
	if !bytes.Equal(decoded, ciphertext) {
		t.Error("ciphertext mismatch")
	}
}

func TestHandleGetMessageNotFound(t *testing.T) {
	p := New()
	server := NewServer(p, ":8080")

	req := httptest.NewRequest(http.MethodGet, "/messages/nonexistent", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestHandleListMessages(t *testing.T) {
	p := New()
	server := NewServer(p, ":8080")

	// Add some messages
	id1, _ := p.Add([]byte("msg1"))
	id2, _ := p.Add([]byte("msg2"))

	req := httptest.NewRequest(http.MethodGet, "/messages", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp ListMessagesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("expected count 2, got %d", resp.Count)
	}
	if len(resp.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(resp.Messages))
	}
	if resp.Messages[0] != id1 || resp.Messages[1] != id2 {
		t.Error("message order incorrect")
	}
}

func TestHandleHealth(t *testing.T) {
	p := New()
	server := NewServer(p, ":8080")

	p.Add([]byte("test"))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != "healthy" {
		t.Errorf("expected status healthy, got %s", resp.Status)
	}
	if resp.MessageCount != 1 {
		t.Errorf("expected message count 1, got %d", resp.MessageCount)
	}
}
