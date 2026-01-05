package sender

import (
	"context"

	"migadu/mizu/pkg/smtp"
)

// SMTPAdapter wraps Validator to provide the interface expected by smtp package
type SMTPAdapter struct {
	validator *Validator
}

// NewSMTPAdapter creates a new adapter around a Validator
func NewSMTPAdapter(validator *Validator) *SMTPAdapter {
	return &SMTPAdapter{validator: validator}
}

// Validate validates a sender and returns smtp.SenderValidationResponse
func (a *SMTPAdapter) Validate(ctx context.Context, clientIP, from string) (*smtp.SenderValidationResponse, error) {
	resp, err := a.validator.Validate(ctx, clientIP, from)
	if err != nil {
		return nil, err
	}

	// Convert to smtp.SenderValidationResponse
	return &smtp.SenderValidationResponse{
		Accepted: resp.Accepted,
		Message:  resp.Message,
	}, nil
}

// ValidateWithContext validates a sender with additional context (PTR, HELO)
func (a *SMTPAdapter) ValidateWithContext(ctx context.Context, clientIP, ptr, helo, from string) (*smtp.SenderValidationResponse, error) {
	resp, err := a.validator.ValidateWithContext(ctx, clientIP, ptr, helo, from)
	if err != nil {
		return nil, err
	}

	// Convert to smtp.SenderValidationResponse
	return &smtp.SenderValidationResponse{
		Accepted: resp.Accepted,
		Message:  resp.Message,
	}, nil
}

// FlushCache flushes the validation cache
func (a *SMTPAdapter) FlushCache() {
	a.validator.FlushCache()
}

// GetStats returns cache statistics
func (a *SMTPAdapter) GetStats() map[string]interface{} {
	return a.validator.GetStats()
}
