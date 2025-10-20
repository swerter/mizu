package smtp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	net_http "net/http"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"migadu/mizu/pkg/blacklist"
	"migadu/mizu/pkg/config"
	"migadu/mizu/pkg/dns"
	"migadu/mizu/pkg/logging"
	"migadu/mizu/pkg/metrics"
	"migadu/mizu/pkg/poster"
	"migadu/mizu/pkg/queue"
	"migadu/mizu/pkg/routing"
	"migadu/mizu/pkg/stats"
	"migadu/mizu/pkg/validation"

	"github.com/emersion/go-msgauth/authres"
	"github.com/emersion/go-smtp"
)

const (
	// Session timeouts for security and resource management
	SessionDeadline   = 5 * time.Minute  // Hard limit for entire SMTP session to prevent hanging connections
	ProcessingTimeout = 30 * time.Second // Timeout for processing a command after it's received
	IdleTimeout       = 1 * time.Minute  // Maximum idle time between commands before disconnect
	DataTimeout       = 2 * time.Minute  // Timeout for receiving email body after DATA command
)

// generateTraceID creates a unique trace ID for correlating logs and tracking emails through the system.
// Format: 16 character hex string (8 random bytes)
func generateTraceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if random fails
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// NewDNSResolver creates a DNS resolver based on configuration.
// If custom DNS servers are configured, uses them with round-robin, failover, and caching;
// otherwise uses system default resolver.
// Returns both the resolver and a caching wrapper for cache-aware operations.
func NewDNSResolver(dnsServers []string, timeout time.Duration, cacheTTL time.Duration) (*net.Resolver, *dns.CachingWrapper) {
	// Use the resilient resolver which implements round-robin, failover, and caching
	resolver, rr := dns.NewResilientResolver(dnsServers, timeout, cacheTTL)

	// Wrap with caching layer
	wrapper := dns.WrapWithCache(resolver, rr)

	return resolver, wrapper
}

// RoutingClient defines the interface for routing lookups
// This allows for easier testing and potential alternative implementations
type RoutingClient interface {
	Resolve(ctx context.Context, recipient, sender, clientIP, subject string) (*routing.ResolveResponse, error)
	FlushCache()
	GetStats() map[string]interface{}
}

// Backend implements smtp.Backend interface for our custom SMTP server.
// It manages the overall server configuration and creates new sessions for incoming connections.
type Backend struct {
	ServerConfig   *config.ServerConfig   // Server-specific configuration (this server instance)
	GlobalConfig   *config.Config         // Global configuration (shared across servers)
	StatsManager   *stats.Manager         // IP and domain reputation tracking
	CircuitBreaker *poster.CircuitBreaker // Circuit breaker for destination HTTP calls
	HTTPClient     *net_http.Client       // HTTP client for posting emails to destination
	Logger         *slog.Logger           // Structured logger for debugging and monitoring
	DNSResolver    *net.Resolver          // Custom DNS resolver (uses config.DNS.Servers or system default)
	Metrics        *metrics.Metrics       // Prometheus metrics for observability

	// Connection tracking for graceful shutdown and DoS protection
	ActiveSessionsWg   *sync.WaitGroup     // Tracks active SMTP sessions
	ActiveSessionCount *atomic.Int64       // Current number of active sessions (for observability)
	ShutdownChan       chan struct{}       // Signals shutdown to new connections
	ConnTracker        *ConnectionTracker  // Tracks connections to enforce limits
	DistTracker        *DistributedTracker // Optional: Distributed connection tracking
	RateLimiter        *RateLimiter        // Rate limiter to prevent rapid connection attempts

	// Authentication and signing (for submission servers)
	Authenticator Authenticator         // Optional: Authenticates users (submission servers)
	DKIMSigner    DKIMSigner            // Optional: Signs outbound mail with DKIM (submission servers)
	ARCSigner     *validation.ARCSigner // Optional: ARC signer for adding ARC headers

	// Routing and delivery
	RoutingClient RoutingClient // Optional: Routing/aliasing client
	DeliveryQueue DeliveryQueue // Optional: Async delivery queue (used with routing)
	SRSRewriter   SRSRewriter   // Optional: SRS rewriter for forwarding
}

// Authenticator interface for SMTP AUTH
type Authenticator interface {
	Authenticate(username, password string) (bool, error)
	CanSendAs(authenticatedUser, fromAddress string) bool
}

// DKIMSigner interface for signing outbound mail
type DKIMSigner interface {
	SignEmail(rawEmail string) (string, error)
}

// SRSRewriter defines the interface for Sender Rewriting Scheme operations
type SRSRewriter interface {
	Encode(originalAddress string) (string, error)
	Decode(srsAddress string) (string, error)
	IsSRSAddress(address string) bool
}

// DeliveryQueue defines the interface for async email delivery
type DeliveryQueue interface {
	Enqueue(job *queue.DeliveryJob) error
	GetStats() queue.QueueStats
	Size() int
	Start() error
	Shutdown(ctx context.Context) error
}

// EHLO/HELO is called for the HELO/EHLO command.
// This implements the optional smtp.EHLOBackend interface
func (be *Backend) EHLO(hostname string) error {
	// This is called by go-smtp when EHLO is received
	// We don't do validation here as it's done in the session
	return nil
}

