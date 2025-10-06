package cluster

import (
	"encoding/json"
	"testing"
)

func TestVectorClock_Increment(t *testing.T) {
	vc := NewVectorClock("node1")

	if vc.GetLocalClock() != 0 {
		t.Errorf("Expected initial clock to be 0, got %d", vc.GetLocalClock())
	}

	vc.Increment()
	if vc.GetLocalClock() != 1 {
		t.Errorf("Expected clock to be 1 after increment, got %d", vc.GetLocalClock())
	}

	vc.Increment()
	if vc.GetLocalClock() != 2 {
		t.Errorf("Expected clock to be 2 after second increment, got %d", vc.GetLocalClock())
	}
}

func TestVectorClock_Update(t *testing.T) {
	vc1 := NewVectorClock("node1")
	vc2 := NewVectorClock("node2")

	// node1 increments its clock
	vc1.Increment()
	vc1.Increment()

	// node2 increments its clock
	vc2.Increment()

	// node1 receives update from node2
	vc1.Update(vc2)

	// After update, node1 should have:
	// - node1: 3 (2 from before + 1 from update operation)
	// - node2: 1 (from node2)
	if vc1.GetClock("node1") != 3 {
		t.Errorf("Expected node1 clock to be 3, got %d", vc1.GetClock("node1"))
	}
	if vc1.GetClock("node2") != 1 {
		t.Errorf("Expected node2 clock to be 1, got %d", vc1.GetClock("node2"))
	}
}

func TestVectorClock_Compare_HappenedBefore(t *testing.T) {
	vc1 := NewVectorClock("node1")
	vc2 := NewVectorClock("node1")

	// vc1 happens first
	vc1.Increment() // node1: 1

	// vc2 happens after
	vc2.clocks["node1"] = 1
	vc2.Increment() // node1: 2

	// vc1 should have happened before vc2
	if vc1.Compare(vc2) != -1 {
		t.Errorf("Expected vc1 < vc2, got %d", vc1.Compare(vc2))
	}
	if !vc1.HappenedBefore(vc2) {
		t.Error("Expected vc1 to have happened before vc2")
	}

	// vc2 should have happened after vc1
	if vc2.Compare(vc1) != 1 {
		t.Errorf("Expected vc2 > vc1, got %d", vc2.Compare(vc1))
	}
	if !vc2.HappenedAfter(vc1) {
		t.Error("Expected vc2 to have happened after vc1")
	}
}

func TestVectorClock_Compare_Concurrent(t *testing.T) {
	vc1 := NewVectorClock("node1")
	vc2 := NewVectorClock("node2")

	// Both nodes increment independently
	vc1.Increment() // node1: 1
	vc2.Increment() // node2: 1

	// These events are concurrent
	if vc1.Compare(vc2) != 0 {
		t.Errorf("Expected vc1 || vc2 (concurrent), got %d", vc1.Compare(vc2))
	}
	if !vc1.IsConcurrent(vc2) {
		t.Error("Expected vc1 and vc2 to be concurrent")
	}
}

func TestVectorClock_Compare_Complex(t *testing.T) {
	// Scenario: 3 nodes with complex causality
	vc1 := NewVectorClock("node1")
	vc2 := NewVectorClock("node2")
	vc3 := NewVectorClock("node3")

	// node1 increments
	vc1.Increment() // node1: 1

	// node2 receives from node1 and increments
	vc2.Update(vc1) // node1: 1, node2: 1

	// node3 increments independently
	vc3.Increment() // node3: 1

	// vc2 should have happened after vc1
	if !vc2.HappenedAfter(vc1) {
		t.Error("Expected vc2 > vc1")
	}

	// vc2 and vc3 should be concurrent
	if !vc2.IsConcurrent(vc3) {
		t.Error("Expected vc2 || vc3")
	}

	// node1 receives from both node2 and node3
	vc1.Update(vc2)
	vc1.Update(vc3)

	// Now vc1 should have happened after both vc2 and vc3
	if !vc1.HappenedAfter(vc2) {
		t.Error("Expected updated vc1 > vc2")
	}
	if !vc1.HappenedAfter(vc3) {
		t.Error("Expected updated vc1 > vc3")
	}
}

