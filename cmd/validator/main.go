// Package main implements the Veil validator node service.
// Validators run BFT consensus to order messages before they
// are committed to the message pool.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/veil-protocol/veil/pkg/consensus"
)

func main() {
	log.Println("veil-validator: starting validator node...")

	// Parse environment variables
	id := os.Getenv("VEIL_VALIDATOR_ID")
	if id == "" {
		log.Fatal("veil-validator: VEIL_VALIDATOR_ID is required")
	}

	peersStr := os.Getenv("VEIL_VALIDATOR_PEERS")
	var peers []string
	if peersStr != "" {
		peers = strings.Split(peersStr, ",")
		for i := range peers {
			peers[i] = strings.TrimSpace(peers[i])
		}
	}

	poolAddr := os.Getenv("VEIL_POOL_ADDR")
	if poolAddr == "" {
		poolAddr = "message-pool:8080"
	}

	port := os.Getenv("VEIL_VALIDATOR_PORT")
	if port == "" {
		port = "9000"
	}

	log.Printf("veil-validator: id=%s peers=%v pool=%s port=%s", id, peers, poolAddr, port)

	// Create validator
	cfg := consensus.ValidatorConfig{
		ID:            id,
		Peers:         peers,
		PoolAddr:      poolAddr,
		MaxBatchSize:  10,
		BatchTimeout:  2 * time.Second,
		EpochDuration: 30 * time.Second,
	}

	validator, err := consensus.NewValidator(cfg)
	if err != nil {
		log.Fatalf("veil-validator: failed to create validator: %v", err)
	}

	// Start validator
	validator.Start()

	// Create and start HTTP server
	addr := ":" + port
	server := consensus.NewServer(validator, addr)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("veil-validator: server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("veil-validator: shutting down...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Stop validator first
	validator.Stop()

	// Then stop server
	if err := server.Shutdown(); err != nil {
		log.Printf("veil-validator: shutdown error: %v", err)
	}

	<-ctx.Done()
	log.Println("veil-validator: shutdown complete")
}
