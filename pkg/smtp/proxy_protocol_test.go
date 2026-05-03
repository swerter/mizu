package smtp

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"migadu/mizu/pkg/config"
	"migadu/mizu/pkg/logging"

	gosmtp "github.com/emersion/go-smtp"
	proxyproto "github.com/pires/go-proxyproto"
)

// buildProxyListener creates a proxyproto.Listener with a trust policy
// that only accepts PROXY headers from the given CIDRs/IPs.
func buildProxyListener(ln net.Listener, trusted []string) *proxyproto.Listener {
	var trustedNets []*net.IPNet
	for _, entry := range trusted {
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			trustedNets = append(trustedNets, cidr)
		} else if ip := net.ParseIP(entry); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			trustedNets = append(trustedNets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}

	return &proxyproto.Listener{
		Listener: ln,
		Policy: func(upstream net.Addr) (proxyproto.Policy, error) {
			tcpAddr, ok := upstream.(*net.TCPAddr)
			if !ok {
				return proxyproto.REJECT, nil
			}
			for _, n := range trustedNets {
				if n.Contains(tcpAddr.IP) {
					return proxyproto.REQUIRE, nil
				}
			}
			return proxyproto.REJECT, nil
		},
	}
}

func startTestSMTPServer(t *testing.T, backend *Backend) (net.Listener, *gosmtp.Server) {
	t.Helper()
	s := gosmtp.NewServer(backend)
	s.Domain = "localhost"
	s.ReadTimeout = 10 * time.Second
	s.WriteTimeout = 10 * time.Second
	s.MaxMessageBytes = 1024 * 1024
	s.AllowInsecureAuth = true
	return nil, s
}

// TestE2E_ProxyProtocol_V1 verifies that when the PROXY protocol is enabled,
// the SMTP session sees the real client IP from the PROXY header instead of
// the local connecting IP.
func TestE2E_ProxyProtocol_V1(t *testing.T) {
	tracker := NewConnectionTracker(100, 10, 0, nil)
	logger := logging.NewTestLogger()

	backend := &Backend{
		ServerConfig: &config.ServerConfig{
			Name:                 "test-proxy",
			Type:                 "relay",
			Hostname:             "localhost",
			ProxyProtocol:        true,
			ProxyProtocolTrusted: []string{"127.0.0.1"},
		},
		GlobalConfig: &config.Config{
			Local: true,
		},
		ConnTracker:      tracker,
		ActiveSessionsWg: &sync.WaitGroup{},
		Logger:           logger,
	}

	s := gosmtp.NewServer(backend)
	s.Domain = "localhost"
	s.ReadTimeout = 10 * time.Second
	s.WriteTimeout = 10 * time.Second
	s.MaxMessageBytes = 1024 * 1024
	s.AllowInsecureAuth = true

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer ln.Close()

	// Trust 127.0.0.1 (our test connects from localhost)
	proxyListener := buildProxyListener(ln, []string{"127.0.0.1"})

	go s.Serve(proxyListener)
	defer s.Close()

	addr := ln.Addr().String()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	// Send PROXY protocol v1 header with fake source IP 10.20.30.40
	proxyHeader := &proxyproto.Header{
		Version:           1,
		Command:           proxyproto.PROXY,
		TransportProtocol: proxyproto.TCPv4,
		SourceAddr:        &net.TCPAddr{IP: net.ParseIP("10.20.30.40"), Port: 12345},
		DestinationAddr:   &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 25},
	}
	if _, err := proxyHeader.WriteTo(conn); err != nil {
		t.Fatalf("Failed to write PROXY header: %v", err)
	}

	buf := make([]byte, 512)
	// Read greeting
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read greeting: %v", err)
	}
	t.Logf("Greeting: %s", string(buf[:n]))

	fmt.Fprintf(conn, "EHLO test.local\r\n")
	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read EHLO response: %v", err)
	}
	t.Logf("EHLO response: %s", string(buf[:n]))

	time.Sleep(200 * time.Millisecond)

	// The connection tracker should have recorded the PROXY protocol source IP (10.20.30.40),
	// NOT the actual local connecting IP (127.0.0.1)
	total, _, perIP := tracker.GetStats()
	t.Logf("Connection stats: total=%d, perIP=%v", total, perIP)

	if total != 1 {
		t.Errorf("Expected 1 connection, got %d", total)
	}

	proxyIP := "10.20.30.40"
	if count, ok := perIP[proxyIP]; !ok || count != 1 {
		t.Errorf("Expected 1 connection from PROXY IP %s, got perIP=%v", proxyIP, perIP)
	}

	if count, ok := perIP["127.0.0.1"]; ok && count > 0 {
		t.Errorf("Local IP 127.0.0.1 should NOT be tracked when PROXY protocol is used, got count=%d", count)
	}

	fmt.Fprintf(conn, "QUIT\r\n")
	conn.Read(buf)
}

