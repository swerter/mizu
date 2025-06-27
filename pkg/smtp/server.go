package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"migadu/mizu/pkg/blacklist"
	"migadu/mizu/pkg/config"
	"migadu/mizu/pkg/poster"
	"migadu/mizu/pkg/validation"

	"github.com/emersion/go-smtp"
	"go.uber.org/zap"
)

const (
	// Session timeouts for security and resource management
	SessionDeadline = 5 * time.Minute  // Hard limit for entire SMTP session to prevent hanging connections
	CommandTimeout  = 30 * time.Second // Timeout for individual SMTP commands (HELO, MAIL FROM, etc.)
	IdleTimeout     = 5 * time.Second  // Maximum idle time between commands before disconnect
	DataTimeout     = 2 * time.Minute  // Timeout for receiving email body after DATA command
)

// Backend implements smtp.Backend interface for our custom SMTP server.
// It manages the overall server configuration and creates new sessions for incoming connections.
type Backend struct {
	Config        *config.Config // Server configuration (ports, TLS, domains, etc.)
	DomainManager DomainManager  // Interface for validating recipient domains
	Logger        *zap.Logger    // Structured logger for debugging and monitoring
}

// EHLO/HELO is called for the HELO/EHLO command.
// This implements the optional smtp.EHLOBackend interface
func (be *Backend) EHLO(hostname string) error {
	// This is called by go-smtp when EHLO is received
	// We don't do validation here as it's done in the session
	return nil
}

// DomainManager interface for domain validation
// Implementations handle checking if a domain is allowed to receive mail
type DomainManager interface {
	IsValidDomain(domain string) bool // Check if domain accepts mail
	IsReady() bool                    // Check if domain list is loaded
	IsStale() bool                    // Check if domain list refresh failed
}

// NewSession is called when a new SMTP session is started.
// It performs initial validation (blacklists, reverse DNS) before creating a session.
func (be *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	remoteAddr := c.Conn().RemoteAddr().String()
	log.Printf("New session from %s", remoteAddr)

	// Perform security checks in production mode
	if !be.Config.Local {
		// Extract IP from address
		host, _, err := net.SplitHostPort(remoteAddr)
		if err != nil {
			log.Printf("Failed to parse remote address %s: %v", remoteAddr, err)
			return nil, ErrInternalServerError
		}

		// Parse IP address
		ip := net.ParseIP(host)
		if ip == nil {
			log.Printf("Failed to parse IP address: %s", host)
			return nil, ErrInternalServerError
		}

		// Check DNS blacklists (RBLs) to prevent spam
		if be.Config.Blacklists.Enabled && len(be.Config.Blacklists.Lists) > 0 {
			checker := blacklist.NewChecker(be.Config.Blacklists.Lists, be.Config.Blacklists.Timeout, be.Logger)
			isListed, reason, err := checker.CheckIP(ip)
			if err != nil {
				be.Logger.Error("Blacklist check error", zap.Error(err), zap.String("ip", host))
				// Don't reject on blacklist check errors - fail open for availability
			} else if isListed {
				log.Printf("Rejecting session from %s: blacklisted (%s)", remoteAddr, reason)
				return nil, &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 7, 1},
					Message:      fmt.Sprintf("your IP address is blacklisted: %s", reason),
				}
			}
		}

		// Require valid reverse DNS (PTR record) - helps prevent spam from compromised hosts
		names, err := net.LookupAddr(host)
		if err != nil || len(names) == 0 {
			log.Printf("Rejecting session from %s: no reverse DNS record", remoteAddr)
			return nil, &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 7, 25},
				Message:      "no reverse DNS record for IP address",
			}
		}
		log.Printf("Reverse DNS for %s: %v", host, names)
	}

	// Check if domain manager is ready (in production mode)
	if !be.Config.Local && be.DomainManager != nil && !be.DomainManager.IsReady() {
		log.Printf("Rejecting session from %s: %s", remoteAddr, LogMsgDomainListNotReady)
		return nil, ErrServerUnavailable
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
		log.Printf("%s: %v", LogMsgFailedSetDeadline, err)
		return nil, ErrInternalServerError
	}

	session := &Session{
		conn:          c,
		helo:          "",
		from:          "",
		to:            make([]string, 0),
		remoteAddr:    c.Conn().RemoteAddr().String(),
		config:        be.Config,
		tlsState:      tlsState,
		domainManager: be.DomainManager,
		ctx:           ctx,
		cancel:        cancel,
		commandState:  stateNew, // Explicitly initialize command state
	}

	return session, nil
}

