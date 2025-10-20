package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Test loading with no config file in local mode
	cfg, err := LoadConfig([]string{"--local"})
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg == nil {
		t.Fatal("LoadConfig returned nil config")
	}

	// Verify defaults exist
	if len(cfg.Servers) == 0 {
		t.Error("Expected at least one default server")
	}

	if cfg.Logging.Format != "console" {
		t.Errorf("Logging.Format = %s; want console", cfg.Logging.Format)
	}
}

func TestLoadConfig_LocalMode(t *testing.T) {
	cfg, err := LoadConfig([]string{"--local"})
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if !cfg.Local {
		t.Error("Local should be true")
	}
}

func TestLoadConfig_ConfigFile(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test-config.toml")

	configContent := `
[[server]]
name = "test-relay"
type = "relay"
listen_addr = ":2525"
domain = "mail.test.com"

[server.tls]
enabled = true
mode = "starttls"

[storage]
backend = "filesystem"
filesystem_path = "/tmp/mizu-test"

[delivery]
url = "https://destination.example.com/email"
api_key = "test-api-key"

log_format = "json"
`

	err := os.WriteFile(configPath, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := LoadConfig([]string{"--config", configPath, "--local"})
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if len(cfg.Servers) != 1 {
		t.Fatalf("Expected 1 server, got %d", len(cfg.Servers))
	}

	srv := cfg.Servers[0]
	if srv.ListenAddr != ":2525" {
		t.Errorf("Server.ListenAddr = %s; want :2525", srv.ListenAddr)
	}

	if srv.Domain != "mail.test.com" {
		t.Errorf("Server.Domain = %s; want mail.test.com", srv.Domain)
	}

	if cfg.Storage.Backend != "filesystem" {
		t.Errorf("Storage.Backend = %s; want filesystem", cfg.Storage.Backend)
	}

	if cfg.Delivery.URL != "https://destination.example.com/email" {
		t.Errorf("Delivery.URL = %s; want https://destination.example.com/email", cfg.Delivery.URL)
	}
}

func TestLoadEnvVars(t *testing.T) {
	// Set environment variables
	os.Setenv("S3_ACCESS_KEY_ID", "test-access-key")
	os.Setenv("S3_SECRET_ACCESS_KEY", "test-secret-key")
	os.Setenv("DELIVERY_API_KEY", "test-dest-key")
	os.Setenv("AUTH_API_KEY", "test-auth-key")

	defer func() {
		os.Unsetenv("S3_ACCESS_KEY_ID")
		os.Unsetenv("S3_SECRET_ACCESS_KEY")
		os.Unsetenv("DELIVERY_API_KEY")
		os.Unsetenv("AUTH_API_KEY")
	}()

	defaultCfg := DefaultConfig()
	cfg := &defaultCfg
	applyEnvironmentVariables(cfg)

	if cfg.Storage.AccessKeyID != "test-access-key" {
		t.Errorf("Storage.AccessKeyID = %s; want test-access-key", cfg.Storage.AccessKeyID)
	}

	if cfg.Storage.SecretAccessKey != "test-secret-key" {
		t.Errorf("Storage.SecretAccessKey = %s; want test-secret-key", cfg.Storage.SecretAccessKey)
	}

	if cfg.Delivery.APIKey != "test-dest-key" {
		t.Errorf("Delivery.APIKey = %s; want test-dest-key", cfg.Delivery.APIKey)
	}

	// Check that AUTH_API_KEY is applied to submission servers
	for i := range cfg.Servers {
		if cfg.Servers[i].IsSubmission() && cfg.Servers[i].Auth.APIKey == "" {
			t.Errorf("Submission server %s should have auth API key set", cfg.Servers[i].Name)
		}
	}
}

func TestValidateConfig_PortConflict(t *testing.T) {
	cfg := DefaultConfig()

	// Add two servers on the same port
	cfg.Servers = []ServerConfig{
		{
			Name:       "server1",
			Type:       "relay",
			ListenAddr: ":25",
			Domain:     "test1.com",
			TLS: ServerTLSConfig{
				Enabled: true,
				Mode:    "starttls",
			},
		},
		{
			Name:       "server2",
			Type:       "submission",
			ListenAddr: ":25", // Same port!
			Domain:     "test2.com",
			TLS: ServerTLSConfig{
				Enabled: true,
				Mode:    "implicit",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Expected port conflict error, got nil")
	}
}

func TestValidateConfig_ValidMultiServer(t *testing.T) {
	cfg := DefaultConfig()

	// Add multiple servers on different ports
	cfg.Servers = []ServerConfig{
		{
			Name:       "relay",
			Type:       "relay",
			ListenAddr: ":25",
			Domain:     "mail.example.com",
			TLS: ServerTLSConfig{
				Enabled: true,
				Mode:    "starttls",
			},
		},
		{
			Name:       "submission",
			Type:       "submission",
			ListenAddr: ":465",
			Domain:     "mail.example.com",
			TLS: ServerTLSConfig{
				Enabled:  true,
				Mode:     "implicit",
				Required: true,
			},
			Auth: ServerAuthConfig{
				Required: true,
				Endpoint: "https://auth.example.com",
				APIKey:   "test-api-key",
			},
		},
	}

	// Set required fields for production mode
	cfg.Local = true // Use local mode to skip some validations
	cfg.Storage.Backend = "filesystem"
	cfg.Storage.FilesystemPath = "/tmp/mizu"
	cfg.Delivery.URL = "https://test.com"
	cfg.Delivery.APIKey = "test-key"

	err := cfg.Validate()
	if err != nil {
		t.Errorf("Unexpected validation error: %v", err)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Verify defaults
	if len(cfg.Servers) == 0 {
		t.Error("DefaultConfig should have at least one server")
	}

	if cfg.Logging.Format != "console" {
		t.Errorf("Logging.Format = %s; want console", cfg.Logging.Format)
	}

	if cfg.Local {
		t.Error("Local should be false by default")
	}

	if !cfg.Stats.Enabled {
		t.Error("Stats.Enabled should be true by default")
	}

	if cfg.Storage.Backend != "s3" {
		t.Errorf("Storage.Backend = %s; want s3", cfg.Storage.Backend)
	}
}

func TestSaveExample(t *testing.T) {
	tmpDir := t.TempDir()
	examplePath := filepath.Join(tmpDir, "config.toml.example")

	err := SaveExample(examplePath)
	if err != nil {
		t.Fatalf("SaveExample failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(examplePath); os.IsNotExist(err) {
		t.Error("example config file was not created")
	}

	// Verify file can be loaded
	data, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("failed to read example file: %v", err)
	}

	if len(data) == 0 {
		t.Error("example file is empty")
	}
}
