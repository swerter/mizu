package recipient

// ValidateRequest represents a request to validate a recipient during RCPT TO
type ValidateRequest struct {
	ClientIP     string `json:"client_ip"`     // Client IP address
	EnvelopeFrom string `json:"envelope_from"` // MAIL FROM address
	EnvelopeTo   string `json:"envelope_to"`   // RCPT TO address being validated
}

// ValidateResponse represents the validation decision from the endpoint
type ValidateResponse struct {
	Accepted bool   `json:"accepted"`          // Whether to accept this recipient
	Message  string `json:"message,omitempty"` // Optional message to include in SMTP response
}

// Error codes for SMTP responses
const (
	// Standard SMTP error codes
	SMTPUserUnknown        = 550 // 5.1.1 User unknown
	SMTPDeliveryNotAuth    = 550 // 5.7.1 Delivery not authorized (blocked sender)
	SMTPMailboxUnavailable = 550 // 5.2.1 Mailbox unavailable
	SMTPTooManyRecipients  = 452 // 4.5.3 Too many recipients
	SMTPTempFailure        = 451 // 4.3.0 Temporary failure
)
