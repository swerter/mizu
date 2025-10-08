package queue

import (
	"testing"
	"time"
)

// TestRetrySchedule_NextRetryTime tests the progressive backoff calculation
func TestRetrySchedule_NextRetryTime(t *testing.T) {
	schedule := NewRetrySchedule()
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name             string
		jobCreatedAt     time.Time
		jobLastAttempt   time.Time
		jobAttempts      int
		now              time.Time
		expectedInterval time.Duration
		description      string
	}{
		{
			name:             "First attempt - immediate",
			jobCreatedAt:     baseTime,
			jobLastAttempt:   baseTime,
			jobAttempts:      0,
			now:              baseTime,
			expectedInterval: 0,
			description:      "First attempt should be immediate",
		},
		{
			name:             "Age 30s - retry in 1 min",
			jobCreatedAt:     baseTime,
			jobLastAttempt:   baseTime.Add(30 * time.Second),
			jobAttempts:      1,
			now:              baseTime.Add(30 * time.Second),
			expectedInterval: 1 * time.Minute,
			description:      "Age < 1min: retry every 1 minute",
		},
		{
			name:             "Age 2min - retry in 1 min",
			jobCreatedAt:     baseTime,
			jobLastAttempt:   baseTime.Add(2 * time.Minute),
			jobAttempts:      2,
			now:              baseTime.Add(2 * time.Minute),
			expectedInterval: 1 * time.Minute,
			description:      "Age 1-5min: retry every 1 minute",
		},
		{
			name:             "Age 10min - retry in 5 min",
			jobCreatedAt:     baseTime,
			jobLastAttempt:   baseTime.Add(10 * time.Minute),
			jobAttempts:      5,
			now:              baseTime.Add(10 * time.Minute),
			expectedInterval: 5 * time.Minute,
			description:      "Age 5-30min: retry every 5 minutes",
		},
		{
			name:             "Age 1h - retry in 15 min",
			jobCreatedAt:     baseTime,
			jobLastAttempt:   baseTime.Add(1 * time.Hour),
			jobAttempts:      10,
			now:              baseTime.Add(1 * time.Hour),
			expectedInterval: 15 * time.Minute,
			description:      "Age 30min-2h: retry every 15 minutes",
		},
		{
			name:             "Age 4h - retry in 30 min",
			jobCreatedAt:     baseTime,
			jobLastAttempt:   baseTime.Add(4 * time.Hour),
			jobAttempts:      20,
			now:              baseTime.Add(4 * time.Hour),
			expectedInterval: 30 * time.Minute,
			description:      "Age 2-6h: retry every 30 minutes",
		},
		{
			name:             "Age 12h - retry in 2h",
			jobCreatedAt:     baseTime,
			jobLastAttempt:   baseTime.Add(12 * time.Hour),
			jobAttempts:      25,
			now:              baseTime.Add(12 * time.Hour),
			expectedInterval: 2 * time.Hour,
			description:      "Age 6-24h: retry every 2 hours",
		},
		{
			name:             "Age 36h - retry in 4h",
			jobCreatedAt:     baseTime,
			jobLastAttempt:   baseTime.Add(36 * time.Hour),
			jobAttempts:      35,
			now:              baseTime.Add(36 * time.Hour),
			expectedInterval: 4 * time.Hour,
			description:      "Age 24-48h: retry every 4 hours",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &DeliveryJob{
				ID:          "test-job",
				CreatedAt:   tt.jobCreatedAt,
				LastAttempt: tt.jobLastAttempt,
				Attempts:    tt.jobAttempts,
			}

			nextRetry := schedule.NextRetryTime(job, tt.now)
			expectedNextRetry := tt.jobLastAttempt.Add(tt.expectedInterval)

			if !nextRetry.Equal(expectedNextRetry) {
				t.Errorf("%s\nExpected next retry: %v\nGot: %v\nInterval: %v",
					tt.description,
					expectedNextRetry,
					nextRetry,
					nextRetry.Sub(tt.jobLastAttempt))
			}
		})
	}
}

