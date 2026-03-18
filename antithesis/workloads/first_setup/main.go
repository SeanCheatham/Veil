package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
)

func main() {
	log.Println("first_setup workload starting...")

	// Get the hello service URL from environment
	helloURL := os.Getenv("HELLO_URL")
	if helloURL == "" {
		helloURL = "http://hello:8080"
	}

	// Wait for hello service to be available
	healthURL := helloURL + "/health"
	maxRetries := 30
	retryInterval := time.Second

	var healthy bool
	for i := 0; i < maxRetries; i++ {
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var result map[string]string
			if json.Unmarshal(body, &result) == nil && result["status"] == "healthy" {
				healthy = true
				log.Printf("Hello service is healthy after %d attempts", i+1)
				break
			}
		}
		if resp != nil {
			resp.Body.Close()
		}
		log.Printf("Waiting for hello service... attempt %d/%d", i+1, maxRetries)
		time.Sleep(retryInterval)
	}

	// Assert that the service became reachable
	assert.Always(healthy, "hello_service_reachable", map[string]any{
		"url":         healthURL,
		"max_retries": maxRetries,
	})

	if !healthy {
		log.Fatal("Hello service did not become healthy")
	}

	// Signal setup complete - the service is reachable
	lifecycle.SetupComplete(map[string]any{
		"workload":      "first_setup",
		"hello_healthy": true,
	})

	fmt.Println("SUCCESS: services_reachable property validated")
	fmt.Println("first_setup workload completed successfully")
}