// Session represents an active SMTP session for an incoming email.
// It tracks the SMTP conversation state and enforces protocol requirements.
type Session struct {
	conn          *smtp.Conn           // The underlying SMTP connection
	helo          string               // HELO/EHLO domain from the client
	from          string               // Sender's email address (MAIL FROM)
	to            []string             // Recipient email addresses (RCPT TO)
	remoteAddr    string               // Remote IP:port of the client
	mailData      bytes.Buffer         // Buffer to store the raw email body
	config        *config.Config       // Server configuration
	tlsState      *tls.ConnectionState // TLS connection state (nil if not using TLS)
	domainManager DomainManager        // Interface for validating recipient domains
	ctx           context.Context      // Session context with deadline for timeout
	cancel        context.CancelFunc   // Cancel function to clean up resources

	// Anti-spam tracking
	isJunk       bool     // Whether this message is considered junk/spam
	junkReasons  []string // Specific reasons why message is marked as junk
	commandState int      // Track SMTP command sequence state for protocol enforcement
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

// Helo is called for the HELO/EHLO command.
// RFC 5321 requires this to be the first command in an SMTP session.
func (s *Session) Helo(hostname string) error {
	// Set timeout for this command
	if err := s.setCommandTimeout(CommandTimeout); err != nil {
		return err
	}

	// Enforce command sequence - HELO/EHLO must be first
	if s.commandState != stateNew {
		log.Printf("Rejecting HELO/EHLO from %s: already received HELO (state=%d)", s.remoteAddr, s.commandState)
		return &smtp.SMTPError{
			Code:         503,
			EnhancedCode: smtp.EnhancedCode{5, 5, 1},
			Message:      "bad sequence of commands",
		}
	}

	// Validate HELO hostname for security (skip in local development mode)
	if !s.config.Local {
		// Reject if HELO claims to be our own domain (spoofing attempt)
		if hostname == s.config.SMTP.Domain {
			log.Printf("Rejecting HELO/EHLO from %s: using our own domain %s", s.remoteAddr, hostname)
			return &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 7, 1},
				Message:      "invalid HELO hostname",
			}
		}

		// Reject localhost or single-label hostnames
		if hostname == "localhost" || !strings.Contains(hostname, ".") {
			log.Printf("Rejecting HELO/EHLO from %s: invalid hostname %s", s.remoteAddr, hostname)
			return &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 7, 1},
				Message:      "HELO requires fully-qualified hostname",
			}
		}

		// Reject bare IP without brackets
		if net.ParseIP(hostname) != nil {
			log.Printf("Rejecting HELO/EHLO from %s: bare IP address %s (use [%s])", s.remoteAddr, hostname, hostname)
			return &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 7, 1},
				Message:      "HELO with bare IP must use [IP] format",
			}
		}

		// Check for invalid characters
		if strings.ContainsAny(hostname, " \t\r\n") {
			log.Printf("Rejecting HELO/EHLO from %s: invalid characters in hostname", s.remoteAddr)
			return &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 7, 1},
				Message:      "invalid characters in HELO hostname",
			}
		}

		// Optional: Verify HELO hostname has valid DNS records
		if s.config.Blacklists.CheckHELOResolves {
			resolves, err := blacklist.CheckHELOResolves(hostname, s.config.Blacklists.Timeout)
			if err != nil || !resolves {
				log.Printf("Rejecting HELO/EHLO from %s: hostname %s does not resolve", s.remoteAddr, hostname)
				return &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 7, 1},
					Message:      "HELO hostname does not resolve",
				}
			}
		}
	}

	s.helo = hostname
	s.commandState = stateHelo
	log.Printf("HELO/EHLO from %s: %s", s.remoteAddr, hostname)
	return nil
}

// updateTLSState updates the TLS state from the connection
func (s *Session) updateTLSState() {
	state, ok := s.conn.TLSConnectionState()
	if ok {
		s.tlsState = &state
	} else {
		s.tlsState = nil
	}
}

// setCommandTimeout sets the deadline for the current command
func (s *Session) setCommandTimeout(timeout time.Duration) error {
	// Check if session deadline has been exceeded
	select {
	case <-s.ctx.Done():
		log.Printf("%s for %s", LogMsgSessionDeadlineExceeded, s.remoteAddr)
		return ErrSessionTimeout
	default:
		// Set the command timeout
		deadline := time.Now().Add(timeout)
		if err := s.conn.Conn().SetDeadline(deadline); err != nil {
			log.Printf("%s: %v", LogMsgFailedSetDeadline, err)
			return ErrInternalServerError
		}
		return nil
	}
}