// NewSession is called when a new SMTP session is started.
// It performs initial validation (blacklists, reverse DNS) before creating a session.
func (be *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	// Check if server is shutting down
	select {
	case <-be.ShutdownChan:
		be.Logger.Info("Server shutting down - rejecting new connection")
		return nil, &smtp.SMTPError{
			Code:         421,
			EnhancedCode: smtp.EnhancedCode{4, 3, 2},
			Message:      "server is shutting down, please try again later",
		}
	default:
		// Continue with session creation
	}

	remoteAddr := c.Conn().RemoteAddr().String()

	// Record connection attempt in metrics
	if be.Metrics != nil {
		be.Metrics.SMTPConnectionsTotal.WithLabelValues(be.ServerConfig.Name, be.ServerConfig.Type).Inc()
	}

	// Track whether session was successfully created for connection cleanup
	sessionCreated := false

	// Check rate limit (prevent rapid connection attempts)
	// At this point, we only have IP information, so only IP-based dimensions will be checked
	if be.RateLimiter != nil {
		sessionCtx := SessionContext{
			RemoteAddr: remoteAddr,
		}
		if err := be.RateLimiter.CheckRateLimit(sessionCtx); err != nil {
			be.Logger.Warn("Rate limit exceeded", "remote_addr", remoteAddr, "error", err)

			// Record rejection in metrics
			if be.Metrics != nil {
				be.Metrics.SMTPMessagesRejected.WithLabelValues(be.ServerConfig.Name, be.ServerConfig.Type, "rate_limit").Inc()
			}

			return nil, &smtp.SMTPError{
				Code:         421,
				EnhancedCode: smtp.EnhancedCode{4, 3, 2},
				Message:      "rate limit exceeded, please slow down",
			}
		}
	}

	// Check connection limits (DoS protection)
	// Use distributed tracker if available, otherwise fall back to local tracker
	tracker := be.ConnTracker
	if be.DistTracker != nil {
		// Distributed tracker wraps local tracker
		if err := be.DistTracker.TryAcquire(remoteAddr); err != nil {
			be.Logger.Warn("Distributed connection limit exceeded", "remote_addr", remoteAddr, "error", err)
			return nil, &smtp.SMTPError{
				Code:         421,
				EnhancedCode: smtp.EnhancedCode{4, 3, 2},
				Message:      "too many connections, please try again later",
			}
		}
		// Ensure connection is released if we fail to create session
		defer func() {
			if !sessionCreated {
				be.DistTracker.Release(remoteAddr)
			}
		}()
	} else if tracker != nil {
		if err := tracker.TryAcquire(remoteAddr); err != nil {
			be.Logger.Warn("Connection limit exceeded", "remote_addr", remoteAddr, "error", err)
			return nil, &smtp.SMTPError{
				Code:         421,
				EnhancedCode: smtp.EnhancedCode{4, 3, 2},
				Message:      "too many connections, please try again later",
			}
		}
		// Ensure connection is released if we fail to create session
		defer func() {
			if !sessionCreated {
				tracker.Release(remoteAddr)
			}
		}()
	}

	// Track this session for graceful shutdown with panic recovery
	if be.ActiveSessionsWg != nil {
		be.ActiveSessionsWg.Add(1)

		// Increment active session counter for observability
		if be.ActiveSessionCount != nil {
			count := be.ActiveSessionCount.Add(1)
			be.Logger.Debug("Session count incremented",
				"active_sessions", count,
				"remote_addr", remoteAddr)

			// Update Prometheus gauge for active connections
			if be.Metrics != nil {
				be.Metrics.SMTPConnectionsActive.WithLabelValues(be.ServerConfig.Name, be.ServerConfig.Type).Set(float64(count))
			}
		}

		// Panic recovery: ensure we call Done() if session creation panics
		defer func() {
			if r := recover(); r != nil {
				be.Logger.Error("Panic in NewSession - recovering",
					"remote_addr", remoteAddr,
					"panic", r,
					"stack", "")

				// Decrement counter and call Done() since we won't reach Logout()
				if be.ActiveSessionCount != nil {
					be.ActiveSessionCount.Add(-1)
				}
				be.ActiveSessionsWg.Done()

				// Re-panic to let go-smtp library handle it
				panic(r)
			}
		}()
	}

	be.Logger.Info("New session", "remote_addr", remoteAddr)

	ipStr := stats.GetIPFromRemoteAddr(remoteAddr)
	hasRDNS := true

	// Perform security checks in production mode (skip for submission servers with relaxed validation)
	// Relay servers should validate, submission servers with skip_rdns/skip_dnsbl will skip
	if !be.GlobalConfig.Local {
		// Extract IP from address
		host, _, err := net.SplitHostPort(remoteAddr)
		if err != nil {
			be.Logger.Error("Failed to parse remote address", "remote_addr", remoteAddr, "error", err)
			return nil, ErrInternalServerError
		}

		// Parse IP address
		ip := net.ParseIP(host)
		if ip == nil {
			be.Logger.Error("Failed to parse IP address", "host", host)
			return nil, ErrInternalServerError
		}

		// Check DNS blacklists (RBLs) to prevent spam
		if be.ServerConfig.DNSBL.Enabled && len(be.ServerConfig.DNSBL.Lists) > 0 {
			timeoutSecs := be.ServerConfig.DNSBL.TimeoutSeconds
			if timeoutSecs == 0 {
				timeoutSecs = 3 // Default timeout
			}
			checker := blacklist.NewChecker(be.ServerConfig.DNSBL.Lists, time.Duration(timeoutSecs)*time.Second, be.Logger)
			isListed, reason, err := checker.CheckIP(ip)
			if err != nil {
				be.Logger.Error("Blacklist check error", "error", err, "ip", host)
				// Don't reject on blacklist check errors - fail open for availability
			} else if isListed {
				// Determine action based on configuration
				action := be.ServerConfig.DNSBL.Action
				if action == "" {
					action = "reject" // Default to reject
				}

				be.Logger.Warn("IP blacklisted", "remote_addr", remoteAddr, "reason", reason, "action", action)

				// Record blacklist detection in metrics
				if be.Metrics != nil {
					be.Metrics.SMTPBlacklistChecks.WithLabelValues(be.ServerConfig.Name, "blocked").Inc()
				}

				switch action {
				case "reject":
					// Reject the connection
					if be.Metrics != nil {
						be.Metrics.SMTPMessagesRejected.WithLabelValues(be.ServerConfig.Name, be.ServerConfig.Type, "blacklist").Inc()
					}
					return nil, &smtp.SMTPError{
						Code:         550,
						EnhancedCode: smtp.EnhancedCode{5, 7, 1},
						Message:      fmt.Sprintf("your IP address is blacklisted: %s", reason),
					}
				case "junk":
					// Mark for junk but allow connection
					be.Logger.Info("Allowing blacklisted IP but marking as junk", "ip", host, "reason", reason)
					// Note: The session will be created and marked as junk
				case "none":
					// Just log but take no action
					be.Logger.Info("Blacklisted IP detected but no action configured", "ip", host, "reason", reason)
				}
			} else if be.Metrics != nil {
				// Record successful blacklist check
				be.Metrics.SMTPBlacklistChecks.WithLabelValues(be.ServerConfig.Name, "pass").Inc()
			}
		}

		// Require valid reverse DNS (PTR record) - helps prevent spam from compromised hosts
		// Use context with timeout to prevent hanging on unresponsive DNS servers
		rdnsCtx, rdnsCancel := context.WithTimeout(context.Background(), time.Duration(be.GlobalConfig.DNS.TimeoutSeconds)*time.Second)
		names, err := be.DNSResolver.LookupAddr(rdnsCtx, host)
		rdnsCancel()
		if err != nil || len(names) == 0 {
			hasRDNS = false
			// Record this in stats
			if be.StatsManager != nil {
				be.StatsManager.RecordConnection(ipStr, false)
			}

			// Record rejection in metrics
			if be.Metrics != nil {
				be.Metrics.SMTPMessagesRejected.WithLabelValues(be.ServerConfig.Name, be.ServerConfig.Type, "no_rdns").Inc()
			}

			be.Logger.Warn("Rejecting session - no reverse DNS", "remote_addr", remoteAddr)
			return nil, &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 7, 25},
				Message:      "no reverse DNS record for IP address",
			}
		}
		be.Logger.Debug("Reverse DNS lookup", "host", host, "names", names)

		// Record connection in stats
		if be.StatsManager != nil {
			be.StatsManager.RecordConnection(ipStr, hasRDNS)

			// Check IP reputation
			if shouldDeny, reputation := be.StatsManager.CheckIPReputation(ipStr); shouldDeny {
				be.Logger.Warn("Rejecting session - poor reputation", "remote_addr", remoteAddr, "score", reputation)
				return nil, &smtp.SMTPError{
					Code:         421,
					EnhancedCode: smtp.EnhancedCode{4, 7, 1},
					Message:      "please try again later",
				}
			}
		}
	}

	// Store connection state for TLS checking
	state, ok := c.TLSConnectionState()
	var tlsState *tls.ConnectionState
	if ok {
		tlsState = &state
	}

	// Create session context with deadline
	ctx, cancel := context.WithTimeout(context.Background(), SessionDeadline)

	// Set initial idle timeout
	if err := c.Conn().SetDeadline(time.Now().Add(IdleTimeout)); err != nil {
		cancel()
		be.Logger.Error("Failed to set deadline", "error", err)
		return nil, ErrInternalServerError
	}

	// Generate unique trace ID for this session
	traceID := generateTraceID()

	session := &Session{
		conn:              c,
		helo:              "",
		from:              "",
		to:                make([]string, 0),
		remoteAddr:        c.Conn().RemoteAddr().String(),
		serverConfig:      be.ServerConfig,
		globalConfig:      be.GlobalConfig,
		tlsState:          tlsState,
		statsManager:      be.StatsManager,
		circuitBreaker:    be.CircuitBreaker,
		httpClient:        be.HTTPClient,
		dnsResolver:       be.DNSResolver,
		connTracker:       be.ConnTracker,
		distTracker:       be.DistTracker,
		rateLimiter:       be.RateLimiter,
		metrics:           be.Metrics,
		ctx:               ctx,
		Logger:            be.Logger.With("trace_id", traceID),
		cancel:            cancel,
		sessionsWg:        be.ActiveSessionsWg,
		sessionCount:      be.ActiveSessionCount,
		commandState:      stateNew, // Explicitly initialize command state
		traceID:           traceID,
		isAuthenticated:   false,            // Not authenticated initially
		authenticatedUser: "",               // No user yet
		authenticator:     be.Authenticator, // Authenticator (nil if not submission)
		dkimSigner:        be.DKIMSigner,    // DKIM signer (nil if not enabled)
		arcSigner:         be.ARCSigner,     // ARC signer (nil if disabled)
		routingClient:     be.RoutingClient, // Routing client (nil if disabled)
		deliveryQueue:     be.DeliveryQueue, // Delivery queue (nil if disabled)
		srsRewriter:       be.SRSRewriter,   // SRS rewriter (nil if disabled)
	}

	sessionCreated = true
	return session, nil
}

