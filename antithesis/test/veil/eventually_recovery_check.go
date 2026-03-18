// Command eventually_recovery_check is an Antithesis test command that runs with faults paused
// to verify the system recovers to a healthy state after fault injection.
// It checks service health, consensus consistency, and message pool accessibility.
// It runs as an "eventually_" prefixed command when Antithesis pauses faults.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// ValidatorStatus is the status response from a validator node.
type ValidatorStatus struct {
	ID            int            `json:"id"`
	PeerCount     int            `json:"peer_count"`
	ProposalCount int64          `json:"proposal_count"`
	Consensus     ConsensusState `json:"consensus,omitempty"`
}

// ConsensusState is the consensus-specific status.
type ConsensusState struct {
	ValidatorID    int      `json:"validator_id"`
	ValidatorCount int      `json:"validator_count"`
	NextSequence   uint64   `json:"next_sequence"`
	CommittedSeq   uint64   `json:"committed_seq"`
	PendingCount   int      `json:"pending_count"`
	PendingSeqs    []uint64 `json:"pending_seqs"`
}

var (
	messagePoolURL = "http://message-pool:8082"
	validatorURLs  = []string{
		"http://validator-node0:8081",
		"http://validator-node1:8081",
		"http://validator-node2:8081",
	}
	relayURLs = []string{
		"http://relay-node0:8080",
		"http://relay-node1:8080",
		"http://relay-node2:8080",
		"http://relay-node3:8080",
		"http://relay-node4:8080",
	}
)

const (
	maxRetries     = 10
	retryDelay     = 2 * time.Second
	maxPendingOK   = 50 // Maximum pending count before we consider consensus stuck
	maxSeqDiff     = 3  // Maximum committed sequence difference between validators
)

