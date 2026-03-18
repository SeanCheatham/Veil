// Package main implements the validator-node service.
// Validator nodes participate in BFT consensus to order messages in the pool.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
)

func main() {
	log.Println("validator-node starting...")

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service": "validator-node",
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("validator-node listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