// Session represents an active SMTP session for an incoming email.
// It tracks the SMTP conversation state and enforces protocol requirements.
type Session struct {
	conn           *smtp.Conn             // The underlying SMTP connection
	helo           string                 // HELO/EHLO domain from the client
	from           string                 // Sender's email address (MAIL FROM)
	to             []string               // Recipient email addresses (RCPT TO)
	remoteAddr     string                 // Remote IP:port of the client
	mailData       bytes.Buffer           // Buffer to store the raw email body
	serverConfig   *config.ServerConfig   // Server-specific configuration (this server instance)
	globalConfig   *config.Config         // Global configuration (shared across servers)
	tlsState       *tls.ConnectionState   // TLS connection state (nil if not using TLS)
	statsManager   *stats.Manager         // IP and domain reputation tracking
	circuitBreaker *poster.CircuitBreaker // Circuit breaker for HTTP destination
	httpClient     *net_http.Client       // HTTP client for posting emails to destination
	dnsResolver    *net.Resolver          // DNS resolver (custom or system default)
	connTracker    *ConnectionTracker     // Connection tracker for DoS protection
	distTracker    *DistributedTracker    // Distributed connection tracker (optional, for cluster-wide limits)
	rateLimiter    *RateLimiter           // Multi-dimensional rate limiter
	metrics        *metrics.Metrics       // Prometheus metrics for observability
	ctx            context.Context        // Session context with deadline for timeout
	Logger         *slog.Logger           // Structured logger for this session
	cancel         context.CancelFunc     // Cancel function to clean up resources
	sessionsWg     *sync.WaitGroup        // WaitGroup to track active sessions for graceful shutdown
	sessionCount   *atomic.Int64          // Pointer to active session counter for observability

	// Authentication (for submission servers)
	isAuthenticated   bool          // Whether user has authenticated via SMTP AUTH
	authenticatedUser string        // Username from successful authentication
	authenticator     Authenticator // Authenticator for this session
	dkimSigner        DKIMSigner    // DKIM signer for outbound mail

	// Anti-spam tracking
	isJunk       bool     // Whether this message is considered junk/spam
	junkReasons  []string // Specific reasons why message is marked as junk
	commandState int      // Track SMTP command sequence state for protocol enforcement
	traceID      string   // Unique trace ID for correlating logs and tracking email through system

	// Stats tracking
	senderDomain string // Domain from MAIL FROM for stats
	spfResult    *validation.SPFResult

	// Validation results for ARC signing
	dmarcResult *validation.DMARCResult
	arcResult   *validation.ARCResult
	arcSigner   *validation.ARCSigner // ARC signer (nil if ARC signing disabled)

	// Routing
	routingClient RoutingClient            // Routing client for recipient validation and aliasing (nil if disabled)
	routingResult *routing.ResolveResponse // Result from routing lookup (cached during RCPT TO)
	deliveryQueue DeliveryQueue            // Async delivery queue (nil if disabled)

	// SRS (Sender Rewriting Scheme)
	srsRewriter SRSRewriter // SRS rewriter for forwarding (nil if disabled)
}

// SMTP command states for sequence validation
// Ensures commands are issued in the correct order per RFC 5321
const (
	stateNew  = iota // Initial state before HELO/EHLO
	stateHelo        // After HELO/EHLO received
	stateMail        // After MAIL FROM received
	stateRcpt        // After at least one RCPT TO received
	stateData        // After DATA command (currently receiving message)
)

// serverName returns the server name for metric labeling
func (s *Session) serverName() string {
	if s.serverConfig != nil {
		return s.serverConfig.Name
	}
	return "unknown"
}

// serverType returns the server type for metric labeling
func (s *Session) serverType() string {
	if s.serverConfig != nil {
		return s.serverConfig.Type
	}
	return "unknown"
}

// Helo is called for the HELO/EHLO command.
// RFC 5321 requires this to be the first command in an SMTP session.
func (s *Session) Helo(hostname string) error {
	// Set timeout for this command
	if err := s.setCommandTimeout(ProcessingTimeout); err != nil {
		return err
	}

	// Enforce command sequence - HELO/EHLO must be first
	if s.commandState != stateNew {
		s.Logger.Warn("Rejecting HELO/EHLO - already received", "remote_addr", s.remoteAddr, "state", int(s.commandState))
		return &smtp.SMTPError{
			Code:         503,
			EnhancedCode: smtp.EnhancedCode{5, 5, 1},
			Message:      "bad sequence of commands",
		}
	}

	// Validate HELO hostname for security (skip in local development mode)
	if !s.globalConfig.Local {
		// Reject if HELO claims to be our own domain or a subdomain (spoofing attempt)
		// Case-insensitive check for robustness.
		if strings.HasSuffix(strings.ToLower(hostname), "."+strings.ToLower(s.serverConfig.Domain)) || strings.EqualFold(hostname, s.serverConfig.Domain) {
			s.Logger.Warn("Rejecting HELO/EHLO - client using our domain", "remote_addr", s.remoteAddr, "hostname", hostname, "our_domain", s.serverConfig.Domain)
			return &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 7, 8},
				Message:      "invalid HELO hostname",
			}
		}

		// Reject localhost or single-label hostnames
		if hostname == "localhost" || !strings.Contains(hostname, ".") {
			s.Logger.Warn("Rejecting HELO/EHLO - invalid hostname", "remote_addr", s.remoteAddr, "hostname", hostname)
			return &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 7, 1},
				Message:      "HELO requires fully-qualified hostname",
			}
		}

		// Reject bare IP addresses. Per RFC 5321, IP literals must be in brackets.
		isIPLiteral := strings.HasPrefix(hostname, "[") && strings.HasSuffix(hostname, "]")
		if !isIPLiteral && net.ParseIP(hostname) != nil {
			s.Logger.Warn("Rejecting HELO/EHLO - bare IP", "remote_addr", s.remoteAddr, "ip", hostname)
			return &smtp.SMTPError{
				Code:    550,
				Message: "HELO with bare IP must use [IP] format",
			}
		}

		// Check for invalid characters
		if strings.ContainsAny(hostname, " \t\r\n") {
			s.Logger.Warn("Rejecting HELO/EHLO - invalid characters", "remote_addr", s.remoteAddr)
			return &smtp.SMTPError{
				Code:    550,
				Message: "invalid characters in HELO hostname",
			}
		}

		// Optional: Verify HELO hostname has valid DNS records
		if s.globalConfig.Blacklists.CheckHELOResolves {
			resolves, reason, err := blacklist.CheckHELOResolves(hostname, time.Duration(s.globalConfig.Blacklists.TimeoutSeconds)*time.Second)
			if err != nil || !resolves {
				s.Logger.Warn("Rejecting HELO/EHLO - hostname check failed", "remote_addr", s.remoteAddr, "hostname", hostname, "reason", reason)
				return &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 7, 27},
					Message:      fmt.Sprintf("HELO hostname check failed: %s", reason),
				}
			}
		}
	}

	s.helo = hostname
	s.commandState = stateHelo
	s.Logger.Info("HELO/EHLO received", "remote_addr", s.remoteAddr, "hostname", hostname)

	// Reset to idle timeout to wait for the next command
	if err := s.setCommandTimeout(IdleTimeout); err != nil {
		return err
	}

	return nil
}

// updateTLSState updates the TLS state from the connection
func (s *Session) updateTLSState() {
	if s.conn == nil {
		return
	}
	state, ok := s.conn.TLSConnectionState()
	if ok {
		s.tlsState = &state
	} else {
		s.tlsState = nil
	}
}

