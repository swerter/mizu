package tls

import (
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"
)

// BenchmarkTraditionalTLS benchmarks traditional TLS listener (handshake in Accept)
func BenchmarkTraditionalTLS(b *testing.B) {
	cert, err := generateTestCert()
	if err != nil {
		b.Fatalf("Failed to generate test cert: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
	}

	// Create TCP listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	// Wrap with traditional TLS listener
	tlsListener := tls.NewListener(listener, tlsConfig)
	addr := listener.Addr().String()

	// Accept connections in background
	var wg sync.WaitGroup
	go func() {
		for {
			conn, err := tlsListener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				buf := make([]byte, 1024)
				c.Read(buf)
			}(conn)
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			b.Fatalf("Failed to connect: %v", err)
		}
		conn.Write([]byte("test"))
		conn.Close()
	}
	b.StopTimer()
	wg.Wait()
}

// BenchmarkDeferredTLS benchmarks deferred TLS listener (handshake deferred)
func BenchmarkDeferredTLS(b *testing.B) {
	cert, err := generateTestCert()
	if err != nil {
		b.Fatalf("Failed to generate test cert: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
	}

	// Create TCP listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Wrap with deferred TLS listener
	deferredListener := NewDeferredTLSListener(listener, tlsConfig, 5*time.Second, logger)
	addr := listener.Addr().String()

	// Accept connections in background
	var wg sync.WaitGroup
	go func() {
		for {
			conn, err := deferredListener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(conn net.Conn) {
				defer wg.Done()
				defer conn.Close()
				buf := make([]byte, 1024)
				conn.Read(buf) // Handshake happens here
			}(conn)
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			b.Fatalf("Failed to connect: %v", err)
		}
		conn.Write([]byte("test"))
		conn.Close()
	}
	b.StopTimer()
	wg.Wait()
}

// BenchmarkConcurrentTraditionalTLS benchmarks traditional TLS under concurrent load
func BenchmarkConcurrentTraditionalTLS(b *testing.B) {
	cert, err := generateTestCert()
	if err != nil {
		b.Fatalf("Failed to generate test cert: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	tlsListener := tls.NewListener(listener, tlsConfig)
	addr := listener.Addr().String()

	// Accept connections
	go func() {
		for {
			conn, err := tlsListener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buf := make([]byte, 1024)
				conn.Read(buf)
			}(conn)
		}
	}()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
			if err != nil {
				b.Errorf("Failed to connect: %v", err)
				return
			}
			conn.Write([]byte("test"))
			conn.Close()
		}
	})
}

// BenchmarkConcurrentDeferredTLS benchmarks deferred TLS under concurrent load
func BenchmarkConcurrentDeferredTLS(b *testing.B) {
	cert, err := generateTestCert()
	if err != nil {
		b.Fatalf("Failed to generate test cert: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	deferredListener := NewDeferredTLSListener(listener, tlsConfig, 5*time.Second, logger)
	addr := listener.Addr().String()

	// Accept connections
	go func() {
		for {
			conn, err := deferredListener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buf := make([]byte, 1024)
				conn.Read(buf) // Handshake deferred to here
			}(conn)
		}
	}()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
			if err != nil {
				b.Errorf("Failed to connect: %v", err)
				return
			}
			conn.Write([]byte("test"))
			conn.Close()
		}
	})
}
