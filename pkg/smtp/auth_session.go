package smtp

import (
	"fmt"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// AuthMechanisms returns the list of supported SMTP AUTH mechanisms
// Implements smtp.AuthSession interface
func (s *Session) AuthMechanisms() []string {
	// Only offer AUTH if enabled (or required, which implies enabled)
	if !s.serverConfig.Auth.Enabled && !s.serverConfig.Auth.Required {
		return nil
	}

	// Only offer AUTH if authenticator is configured
	if s.authenticator == nil {
		return nil
	}

	// Support PLAIN and LOGIN mechanisms
	mechanisms := []string{sasl.Plain, sasl.Login}

	// Don't offer AUTH over unencrypted connection (unless in local mode)
	if !s.globalConfig.Local && s.tlsState == nil {
		s.Logger.Debug("Not offering AUTH - TLS not active")
		return nil
	}

	return mechanisms
}

// Auth handles SMTP AUTH command
// Implements smtp.AuthSession interface
func (s *Session) Auth(mech string) (sasl.Server, error) {
	// Verify authenticator is configured
	if s.authenticator == nil {
		s.Logger.Warn("AUTH attempted but no authenticator configured")
		return nil, &smtp.SMTPError{
			Code:         502,
			EnhancedCode: smtp.EnhancedCode{5, 7, 0},
			Message:      "authentication not supported",
		}
	}

	// Require TLS for AUTH (unless in local mode)
	if !s.globalConfig.Local && s.tlsState == nil {
		s.Logger.Warn("AUTH attempted without TLS", "remote_addr", s.remoteAddr)
		return nil, &smtp.SMTPError{
			Code:         538,
			EnhancedCode: smtp.EnhancedCode{5, 7, 11},
			Message:      "encryption required for authentication",
		}
	}

	// Check if already authenticated
	if s.isAuthenticated {
		s.Logger.Warn("Already authenticated", "user", s.authenticatedUser)
		return nil, &smtp.SMTPError{
			Code:         503,
			EnhancedCode: smtp.EnhancedCode{5, 5, 1},
			Message:      "already authenticated",
		}
	}

	// Create authenticator function that captures session state
	authenticatorFunc := func(identity, username, password string) error {
		// For PLAIN, username is in the 'username' parameter
		// For LOGIN, username comes from the handshake
		user := username
		if user == "" {
			user = identity
		}

		s.Logger.Debug("Authentication attempt", "username", user, "mechanism", mech)

		// Call the HTTP authenticator
		authenticated, err := s.authenticator.Authenticate(user, password)
		if err != nil {
			s.Logger.Error("Authentication error", "username", user, "error", err)
			return fmt.Errorf("authentication service error")
		}

		if !authenticated {
			s.Logger.Warn("Authentication failed", "username", user)
			return fmt.Errorf("invalid credentials")
		}

		// Mark session as authenticated
		s.isAuthenticated = true
		s.authenticatedUser = user
		s.Logger.Info("User authenticated successfully", "username", user, "mechanism", mech)

		return nil
	}

	// Return appropriate SASL server based on mechanism
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(authenticatorFunc), nil
	case sasl.Login:
		// Create a wrapper for LOGIN that matches the expected signature
		loginAuth := func(username, password string) error {
			return authenticatorFunc("", username, password)
		}
		return NewLoginServer(loginAuth), nil
	default:
		s.Logger.Warn("Unsupported AUTH mechanism", "mechanism", mech)
		return nil, &smtp.SMTPError{
			Code:         504,
			EnhancedCode: smtp.EnhancedCode{5, 5, 4},
			Message:      fmt.Sprintf("unsupported authentication mechanism: %s", mech),
		}
	}
}
