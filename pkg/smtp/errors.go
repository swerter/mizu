package smtp

import (
	"errors"

	"github.com/emersion/go-smtp"
)

// Common SMTP server errors
var (
	// Session errors
	ErrSessionTimeout      = errors.New("session timeout")
	ErrInternalServerError = errors.New("internal server error")
	ErrServerUnavailable   = errors.New("server temporarily unavailable")
	ErrNoReverseDNS        = errors.New("no reverse DNS record")

	// TLS errors. These are structured SMTPErrors so clients receive the
	// RFC 3207 §4 mandated "530 5.7.0 Must issue a STARTTLS command first"
	// response rather than go-smtp's generic default reply.
	ErrTLSRequired = &smtp.SMTPError{
		Code:         530,
		EnhancedCode: smtp.EnhancedCode{5, 7, 0},
		Message:      "Must issue a STARTTLS command first",
	}
	ErrTLSRequiredStartTLS = &smtp.SMTPError{
		Code:         530,
		EnhancedCode: smtp.EnhancedCode{5, 7, 0},
		Message:      "Must issue a STARTTLS command first",
	}

	// Message errors
	ErrMessageTooBig = errors.New("message too big")

	// Context errors
	ErrContextCancelled = errors.New("context cancelled")
	ErrContextTimeout   = errors.New("context deadline exceeded")
)

// Common error messages for logging (not returned to clients)
const (
	LogMsgFailedSetDeadline       = "Failed to set connection deadline"
	LogMsgDomainListNotReady      = "domain list not ready"
	LogMsgSessionDeadlineExceeded = "Session deadline exceeded"
)
