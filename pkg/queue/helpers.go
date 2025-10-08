package queue

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// GenerateJobID creates a unique ID for a delivery job
// Format: 16-character hex string (8 random bytes)
func GenerateJobID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if random fails
		// This should never happen in practice
		return fmt.Sprintf("job-%d", 0)
	}
	return hex.EncodeToString(b)
}