// setCommandTimeout sets the deadline for the current command
func (s *Session) setCommandTimeout(timeout time.Duration) error {
	// Skip timeout if no connection (e.g., in tests)
	if s.conn == nil {
		return nil
	}

	// Check if session deadline has been exceeded
	select {
	case <-s.ctx.Done():
		s.Logger.Warn("Session deadline exceeded", "remote_addr", s.remoteAddr)
		return ErrSessionTimeout
	default:
		// Set the command timeout
		deadline := time.Now().Add(timeout)
		if err := s.conn.Conn().SetDeadline(deadline); err != nil {
			s.Logger.Error("Failed to set deadline", "error", err)
			return ErrInternalServerError
		}
		return nil
	}
}

// Mail is called for the MAIL FROM command.
// This sets the envelope sender for the SMTP transaction.
func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	// Reject null sender <> (bounce messages) to prevent backscatter
	// Check this FIRST before any other processing for security
	if s.serverConfig.Junk.RejectNullSender && (from == "" || from == "<>") {
		s.Logger.Warn("Rejecting null sender", "remote_addr", s.remoteAddr)
		return &smtp.SMTPError{
			Code:         550,
			EnhancedCode: smtp.EnhancedCode{5, 7, 1},
			Message:      "null sender not accepted",
		}
	}

	// Handle case where go-smtp library processed EHLO internally
	heloHostname := s.conn.Hostname()
	if heloHostname != "" && s.helo == "" {
		// EHLO was handled by go-smtp internally, update our state
		s.helo = heloHostname
		s.commandState = stateHelo
		s.Logger.Debug("HELO/EHLO set via conn.Hostname", "remote_addr", s.remoteAddr, "hostname", heloHostname)
	}

	// Set timeout for this command
	if err := s.setCommandTimeout(ProcessingTimeout); err != nil {
		return err
	}

	// Check if HELO/EHLO has been received
	if s.helo == "" {
		s.Logger.Warn("Rejecting MAIL FROM - no HELO/EHLO", "remote_addr", s.remoteAddr)
		return &smtp.SMTPError{
			Code:         503,
			EnhancedCode: smtp.EnhancedCode{5, 5, 1},
			Message:      "bad sequence of commands - HELO/EHLO first",
		}
	}

	// Update and check TLS state (skip in local mode)
	s.updateTLSState()
	if !s.globalConfig.Local && s.tlsState == nil {
		s.Logger.Warn("Rejecting MAIL FROM - TLS required", "from", from)
		return ErrTLSRequiredStartTLS
	}

	// Extract domain from sender
	senderDomain := stats.ExtractDomainFromEmail(from)
	s.senderDomain = senderDomain

	// Record MAIL FROM in stats
	if s.statsManager != nil && senderDomain != "" {
		s.statsManager.RecordMailFrom(senderDomain)

		// Check domain reputation
		if shouldDeny, reputation := s.statsManager.CheckDomainReputation(senderDomain); shouldDeny {
			s.Logger.Warn("Rejecting MAIL FROM - poor domain reputation", "from", from, "score", reputation)
			return &smtp.SMTPError{
				Code:         421,
				EnhancedCode: smtp.EnhancedCode{4, 7, 1},
				Message:      "please try again later",
			}
		}
	}

	// Note: Mailbox/domain validation is now handled by the destination HTTP endpoint
	// The worker can reject messages for unknown users by returning 404
	// This allows dynamic user management without syncing mailbox lists

	// Perform SPF and MX checks in parallel (skip in local mode)
	if !s.globalConfig.Local {
		var wg sync.WaitGroup
		var spfMu sync.Mutex // Protect SPF result writes

		// SPF check in parallel
		ipStr := stats.GetIPFromRemoteAddr(s.remoteAddr)
		ip := net.ParseIP(ipStr)
		if ip != nil {
			wg.Add(1)
			logging.SafeGo(s.Logger, "spf-check", func() {
				defer wg.Done()
				res, err := validation.CheckSPF(context.Background(), ip, s.helo, from)
				if err != nil {
					s.Logger.Debug("SPF check error", "from", from, "error", err)
					if s.metrics != nil {
						s.metrics.SMTPSPFChecks.WithLabelValues(s.serverName(), "error").Inc()
					}
				} else if res != nil {
					resultStr := string(*res)
					s.Logger.Debug("SPF result", "from", from, "result", resultStr)

					// Record SPF check result in metrics
					if s.metrics != nil {
						s.metrics.SMTPSPFChecks.WithLabelValues(s.serverName(), resultStr).Inc()
					}

					spfMu.Lock()
					s.spfResult = &validation.SPFResult{
						Domain: s.senderDomain,
						Result: authres.SPFResult{
							Value: validation.ConvertSPFResult(*res),
						},
					}
					spfMu.Unlock()
				}
			})
		}

		// MX check in parallel
		var mxErr error
		var hasMX bool
		if s.serverConfig.DNSChecks.RequireSenderMX && senderDomain != "" {
			wg.Add(1)
			logging.SafeGo(s.Logger, "mx-check", func() {
				defer wg.Done()
				var err error
				hasMX, err = validation.CheckMXRecord(context.Background(), senderDomain, s.dnsResolver, time.Duration(s.globalConfig.DNS.TimeoutSeconds)*time.Second)
				if err != nil {
					s.Logger.Warn("MX lookup error for sender domain", "from", from, "domain", senderDomain, "error", err)
					mxErr = err
					// Continue despite lookup error - don't fail on temporary DNS issues
				} else if !hasMX {
					s.Logger.Warn("Sender domain has no MX records", "from", from, "domain", senderDomain)
				} else {
					s.Logger.Debug("Sender domain has valid MX records", "from", from, "domain", senderDomain)
				}
			})
		}

		// Wait for both DNS queries to complete
		wg.Wait()

		// Check MX result after parallel execution
		if s.serverConfig.DNSChecks.RequireSenderMX && senderDomain != "" && mxErr == nil && !hasMX {
			return &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 7, 1},
				Message:      "sender domain has no mail servers (no MX records)",
			}
		}
	}

	s.from = from
	s.commandState = stateMail

	// Check rate limits now that we have FROM information
	// This allows FROM, FROM_DOMAIN, and IP+FROM combination checks
	if s.rateLimiter != nil {
		sessionCtx := SessionContext{
			RemoteAddr: s.remoteAddr,
			From:       from,
			To:         s.to, // May be empty at this point
		}
		if err := s.rateLimiter.CheckRateLimit(sessionCtx); err != nil {
			s.Logger.Warn("Rate limit exceeded for sender", "from", from, "error", err)
			return &smtp.SMTPError{
				Code:         421,
				EnhancedCode: smtp.EnhancedCode{4, 3, 2},
				Message:      "rate limit exceeded, please slow down",
			}
		}
	}

	if s.tlsState != nil {
		s.Logger.Info("MAIL FROM", "from", from, "remote_addr", s.remoteAddr, "tls", tlsVersionString(s.tlsState.Version))
	} else {
		s.Logger.Info("MAIL FROM", "from", from, "remote_addr", s.remoteAddr, "tls", "none")
	}

	// Reset to idle timeout to wait for the next command
	if err := s.setCommandTimeout(IdleTimeout); err != nil {
		return err
	}

	return nil
}