// TestE2E_ProxyProtocol_V2 verifies PROXY protocol v2 (binary format) works correctly.
func TestE2E_ProxyProtocol_V2(t *testing.T) {
	tracker := NewConnectionTracker(100, 10, 0, nil)
	logger := logging.NewTestLogger()

	backend := &Backend{
		ServerConfig: &config.ServerConfig{
			Name:                 "test-proxy-v2",
			Type:                 "relay",
			Hostname:             "localhost",
			ProxyProtocol:        true,
			ProxyProtocolTrusted: []string{"127.0.0.0/8"},
		},
		GlobalConfig: &config.Config{
			Local: true,
		},
		ConnTracker:      tracker,
		ActiveSessionsWg: &sync.WaitGroup{},
		Logger:           logger,
	}

	s := gosmtp.NewServer(backend)
	s.Domain = "localhost"
	s.ReadTimeout = 10 * time.Second
	s.WriteTimeout = 10 * time.Second
	s.MaxMessageBytes = 1024 * 1024
	s.AllowInsecureAuth = true

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer ln.Close()

	proxyListener := buildProxyListener(ln, []string{"127.0.0.0/8"})

	go s.Serve(proxyListener)
	defer s.Close()

	addr := ln.Addr().String()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	// Send PROXY protocol v2 header with source IP 203.0.113.50
	proxyHeader := &proxyproto.Header{
		Version:           2,
		Command:           proxyproto.PROXY,
		TransportProtocol: proxyproto.TCPv4,
		SourceAddr:        &net.TCPAddr{IP: net.ParseIP("203.0.113.50"), Port: 54321},
		DestinationAddr:   &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 25},
	}
	if _, err := proxyHeader.WriteTo(conn); err != nil {
		t.Fatalf("Failed to write PROXY v2 header: %v", err)
	}

	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read greeting: %v", err)
	}
	t.Logf("Greeting: %s", string(buf[:n]))

	fmt.Fprintf(conn, "EHLO test.local\r\n")
	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read EHLO response: %v", err)
	}
	t.Logf("EHLO response: %s", string(buf[:n]))

	time.Sleep(200 * time.Millisecond)

	total, _, perIP := tracker.GetStats()
	t.Logf("Connection stats: total=%d, perIP=%v", total, perIP)

	if total != 1 {
		t.Errorf("Expected 1 connection, got %d", total)
	}

	proxyIP := "203.0.113.50"
	if count, ok := perIP[proxyIP]; !ok || count != 1 {
		t.Errorf("Expected 1 connection from PROXY IP %s, got perIP=%v", proxyIP, perIP)
	}

	if count, ok := perIP["127.0.0.1"]; ok && count > 0 {
		t.Errorf("Local IP 127.0.0.1 should NOT be tracked, got count=%d", count)
	}

	fmt.Fprintf(conn, "QUIT\r\n")
	conn.Read(buf)
}

