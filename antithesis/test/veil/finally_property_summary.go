// Command finally_property_summary is an Antithesis test command that runs at the end
// to log a human-readable summary of all property states and system statistics.
// It does not add new assertions - just reporting for debugging and analysis.
// It runs once at the end as a "finally_" prefixed command.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// MessagePoolMessage represents a message from the pool.
type MessagePoolMessage struct {
	Index             int    `json:"index"`
	Payload           []byte `json:"payload"`
	ConsensusSequence *int64 `json:"consensus_sequence,omitempty"`
}

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

// RelayStatus matches the status response from relay nodes.
type RelayStatus struct {
	ID            int    `json:"id"`
	ForwardCount  int64  `json:"forward_count"`
	PublicKey     string `json:"public_key"`
	CurrentEpoch  uint64 `json:"current_epoch"`
	InGracePeriod bool   `json:"in_grace_period"`
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

func main() {
	log.Println("========================================")
	log.Println("VEIL PROPERTY SUMMARY - FINAL REPORT")
	log.Println("========================================")

	// Override from environment if set
	if url := os.Getenv("MESSAGE_POOL_URL"); url != "" {
		messagePoolURL = url
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Section 1: Message Pool Statistics
	log.Println("\n--- MESSAGE POOL STATISTICS ---")
	poolStats := collectMessagePoolStats(httpClient)
	log.Printf("Total Messages: %d", poolStats.totalMessages)
	log.Printf("Valid Messages: %d", poolStats.validMessages)
	log.Printf("Cover Messages: %d", poolStats.coverMessages)
	log.Printf("Pool Accessible: %t", poolStats.accessible)

	// Section 2: Validator Statistics
	log.Println("\n--- VALIDATOR STATISTICS ---")
	validatorStats := collectValidatorStats(httpClient)
	for _, vs := range validatorStats {
		if vs.reachable {
			log.Printf("Validator %d: proposals=%d, committed_seq=%d, pending=%d",
				vs.id, vs.proposalCount, vs.committedSeq, vs.pendingCount)
		} else {
			log.Printf("Validator %d: UNREACHABLE", vs.id)
		}
	}

	// Consensus summary
	var reachableValidators int
	var totalProposals int64
	var maxCommittedSeq uint64
	var totalPending int
	for _, vs := range validatorStats {
		if vs.reachable {
			reachableValidators++
			totalProposals += vs.proposalCount
			if vs.committedSeq > maxCommittedSeq {
				maxCommittedSeq = vs.committedSeq
			}
			totalPending += vs.pendingCount
		}
	}
	log.Printf("Summary: %d/%d validators reachable, %d total proposals, max_committed=%d, total_pending=%d",
		reachableValidators, len(validatorURLs), totalProposals, maxCommittedSeq, totalPending)

	// Section 3: Relay Statistics
	log.Println("\n--- RELAY STATISTICS ---")
	relayStats := collectRelayStats(httpClient)
	var totalForwards int64
	var minEpoch, maxEpoch uint64
	var reachableRelays int
	first := true
	for _, rs := range relayStats {
		if rs.reachable {
			reachableRelays++
			totalForwards += rs.forwardCount
			if first {
				minEpoch = rs.epoch
				maxEpoch = rs.epoch
				first = false
			} else {
				if rs.epoch < minEpoch {
					minEpoch = rs.epoch
				}
				if rs.epoch > maxEpoch {
					maxEpoch = rs.epoch
				}
			}
			log.Printf("Relay %d: forwards=%d, epoch=%d, grace=%t",
				rs.id, rs.forwardCount, rs.epoch, rs.inGrace)
		} else {
			log.Printf("Relay %d: UNREACHABLE", rs.id)
		}
	}
	log.Printf("Summary: %d/%d relays reachable, %d total forwards, epoch_range=[%d, %d]",
		reachableRelays, len(relayURLs), totalForwards, minEpoch, maxEpoch)

	// Section 4: Property Status Summary
	log.Println("\n--- PROPERTY STATUS SUMMARY ---")

	// Compute derived property status
	log.Printf("Message Delivery: %s", boolStatus(poolStats.totalMessages > 0))
	log.Printf("Message Validity: %s", boolStatus(poolStats.totalMessages == 0 || poolStats.validMessages == poolStats.totalMessages))
	log.Printf("Cover Traffic Active: %s", boolStatus(poolStats.coverMessages > 0))
	log.Printf("Consensus Active: %s", boolStatus(maxCommittedSeq > 0))
	log.Printf("No Stuck Consensus: %s", boolStatus(totalPending < 100))
	log.Printf("Epoch Progression: %s", boolStatus(maxEpoch > 0))
	log.Printf("Epoch Synchronized: %s", boolStatus(maxEpoch-minEpoch <= 1))
	log.Printf("Relays Forwarding: %s", boolStatus(totalForwards > 0))

	log.Println("\n========================================")
	log.Println("END OF PROPERTY SUMMARY")
	log.Println("========================================")

	os.Exit(0)
}

type messagePoolStats struct {
	accessible    bool
	totalMessages int
	validMessages int
	coverMessages int
}

func collectMessagePoolStats(client *http.Client) messagePoolStats {
	stats := messagePoolStats{}

	resp, err := client.Get(messagePoolURL + "/messages?since=0")
	if err != nil {
		log.Printf("Could not reach message pool: %v", err)
		return stats
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Message pool returned status %d", resp.StatusCode)
		return stats
	}

	stats.accessible = true

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Could not read message pool response: %v", err)
		return stats
	}

	var messages []MessagePoolMessage
	if err := json.Unmarshal(body, &messages); err != nil {
		log.Printf("Could not decode messages: %v", err)
		return stats
	}

	stats.totalMessages = len(messages)

	for _, msg := range messages {
		payload := string(msg.Payload)
		// Check for valid format (MSG:<id>:DATA or COVER:...)
		if len(payload) >= 4 {
			if payload[:4] == "MSG:" {
				stats.validMessages++
			} else if len(payload) >= 6 && payload[:6] == "COVER:" {
				stats.coverMessages++
				stats.validMessages++ // Cover is also valid
			}
		}
	}

	return stats
}

type validatorStat struct {
	id            int
	reachable     bool
	proposalCount int64
	committedSeq  uint64
	pendingCount  int
}

func collectValidatorStats(client *http.Client) []validatorStat {
	var stats []validatorStat

	for i, url := range validatorURLs {
		stat := validatorStat{id: i}

		resp, err := client.Get(url + "/status")
		if err != nil {
			stats = append(stats, stat)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			stats = append(stats, stat)
			continue
		}

		var status ValidatorStatus
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			stats = append(stats, stat)
			continue
		}

		stat.reachable = true
		stat.proposalCount = status.ProposalCount
		stat.committedSeq = status.Consensus.CommittedSeq
		stat.pendingCount = status.Consensus.PendingCount
		stats = append(stats, stat)
	}

	return stats
}

type relayStat struct {
	id           int
	reachable    bool
	forwardCount int64
	epoch        uint64
	inGrace      bool
}

func collectRelayStats(client *http.Client) []relayStat {
	var stats []relayStat

	for i, url := range relayURLs {
		stat := relayStat{id: i}

		resp, err := client.Get(url + "/status")
		if err != nil {
			stats = append(stats, stat)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			stats = append(stats, stat)
			continue
		}

		var status RelayStatus
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			stats = append(stats, stat)
			continue
		}

		stat.reachable = true
		stat.forwardCount = status.ForwardCount
		stat.epoch = status.CurrentEpoch
		stat.inGrace = status.InGracePeriod
		stats = append(stats, stat)
	}

	return stats
}

func boolStatus(b bool) string {
	if b {
		return fmt.Sprintf("PASS")
	}
	return fmt.Sprintf("FAIL")
}
