package cluster

import (
	"encoding/json"
	"sync"
)

// VectorClock implements a vector clock for distributed conflict resolution
// Each node maintains a logical clock that increments on local events and
// merges with clocks from remote nodes to establish causality relationships
type VectorClock struct {
	mu      sync.RWMutex
	clocks  map[string]uint64 // nodeID -> clock value
	localID string            // This node's ID
}

// NewVectorClock creates a new vector clock for the given node ID
func NewVectorClock(nodeID string) *VectorClock {
	return &VectorClock{
		clocks:  make(map[string]uint64),
		localID: nodeID,
	}
}

// Increment increments the local node's clock
func (vc *VectorClock) Increment() {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.clocks[vc.localID]++
}

// Update updates the clock with a remote clock, taking the maximum of each entry
func (vc *VectorClock) Update(remote *VectorClock) {
	if remote == nil {
		return
	}

	vc.mu.Lock()
	defer vc.mu.Unlock()

	remote.mu.RLock()
	defer remote.mu.RUnlock()

	for nodeID, remoteClock := range remote.clocks {
		if localClock, exists := vc.clocks[nodeID]; !exists || remoteClock > localClock {
			vc.clocks[nodeID] = remoteClock
		}
	}

	// Increment our own clock after merging
	vc.clocks[vc.localID]++
}

// Compare compares two vector clocks and returns the relationship
// Returns:
//   - 1 if vc happened after other (vc > other)
//   - -1 if vc happened before other (vc < other)
//   - 0 if vc and other are concurrent (vc || other)
func (vc *VectorClock) Compare(other *VectorClock) int {
	if other == nil {
		return 1
	}

	vc.mu.RLock()
	defer vc.mu.RUnlock()

	other.mu.RLock()
	defer other.mu.RUnlock()

	// Collect all node IDs from both clocks
	allNodes := make(map[string]bool)
	for nodeID := range vc.clocks {
		allNodes[nodeID] = true
	}
	for nodeID := range other.clocks {
		allNodes[nodeID] = true
	}

	greater := false
	less := false

	for nodeID := range allNodes {
		vcClock := vc.clocks[nodeID]
		otherClock := other.clocks[nodeID]

		if vcClock > otherClock {
			greater = true
		} else if vcClock < otherClock {
			less = true
		}
	}

	// If all entries are greater or equal, and at least one is greater
	if greater && !less {
		return 1 // vc > other (vc happened after)
	}
	// If all entries are less or equal, and at least one is less
	if less && !greater {
		return -1 // vc < other (vc happened before)
	}
	// Otherwise, clocks are concurrent
	return 0 // vc || other (concurrent)
}

// HappenedBefore returns true if this clock happened before the other
func (vc *VectorClock) HappenedBefore(other *VectorClock) bool {
	return vc.Compare(other) == -1
}

// HappenedAfter returns true if this clock happened after the other
func (vc *VectorClock) HappenedAfter(other *VectorClock) bool {
	return vc.Compare(other) == 1
}

// IsConcurrent returns true if this clock is concurrent with the other
func (vc *VectorClock) IsConcurrent(other *VectorClock) bool {
	return vc.Compare(other) == 0
}

// Copy creates a deep copy of the vector clock
func (vc *VectorClock) Copy() *VectorClock {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	clocks := make(map[string]uint64, len(vc.clocks))
	for nodeID, clock := range vc.clocks {
		clocks[nodeID] = clock
	}

	return &VectorClock{
		clocks:  clocks,
		localID: vc.localID,
	}
}

// MarshalJSON implements json.Marshaler
func (vc *VectorClock) MarshalJSON() ([]byte, error) {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	return json.Marshal(map[string]interface{}{
		"clocks":   vc.clocks,
		"local_id": vc.localID,
	})
}

// UnmarshalJSON implements json.Unmarshaler
func (vc *VectorClock) UnmarshalJSON(data []byte) error {
	var tmp struct {
		Clocks  map[string]uint64 `json:"clocks"`
		LocalID string            `json:"local_id"`
	}

	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}

	vc.mu.Lock()
	defer vc.mu.Unlock()

	vc.clocks = tmp.Clocks
	vc.localID = tmp.LocalID

	return nil
}

// GetClock returns the clock value for a specific node
func (vc *VectorClock) GetClock(nodeID string) uint64 {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	return vc.clocks[nodeID]
}

// GetLocalClock returns the clock value for the local node
func (vc *VectorClock) GetLocalClock() uint64 {
	return vc.GetClock(vc.localID)
}

// String returns a string representation of the vector clock
func (vc *VectorClock) String() string {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	data, _ := json.Marshal(vc.clocks)
	return string(data)
}