func main() {
	log.Println("eventually_recovery_check: verifying system recovery after faults paused")

	// Override from environment if set
	if url := os.Getenv("MESSAGE_POOL_URL"); url != "" {
		messagePoolURL = url
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Step 1: Wait for all services to become healthy
	log.Println("eventually_recovery_check: waiting for services to become healthy")

	messagePoolHealthy := waitForHealth(httpClient, messagePoolURL+"/health", "message-pool")
	validatorsHealthy := waitForValidatorsHealthy(httpClient)
	relaysHealthy := waitForRelaysHealthy(httpClient)

	allServicesHealthy := messagePoolHealthy && validatorsHealthy && relaysHealthy

	// Assertion: All services should recover to healthy state
	assert.Always(allServicesHealthy, "All services recover to healthy state after faults paused", map[string]any{
		"message_pool_healthy": messagePoolHealthy,
		"validators_healthy":   validatorsHealthy,
		"relays_healthy":       relaysHealthy,
	})

	if !allServicesHealthy {
		log.Printf("eventually_recovery_check: WARNING - not all services healthy: pool=%t, validators=%t, relays=%t",
			messagePoolHealthy, validatorsHealthy, relaysHealthy)
	}

	// Step 2: Check validator consensus consistency
	log.Println("eventually_recovery_check: checking validator consensus consistency")

	var committedSeqs []uint64
	var pendingCounts []int
	var reachableCount int

	for _, url := range validatorURLs {
		status, err := getValidatorStatus(httpClient, url)
		if err != nil {
			log.Printf("eventually_recovery_check: could not get status from %s: %v", url, err)
			continue
		}
		reachableCount++
		committedSeqs = append(committedSeqs, status.Consensus.CommittedSeq)
		pendingCounts = append(pendingCounts, status.Consensus.PendingCount)
	}

	// Check consensus agreement
	consensusAgreed := true
	if len(committedSeqs) > 1 {
		first := committedSeqs[0]
		for _, seq := range committedSeqs[1:] {
			diff := int64(seq) - int64(first)
			if diff < 0 {
				diff = -diff
			}
			if diff > maxSeqDiff {
				consensusAgreed = false
				break
			}
		}
	}

	// Assertion: After recovery, validators should have consistent committed sequences
	assert.Always(consensusAgreed, "Validators have consistent committed sequences after recovery", map[string]any{
		"committed_seqs":  committedSeqs,
		"reachable_count": reachableCount,
		"max_diff":        maxSeqDiff,
	})

	if !consensusAgreed {
		log.Printf("eventually_recovery_check: WARNING - validators disagree on committed sequence: %v", committedSeqs)
	}

	// Check for stuck consensus (too many pending)
	totalPending := 0
	for _, pc := range pendingCounts {
		totalPending += pc
	}

	consensusNotStuck := totalPending <= maxPendingOK

	// Assertion: Consensus should not be stuck with many pending proposals
	assert.Always(consensusNotStuck, "Consensus is not stuck after recovery", map[string]any{
		"total_pending":    totalPending,
		"pending_counts":   pendingCounts,
		"max_pending_ok":   maxPendingOK,
		"reachable_count":  reachableCount,
	})

	if !consensusNotStuck {
		log.Printf("eventually_recovery_check: WARNING - consensus may be stuck with %d pending", totalPending)
	}

	// Step 3: Verify message pool is accessible and responds
	log.Println("eventually_recovery_check: verifying message pool accessibility")

	poolAccessible := false
	var messageCount int

	resp, err := httpClient.Get(messagePoolURL + "/messages?since=0")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			poolAccessible = true
			var messages []map[string]any
			if json.NewDecoder(resp.Body).Decode(&messages) == nil {
				messageCount = len(messages)
			}
		}
	}

	// Assertion: Message pool should be accessible after recovery
	assert.Always(poolAccessible, "Message pool is accessible after recovery", map[string]any{
		"accessible":    poolAccessible,
		"message_count": messageCount,
	})

	if !poolAccessible {
		log.Printf("eventually_recovery_check: WARNING - message pool not accessible")
	}

	// Summary
	log.Printf("eventually_recovery_check: completed. Services healthy=%t, Consensus agreed=%t, Not stuck=%t, Pool accessible=%t",
		allServicesHealthy, consensusAgreed, consensusNotStuck, poolAccessible)

	// Overall success
	allRecovered := allServicesHealthy && consensusAgreed && consensusNotStuck && poolAccessible
	if allRecovered {
		log.Println("eventually_recovery_check: SUCCESS - system fully recovered")
	} else {
		log.Println("eventually_recovery_check: WARNING - system may not be fully recovered")
	}

	os.Exit(0)
}

func waitForHealth(client *http.Client, url, name string) bool {
	for i := 0; i < maxRetries; i++ {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				log.Printf("eventually_recovery_check: %s is healthy (attempt %d)", name, i+1)
				return true
			}
		}

		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	log.Printf("eventually_recovery_check: %s did not become healthy after %d attempts", name, maxRetries)
	return false
}

func waitForValidatorsHealthy(client *http.Client) bool {
	healthyCount := 0
	for i, url := range validatorURLs {
		if waitForHealth(client, url+"/health", fmt.Sprintf("validator-%d", i)) {
			healthyCount++
		}
	}

	// All validators should be healthy
	allHealthy := healthyCount == len(validatorURLs)
	log.Printf("eventually_recovery_check: %d/%d validators healthy", healthyCount, len(validatorURLs))
	return allHealthy
}

func waitForRelaysHealthy(client *http.Client) bool {
	healthyCount := 0
	for i, url := range relayURLs {
		if waitForHealth(client, url+"/health", fmt.Sprintf("relay-%d", i)) {
			healthyCount++
		}
	}

	// All relays should be healthy
	allHealthy := healthyCount == len(relayURLs)
	log.Printf("eventually_recovery_check: %d/%d relays healthy", healthyCount, len(relayURLs))
	return allHealthy
}

func getValidatorStatus(client *http.Client, baseURL string) (ValidatorStatus, error) {
	resp, err := client.Get(baseURL + "/status")
	if err != nil {
		return ValidatorStatus{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ValidatorStatus{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	var status ValidatorStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return ValidatorStatus{}, fmt.Errorf("decode failed: %w", err)
	}

	return status, nil
}
