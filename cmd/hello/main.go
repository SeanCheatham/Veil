package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
)

func main() {
	// Bootstrap assertion: fires once at startup to prove the SDK is wired correctly.
	assert.Always(true, "hello_service_started", map[string]any{
		"service": "hello",
		"event":   "startup",
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Signal to Antithesis that the system is ready for testing.
	lifecycle.SetupComplete(map[string]any{
		"service": "hello",
		"message": "hello service is ready",
	})

	fmt.Println("hello service listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
