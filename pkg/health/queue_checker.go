package health

import (
	"fmt"
	"time"
)

// QueueStatsProvider defines an interface for getting queue statistics
type QueueStatsProvider interface {
	GetStats() QueueStats
	GetStorageStats() (map[string]interface{}, error)
	GetEmailStorageStats() (map[string]interface{}, error)
}

// QueueStats represents queue operational statistics
type QueueStats struct {
	TotalEnqueued  int64
	TotalDelivered int64
	TotalFailed    int64
	TotalRetries   int64
	CurrentSize    int
	WorkersActive  int
	WorkersRunning int
}

// CheckQueue checks the health of the persistent queue
type CheckQueue struct {
	QueueProvider QueueStatsProvider
	MaxQueueSize  int           // Warn if queue size exceeds this
	MaxJobAge     time.Duration // Warn if oldest job exceeds this age
}

// NewCheckQueue creates a new queue health checker
func NewCheckQueue(provider QueueStatsProvider, maxQueueSize int, maxJobAge time.Duration) *CheckQueue {
	return &CheckQueue{
		QueueProvider: provider,
		MaxQueueSize:  maxQueueSize,
		MaxJobAge:     maxJobAge,
	}
}

func (c *CheckQueue) Name() string {
	return "queue"
}

func (c *CheckQueue) CheckHealth() ComponentStatus {
	if c.QueueProvider == nil {
		return ComponentStatus{
			Status:  "disabled",
			Details: "Queue not configured",
		}
	}

	stats := c.QueueProvider.GetStats()

	// Get storage stats
	storageStats, err := c.QueueProvider.GetStorageStats()
	if err != nil {
		return ComponentStatus{
			Status: "unhealthy",
			Details: map[string]interface{}{
				"error": fmt.Sprintf("Failed to get storage stats: %v", err),
			},
		}
	}

	// Get email storage stats
	emailStats, err := c.QueueProvider.GetEmailStorageStats()
	if err != nil {
		return ComponentStatus{
			Status: "degraded",
			Details: map[string]interface{}{
				"warning":         fmt.Sprintf("Failed to get email storage stats: %v", err),
				"queue_size":      stats.CurrentSize,
				"workers":         stats.WorkersActive,
				"total_enqueued":  stats.TotalEnqueued,
				"total_delivered": stats.TotalDelivered,
			},
		}
	}

	// Determine health status
	status := "healthy"
	warnings := []string{}

	// Check queue size
	if c.MaxQueueSize > 0 && stats.CurrentSize > c.MaxQueueSize {
		status = "degraded"
		warnings = append(warnings, fmt.Sprintf("Queue size (%d) exceeds threshold (%d)",
			stats.CurrentSize, c.MaxQueueSize))
	}

	// Check if queue is growing too large
	if stats.CurrentSize > 1000 {
		status = "degraded"
		warnings = append(warnings, fmt.Sprintf("Large queue size: %d jobs pending", stats.CurrentSize))
	}

	// Check DLQ size
	dlqEntries := 0
	if val, ok := storageStats["dlq_entries"].(int); ok {
		dlqEntries = val
		if val > 100 {
			if status == "healthy" {
				status = "degraded"
			}
			warnings = append(warnings, fmt.Sprintf("High DLQ count: %d failed jobs", val))
		}
	}

	// Check if workers are running
	if stats.WorkersActive > 0 && stats.WorkersRunning == 0 {
		status = "degraded"
		warnings = append(warnings, "No workers currently processing jobs")
	}

	// Build details
	details := map[string]interface{}{
		"queue_size":        stats.CurrentSize,
		"workers_active":    stats.WorkersActive,
		"workers_running":   stats.WorkersRunning,
		"total_enqueued":    stats.TotalEnqueued,
		"total_delivered":   stats.TotalDelivered,
		"total_failed":      stats.TotalFailed,
		"total_retries":     stats.TotalRetries,
		"dlq_entries":       dlqEntries,
		"storage_jobs":      storageStats["jobs"],
		"storage_schedules": storageStats["schedule_entries"],
	}

	// Add email storage info if available
	if emailStats != nil {
		details["email_files"] = emailStats["total_files"]
		details["email_storage_mb"] = emailStats["total_mb"]
	}

	// Add warnings if any
	if len(warnings) > 0 {
		details["warnings"] = warnings
	}

	// Calculate health metrics
	if stats.TotalEnqueued > 0 {
		deliveryRate := float64(stats.TotalDelivered) / float64(stats.TotalEnqueued) * 100
		details["delivery_rate_percent"] = fmt.Sprintf("%.2f", deliveryRate)

		if stats.TotalDelivered > 0 {
			failureRate := float64(stats.TotalFailed) / float64(stats.TotalDelivered) * 100
			details["failure_rate_percent"] = fmt.Sprintf("%.2f", failureRate)

			// High failure rate is concerning
			if failureRate > 10 {
				status = "degraded"
				warnings = append(warnings, fmt.Sprintf("High failure rate: %.2f%%", failureRate))
				details["warnings"] = warnings
			}
		}
	}

	return ComponentStatus{
		Status:  status,
		Details: details,
	}
}
