package recipient

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

// Validate validates a recipient and returns smtp.RecipientValidationResponse
func (a *SMTPAdapter) Validate(ctx context.Context, clientIP, from, to string) (*smtp.RecipientValidationResponse, error) {
	resp, err := a.validator.Validate(ctx, clientIP, from, to)
	if err != nil {
		return nil, err
	}

	// Convert to smtp.RecipientValidationResponse
	return &smtp.RecipientValidationResponse{
		Accepted:  resp.Accepted,
		Message:   resp.Message,
		Temporary: resp.Temporary,
	}, nil
}

// ValidateWithContext validates a recipient with additional context (PTR, HELO)
func (a *SMTPAdapter) ValidateWithContext(ctx context.Context, clientIP, ptr, helo, from, to string) (*smtp.RecipientValidationResponse, error) {
	resp, err := a.validator.ValidateWithContext(ctx, clientIP, ptr, helo, from, to)
	if err != nil {
		return nil, err
	}

	// Convert to smtp.RecipientValidationResponse
	return &smtp.RecipientValidationResponse{
		Accepted:  resp.Accepted,
		Message:   resp.Message,
		Temporary: resp.Temporary,
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
