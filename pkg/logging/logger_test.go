package logging

import (
	"testing"
)

func TestSetup_JSON(t *testing.T) {
	logger, err := Setup("json", false)
	if err != nil {
		t.Fatalf("Failed to setup JSON logger: %v", err)
	}
	if logger == nil {
		t.Fatal("Logger is nil")
	}

	logger.Info("test message")
	t.Log("✓ JSON logger created successfully")
}

func TestSetup_Console(t *testing.T) {
	logger, err := Setup("console", false)
	if err != nil {
		t.Fatalf("Failed to setup console logger: %v", err)
	}
	if logger == nil {
		t.Fatal("Logger is nil")
	}

	logger.Info("test message")
	t.Log("✓ Console logger created successfully")
}

func TestSetup_Verbose(t *testing.T) {
	logger, err := Setup("console", true)
	if err != nil {
		t.Fatalf("Failed to setup verbose logger: %v", err)
	}
	if logger == nil {
		t.Fatal("Logger is nil")
	}

	logger.Debug("debug message")
	t.Log("✓ Verbose logger created successfully")
}

func TestSetup_NonVerbose(t *testing.T) {
	logger, err := Setup("json", false)
	if err != nil {
		t.Fatalf("Failed to setup non-verbose logger: %v", err)
	}
	if logger == nil {
		t.Fatal("Logger is nil")
	}

	logger.Debug("debug message") // Should not appear
	logger.Info("info message")   // Should appear
	t.Log("✓ Non-verbose logger created successfully")
}
