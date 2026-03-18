// Package main implements the Veil workload driver.
// This service generates test traffic and runs as part of
// the Antithesis test harness.
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.Println("veil-workload: starting workload driver...")

	// TODO: Implement workload
	// - Message generation with onion encryption
	// - Sender patterns (random, burst, steady)
	// - Receiver polling and verification
	// - Antithesis SDK assertions for message_forwarding, cover_traffic

	relayAddrs := os.Getenv("VEIL_RELAY_ADDRS")
	poolAddr := os.Getenv("VEIL_POOL_ADDR")

	log.Printf("veil-workload: relays=%s pool=%s", relayAddrs, poolAddr)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("veil-workload: shutting down")
}
