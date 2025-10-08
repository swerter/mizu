package queue

import (
	"time"
)

// RetrySchedule defines the retry intervals for a mail queue
// This implements a typical SMTP queue retry schedule over 48 hours
type RetrySchedule struct {
	MaxAge time.Duration // Maximum age before giving up (default: 48 hours)
}

// NewRetrySchedule creates a default retry schedule
func NewRetrySchedule() *RetrySchedule {
	return &RetrySchedule{
		MaxAge: 48 * time.Hour,
	}
}

// NextRetryTime calculates when to retry next based on job age
// This implements a progressive backoff strategy typical of SMTP mail queues:
//
// Age Range        | Retry Interval | Attempts
// -----------------|----------------|----------
// 0-1 min          | immediate      | 1
// 1-5 min          | 1 min          | 4
// 5-30 min         | 5 min          | 5
// 30 min-2 hours   | 15 min         | 6
// 2-6 hours        | 30 min         | 8
// 6-24 hours       | 2 hours        | 9
// 24-48 hours      | 4 hours        | 6
//
// Total: ~39 attempts over 48 hours
func (rs *RetrySchedule) NextRetryTime(job *DeliveryJob, now time.Time) time.Time {
	age := now.Sub(job.CreatedAt)

	// First attempt - immediate
	if job.Attempts == 0 {
		return now
	}

	// Determine retry interval based on age
	var interval time.Duration

	switch {
	case age < 1*time.Minute:
		// Very fresh: retry every 1 minute
		interval = 1 * time.Minute

	case age < 5*time.Minute:
		// Fresh: retry every 1 minute
		interval = 1 * time.Minute

	case age < 30*time.Minute:
		// Recent: retry every 5 minutes
		interval = 5 * time.Minute

	case age < 2*time.Hour:
		// Within 2 hours: retry every 15 minutes
		interval = 15 * time.Minute

	case age < 6*time.Hour:
		// Within 6 hours: retry every 30 minutes
		interval = 30 * time.Minute

	case age < 24*time.Hour:
		// Within 24 hours: retry every 2 hours
		interval = 2 * time.Hour

	default:
		// After 24 hours: retry every 4 hours
		interval = 4 * time.Hour
	}

	return job.LastAttempt.Add(interval)
}

// ShouldRetry determines if a job should be retried based on its age
func (rs *RetrySchedule) ShouldRetry(job *DeliveryJob, now time.Time) bool {
	age := now.Sub(job.CreatedAt)
	return age < rs.MaxAge
}

// ShouldGiveUp determines if we should give up on a job
func (rs *RetrySchedule) ShouldGiveUp(job *DeliveryJob, now time.Time) bool {
	return !rs.ShouldRetry(job, now)
}

// GetMaxAge returns the maximum age before giving up
func (rs *RetrySchedule) GetMaxAge() time.Duration {
	return rs.MaxAge
}

// EstimateRetryCount estimates total retry attempts over the max age
// This is approximate since actual attempts depend on when failures occur
func (rs *RetrySchedule) EstimateRetryCount() int {
	// Based on our schedule:
	// 0-1 min: 1 attempt (immediate)
	// 1-5 min: 4 attempts (every 1 min)
	// 5-30 min: 5 attempts (every 5 min)
	// 30 min-2h: 6 attempts (every 15 min)
	// 2-6h: 8 attempts (every 30 min)
	// 6-24h: 9 attempts (every 2 hours)
	// 24-48h: 6 attempts (every 4 hours)
	// Total: ~39 attempts over 48 hours
	return 39
}

// GetRetryStats returns statistics about the retry schedule
func (rs *RetrySchedule) GetRetryStats(job *DeliveryJob, now time.Time) map[string]interface{} {
	age := now.Sub(job.CreatedAt)
	remaining := rs.MaxAge - age

	stats := map[string]interface{}{
		"age_seconds":       age.Seconds(),
		"remaining_seconds": remaining.Seconds(),
		"max_age_seconds":   rs.MaxAge.Seconds(),
		"attempts":          job.Attempts,
		"should_retry":      rs.ShouldRetry(job, now),
	}

	if !job.NextRetry.IsZero() {
		timeUntilRetry := job.NextRetry.Sub(now)
		stats["next_retry_in_seconds"] = timeUntilRetry.Seconds()
	}

	return stats
}
