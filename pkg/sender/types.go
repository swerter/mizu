package sender

// ValidateRequest represents a request to validate a sender during MAIL FROM
type ValidateRequest struct {
	ClientIP     string `json:"client_ip"`     // Client IP address
	EnvelopeFrom string `json:"envelope_from"` // MAIL FROM address being validated
}

// ValidateResponse represents the validation decision from the endpoint
type ValidateResponse struct {
	Accepted bool   `json:"accepted"`          // Whether to accept this sender
	Message  string `json:"message,omitempty"` // Optional message to include in SMTP response
}

// Error codes for SMTP responses
const (
	// Standard SMTP error codes
	SMTPSenderNotAuth      = 550 // 5.7.1 Sender not authorized
	SMTPSenderRejected     = 550 // 5.1.8 Sender rejected
	SMTPSenderUnavailable  = 550 // 5.1.1 Sender address unavailable
	SMTPTempFailure        = 451 // 4.3.0 Temporary failure
)