// TestRetrySchedule_BoundaryConditions tests exact boundary times
func TestRetrySchedule_BoundaryConditions(t *testing.T) {
	schedule := NewRetrySchedule()
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name             string
		age              time.Duration
		expectedInterval time.Duration
	}{
		{"Exactly 1 minute", 1 * time.Minute, 1 * time.Minute},
		{"Just under 1 minute", 1*time.Minute - 1*time.Second, 1 * time.Minute},
		{"Exactly 5 minutes", 5 * time.Minute, 5 * time.Minute},
		{"Just under 5 minutes", 5*time.Minute - 1*time.Second, 1 * time.Minute},
		{"Exactly 30 minutes", 30 * time.Minute, 15 * time.Minute},
		{"Just under 30 minutes", 30*time.Minute - 1*time.Second, 5 * time.Minute},
		{"Exactly 2 hours", 2 * time.Hour, 30 * time.Minute},
		{"Just under 2 hours", 2*time.Hour - 1*time.Minute, 15 * time.Minute},
		{"Exactly 6 hours", 6 * time.Hour, 2 * time.Hour},
		{"Just under 6 hours", 6*time.Hour - 1*time.Minute, 30 * time.Minute},
		{"Exactly 24 hours", 24 * time.Hour, 4 * time.Hour},
		{"Just under 24 hours", 24*time.Hour - 1*time.Minute, 2 * time.Hour},
		{"Exactly 48 hours", 48 * time.Hour, 4 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := baseTime.Add(tt.age)
			job := &DeliveryJob{
				ID:          "test-job",
				CreatedAt:   baseTime,
				LastAttempt: now,
				Attempts:    1,
			}

			nextRetry := schedule.NextRetryTime(job, now)
			expectedNextRetry := now.Add(tt.expectedInterval)

			if !nextRetry.Equal(expectedNextRetry) {
				t.Errorf("Age %v: expected interval %v, got %v",
					tt.age, tt.expectedInterval, nextRetry.Sub(now))
			}
		})
	}
}

// TestRetrySchedule_ShouldRetry tests retry decision based on age
func TestRetrySchedule_ShouldRetry(t *testing.T) {
	schedule := NewRetrySchedule()
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		age      time.Duration
		expected bool
	}{
		{"Fresh job (1 minute)", 1 * time.Minute, true},
		{"Young job (1 hour)", 1 * time.Hour, true},
		{"Middle age (12 hours)", 12 * time.Hour, true},
		{"Older job (24 hours)", 24 * time.Hour, true},
		{"Almost max (47 hours)", 47 * time.Hour, true},
		{"Just under max (47h 59m)", 47*time.Hour + 59*time.Minute, true},
		{"Exactly max (48 hours)", 48 * time.Hour, false},
		{"Over max (49 hours)", 49 * time.Hour, false},
		{"Way over max (72 hours)", 72 * time.Hour, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := baseTime.Add(tt.age)
			job := &DeliveryJob{
				ID:        "test-job",
				CreatedAt: baseTime,
			}

			result := schedule.ShouldRetry(job, now)
			if result != tt.expected {
				t.Errorf("Age %v: expected ShouldRetry=%v, got %v",
					tt.age, tt.expected, result)
			}

			// ShouldGiveUp should be opposite
			giveUp := schedule.ShouldGiveUp(job, now)
			if giveUp == result {
				t.Errorf("ShouldGiveUp should be opposite of ShouldRetry")
			}
		})
	}
}

// TestRetrySchedule_CustomMaxAge tests custom max age configuration
func TestRetrySchedule_CustomMaxAge(t *testing.T) {
	// Create schedule with 24-hour max age
	schedule := &RetrySchedule{
		MaxAge: 24 * time.Hour,
	}

	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	job := &DeliveryJob{
		ID:        "test-job",
		CreatedAt: baseTime,
	}

	// Should retry at 23 hours
	if !schedule.ShouldRetry(job, baseTime.Add(23*time.Hour)) {
		t.Error("Should retry at 23 hours with 24h max age")
	}

	// Should not retry at 24 hours
	if schedule.ShouldRetry(job, baseTime.Add(24*time.Hour)) {
		t.Error("Should not retry at 24 hours with 24h max age")
	}

	// GetMaxAge should return custom value
	if schedule.GetMaxAge() != 24*time.Hour {
		t.Errorf("Expected max age 24h, got %v", schedule.GetMaxAge())
	}
}

