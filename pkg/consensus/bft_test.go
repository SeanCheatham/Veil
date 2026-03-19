package consensus

import (
	"testing"
)

func TestLeaderRotation(t *testing.T) {
	// 3 validators, IDs 1-3
	// seq 0 → validator 1, seq 1 → validator 2, seq 2 → validator 3, seq 3 → validator 1
	tests := []struct {
		seq      uint64
		expected int
	}{
		{0, 1},
		{1, 2},
		{2, 3},
		{3, 1},
		{4, 2},
		{5, 3},
	}
	for _, tt := range tests {
		got := LeaderID(tt.seq, 3)
		if got != tt.expected {
			t.Errorf("LeaderID(%d, 3) = %d, want %d", tt.seq, got, tt.expected)
		}
	}
}

func TestQuorum(t *testing.T) {
	// 3 validators: quorum = ceil((3+1)/2) = 2
	q := Quorum(3)
	if q != 2 {
		t.Errorf("Quorum(3) = %d, want 2", q)
	}
}

func TestStateTransitions(t *testing.T) {
	peers := []string{"validator-1:8082", "validator-2:8082", "validator-3:8082"}
	node1 := NewNode("1", peers)
	node2 := NewNode("2", peers)
	node3 := NewNode("3", peers)

	// seq 0: leader is validator 1
	if !node1.IsLeader() {
		t.Fatal("node1 should be leader for seq 0")
	}

	// Leader proposes
	msgs := []string{"msg1", "msg2"}
	block := node1.Propose(msgs)
	if block == nil {
		t.Fatal("Propose should return a block")
	}
	if block.SeqNum != 0 {
		t.Errorf("block.SeqNum = %d, want 0", block.SeqNum)
	}
	if node1.State != StatePrePrepare {
		t.Errorf("node1 state = %d, want StatePrePrepare", node1.State)
	}

	// Non-leaders receive propose
	if !node2.HandlePropose(*block) {
		t.Fatal("node2 should accept propose")
	}
	if node2.State != StatePrepare {
		t.Errorf("node2 state = %d, want StatePrepare", node2.State)
	}

	if !node3.HandlePropose(*block) {
		t.Fatal("node3 should accept propose")
	}

	// Prepare votes: node2 sends prepare to node1 and node3
	// node1 receives prepare from node2 → quorum reached (node1 + node2 = 2)
	commitReady := node1.HandlePrepare(0, "2")
	if !commitReady {
		t.Fatal("node1 should reach prepare quorum after node2's vote")
	}
	if node1.State != StateCommit {
		t.Errorf("node1 state = %d, want StateCommit", node1.State)
	}

	// node2 receives prepare from node1 → quorum (node2 + node1 = 2)
	commitReady2 := node2.HandlePrepare(0, "1")
	if !commitReady2 {
		t.Fatal("node2 should reach prepare quorum after node1's vote")
	}

	// Commit votes: node1 receives commit from node2 → quorum
	committed := node1.HandleCommit(0, "2")
	if committed == nil {
		t.Fatal("node1 should commit after receiving node2's commit vote")
	}
	if len(committed.Messages) != 2 {
		t.Errorf("committed block has %d messages, want 2", len(committed.Messages))
	}
	if committed.Messages[0] != "msg1" || committed.Messages[1] != "msg2" {
		t.Error("committed messages not in order")
	}

	// Node1 should have advanced to seq 1
	if node1.SeqCounter != 1 {
		t.Errorf("node1.SeqCounter = %d, want 1", node1.SeqCounter)
	}
	if node1.State != StateIdle {
		t.Errorf("node1 state = %d, want StateIdle after commit", node1.State)
	}
}

func TestNonLeaderCannotPropose(t *testing.T) {
	peers := []string{"validator-1:8082", "validator-2:8082", "validator-3:8082"}
	node2 := NewNode("2", peers)

	block := node2.Propose([]string{"msg"})
	if block != nil {
		t.Error("non-leader should not be able to propose")
	}
}

func TestCommittedBlockMessagesOrder(t *testing.T) {
	peers := []string{"validator-1:8082", "validator-2:8082", "validator-3:8082"}
	node := NewNode("1", peers)

	msgs := []string{"alpha", "beta", "gamma", "delta"}
	block := node.Propose(msgs)
	if block == nil {
		t.Fatal("Propose returned nil")
	}

	// Simulate full consensus round
	node.HandlePrepare(0, "2")
	node.HandleCommit(0, "2")

	if len(node.CommittedBlocks) != 1 {
		t.Fatalf("expected 1 committed block, got %d", len(node.CommittedBlocks))
	}

	committed := node.CommittedBlocks[0]
	for i, m := range msgs {
		if committed.Messages[i] != m {
			t.Errorf("message %d: got %s, want %s", i, committed.Messages[i], m)
		}
	}
}
