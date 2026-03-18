// Package main implements the sender-workload test driver.
// This workload generates and sends test messages through the Veil network.
package main

import (
	"log"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
)

func main() {
	log.Println("sender-workload starting...")

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service": "sender-workload",
	})

	log.Println("sender-workload ready")

	// TODO: Implement message sending logic
	// For now, just keep running
	select {}
}
