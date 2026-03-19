package consensus

import (
	"sync"
)

// State represents the current phase of the BFT consensus protocol.
type State int

const (
	StateIdle       State = iota
	StatePrePrepare
	StatePrepare
	StateCommit
)

// Block represents a batch of ciphertexts proposed for ordering.
type Block struct {
	SeqNum   uint64   `json:"seq"`
	Messages []string `json:"messages"`
}

// Node holds the state for a single BFT consensus participant.
type Node struct {
	ID           string
	Peers        []string
	State        State
	CurrentBlock *Block
	PrepareVotes map[string]bool
	CommitVotes  map[string]bool
	SeqCounter   uint64
	mu           sync.Mutex

	// CommittedBlocks holds blocks that reached commit quorum, for external consumption.
	CommittedBlocks []Block
}

// Quorum returns the number of votes needed for agreement (⌈(n+1)/2⌉ where n = total validators).
func Quorum(totalValidators int) int {
	return (totalValidators + 2) / 2 // equivalent to ceil((n+1)/2)
}

// LeaderID returns the validator ID (1-indexed) that is leader for the given sequence number.
func LeaderID(seq uint64, totalValidators int) int {
	return int(seq%uint64(totalValidators)) + 1
}

// NewNode creates a new consensus node.
func NewNode(id string, peers []string) *Node {
	return &Node{
		ID:           id,
		Peers:        peers,
		State:        StateIdle,
		PrepareVotes: make(map[string]bool),
		CommitVotes:  make(map[string]bool),
	}
}

// IsLeader returns true if this node is the leader for the current sequence number.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return LeaderID(n.SeqCounter, len(n.Peers)) == n.idNum()
}

// GetSeqCounter returns the current sequence counter.
func (n *Node) GetSeqCounter() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.SeqCounter
}

// idNum parses the numeric ID (assumes ID is like "1", "2", "3").
func (n *Node) idNum() int {
	if len(n.ID) == 0 {
		return 0
	}
	num := 0
	for _, c := range n.ID {
		if c >= '0' && c <= '9' {
			num = num*10 + int(c-'0')
		}
	}
	return num
}

// Propose creates a block from pending messages. Only the current leader should call this.
// Returns the block to be broadcast, or nil if this node is not the leader.
func (n *Node) Propose(messages []string) *Block {
	n.mu.Lock()
	defer n.mu.Unlock()

	if LeaderID(n.SeqCounter, len(n.Peers)) != n.idNum() {
		return nil
	}
	if n.State != StateIdle {
		return nil
	}

	block := &Block{
		SeqNum:   n.SeqCounter,
		Messages: messages,
	}
	n.CurrentBlock = block
	n.State = StatePrePrepare
	n.PrepareVotes = map[string]bool{n.ID: true} // leader votes for itself
	n.CommitVotes = make(map[string]bool)
	return block
}

// HandlePropose processes a propose message from the leader.
// Returns true if the node accepted the proposal (and should broadcast prepare).
func (n *Node) HandlePropose(block Block) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	if block.SeqNum != n.SeqCounter {
		return false
	}
	if n.State != StateIdle {
		return false
	}

	n.CurrentBlock = &block
	n.State = StatePrepare
	n.PrepareVotes = map[string]bool{n.ID: true}
	n.CommitVotes = make(map[string]bool)
	return true
}

// HandlePrepare processes a prepare vote from a peer.
// Returns true if quorum was just reached (caller should broadcast commit).
func (n *Node) HandlePrepare(seq uint64, validatorID string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	if seq != n.SeqCounter {
		return false
	}
	if n.State != StatePrePrepare && n.State != StatePrepare {
		return false
	}

	n.PrepareVotes[validatorID] = true

	quorum := Quorum(len(n.Peers))
	if len(n.PrepareVotes) >= quorum && n.State != StateCommit {
		n.State = StateCommit
		n.CommitVotes[n.ID] = true // vote commit for ourselves
		return true
	}
	return false
}

// HandleCommit processes a commit vote from a peer.
// Returns the committed block if quorum was just reached, nil otherwise.
func (n *Node) HandleCommit(seq uint64, validatorID string) *Block {
	n.mu.Lock()
	defer n.mu.Unlock()

	if seq != n.SeqCounter {
		return nil
	}
	if n.State != StateCommit {
		return nil
	}

	n.CommitVotes[validatorID] = true

	quorum := Quorum(len(n.Peers))
	if len(n.CommitVotes) >= quorum {
		committed := n.CurrentBlock
		n.CommittedBlocks = append(n.CommittedBlocks, *committed)
		// Reset for next round
		n.SeqCounter++
		n.State = StateIdle
		n.CurrentBlock = nil
		n.PrepareVotes = make(map[string]bool)
		n.CommitVotes = make(map[string]bool)
		return committed
	}
	return nil
}
