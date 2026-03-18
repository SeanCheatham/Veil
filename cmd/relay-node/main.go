// Package main implements the relay-node service.
// Relay nodes handle onion layer peeling and mix-and-forward of messages.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
)

func main() {
	log.Println("relay-node starting...")

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service": "relay-node",
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("relay-node listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