func TestVectorClock_Copy(t *testing.T) {
	vc1 := NewVectorClock("node1")
	vc1.Increment()
	vc1.Increment()

	vc2 := vc1.Copy()

	// Copies should be equal
	if vc1.Compare(vc2) != 0 && vc2.Compare(vc1) != 0 {
		t.Error("Expected copies to be equal")
	}

	// Modifying copy should not affect original
	vc2.Increment()

	if vc1.GetLocalClock() == vc2.GetLocalClock() {
		t.Error("Expected copy modification to not affect original")
	}

	if !vc2.HappenedAfter(vc1) {
		t.Error("Expected modified copy to have happened after original")
	}
}

func TestVectorClock_JSON(t *testing.T) {
	vc1 := NewVectorClock("node1")
	vc1.Increment()
	vc1.Increment()

	// Add another node's clock
	vc1.clocks["node2"] = 5

	// Marshal to JSON
	data, err := json.Marshal(vc1)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Unmarshal from JSON
	vc2 := &VectorClock{}
	if err := json.Unmarshal(data, vc2); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Check values
	if vc2.GetClock("node1") != 2 {
		t.Errorf("Expected node1 clock to be 2, got %d", vc2.GetClock("node1"))
	}
	if vc2.GetClock("node2") != 5 {
		t.Errorf("Expected node2 clock to be 5, got %d", vc2.GetClock("node2"))
	}
	if vc2.localID != "node1" {
		t.Errorf("Expected local ID to be node1, got %s", vc2.localID)
	}
}

func TestVectorClock_UpdateWithHigherClock(t *testing.T) {
	vc1 := NewVectorClock("node1")
	vc2 := NewVectorClock("node2")

	// node1 increments
	vc1.Increment() // node1: 1

	// node2 has a higher clock for node1 (simulating delayed message)
	vc2.clocks["node1"] = 5
	vc2.Increment() // node1: 5, node2: 1

	// node1 receives update from node2
	vc1.Update(vc2)

	// node1 should now have the higher value from node2
	if vc1.GetClock("node1") != 6 { // 5 from vc2 + 1 from update
		t.Errorf("Expected node1 clock to be 6, got %d", vc1.GetClock("node1"))
	}
	if vc1.GetClock("node2") != 1 {
		t.Errorf("Expected node2 clock to be 1, got %d", vc1.GetClock("node2"))
	}
}

func TestVectorClock_CompareEqual(t *testing.T) {
	vc1 := NewVectorClock("node1")
	vc2 := NewVectorClock("node1")

	// Both increment the same amount
	vc1.Increment()
	vc2.Increment()

	// They should not be equal (concurrent events from same node)
	// because they represent different events
	comparison := vc1.Compare(vc2)
	if comparison != 0 {
		t.Errorf("Expected concurrent (0), got %d", comparison)
	}
}

func TestVectorClock_MultipleNodes(t *testing.T) {
	vc1 := NewVectorClock("node1")
	vc2 := NewVectorClock("node2")
	vc3 := NewVectorClock("node3")

	// Simulate a chain of events: node1 -> node2 -> node3
	vc1.Increment() // node1: 1

	vc2.Update(vc1) // node1: 1, node2: 1

	vc3.Update(vc2) // node1: 1, node2: 1, node3: 1

	// Verify causality chain
	if !vc2.HappenedAfter(vc1) {
		t.Error("Expected vc2 > vc1")
	}
	if !vc3.HappenedAfter(vc2) {
		t.Error("Expected vc3 > vc2")
	}
	if !vc3.HappenedAfter(vc1) {
		t.Error("Expected vc3 > vc1 (transitive)")
	}

	// Verify clocks
	if vc3.GetClock("node1") != 1 {
		t.Errorf("Expected node1 clock to be 1, got %d", vc3.GetClock("node1"))
	}
	if vc3.GetClock("node2") != 1 {
		t.Errorf("Expected node2 clock to be 1, got %d", vc3.GetClock("node2"))
	}
	if vc3.GetClock("node3") != 1 {
		t.Errorf("Expected node3 clock to be 1, got %d", vc3.GetClock("node3"))
	}
}

func TestVectorClock_ConcurrentWithPartialOverlap(t *testing.T) {
	vc1 := NewVectorClock("node1")
	vc2 := NewVectorClock("node2")

	// node1 and node2 both increment
	vc1.Increment() // node1: 1
	vc2.Increment() // node2: 1

	// node1 increments again
	vc1.Increment() // node1: 2

	// node2 increments again
	vc2.Increment() // node2: 2

	// They should still be concurrent
	if !vc1.IsConcurrent(vc2) {
		t.Error("Expected vc1 and vc2 to be concurrent")
	}
}