// Mail is called for the MAIL FROM command.
// This sets the envelope sender for the SMTP transaction.
func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	// Handle case where go-smtp library processed EHLO internally
	heloHostname := s.conn.Hostname()
	if heloHostname != "" && s.helo == "" {
		// EHLO was handled by go-smtp internally, update our state
		s.helo = heloHostname
		s.commandState = stateHelo
		log.Printf("EHLO/HELO from %s: %s (set via conn.Hostname)", s.remoteAddr, heloHostname)
	}

	// Set timeout for this command
	if err := s.setCommandTimeout(CommandTimeout); err != nil {
		return err
	}

	// Check if HELO/EHLO has been received
	if s.helo == "" {
		log.Printf("Rejecting MAIL FROM from %s: no HELO/EHLO received", s.remoteAddr)
		return &smtp.SMTPError{
			Code:         503,
			EnhancedCode: smtp.EnhancedCode{5, 5, 1},
			Message:      "bad sequence of commands - HELO/EHLO first",
		}
	}

	// Update and check TLS state (skip in local mode)
	s.updateTLSState()
	if !s.config.Local && s.tlsState == nil {
		log.Printf("Rejecting MAIL FROM %s: TLS required (use STARTTLS)", from)
		return ErrTLSRequiredStartTLS
	}

	// Reject null sender <> (bounce messages) to prevent backscatter
	if from == "" || from == "<>" {
		log.Printf("Rejecting null sender from %s", s.remoteAddr)
		return &smtp.SMTPError{
			Code:         550,
			EnhancedCode: smtp.EnhancedCode{5, 7, 1},
			Message:      "null sender not accepted",
		}
	}

	// Extract domain from sender
	senderDomain := ""
	if idx := strings.LastIndex(from, "@"); idx != -1 {
		senderDomain = from[idx+1:]
		// Remove trailing > if present
		senderDomain = strings.TrimSuffix(senderDomain, ">")
	}

	// Prevent spoofing: reject if sender claims to be from our domains
	// Local senders should use the outbound relay, not the inbound MX
	if senderDomain != "" {
		isLocalDomain := false

		// Check against domain manager if available
		if s.domainManager != nil {
			isLocalDomain = s.domainManager.IsValidDomain(senderDomain)
		}

		// In local mode, also explicitly check against the SMTP domain
		if s.config.Local && strings.EqualFold(senderDomain, s.config.SMTP.Domain) {
			isLocalDomain = true
		}

		if isLocalDomain {
			log.Printf("Rejecting MAIL FROM %s: sender domain %s is local (use outbound relay)", from, senderDomain)
			return &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 7, 1},
				Message:      "local domains must use outbound relay",
			}
		}
	}

	s.from = from
	s.commandState = stateMail
	if s.tlsState != nil {
		log.Printf("MAIL FROM: %s (Remote: %s, TLS: %s)", from, s.remoteAddr, tlsVersionString(s.tlsState.Version))
	} else {
		log.Printf("MAIL FROM: %s (Remote: %s, Local mode - no TLS)", from, s.remoteAddr)
	}

	return nil
}

// Rcpt is called for the RCPT TO command.
// This validates and adds recipients to the envelope.
func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	// Set timeout for this command
	if err := s.setCommandTimeout(CommandTimeout); err != nil {
		return err
	}

	// Enforce command sequence - must have MAIL FROM before RCPT TO
	if s.commandState != stateMail && s.commandState != stateRcpt {
		log.Printf("Rejecting RCPT TO from %s: bad command sequence", s.remoteAddr)
		return &smtp.SMTPError{
			Code:         503,
			EnhancedCode: smtp.EnhancedCode{5, 5, 1},
			Message:      "bad sequence of commands - MAIL FROM first",
		}
	}

	// Update and check TLS state (skip in local mode)
	s.updateTLSState()
	if !s.config.Local && s.tlsState == nil {
		log.Printf("Rejecting RCPT TO %s: TLS required", to)
		return ErrTLSRequired
	}

	log.Printf("RCPT TO: %s (Remote: %s)", to, s.remoteAddr)

	// Verify recipient domain is one we handle mail for
	if s.domainManager != nil && !s.domainManager.IsValidDomain(to) {
		// If domain list is stale (last refresh failed), return temporary error
		// This prevents rejecting valid mail during transient API failures
		if s.domainManager.IsStale() {
			log.Printf("Temporarily rejecting recipient %s: domain list is stale (refresh failed)", to)
			return &smtp.SMTPError{
				Code:         451,
				EnhancedCode: smtp.EnhancedCode{4, 7, 1},
				Message:      "temporary error - please try again later",
			}
		}
		// Otherwise, permanently reject unknown domains
		log.Printf("Rejecting recipient %s: domain not in valid domains list", to)
		return &smtp.SMTPError{
			Code:         550,
			EnhancedCode: smtp.EnhancedCode{5, 7, 1},
			Message:      "recipient domain not accepted",
		}
	}

	s.to = append(s.to, to)
	s.commandState = stateRcpt
	return nil
}