// TestE2E_ProxyProtocol_UntrustedSource verifies that connections from
// untrusted IPs are rejected when PROXY protocol is enabled.
func TestE2E_ProxyProtocol_UntrustedSource(t *testing.T) {
	tracker := NewConnectionTracker(100, 10, 0, nil)
	logger := logging.NewTestLogger()

	backend := &Backend{
		ServerConfig: &config.ServerConfig{
			Name:                 "test-proxy-untrusted",
			Type:                 "relay",
			Hostname:             "localhost",
			ProxyProtocol:        true,
			ProxyProtocolTrusted: []string{"10.0.0.0/8"}, // Only trust 10.x.x.x, NOT 127.0.0.1
		},
		GlobalConfig: &config.Config{
			Local: true,
		},
		ConnTracker:      tracker,
		ActiveSessionsWg: &sync.WaitGroup{},
		Logger:           logger,
	}

	s := gosmtp.NewServer(backend)
	s.Domain = "localhost"
	s.ReadTimeout = 5 * time.Second
	s.WriteTimeout = 5 * time.Second
	s.MaxMessageBytes = 1024 * 1024
	s.AllowInsecureAuth = true

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer ln.Close()

	// Only trust 10.0.0.0/8 — our connection from 127.0.0.1 is untrusted
	proxyListener := buildProxyListener(ln, []string{"10.0.0.0/8"})

	go s.Serve(proxyListener)
	defer s.Close()

	addr := ln.Addr().String()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	// Send a PROXY header from an untrusted source — connection should be rejected
	proxyHeader := &proxyproto.Header{
		Version:           1,
		Command:           proxyproto.PROXY,
		TransportProtocol: proxyproto.TCPv4,
		SourceAddr:        &net.TCPAddr{IP: net.ParseIP("10.20.30.40"), Port: 12345},
		DestinationAddr:   &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 25},
	}
	if _, err := proxyHeader.WriteTo(conn); err != nil {
		t.Fatalf("Failed to write PROXY header: %v", err)
	}

	// The connection should be closed by the server since 127.0.0.1 is not trusted
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 512)
	_, err = conn.Read(buf)

	// We expect either EOF or a connection reset — the server should reject the connection
	if err == nil {
		t.Errorf("Expected connection to be rejected from untrusted source, but got data: %s", string(buf))
	} else {
		t.Logf("Connection correctly rejected from untrusted source: %v", err)
	}

	// Verify no sessions were created
	total, _, _ := tracker.GetStats()
	if total != 0 {
		t.Errorf("Expected 0 connections (untrusted source should be rejected), got %d", total)
	}
}

// TestE2E_WithoutProxyProtocol_UsesRealIP verifies that without PROXY protocol,
// the real connecting IP is used as expected.
func TestE2E_WithoutProxyProtocol_UsesRealIP(t *testing.T) {
	tracker := NewConnectionTracker(100, 10, 0, nil)
	logger := logging.NewTestLogger()

	backend := &Backend{
		ServerConfig: &config.ServerConfig{
			Name:     "test-no-proxy",
			Type:     "relay",
			Hostname: "localhost",
		},
		GlobalConfig: &config.Config{
			Local: true,
		},
		ConnTracker:      tracker,
		ActiveSessionsWg: &sync.WaitGroup{},
		Logger:           logger,
	}

	s := gosmtp.NewServer(backend)
	s.Domain = "localhost"
	s.ReadTimeout = 10 * time.Second
	s.WriteTimeout = 10 * time.Second
	s.MaxMessageBytes = 1024 * 1024
	s.AllowInsecureAuth = true

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer ln.Close()

	// No proxy protocol wrapping — plain listener
	go s.Serve(ln)
	defer s.Close()

	addr := ln.Addr().String()

	c, err := gosmtp.Dial(addr)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer c.Close()

	if err := c.Hello("test.local"); err != nil {
		t.Fatalf("Hello failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	total, _, perIP := tracker.GetStats()
	t.Logf("Connection stats: total=%d, perIP=%v", total, perIP)

	if total != 1 {
		t.Errorf("Expected 1 connection, got %d", total)
	}

	if count, ok := perIP["127.0.0.1"]; !ok || count != 1 {
		t.Errorf("Expected 1 connection from 127.0.0.1, got perIP=%v", perIP)
	}

	c.Quit()
}
