// Package main implements the Veil validator node service.
// Validators run BFT consensus to order messages before they
// are committed to the message pool.
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.Println("veil-validator: starting validator node...")

	// TODO: Implement validator
	// - BFT consensus protocol
	// - Peer discovery and communication
	// - Batch ordering and commitment
	// - Antithesis SDK assertions for validator_agreement, chain_progression

	id := os.Getenv("VEIL_VALIDATOR_ID")
	peers := os.Getenv("VEIL_VALIDATOR_PEERS")
	poolAddr := os.Getenv("VEIL_POOL_ADDR")

	log.Printf("veil-validator: id=%s peers=%s pool=%s", id, peers, poolAddr)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("veil-validator: shutting down")
}