// Data is called when the email body is received.
// This is where we process the message headers and body, perform validation, and forward the email.
func (s *Session) Data(r io.Reader) error {
	// Set extended timeout for receiving potentially large email data
	if err := s.setCommandTimeout(DataTimeout); err != nil {
		return err
	}

	// Enforce command sequence - must have at least one recipient
	if s.commandState != stateRcpt {
		log.Printf("Rejecting DATA from %s: bad command sequence", s.remoteAddr)
		return &smtp.SMTPError{
			Code:         503,
			EnhancedCode: smtp.EnhancedCode{5, 5, 1},
			Message:      "bad sequence of commands - RCPT TO first",
		}
	}

	// Final update and check of TLS state (skip in local mode)
	s.updateTLSState()
	if !s.config.Local && s.tlsState == nil {
		log.Printf("Rejecting DATA: TLS required")
		return ErrTLSRequired
	}

	log.Printf("Receiving data from %s to %s", s.from, s.to)

	// Read the entire email into a buffer.
	// io.LimitReader ensures that no more than maxMessageSize bytes are read.
	if _, err := io.Copy(&s.mailData, io.LimitReader(r, int64(s.config.SMTP.MaxMessageSize))); err != nil {
		log.Printf("Failed to read message data: %v", err)
		return err // Or a more specific SMTP error
	}

	// This check is technically redundant due to io.LimitReader but acts as a safeguard.
	if s.mailData.Len() > s.config.SMTP.MaxMessageSize {
		log.Printf("Message from %s exceeded max size of %d bytes", s.from, s.config.SMTP.MaxMessageSize)
		return ErrMessageTooBig
	}

	rawEmail := s.mailData.String() // Get the raw email as a string

	// Check for empty message
	trimmed := strings.TrimSpace(rawEmail)
	if len(trimmed) == 0 {
		log.Printf("Rejecting empty message from %s", s.from)
		return &smtp.SMTPError{
			Code:         550,
			EnhancedCode: smtp.EnhancedCode{5, 7, 1},
			Message:      "empty message not accepted",
		}
	}

	// Header validation (skip in local mode)
	if !s.config.Local {
		// Simple header parsing - find where headers end
		headerEnd := strings.Index(rawEmail, "\r\n\r\n")
		if headerEnd == -1 {
			headerEnd = strings.Index(rawEmail, "\n\n")
		}

		if headerEnd > 0 {
			headerSection := rawEmail[:headerEnd]
			headers := make(map[string][]string)

			// Parse headers manually
			lines := strings.Split(headerSection, "\n")
			currentHeader := ""
			for _, line := range lines {
				line = strings.TrimRight(line, "\r")
				if line == "" {
					break
				}

				// Continuation line
				if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
					if currentHeader != "" {
						headers[currentHeader][len(headers[currentHeader])-1] += " " + strings.TrimSpace(line)
					}
					continue
				}

				// New header
				colonIdx := strings.Index(line, ":")
				if colonIdx > 0 {
					headerName := strings.ToLower(strings.TrimSpace(line[:colonIdx]))
					headerValue := strings.TrimSpace(line[colonIdx+1:])
					currentHeader = headerName
					headers[headerName] = append(headers[headerName], headerValue)
				}
			}

			// Check required headers
			if _, hasFrom := headers["from"]; !hasFrom {
				log.Printf("Rejecting message from %s: missing From header", s.from)
				return &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 7, 1},
					Message:      "missing required From header",
				}
			}

			if _, hasDate := headers["date"]; !hasDate {
				log.Printf("Rejecting message from %s: missing Date header", s.from)
				return &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 7, 1},
					Message:      "missing required Date header",
				}
			}

			// Check for junk indicators
			if _, hasMessageID := headers["message-id"]; !hasMessageID {
				s.isJunk = true
				s.junkReasons = append(s.junkReasons, "missing Message-ID header")
				log.Printf("Marking as junk from %s: missing Message-ID header", s.from)
			}

			// Check for duplicate headers
			for headerName, values := range headers {
				if len(values) > 1 {
					switch headerName {
					case "from", "date", "message-id", "subject":
						s.isJunk = true
						s.junkReasons = append(s.junkReasons, fmt.Sprintf("duplicate %s header", headerName))
						log.Printf("Marking as junk from %s: duplicate %s header", s.from, headerName)
					}
				}
			}

			// Validate Date header format
			if dateHeaders, hasDate := headers["date"]; hasDate && len(dateHeaders) > 0 {
				// Try to parse the date
				dateStr := dateHeaders[0]
				if _, err := time.Parse(time.RFC1123Z, dateStr); err != nil {
					// Try other common formats
					formats := []string{
						time.RFC1123,
						time.RFC822Z,
						time.RFC822,
						"Mon, 2 Jan 2006 15:04:05 -0700",
						"Mon, 2 Jan 2006 15:04:05 MST",
					}
					parsed := false
					for _, format := range formats {
						if _, err := time.Parse(format, dateStr); err == nil {
							parsed = true
							break
						}
					}
					if !parsed {
						s.isJunk = true
						s.junkReasons = append(s.junkReasons, "invalid Date header format")
						log.Printf("Marking as junk from %s: invalid Date header format", s.from)
					}
				}
			}
		}

		// Log junk status
		if s.isJunk {
			log.Printf("Message from %s marked as junk. Reasons: %v", s.from, s.junkReasons)
		}
	}

	// In local mode, dump to terminal instead of posting
	if s.config.Local {
		log.Println("=== LOCAL MODE: EMAIL CONTENT START ===")
		fmt.Println(rawEmail)
		log.Println("=== LOCAL MODE: EMAIL CONTENT END ===")
		log.Printf("Local mode: Received email from %s to %s", s.from, s.to)
		s.mailData.Reset()
		return nil
	}

	// DMARC validation
	// Get remote IP for DMARC SPF check
	var remoteIP net.IP
	if tcpAddr, err := net.ResolveTCPAddr("tcp", s.remoteAddr); err == nil {
		remoteIP = tcpAddr.IP
	}

	// Perform DMARC validation
	if remoteIP != nil {
		dmarcResult, err := validation.CheckDMARC(context.Background(), rawEmail, remoteIP, s.helo)
		if err != nil {
			log.Printf("DMARC validation error: %v", err)
		} else {
			log.Printf("DMARC result for %s: Pass=%v, Policy=%s, SPF Aligned=%v, DKIM Aligned=%v",
				s.from, dmarcResult.Pass, dmarcResult.Policy, dmarcResult.SPFAligned, dmarcResult.DKIMAligned)

			// If DMARC policy is 'reject' and validation failed, reject the message
			if !dmarcResult.Pass && dmarcResult.Policy == "reject" {
				log.Printf("Rejecting email from %s: DMARC policy is 'reject' and validation failed. Reasons: %v",
					s.from, dmarcResult.FailureReasons)
				s.mailData.Reset()
				return &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 7, 1},
					Message:      "message rejected due to DMARC policy",
				}
			}

			// Mark as junk if no DMARC record and SPF/DKIM failed
			if dmarcResult.ShouldBeJunk {
				s.isJunk = true
				s.junkReasons = append(s.junkReasons, "No DMARC record and authentication failed")
				log.Printf("Marking message as junk: %s", dmarcResult.FailureReasons)
			}
		}
	}

	// Post the raw email to the configured destination
	// Use the session context to ensure posting respects the session deadline
	err := poster.PostEmailToDestinationWithContext(s.ctx, rawEmail, s.config.Destination.URL, s.config.Destination.APIKey, s.config.Destination.MaxRetryAttempts, s.isJunk)
	if err != nil {
		log.Printf("Failed to post email destination URL: %v", err)
		// Return a temporary SMTP error to tell the sender to retry later
		return &smtp.SMTPError{
			Code:         451,
			EnhancedCode: smtp.EnhancedCode{4, 7, 1},
			Message:      "temporary error - please try again later",
		}
	}

	log.Printf("Successfully processed and posted email from %s to %s", s.from, s.to)
	s.mailData.Reset() // Clear buffer after processing
	return nil
}

// Reset is called to reset the session after a message.
func (s *Session) Reset() {
	log.Println("Session reset")
	s.from = ""
	s.to = make([]string, 0)
	s.mailData.Reset()
	s.commandState = stateHelo // After reset, we're back to post-HELO state
	s.isJunk = false
	s.junkReasons = nil

	// Reset idle timeout after successful message
	if err := s.setCommandTimeout(IdleTimeout); err != nil {
		log.Printf("Failed to reset idle timeout: %v", err)
	}
}

// Logout is called when the session ends.
func (s *Session) Logout() error {
	log.Println("Session logout")
	if s.cancel != nil {
		s.cancel()
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
