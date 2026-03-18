// Package main implements the Veil message pool service.
// The message pool is an append-only ciphertext store that receives
// messages from the relay network after BFT consensus ordering.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/veil-protocol/veil/pkg/pool"
)

func main() {
	log.Println("veil-pool: starting message pool service...")

	port := os.Getenv("VEIL_POOL_PORT")
	if port == "" {
		port = "8080"
	}

	// Create the message pool
	p := pool.New()

	// Create and start the HTTP server
	addr := ":" + port
	server := pool.NewServer(p, addr)

	// Start server in a goroutine
	go func() {
		log.Printf("veil-pool: listening on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("veil-pool: server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("veil-pool: shutting down...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(); err != nil {
		log.Printf("veil-pool: shutdown error: %v", err)
	}

	// Wait for context to complete
	<-ctx.Done()
	log.Println("veil-pool: shutdown complete")
}