// Rcpt is called for the RCPT TO command.
// This validates and adds recipients to the envelope.
func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	// Set timeout for this command
	if err := s.setCommandTimeout(ProcessingTimeout); err != nil {
		return err
	}

	// Enforce command sequence - must have MAIL FROM before RCPT TO
	if s.commandState != stateMail && s.commandState != stateRcpt {
		s.Logger.Warn("Rejecting RCPT TO - bad sequence", "remote_addr", s.remoteAddr)
		return &smtp.SMTPError{
			Code:         503,
			EnhancedCode: smtp.EnhancedCode{5, 5, 1},
			Message:      "bad sequence of commands - MAIL FROM first",
		}
	}

	// Update and check TLS state (skip in local mode)
	s.updateTLSState()
	if !s.globalConfig.Local && s.tlsState == nil {
		s.Logger.Warn("Rejecting RCPT TO - TLS required", "to", to)
		return ErrTLSRequired
	}

	s.Logger.Info("RCPT TO", "to", to, "remote_addr", s.remoteAddr)

	// Decode SRS addresses if this is an SRS bounce/reply
	// This converts SRS0=...@relay.mizu.com back to original@example.com
	if s.srsRewriter != nil && s.srsRewriter.IsSRSAddress(to) {
		decoded, err := s.srsRewriter.Decode(to)
		if err != nil {
			s.Logger.Warn("Failed to decode SRS address",
				"srs_address", to,
				"error", err)
			return &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 1, 1},
				Message:      "invalid SRS address",
			}
		}
		s.Logger.Info("Decoded SRS address",
			"srs_address", to,
			"original_address", decoded)
		to = decoded // Use decoded address for all subsequent processing
	}

	// Enforce single recipient per transaction
	// This ensures per-recipient validation at the destination and proper retry behavior
	if len(s.to) >= 1 {
		s.Logger.Warn("Rejecting RCPT TO - multiple recipients", "to", to)
		return &smtp.SMTPError{
			Code:         452,
			EnhancedCode: smtp.EnhancedCode{4, 5, 3},
			Message:      "Too many recipients (only one allowed per transaction)",
		}
	}

	s.to = append(s.to, to)
	s.commandState = stateRcpt

	// Check rate limits now that we have TO information
	// This allows TO, TO_DOMAIN, FROM+TO, and other recipient-based combination checks
	if s.rateLimiter != nil {
		sessionCtx := SessionContext{
			RemoteAddr: s.remoteAddr,
			From:       s.from,
			To:         s.to,
		}
		if err := s.rateLimiter.CheckRateLimit(sessionCtx); err != nil {
			s.Logger.Warn("Rate limit exceeded for recipient", "to", to, "error", err)
			return &smtp.SMTPError{
				Code:         421,
				EnhancedCode: smtp.EnhancedCode{4, 3, 2},
				Message:      "rate limit exceeded, please slow down",
			}
		}
	}

	// Perform routing lookup if enabled and configured to validate during RCPT
	if s.routingClient != nil && s.globalConfig.Routing.Enabled && s.globalConfig.Routing.ValidateDuringRcpt {
		ipStr := stats.GetIPFromRemoteAddr(s.remoteAddr)
		// Note: subject is not available during RCPT TO, will be empty
		result, err := s.routingClient.Resolve(s.ctx, to, s.from, ipStr, "")
		if err != nil {
			s.Logger.Warn("Routing lookup failed", "to", to, "error", err)

			// Handle fallback based on configuration
			if s.globalConfig.Routing.FallbackOnError == "reject" {
				return &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 4, 0},
					Message:      "recipient validation failed",
				}
			}
			// Default: tempfail
			return &smtp.SMTPError{
				Code:         451,
				EnhancedCode: smtp.EnhancedCode{4, 4, 0},
				Message:      "temporary failure, please try again later",
			}
		}

		// Check if recipient is accepted
		if !result.Accepted {
			s.Logger.Info("Recipient rejected by routing",
				"to", to,
				"error_code", result.ErrorCode,
				"error_message", result.ErrorMessage)

			// Map error codes to SMTP errors
			switch result.ErrorCode {
			case routing.ErrorCodeDomainNotFound:
				return &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 1, 1},
					Message:      "relay not permitted",
				}
			case routing.ErrorCodeRecipientNotFound:
				return &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 1, 1},
					Message:      "mailbox does not exist",
				}
			case routing.ErrorCodeRecipientBlocked:
				return &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 7, 1},
					Message:      "recipient blocked",
				}
			case routing.ErrorCodeQuotaExceeded:
				return &smtp.SMTPError{
					Code:         552,
					EnhancedCode: smtp.EnhancedCode{5, 2, 2},
					Message:      "mailbox full",
				}
			default:
				return &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 7, 1},
					Message:      result.ErrorMessage,
				}
			}
		}

		// Store routing result for use in DATA
		s.routingResult = result
		s.Logger.Info("Recipient accepted by routing",
			"to", to,
			"deliver_to", result.DeliverTo,
			"forward_to", result.ForwardTo,
			"is_catchall", result.IsCatchall)
	}

	// Reset to idle timeout to wait for the next command
	if err := s.setCommandTimeout(IdleTimeout); err != nil {
		return err
	}

	return nil
}

// Data is called when the email body is received.
// This is where we process the message headers and body, perform validation, and forward the email.
func (s *Session) Data(r io.Reader) (err error) {
	// 1. Perform initial checks and read the message data from the client.
	rawEmail, err := s.readMessageData(r)
	if err != nil {
		return err
	}
	defer s.mailData.Reset() // Ensure buffer is cleared after processing.

	// 2. Handle local mode separately for development and testing.
	if s.globalConfig.Local {
		return s.handleLocalMode(rawEmail)
	}

	// 3. Perform pre-delivery validation (headers, DMARC, etc.).
	// This may mark the message as junk or return a hard rejection error.
	if err := s.performPreDeliveryChecks(rawEmail); err != nil {
		return err
	}

	// 4. Attempt to deliver the message to the final destination.
	if err := s.deliverMessage(rawEmail); err != nil {
		return err
	}

	// 5. Finalize the session by recording stats for the successful delivery.
	s.finalizeSuccessfulDelivery()

	return nil
}

// readMessageData handles the initial checks and reads the email content from the client.
func (s *Session) readMessageData(r io.Reader) (string, error) {
	// Set extended timeout for receiving potentially large email data.
	if err := s.setCommandTimeout(DataTimeout); err != nil {
		return "", err
	}

	// Enforce command sequence - must have at least one recipient.
	if s.commandState != stateRcpt {
		s.Logger.Warn("Rejecting DATA - bad sequence", "remote_addr", s.remoteAddr)
		return "", &smtp.SMTPError{Code: 503, EnhancedCode: smtp.EnhancedCode{5, 5, 1}, Message: "bad sequence of commands - RCPT TO first"}
	}

	// Final update and check of TLS state.
	s.updateTLSState()
	if !s.globalConfig.Local && s.tlsState == nil {
		s.Logger.Warn("Rejecting DATA - TLS required")
		return "", ErrTLSRequired
	}

	s.Logger.Info("Receiving DATA", "from", s.from, "to", s.to)

	// Read the entire email into a buffer, respecting the size limit.
	if _, err := io.Copy(&s.mailData, io.LimitReader(r, int64(s.serverConfig.MaxMessageSize))); err != nil {
		s.Logger.Error("Failed to read message data", "error", err)
		return "", err
	}

	rawEmail := s.mailData.String()

	// Record message received and size in metrics
	if s.metrics != nil {
		s.metrics.SMTPMessagesReceived.WithLabelValues(s.serverName(), s.serverType()).Inc()
		s.metrics.SMTPMessageSize.WithLabelValues(s.serverName(), s.serverType()).Observe(float64(len(rawEmail)))
	}

	// Check for empty message.
	if strings.TrimSpace(rawEmail) == "" {
		s.Logger.Warn("Rejecting empty message", "from", s.from)
		return "", &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "empty message not accepted"}
	}

	return rawEmail, nil
}

// handleLocalMode dumps the email content to the console for development/testing.
func (s *Session) handleLocalMode(rawEmail string) error {
	s.Logger.Info("=== LOCAL MODE: EMAIL CONTENT START ===")
	fmt.Println(rawEmail)
	s.Logger.Info("=== LOCAL MODE: EMAIL CONTENT END ===")
	s.Logger.Info("Local mode - received email", "from", s.from, "to", s.to)
	return nil
}