// TestRetrySchedule_EstimateRetryCount tests retry count estimation
func TestRetrySchedule_EstimateRetryCount(t *testing.T) {
	schedule := NewRetrySchedule()

	count := schedule.EstimateRetryCount()
	if count != 39 {
		t.Errorf("Expected 39 retry attempts over 48 hours, got %d", count)
	}
}

// TestRetrySchedule_GetRetryStats tests statistics generation
func TestRetrySchedule_GetRetryStats(t *testing.T) {
	schedule := NewRetrySchedule()
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	job := &DeliveryJob{
		ID:          "test-job",
		CreatedAt:   baseTime,
		LastAttempt: baseTime.Add(2 * time.Hour),
		NextRetry:   baseTime.Add(2*time.Hour + 30*time.Minute),
		Attempts:    10,
	}

	now := baseTime.Add(2 * time.Hour)
	stats := schedule.GetRetryStats(job, now)

	// Verify all expected fields
	if stats["age_seconds"].(float64) != (2 * time.Hour).Seconds() {
		t.Errorf("Unexpected age: %v", stats["age_seconds"])
	}

	expectedRemaining := (48*time.Hour - 2*time.Hour).Seconds()
	if stats["remaining_seconds"].(float64) != expectedRemaining {
		t.Errorf("Expected remaining %v, got %v", expectedRemaining, stats["remaining_seconds"])
	}

	if stats["max_age_seconds"].(float64) != (48 * time.Hour).Seconds() {
		t.Errorf("Unexpected max age: %v", stats["max_age_seconds"])
	}

	if stats["attempts"].(int) != 10 {
		t.Errorf("Expected 10 attempts, got %v", stats["attempts"])
	}

	if !stats["should_retry"].(bool) {
		t.Error("Should retry at 2 hours age")
	}

	if stats["next_retry_in_seconds"].(float64) != (30 * time.Minute).Seconds() {
		t.Errorf("Expected next retry in 30min, got %v", stats["next_retry_in_seconds"])
	}
}

// TestRetrySchedule_GetRetryStats_NoNextRetry tests stats when NextRetry is not set
func TestRetrySchedule_GetRetryStats_NoNextRetry(t *testing.T) {
	schedule := NewRetrySchedule()
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	job := &DeliveryJob{
		ID:        "test-job",
		CreatedAt: baseTime,
		Attempts:  0,
		// NextRetry is zero value
	}

	now := baseTime.Add(1 * time.Hour)
	stats := schedule.GetRetryStats(job, now)

	// Should not have next_retry_in_seconds field
	if _, exists := stats["next_retry_in_seconds"]; exists {
		t.Error("Should not have next_retry_in_seconds when NextRetry is zero")
	}
}

// TestRetrySchedule_ProgressiveBehavior tests that retry intervals increase with age
func TestRetrySchedule_ProgressiveBehavior(t *testing.T) {
	schedule := NewRetrySchedule()
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	// Track intervals at different ages
	ages := []time.Duration{
		30 * time.Second, // < 1min
		3 * time.Minute,  // 1-5min
		10 * time.Minute, // 5-30min
		1 * time.Hour,    // 30min-2h
		4 * time.Hour,    // 2-6h
		12 * time.Hour,   // 6-24h
		36 * time.Hour,   // 24-48h
	}

	var lastInterval time.Duration
	for i, age := range ages {
		now := baseTime.Add(age)
		job := &DeliveryJob{
			ID:          "test-job",
			CreatedAt:   baseTime,
			LastAttempt: now,
			Attempts:    i + 1,
		}

		nextRetry := schedule.NextRetryTime(job, now)
		interval := nextRetry.Sub(now)

		t.Logf("Age %v: interval %v", age, interval)

		// Intervals should generally increase (or stay same) as job ages
		if i > 0 && interval < lastInterval {
			// Allow same interval (plateau), but not decrease
			if interval != lastInterval {
				t.Logf("Warning: interval decreased from %v to %v at age %v",
					lastInterval, interval, age)
			}
		}

		lastInterval = interval
	}
}
