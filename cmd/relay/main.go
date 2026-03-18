// Package main implements the Veil relay node service.
// Relays perform onion layer peeling and mix-and-forward
// operations to ensure sender anonymity.
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/veil-protocol/veil/pkg/relay"
)

const (
	// DefaultPort is the default HTTP server port.
	DefaultPort = 7000

	// DefaultEpochDuration is the default epoch duration.
	DefaultEpochDuration = 30 * time.Second
)

func main() {
	log.Println("veil-relay: starting relay node...")

	// Parse configuration from environment
	id := os.Getenv("VEIL_RELAY_ID")
	if id == "" {
		id = "1"
	}

	peersStr := os.Getenv("VEIL_RELAY_PEERS")
	var peers []string
	if peersStr != "" {
		for _, p := range strings.Split(peersStr, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				peers = append(peers, p)
			}
		}
	}

	validatorAddr := os.Getenv("VEIL_VALIDATOR_ADDR")
	if validatorAddr == "" {
		validatorAddr = "validator-1:9000"
	}

	port := os.Getenv("VEIL_RELAY_PORT")
	if port == "" {
		port = fmt.Sprintf("%d", DefaultPort)
	}

	log.Printf("veil-relay: id=%s peers=%v validator=%s port=%s", id, peers, validatorAddr, port)

	// Create relay configuration
	cfg := relay.RelayConfig{
		ID:            id,
		PeerAddresses: peers,
		ValidatorAddr: validatorAddr,
		EpochDuration: DefaultEpochDuration,
	}

	// Create relay
	r, err := relay.NewRelay(cfg)
	if err != nil {
		log.Fatalf("veil-relay: failed to create relay: %v", err)
	}

	// Start relay
	if err := r.Start(); err != nil {
		log.Fatalf("veil-relay: failed to start relay: %v", err)
	}

	// Create and start HTTP server
	addr := fmt.Sprintf(":%s", port)
	server := relay.NewServer(r, addr)

	// Start server in background
	go func() {
		if err := server.ListenAndServe(); err != nil {
			log.Printf("veil-relay: server error: %v", err)
		}
	}()

	log.Printf("veil-relay: relay %s running on port %s", id, port)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("veil-relay: shutting down...")

	// Graceful shutdown
	server.Shutdown()
	r.Stop()

	log.Println("veil-relay: shutdown complete")
}