// performPreDeliveryChecks runs all content validation checks (headers, DMARC).
// It may mark the message as junk or return an SMTPError for a hard rejection.
func (s *Session) performPreDeliveryChecks(rawEmail string) error {
	// Header validation
	if err := s.validateHeaders(rawEmail); err != nil {
		return err
	}

	// DMARC validation
	quarantineAction := s.serverConfig.DMARC.QuarantinePolicyAction
	if quarantineAction == "" {
		quarantineAction = "junk" // Default to junk for quarantine
	}
	dmarcResult, err := validation.CheckDMARC(context.Background(), rawEmail, s.spfResult, quarantineAction, s.Logger)
	s.dmarcResult = dmarcResult // Store for ARC signing
	if err != nil {
		s.Logger.Warn("DMARC validation error", "error", err)
		if s.metrics != nil {
			s.metrics.SMTPDMARCChecks.WithLabelValues(s.serverName(), "error").Inc()
		}
	} else if dmarcResult != nil {
		// Record DMARC check result in metrics
		if s.metrics != nil {
			if dmarcResult.Pass {
				s.metrics.SMTPDMARCChecks.WithLabelValues(s.serverName(), "pass").Inc()
			} else {
				s.metrics.SMTPDMARCChecks.WithLabelValues(s.serverName(), "fail").Inc()
			}
		}

		// Handle DMARC policy=reject failures based on configured action
		if !dmarcResult.Pass && dmarcResult.Policy == "reject" {
			rejectAction := s.serverConfig.DMARC.RejectPolicyAction
			if rejectAction == "" {
				rejectAction = "reject" // Default to reject for reject policy
			}

			switch rejectAction {
			case "reject":
				s.recordDMARCFailure()
				s.Logger.Warn("Rejecting email - DMARC reject policy", "from", s.from, "reasons", dmarcResult.FailureReasons)
				if s.metrics != nil {
					s.metrics.SMTPMessagesRejected.WithLabelValues(s.serverName(), s.serverType(), "dmarc_reject").Inc()
				}
				return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "message rejected due to DMARC policy"}
			case "junk":
				s.isJunk = true
				s.Logger.Info("Marking message as junk - DMARC reject policy", "from", s.from)
			case "none":
				s.Logger.Debug("DMARC reject policy - no action configured", "from", s.from)
			}
		}

		// Handle DMARC policy=quarantine failures
		if !dmarcResult.Pass && dmarcResult.Policy == "quarantine" {
			switch quarantineAction {
			case "reject":
				s.recordDMARCFailure()
				s.Logger.Warn("Rejecting email - DMARC quarantine policy with reject action", "from", s.from, "reasons", dmarcResult.FailureReasons)
				if s.metrics != nil {
					s.metrics.SMTPMessagesRejected.WithLabelValues(s.serverName(), s.serverType(), "dmarc_quarantine").Inc()
				}
				return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "message rejected due to DMARC quarantine policy"}
			case "junk":
				// Already handled by CheckDMARC via ShouldBeJunk
			case "none":
				s.Logger.Debug("DMARC quarantine policy - no action configured", "from", s.from)
			}
		}

		// Mark as junk if DMARC suggests it.
		if dmarcResult.ShouldBeJunk {
			s.isJunk = true
			s.junkReasons = append(s.junkReasons, "DMARC check failed or missing with unaligned auth")
			s.Logger.Info("Marking as junk - DMARC", "reasons", dmarcResult.FailureReasons)
		}
	}

	// ARC (Authenticated Received Chain) validation
	// ARC preserves email authentication results through forwarding intermediaries
	if s.serverConfig.ARC.Enabled && (s.serverConfig.ARC.Mode == "" || s.serverConfig.ARC.Mode == "check") {
		arcResult, err := validation.CheckARC(context.Background(), rawEmail, s.Logger)
		s.arcResult = arcResult // Store for ARC signing
		if err != nil {
			s.Logger.Warn("ARC validation error", "error", err)
			if s.metrics != nil {
				s.metrics.SMTPARCChecks.WithLabelValues(s.serverName(), "error").Inc()
			}
		} else if arcResult != nil {
			// Record ARC check result in metrics
			if s.metrics != nil {
				if arcResult.Pass {
					s.metrics.SMTPARCChecks.WithLabelValues(s.serverName(), "pass").Inc()
				} else {
					s.metrics.SMTPARCChecks.WithLabelValues(s.serverName(), "fail").Inc()
				}
			}

			// Log ARC validation result
			s.Logger.Debug("ARC validation result",
				"pass", arcResult.Pass,
				"chain_valid", arcResult.ChainValid,
				"instance", arcResult.Instance,
				"failure_reasons", arcResult.FailureReasons)

			// If ARC chain is broken (invalid), mark as suspicious but don't reject
			// ARC failure doesn't mean the message is spam, just that the chain is broken
			if !arcResult.Pass && arcResult.Instance > 0 {
				s.Logger.Info("ARC chain validation failed",
					"instance", arcResult.Instance,
					"reasons", arcResult.FailureReasons)
				// Note: We don't mark as junk solely based on ARC failure
				// ARC is informational and helps with forwarded emails
			}

			// If ARC validates successfully and shows the message passed authentication
			// at an earlier hop, this can override DMARC failures for forwarded emails
			if arcResult.Pass && arcResult.Instance > 0 && s.isJunk {
				s.Logger.Info("ARC chain valid - message likely forwarded, reconsidering junk status",
					"arc_instance", arcResult.Instance)
				// Note: In a full implementation, we'd parse ARC-Authentication-Results
				// to see if earlier hops validated successfully
			}
		}
	}

	if s.isJunk {
		s.Logger.Info("Message marked as junk", "from", s.from, "reasons", s.junkReasons)
	}

	return nil
}

// deliverMessage attempts to post the email to the destination endpoint.
// It translates delivery errors into appropriate SMTP temporary or permanent failure codes.
// It also checks the recipient cache before attempting delivery and caches 404/403 responses.
func (s *Session) deliverMessage(rawEmail string) error {
	// Step 1: Inject Received and X-Mizu-* headers
	// This must happen BEFORE ARC signing so the signature covers these headers
	tlsVersionStr := "none"
	if s.tlsState != nil {
		tlsVersionStr = tlsVersionString(s.tlsState.Version)
	}

	emailWithHeaders := InjectMizuHeaders(
		rawEmail,
		s.serverConfig.Domain,
		s.remoteAddr,
		s.helo,
		s.traceID,
		tlsVersionStr,
		s.spfResult,
		s.dmarcResult,
		s.arcResult,
		s.isJunk,
	)

	s.Logger.Debug("Injected Received and X-Mizu-* headers")

	// Apply junk modifications if message is marked as junk
	if s.isJunk {
		action := s.serverConfig.Junk.ApplyAction
		if action == "" {
			action = "header" // Default action
		}

		switch action {
		case "header":
			// Add custom junk header
			headerName := s.serverConfig.Junk.Header
			if headerName == "" {
				headerName = "X-Spam" // Default header
			}
			emailWithHeaders = addJunkHeader(emailWithHeaders, headerName)
			s.Logger.Debug("Added junk header", "header", headerName)

		case "subject":
			// Modify subject with pattern
			pattern := s.serverConfig.Junk.SubjectPattern
			if pattern == "" {
				pattern = "[spam] %s" // Default pattern
			}
			emailWithHeaders = modifySubject(emailWithHeaders, pattern)
			s.Logger.Debug("Modified subject for junk", "pattern", pattern)
		}
	}

	// Step 2: Sign email with DKIM if enabled (for submission servers)
	// DKIM signing should happen before ARC signing
	signedEmail := emailWithHeaders
	if s.dkimSigner != nil && s.serverConfig.DKIM.Enabled && s.serverConfig.DKIM.Mode == "sign" {
		var err error
		signedEmail, err = s.dkimSigner.SignEmail(signedEmail)
		if err != nil {
			s.Logger.Warn("Failed to sign email with DKIM", "error", err)
			// Don't fail delivery on DKIM signing error, just log and continue
		} else {
			s.Logger.Debug("Email signed with DKIM")
		}
	}

	// Step 3: Sign email with ARC if enabled
	// ARC signature covers the complete message including our added headers and DKIM signature
	if s.arcSigner != nil && s.serverConfig.ARC.Enabled && s.serverConfig.ARC.Mode == "sign" {
		var err error
		signedEmail, err = s.arcSigner.SignEmail(signedEmail, s.spfResult, s.dmarcResult, s.arcResult)
		if err != nil {
			s.Logger.Warn("Failed to sign email with ARC", "error", err)
			// Don't fail delivery on ARC signing error, just log and continue
		} else {
			s.Logger.Debug("Email signed with ARC headers")
		}
	}

	// Step 4: Choose delivery path based on routing configuration
	if s.routingClient != nil && s.globalConfig.Routing.Enabled && s.deliveryQueue != nil && s.globalConfig.Queue.Enabled {
		// Async delivery with routing
		return s.deliverWithRouting(signedEmail)
	}

	// Traditional synchronous delivery
	return s.deliverSynchronous(signedEmail)
}

