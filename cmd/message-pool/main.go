// Package main implements the message-pool service.
// The message pool is an append-only ciphertext store that holds encrypted messages.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
)

func main() {
	log.Println("message-pool starting...")

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service": "message-pool",
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	log.Printf("message-pool listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
