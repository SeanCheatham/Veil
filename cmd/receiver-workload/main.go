// Package main implements the receiver-workload test driver.
// This workload polls the message pool and verifies message delivery.
package main

import (
	"log"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
)

func main() {
	log.Println("receiver-workload starting...")

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service": "receiver-workload",
	})

	log.Println("receiver-workload ready")

	// TODO: Implement message receiving logic
	// For now, just keep running
	select {}
}
