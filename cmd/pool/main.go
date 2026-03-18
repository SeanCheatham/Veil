// Package main implements the Veil message pool service.
// The message pool is an append-only ciphertext store that receives
// messages from the relay network after BFT consensus ordering.
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.Println("veil-pool: starting message pool service...")

	// TODO: Implement message pool
	// - HTTP server for message submission
	// - Append-only storage
	// - Message integrity verification
	// - Antithesis SDK assertions for message_integrity

	port := os.Getenv("VEIL_POOL_PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("veil-pool: listening on port %s", port)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("veil-pool: shutting down")
}
