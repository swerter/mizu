package blacklist

import (
	"io"

	"context"
	"errors"
	"net"
	"testing"
	"time"

	"log/slog"
)

// mockResolver is a mock implementation of the resolver interface for testing.
type mockResolver struct {
	// lookupIPAddrResults maps a hostname to a slice of IP addresses and an error.
	lookupIPAddrResults map[string]struct {
		addrs []net.IPAddr
		err   error
	}
}

func (m *mockResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	if res, ok := m.lookupIPAddrResults[host]; ok {
		return res.addrs, res.err
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func (m *mockResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	// Not needed for these tests, but required by the interface.
	return nil, errors.New("not implemented")
}

func (m *mockResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	// Not needed for these tests, but required by the interface.
	return nil, errors.New("not implemented")
}

func TestChecker_CheckIP(t *testing.T) {
	tests := []struct {
		name        string
		ip          string
		lists       []string
		mockResults map[string]struct {
			addrs []net.IPAddr
			err   error
		}
		expectListed bool
		expectReason string
		expectError  bool
	}{
		{
			name:  "IP is listed on one blacklist",
			ip:    "1.2.3.4",
			lists: []string{"zen.spamhaus.org", "bl.spamcop.net"},
			mockResults: map[string]struct {
				addrs []net.IPAddr
				err   error
			}{
				"4.3.2.1.zen.spamhaus.org": {addrs: []net.IPAddr{{IP: net.ParseIP("127.0.0.2")}}, err: nil},
			},
			expectListed: true,
			expectReason: "zen.spamhaus.org (SBL - Spam source)",
			expectError:  false,
		},
		{
			name:  "IP is not listed on any blacklist",
			ip:    "8.8.8.8",
			lists: []string{"zen.spamhaus.org", "bl.spamcop.net"},
			mockResults: map[string]struct {
				addrs []net.IPAddr
				err   error
			}{},
			expectListed: false,
			expectReason: "",
			expectError:  false, // No error, just not found
		},
		{
			name:  "DNS lookup returns an error",
			ip:    "1.2.3.4",
			lists: []string{"zen.spamhaus.org"},
			mockResults: map[string]struct {
				addrs []net.IPAddr
				err   error
			}{
				"4.3.2.1.zen.spamhaus.org": {addrs: nil, err: errors.New("dns server error")},
			},
			expectListed: false,
			expectReason: "",
			expectError:  false, // Errors are logged but don't block
		},
		{
			name:         "No blacklists configured",
			ip:           "1.2.3.4",
			lists:        []string{},
			mockResults:  nil,
			expectListed: false,
			expectReason: "",
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := NewChecker(tt.lists, 50*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
			checker.resolver = &mockResolver{lookupIPAddrResults: tt.mockResults}

			ip := net.ParseIP(tt.ip)
			isListed, reason, err := checker.CheckIP(ip)

			if isListed != tt.expectListed {
				t.Errorf("Expected isListed to be %v, but got %v", tt.expectListed, isListed)
			}

			if reason != tt.expectReason {
				t.Errorf("Expected reason to be '%s', but got '%s'", tt.expectReason, reason)
			}

			if (err != nil) != tt.expectError {
				t.Errorf("Expected error presence to be %v, but got error: %v", tt.expectError, err)
			}
		})
	}
}