// deliverWithRouting handles async delivery using routing results and queue
func (s *Session) deliverWithRouting(signedEmail string) error {
	var routingResult *routing.ResolveResponse

	// If we have a cached result from RCPT TO, perform a fresh lookup with subject
	// This allows routing policies to consider the email subject
	if s.routingResult != nil {
		// Extract subject from the email
		subject := extractSubject(signedEmail)

		// Perform fresh routing lookup with subject for the first recipient
		// (in most cases there's only one recipient per SMTP transaction)
		if len(s.to) > 0 {
			ipStr := stats.GetIPFromRemoteAddr(s.remoteAddr)
			freshResult, err := s.routingClient.Resolve(s.ctx, s.to[0], s.from, ipStr, subject)
			if err != nil {
				s.Logger.Warn("Fresh routing lookup with subject failed, using cached result",
					"recipient", s.to[0],
					"error", err)
				routingResult = s.routingResult // Fall back to cached result
			} else {
				routingResult = freshResult
				s.Logger.Debug("Used fresh routing lookup with subject",
					"recipient", s.to[0],
					"subject", subject)
			}
		} else {
			routingResult = s.routingResult
		}
	} else {
		s.Logger.Warn("No routing result available - falling back to synchronous delivery")
		return s.deliverSynchronous(signedEmail)
	}

	// Create delivery jobs based on routing result
	jobs := s.createDeliveryJobs(signedEmail, routingResult)

	if len(jobs) == 0 {
		s.Logger.Warn("No delivery jobs created from routing result")
		return &smtp.SMTPError{
			Code:         550,
			EnhancedCode: smtp.EnhancedCode{5, 4, 0},
			Message:      "no valid delivery targets",
		}
	}

	// Enqueue all jobs
	for _, job := range jobs {
		if err := s.deliveryQueue.Enqueue(job); err != nil {
			s.Logger.Error("Failed to enqueue delivery job",
				"job_id", job.ID,
				"endpoint", job.Endpoint,
				"error", err)

			return &smtp.SMTPError{
				Code:         451,
				EnhancedCode: smtp.EnhancedCode{4, 4, 0},
				Message:      "temporary failure, please try again later",
			}
		}

		s.Logger.Info("Delivery job enqueued",
			"job_id", job.ID,
			"trace_id", s.traceID,
			"endpoint", job.Endpoint,
			"recipients", job.Recipients,
			"is_forwarding", job.IsForwarding)
	}

	// Return success immediately - async delivery
	s.Logger.Info("Message queued for async delivery",
		"job_count", len(jobs),
		"trace_id", s.traceID)

	// Record as successful delivery for stats (we accepted it)
	s.finalizeSuccessfulDelivery()

	return nil
}

// createDeliveryJobs creates queue jobs from routing response
func (s *Session) createDeliveryJobs(signedEmail string, routing *routing.ResolveResponse) []*queue.DeliveryJob {
	jobs := []*queue.DeliveryJob{}

	// Job for local delivery
	if len(routing.DeliverTo) > 0 {
		endpoint := routing.DeliveryEndpoint
		isCustomEndpoint := (routing.DeliveryEndpoint != "")
		if endpoint == "" {
			endpoint = s.globalConfig.Delivery.URL
		}

		// For custom endpoints from routing, don't send API key
		// Custom endpoints should have authentication in their URL
		apiKey := s.globalConfig.Delivery.APIKey
		if routing.DeliveryEndpoint != "" {
			apiKey = "" // Custom endpoint - no API key (auth should be in URL)
		}

		job := &queue.DeliveryJob{
			ID:               queue.GenerateJobID(),
			TraceID:          s.traceID,
			EmailContent:     signedEmail,
			Recipients:       routing.DeliverTo,
			Endpoint:         endpoint,
			APIKey:           apiKey,
			IsForwarding:     false,
			IsCustomEndpoint: isCustomEndpoint,
			From:             s.from,
			OriginalFrom:     s.from,  // No SRS for delivery, From == OriginalFrom
			OriginalTo:       s.to[0], // Original RCPT TO
			IsJunk:           s.isJunk,
			MaxAttempts:      0,                // Not used by persistent queue (uses time-based retries)
			Priority:         routing.Priority, // Priority from routing response
		}

		jobs = append(jobs, job)

		s.Logger.Debug("Created delivery job",
			"job_id", job.ID,
			"endpoint", endpoint,
			"recipients", routing.DeliverTo)
	}

	// Job for forwarding
	if len(routing.ForwardTo) > 0 {
		endpoint := routing.ForwardEndpoint
		isCustomEndpoint := (routing.ForwardEndpoint != "")
		if endpoint == "" {
			endpoint = s.globalConfig.Forwarding.URL
		}

		// For custom endpoints from routing, don't send API key
		// Custom endpoints should have authentication in their URL
		apiKey := s.globalConfig.Forwarding.APIKey
		if routing.ForwardEndpoint != "" {
			apiKey = "" // Custom endpoint - no API key (auth should be in URL)
		}

		// Apply SRS rewriting for forwarding to prevent SPF failures
		srsFrom := s.from
		originalFrom := s.from
		if s.srsRewriter != nil {
			rewritten, err := s.srsRewriter.Encode(s.from)
			if err != nil {
				s.Logger.Warn("Failed to apply SRS rewriting, using original sender",
					"from", s.from,
					"error", err)
			} else {
				srsFrom = rewritten
				s.Logger.Debug("Applied SRS rewriting for forwarding",
					"original_from", s.from,
					"srs_from", srsFrom)
			}
		}

		job := &queue.DeliveryJob{
			ID:               queue.GenerateJobID(),
			TraceID:          s.traceID,
			EmailContent:     signedEmail,
			Recipients:       routing.ForwardTo,
			Endpoint:         endpoint,
			APIKey:           apiKey,
			IsForwarding:     true,
			IsCustomEndpoint: isCustomEndpoint,
			From:             srsFrom,      // SRS-rewritten sender
			OriginalFrom:     originalFrom, // Keep original for logging
			OriginalTo:       s.to[0],
			IsJunk:           s.isJunk,
			MaxAttempts:      0,                // Not used by persistent queue (uses time-based retries)
			Priority:         routing.Priority, // Priority from routing response
		}

		jobs = append(jobs, job)

		s.Logger.Debug("Created forwarding job",
			"job_id", job.ID,
			"endpoint", endpoint,
			"recipients", routing.ForwardTo,
			"srs_from", srsFrom)
	}

	return jobs
}

