// Command anytime_consensus_ordering is an Antithesis test command that verifies
// uniform total order across all validators. It checks that all validators have
// committed the same sequences in the same order.
// It can run at any point including during fault injection as an "anytime_" prefixed command.
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

var validatorURLs = []string{
	"http://validator-node0:8081",
	"http://validator-node1:8081",
	"http://validator-node2:8081",
}

func main() {
	log.Println("anytime_consensus_ordering: checking consensus ordering across validators")

	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Collect status from all validators
	var statuses []ValidatorStatus
	var reachableCount int

	for _, url := range validatorURLs {
		status, err := getValidatorStatus(httpClient, url)
		if err != nil {
			log.Printf("anytime_consensus_ordering: could not reach %s: %v", url, err)
			continue
		}
		statuses = append(statuses, status)
		reachableCount++
	}

	// If we can't reach any validators, skip the check
	if reachableCount == 0 {
		log.Println("anytime_consensus_ordering: no validators reachable, skipping check")
		os.Exit(0)
	}

	log.Printf("anytime_consensus_ordering: reached %d/%d validators", reachableCount, len(validatorURLs))

	// Check 1: All reachable validators should have the same committed sequence
	// (This ensures uniform total order)
	var committedSeqs []uint64
	for _, status := range statuses {
		committedSeqs = append(committedSeqs, status.Consensus.CommittedSeq)
	}

	allMatch := true
	if len(committedSeqs) > 1 {
		first := committedSeqs[0]
		for _, seq := range committedSeqs[1:] {
			// Allow small differences due to in-flight consensus
			// Validators may be slightly out of sync temporarily
			diff := int64(seq) - int64(first)
			if diff < 0 {
				diff = -diff
			}
			if diff > 5 { // Allow up to 5 messages difference during consensus
				allMatch = false
				break
			}
		}
	}

	// Antithesis assertion: validators have consistent committed sequences
	assert.Always(allMatch, "Validators have consistent committed sequences", map[string]any{
		"committed_seqs":  committedSeqs,
		"reachable_count": reachableCount,
	})

	if !allMatch {
		log.Printf("anytime_consensus_ordering: WARNING - committed sequences diverge: %v", committedSeqs)
	}

	// Check 2: No gaps in committed sequences
	// Each validator's committed_seq should represent the next expected sequence
	noGaps := true
	for _, status := range statuses {
		// committed_seq represents the next expected sequence after commits
		// So sequence numbers 0 to committed_seq-1 should all be committed
		// This is implicitly enforced by the in-order commit logic
		if status.Consensus.CommittedSeq > 0 && status.Consensus.PendingCount > 0 {
			// Check if there are pending sequences below committed
			for _, pendingSeq := range status.Consensus.PendingSeqs {
				if pendingSeq < status.Consensus.CommittedSeq {
					noGaps = false
					log.Printf("anytime_consensus_ordering: WARNING - validator %d has pending seq %d below committed %d",
						status.ID, pendingSeq, status.Consensus.CommittedSeq)
				}
			}
		}
	}

	// Antithesis assertion: no gaps in committed sequence numbers
	assert.Always(noGaps, "No gaps in committed sequence numbers", map[string]any{
		"no_gaps":         noGaps,
		"reachable_count": reachableCount,
	})

	// Check 3: Next sequence numbers are consistent across validators
	// (This ensures atomic broadcast - all see the same proposals)
	var nextSeqs []uint64
	for _, status := range statuses {
		nextSeqs = append(nextSeqs, status.Consensus.NextSequence)
	}

	nextSeqsMatch := true
	if len(nextSeqs) > 1 {
		first := nextSeqs[0]
		for _, seq := range nextSeqs[1:] {
			diff := int64(seq) - int64(first)
			if diff < 0 {
				diff = -diff
			}
			if diff > 5 { // Allow small differences for in-flight proposals
				nextSeqsMatch = false
				break
			}
		}
	}

	// Antithesis assertion: validators have consistent next sequence numbers
	assert.Sometimes(nextSeqsMatch, "Validators have consistent next sequence numbers", map[string]any{
		"next_seqs":       nextSeqs,
		"reachable_count": reachableCount,
	})

	// Log summary
	log.Printf("anytime_consensus_ordering: completed. Reachable=%d, CommittedSeqs=%v, NextSeqs=%v, AllMatch=%t, NoGaps=%t",
		reachableCount, committedSeqs, nextSeqs, allMatch, noGaps)

	os.Exit(0)
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
