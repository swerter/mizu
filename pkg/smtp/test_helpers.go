package smtp

import "migadu/mizu/pkg/config"

// testServerConfig returns a minimal server config for testing
func testServerConfig() config.ServerConfig {
	return config.ServerConfig{
		Name:       "test",
		Type:       "relay",
		ListenAddr: ":25",
		Domain:     "test.local",
		Junk: config.ServerJunkConfig{
			RejectNullSender: true,
			CheckHeaders:     []string{"X-Spam-Flag"},
			ApplyAction:      "reject",
		},
		// TLS section omitted - no TLS for tests
	}
}