// deliverSynchronous handles traditional synchronous delivery
func (s *Session) deliverSynchronous(signedEmail string) error {
	// Check recipient cache first (if distributed tracking is enabled)
	if s.distTracker != nil && len(s.to) > 0 {
		// Check cache for all recipients
		for _, recipient := range s.to {
			if found, isBlocked, reason := s.distTracker.IsRecipientCached(recipient); found {
				s.Logger.Info(fmt.Sprintf("Recipient %s found in cache: %s", recipient, reason))
				if isBlocked {
					// Recipient is blocked (403)
					return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "Recipient blocked by destination"}
				}
				// Recipient not found (404)
				return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "Recipient not found"}
			}
		}
	}

	err := poster.PostEmailToDestinationWithContext(
		s.ctx,
		signedEmail,
		s.globalConfig.Delivery.URL,
		s.globalConfig.Delivery.APIKey,
		s.globalConfig.Delivery.MaxRetryAttempts,
		s.isJunk,
		s.from,
		s.to,
		s.traceID,
		s.circuitBreaker,
		s.httpClient,
		s.Logger,
	)

	if err != nil {
		s.Logger.Error(fmt.Sprintf("Failed to deliver message to destination: %v", err))

		// Check if this is a recipient-specific error that should be cached
		var httpErr *poster.HTTPStatusError
		if errors.As(err, &httpErr) {
			// Cache 404 responses (recipient not found)
			if httpErr.IsRecipientNotFound() && s.distTracker != nil && s.distTracker.recipientCacheTTL > 0 && len(s.to) > 0 {
				for _, recipient := range s.to {
					s.distTracker.CacheRecipientNotFound(recipient)
				}
				return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "Recipient not found"}
			}

			// Cache 403 responses (recipient blocked)
			if httpErr.IsRecipientBlocked() && s.distTracker != nil && s.distTracker.recipientCacheTTL > 0 && len(s.to) > 0 {
				for _, recipient := range s.to {
					s.distTracker.CacheRecipientBlocked(recipient)
				}
				return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "Recipient blocked by destination"}
			}
		}

		if poster.IsRetryableError(err) {
			// Temporary failure (e.g., network error, 5xx, circuit open).
			return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 4, 0}, Message: "Temporary failure, please try again later"}
		}
		// Permanent failure (e.g., 4xx error, context cancelled, bad request).
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 4, 0}, Message: "Message delivery failed"}
	}

	s.Logger.Info("Successfully delivered message to destination")
	return nil
}

// finalizeSuccessfulDelivery records statistics for a successfully delivered message.
func (s *Session) finalizeSuccessfulDelivery() {
	if s.statsManager != nil {
		ipStr := stats.GetIPFromRemoteAddr(s.remoteAddr)
		if s.isJunk {
			s.statsManager.RecordJunkMessage(ipStr, s.senderDomain)
		} else {
			s.statsManager.RecordHamDelivery(ipStr, s.senderDomain)
		}
	}
	s.Logger.Info("Email delivered successfully", "from", s.from, "to", s.to)
}

// recordDMARCFailure is a helper to record DMARC failure stats.
func (s *Session) recordDMARCFailure() {
	if s.statsManager != nil {
		ipStr := stats.GetIPFromRemoteAddr(s.remoteAddr)
		s.statsManager.RecordDMARCFailure(ipStr, s.senderDomain)
	}
}

// validateHeaders parses and validates the email headers.
// It checks for required headers, spam flags, and common formatting issues.
// It returns an SMTPError for hard rejections or nil if validation passes (or only marks as junk).
func (s *Session) validateHeaders(rawEmail string) error {
	// Use net/mail to parse headers robustly.
	msg, err := mail.ReadMessage(strings.NewReader(rawEmail))
	if err != nil {
		s.Logger.Warn("Rejecting message - failed to parse headers", "from", s.from, "error", err)
		return &smtp.SMTPError{
			Code:    550,
			Message: "invalid header format",
		}
	}

	if msg == nil {
		return nil // Should not happen if ReadMessage doesn't error, but good practice.
	}

	headers := msg.Header

	// Check for junk headers if configured
	if len(s.serverConfig.Junk.CheckHeaders) > 0 {
		for _, headerName := range s.serverConfig.Junk.CheckHeaders {
			if headerValue := headers.Get(headerName); headerValue != "" {
				// Header detected - apply configured action
				action := s.serverConfig.Junk.ApplyAction
				if action == "" {
					action = "reject" // Default action
				}

				s.Logger.Info("Junk header detected", "from", s.from, "header", headerName, "value", headerValue, "action", action)

				// Record this as a junk message for stats
				if s.statsManager != nil {
					ipStr := stats.GetIPFromRemoteAddr(s.remoteAddr)
					s.statsManager.RecordJunkMessage(ipStr, s.senderDomain)
				}

				switch action {
				case "reject":
					s.Logger.Warn("Rejecting message - junk header present", "from", s.from, "header", headerName)
					return &smtp.SMTPError{
						Code:         550,
						EnhancedCode: smtp.EnhancedCode{5, 7, 1},
						Message:      "Message identified as junk",
					}
				case "header":
					// Mark message as junk (will add header in Data method)
					s.isJunk = true
					s.Logger.Info("Marking message as junk - will add header", "from", s.from)
				case "subject":
					// Mark message as junk (will modify subject in Data method)
					s.isJunk = true
					s.Logger.Info("Marking message as junk - will modify subject", "from", s.from)
				case "warn":
					// Just log, don't modify or reject
					s.Logger.Warn("Junk header detected but no action taken", "from", s.from, "header", headerName)
				}
				break // Only process first matching header
			}
		}
	}

	// Check required headers
	if _, hasFrom := headers["From"]; !hasFrom {
		s.Logger.Warn("Rejecting message - missing From header", "from", s.from)
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "missing required From header"}
	}
	if _, hasDate := headers["Date"]; !hasDate {
		s.Logger.Warn("Rejecting message - missing Date header", "from", s.from)
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "missing required Date header"}
	}

	// Check for junk indicators
	if _, hasMessageID := headers["Message-Id"]; !hasMessageID {
		s.isJunk = true
		s.junkReasons = append(s.junkReasons, "missing Message-ID header")
		s.Logger.Info("Marking as junk - missing Message-ID", "from", s.from)
	}

	// Check for duplicate headers that should be unique
	for headerKey, values := range headers {
		if len(values) > 1 {
			switch strings.ToLower(headerKey) {
			case "from", "date", "message-id", "subject", "to":
				s.isJunk = true
				s.junkReasons = append(s.junkReasons, fmt.Sprintf("duplicate %s header", headerKey))
				s.Logger.Info("Marking as junk - duplicate header", "from", s.from, "header", headerKey)
			}
		}
	}

	// Validate Date header format
	if dateHeaders, hasDate := headers["Date"]; hasDate && len(dateHeaders) > 0 {
		// Try to parse the date
		if _, err := mail.ParseDate(dateHeaders[0]); err != nil {
			s.isJunk = true
			s.junkReasons = append(s.junkReasons, "invalid Date header format")
			s.Logger.Info("Marking as junk - invalid Date format", "from", s.from, "error", err)
		}
	}

	return nil
}

// Reset is called to reset the session after a message.
func (s *Session) Reset() {
	s.Logger.Debug("Session reset")
	s.from = ""
	s.to = make([]string, 0)
	s.mailData.Reset()
	s.commandState = stateHelo // After reset, we're back to post-HELO state
	s.isJunk = false
	s.junkReasons = nil
	s.senderDomain = ""
	s.spfResult = nil

	// Reset idle timeout after successful message
	if err := s.setCommandTimeout(IdleTimeout); err != nil {
		s.Logger.Error("Failed to reset idle timeout", "error", err)
	}
}

// Logout is called when the session ends.
func (s *Session) Logout() error {
	s.Logger.Debug("Session logout", "remote_addr", s.remoteAddr)
	if s.cancel != nil {
		s.cancel()
	}
	// Release connection slot for DoS protection
	// Prefer distributed tracker for cluster-wide coordination
	if s.distTracker != nil {
		s.distTracker.Release(s.remoteAddr)
	} else if s.connTracker != nil {
		s.connTracker.Release(s.remoteAddr)
	}
	// Signal that this session has completed (for graceful shutdown)
	if s.sessionsWg != nil {
		// Decrement active session counter for observability
		if s.sessionCount != nil {
			count := s.sessionCount.Add(-1)
			s.Logger.Debug("Session count decremented",
				"active_sessions", count,
				"remote_addr", s.remoteAddr)
		}

		s.sessionsWg.Done()
	}
	return nil
}

// tlsVersionString returns a human-readable TLS version string
func tlsVersionString(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("Unknown (0x%x)", version)
	}
}

// extractSubject extracts the Subject header from an email
func extractSubject(rawEmail string) string {
	msg, err := mail.ReadMessage(strings.NewReader(rawEmail))
	if err != nil {
		return ""
	}
	return msg.Header.Get("Subject")
}
