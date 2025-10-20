package blacklist

import (
	"io"

	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"log/slog"

	"migadu/mizu/pkg/logging"
)

// resolver defines an interface for DNS lookups, allowing for mocking in tests.
type resolver interface {
	LookupAddr(ctx context.Context, addr string) ([]string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Checker is responsible for checking an IP against a set of DNS blacklists.
type Checker struct {
	lists    []string
	timeout  time.Duration
	logger   *slog.Logger
	resolver resolver // Use interface for testability
}

// NewChecker creates a new blacklist checker.
func NewChecker(lists []string, timeout time.Duration, logger *slog.Logger) *Checker {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Checker{
		lists:    lists,
		timeout:  timeout,
		logger:   logger,
		resolver: &net.Resolver{}, // Use the default net.Resolver
	}
}

// CheckIP checks if the given IP address is on any of the configured blacklists.
func (c *Checker) CheckIP(ip net.IP) (bool, string, error) {
	if ip == nil {
		return false, "", fmt.Errorf("invalid IP address")
	}

	// Skip if no blacklists configured
	if len(c.lists) == 0 {
		return false, "", nil
	}

	// Reverse the IP address for DNSBL lookup.
	reversedIP := reverseIP(ip)
	if reversedIP == "" {
		return false, "", fmt.Errorf("unsupported IP format")
	}

	// Create a context with timeout for all lookups.
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	// Use channels to perform lookups concurrently.
	type result struct {
		list   string
		reason string
	}
	resultChan := make(chan result, len(c.lists))
	var wg sync.WaitGroup

	for _, list := range c.lists {
		wg.Add(1)
		logging.SafeGoWithWg(c.logger, fmt.Sprintf("blacklist-lookup-%s", list), &wg, func(l string) func() {
			return func() {
				query := fmt.Sprintf("%s.%s", reversedIP, l)
				addrs, err := c.resolver.LookupIPAddr(ctx, query)
				if err != nil {
					// Not listed or error - either way, skip
					c.logger.Debug("blacklist lookup", "query", query, "error", err)
					return
				}
				if len(addrs) > 0 {
					// IP is listed. Check if it's Spamhaus to get detailed reason
					reason := l
					if strings.Contains(l, "spamhaus") && len(addrs) > 0 {
						// Extract last octet from response (e.g., 127.0.0.2 -> 2)
						responseIP := addrs[0].IP.To4()
						if responseIP != nil && len(responseIP) == 4 {
							code := responseIP[3]
							spamhausReason := getSpamhausReason(code)
							reason = fmt.Sprintf("%s (%s)", l, spamhausReason)
						}
					}
					resultChan <- result{list: l, reason: reason}
				}
			}
		}(list))
	}

	// Wait for all lookups to complete in a separate goroutine to not block the timeout.
	logging.SafeGo(c.logger, "blacklist-closer", func() {
		wg.Wait()
		close(resultChan)
	})

	select {
	case res, ok := <-resultChan:
		if !ok {
			// Channel closed, no results found
			return false, "", nil
		}
		// As soon as we get one result, we can return.
		return true, res.reason, nil
	case <-ctx.Done():
		// Timeout occurred
		return false, "", nil
	}
}

// CheckHELOResolves verifies that a given HELO hostname resolves to an IP address.
func CheckHELOResolves(hostname string, timeout time.Duration) (bool, string, error) {
	// Handle IP address literals in brackets [192.168.1.1]
	if strings.HasPrefix(hostname, "[") && strings.HasSuffix(hostname, "]") {
		ipStr := strings.Trim(hostname, "[]")
		ip := net.ParseIP(ipStr)
		if ip != nil {
			return true, "IP address literal", nil
		}
		return false, "Invalid IP address literal", nil
	}

	// Empty hostname doesn't resolve
	if hostname == "" {
		return false, "Empty hostname", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	addrs, err := (&net.Resolver{}).LookupHost(ctx, hostname)
	if err != nil {
		// Check if it's a "no such host" error, which is a valid check result.
		if dnsErr, ok := err.(*net.DNSError); ok && dnsErr.IsNotFound {
			return false, "Hostname does not resolve", nil
		}
		// For other errors (e.g., timeout), return the error.
		return false, "", err
	}

	if len(addrs) > 0 {
		return true, "Valid hostname", nil
	}
	return false, "No addresses found", nil
}

// reverseIP reverses the octets of an IPv4 address for DNSBL lookups.
func reverseIP(ip net.IP) string {
	// Ensure we're working with a 4-byte IPv4 address.
	ipv4 := ip.To4()
	if ipv4 == nil {
		return "" // Not an IPv4 address
	}

	parts := strings.Split(ipv4.String(), ".")
	if len(parts) != 4 {
		return ""
	}

	return fmt.Sprintf("%s.%s.%s.%s", parts[3], parts[2], parts[1], parts[0])
}
