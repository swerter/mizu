package tls

import (
	"crypto/tls"
	"log/slog"
	"net"
	"time"
)

// DeferredTLSListener wraps a TCP listener and returns connections with deferred TLS handshakes.
// This prevents head-of-line blocking where slow TLS handshakes block subsequent connections
// from being accepted.
type DeferredTLSListener struct {
	listener         net.Listener
	tlsConfig        *tls.Config
	handshakeTimeout time.Duration
	logger           *slog.Logger
}

// NewDeferredTLSListener creates a new listener that wraps accepted connections with deferred TLS.
// The TLS handshake will be deferred until the first Read/Write operation on the connection.
//
// Parameters:
//   - listener: The underlying TCP listener
//   - tlsConfig: TLS configuration for handshakes
//   - handshakeTimeout: Timeout for TLS handshake (0 for no timeout)
//   - logger: Logger for debugging (can be nil)
func NewDeferredTLSListener(listener net.Listener, tlsConfig *tls.Config, handshakeTimeout time.Duration, logger *slog.Logger) *DeferredTLSListener {
	return &DeferredTLSListener{
		listener:         listener,
		tlsConfig:        tlsConfig,
		handshakeTimeout: handshakeTimeout,
		logger:           logger,
	}
}

// Accept waits for and returns the next connection to the listener.
// The returned connection is wrapped with DeferredTLSConn, which performs
// the TLS handshake on the first Read/Write operation instead of in Accept().
//
// This prevents head-of-line blocking where a slow TLS handshake blocks
// all subsequent connections from being accepted.
func (l *DeferredTLSListener) Accept() (net.Conn, error) {
	// Accept TCP connection (fast, non-blocking)
	tcpConn, err := l.listener.Accept()
	if err != nil {
		return nil, err
	}

	remoteAddr := tcpConn.RemoteAddr().String()
	if l.logger != nil {
		l.logger.Debug("Accepted TCP connection (TLS handshake deferred)",
			"remote_addr", remoteAddr)
	}

	// Return connection WITHOUT performing handshake
	// Handshake will be performed by protocol handler on first Read/Write
	return NewDeferredTLSConn(tcpConn, l.tlsConfig, l.handshakeTimeout, l.logger), nil
}

// Close closes the underlying listener
func (l *DeferredTLSListener) Close() error {
	return l.listener.Close()
}

// Addr returns the listener's network address
func (l *DeferredTLSListener) Addr() net.Addr {
	return l.listener.Addr()
}
