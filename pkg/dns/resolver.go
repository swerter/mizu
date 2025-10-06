package dns

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ResilientResolver provides DNS resolution with round-robin load balancing
// and automatic failover across multiple DNS servers.
type ResilientResolver struct {
	servers []string      // List of DNS servers
	timeout time.Duration // Timeout for each DNS query
	index   atomic.Uint32 // Current server index for round-robin
	mu      sync.RWMutex  // Protects servers list
}

// NewResilientResolver creates a new resilient DNS resolver.
// If servers is empty or nil, returns the system default resolver.
func NewResilientResolver(servers []string, timeout time.Duration) *net.Resolver {
	if len(servers) == 0 {
		return net.DefaultResolver
	}

	rr := &ResilientResolver{
		servers: servers,
		timeout: timeout,
	}

	return &net.Resolver{
		PreferGo: true,
		Dial:     rr.dial,
	}
}

// dial implements the Dial function for net.Resolver with round-robin and failover.
// It tries each DNS server in round-robin order until one succeeds or all fail.
func (r *ResilientResolver) dial(ctx context.Context, network, address string) (net.Conn, error) {
	r.mu.RLock()
	serverCount := len(r.servers)
	servers := make([]string, serverCount)
	copy(servers, r.servers)
	r.mu.RUnlock()

	if serverCount == 0 {
		return nil, fmt.Errorf("no DNS servers configured")
	}

	// Get starting index for round-robin (atomic increment)
	startIdx := int(r.index.Add(1) % uint32(serverCount))

	var lastErr error

	// Try each server once in round-robin order
	for i := 0; i < serverCount; i++ {
		// Calculate current server index with wraparound
		idx := (startIdx + i) % serverCount
		server := servers[idx]

		// Create dialer with timeout
		d := net.Dialer{
			Timeout: r.timeout,
		}

		// Attempt connection
		conn, err := d.DialContext(ctx, "udp", server)
		if err == nil {
			return conn, nil
		}

		lastErr = err
	}

	// All servers failed
	return nil, fmt.Errorf("all DNS servers failed, last error: %w", lastErr)
}

// GetServers returns a copy of the current server list.
func (r *ResilientResolver) GetServers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	servers := make([]string, len(r.servers))
	copy(servers, r.servers)
	return servers
}

// UpdateServers updates the list of DNS servers.
// This can be used for dynamic DNS server configuration.
func (r *ResilientResolver) UpdateServers(servers []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.servers = make([]string, len(servers))
	copy(r.servers, servers)
}
