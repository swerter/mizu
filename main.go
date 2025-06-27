package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"migadu/mizu/pkg/config"
	"migadu/mizu/pkg/domains"
	"migadu/mizu/pkg/smtp"
	"migadu/mizu/pkg/storage"

	"github.com/caddyserver/certmagic"
	gosmtp "github.com/emersion/go-smtp"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	// Check for special flags - allows generating example config file
	if len(os.Args) > 1 && os.Args[1] == "generate-config" {
		if err := config.SaveExample("config.toml.example"); err != nil {
			log.Fatalf("Failed to generate example config: %v", err)
		}
		fmt.Println("Generated example configuration file: config.toml.example")
		return
	}

	// Load configuration
	cfg, err := config.LoadConfig(os.Args[1:])
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Setup logging
	logger, err := setupLogging(cfg.LogFormat, cfg.TLS.CertMagicVerbose)
	if err != nil {
		log.Fatalf("Failed to setup logging: %v", err)
	}
	defer logger.Sync()

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Sugar().Info("Received shutdown signal")
		cancel()
	}()

	// Initialize TLS configuration and domain manager based on mode
	var tlsConfig *tls.Config
	var domainManager smtp.DomainManager

	if cfg.Local {
		logger.Sugar().Info("Running in LOCAL mode - TLS disabled, messages will be dumped to terminal")
		tlsConfig = nil

		// In local mode, check if domains URL is provided
		if cfg.Domains.URL != "" {
			// Use the full domain manager even in local mode
			logger.Sugar().Infof("Local mode: loading domains from %s", cfg.Domains.URL)
			manager := domains.NewManager(cfg.Domains.URL, cfg.Domains.APIKey, logger)
			if err := manager.Start(ctx); err != nil {
				logger.Sugar().Fatalf("Failed to initialize domain manager: %v", err)
			}
			domainManager = manager
		} else {
			// Fall back to simple domain validator for SMTP domain only
			domainManager = domains.NewLocalManager(cfg.SMTP.Domain)
			logger.Sugar().Infof("Local mode: accepting emails for domain %s only", cfg.SMTP.Domain)
		}
	} else {
		// Production mode: Initialize full domain manager with API support
		manager := domains.NewManager(cfg.Domains.URL, cfg.Domains.APIKey, logger)
		if err := manager.Start(ctx); err != nil {
			logger.Sugar().Fatalf("Failed to initialize domain manager: %v", err)
		}
		domainManager = manager

		// Configure certmagic logging for debugging TLS certificate issues
		if cfg.TLS.CertMagicVerbose {
			certmagic.Default.Logger = logger
		}

		// Initialize S3 client for MinIO (S3-compatible)
		s3Client, err := minio.New(cfg.S3.Endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.S3.AccessKeyID, cfg.S3.SecretAccessKey, ""),
			Region: cfg.S3.Region,
			Secure: true, // Use HTTPS
		})
		if err != nil {
			logger.Sugar().Fatalf("failed to init S3 client: %v", err)
		}

		// Set up Certmagic storage to use S3 (MinIO client)
		certmagic.Default.Storage = storage.NewS3CertStorage(s3Client, cfg.S3.Bucket, cfg.S3.Prefix, logger)

		// Configure Certmagic for automatic TLS with ACME (Let's Encrypt)
		if cfg.TLS.Email != "" {
			certmagic.DefaultACME.Email = cfg.TLS.Email
		}

		if !cfg.TLS.UseProduction || cfg.TLS.UseLocalCA {
			certmagic.DefaultACME.CA = certmagic.LetsEncryptStagingCA
			logger.Sugar().Info("Using Let's Encrypt staging CA")
		} else {
			certmagic.DefaultACME.CA = certmagic.LetsEncryptProductionCA
			logger.Sugar().Info("Using Let's Encrypt production CA")
		}

		certmagic.Default.OnDemand = &certmagic.OnDemandConfig{} // Enable on-demand certs if needed

		// Load or issue initial certificate
		logger.Sugar().Infof("Attempting to get TLS certificate for %s...", cfg.SMTP.Domain)
		tlsConfig, err = certmagic.TLS([]string{cfg.SMTP.Domain})
		if err != nil {
			logger.Sugar().Fatalf("failed to get initial TLS certificate: %v", err)
		}

		// Configure minimum TLS version
		minVersion := getTLSVersion(cfg.SMTP.MinTLSVersion)
		tlsConfig.MinVersion = minVersion

		// Log warning if unsupported version was requested
		if cfg.SMTP.MinTLSVersion != "" && cfg.SMTP.MinTLSVersion != "1.2" && cfg.SMTP.MinTLSVersion != "1.3" {
			logger.Sugar().Warnf("Unsupported TLS version '%s' requested - using TLS 1.2. Only TLS 1.2 and 1.3 are supported.", cfg.SMTP.MinTLSVersion)
		} else {
			logger.Sugar().Infof("Minimum TLS version set to: %s", cfg.SMTP.MinTLSVersion)
		}

		// Extract the first certificate from the TLS config
		if tlsConfig.GetCertificate != nil {
			logger.Sugar().Infof("Successfully configured TLS certificate for %s", cfg.SMTP.Domain)
		}
	}

	// --- SMTP Server Setup ---
	// Create the backend that handles SMTP protocol logic
	be := &smtp.Backend{
		Config:        cfg,
		DomainManager: domainManager,
		Logger:        logger,
	}
	server := gosmtp.NewServer(be)
	// Configure SMTP server parameters
	server.Addr = cfg.SMTP.ListenAddr                       // e.g., ":25" for standard SMTP port
	server.Domain = cfg.SMTP.Domain                         // Server's hostname for HELO/EHLO responses
	server.ReadTimeout = cfg.SMTP.TimeoutDuration           // Timeout for reading client commands
	server.WriteTimeout = cfg.SMTP.TimeoutDuration          // Timeout for writing responses
	server.MaxMessageBytes = int64(cfg.SMTP.MaxMessageSize) // Limit email size to prevent abuse
	server.AllowInsecureAuth = false                        // Require TLS for authentication
	server.EnableSMTPUTF8 = true                            // Support international characters in addresses
	// Use the TLS config from certmagic which handles certificate management
	server.TLSConfig = tlsConfig

	// Create a listener that we can close for graceful shutdown
	listener, err := net.Listen("tcp", cfg.SMTP.ListenAddr)
	if err != nil {
		logger.Sugar().Fatalf("Failed to create listener: %v", err)
	}

	// Start server in a goroutine
	serverErrors := make(chan error, 1)
	go func() {
		logger.Sugar().Infof("Starting SMTP server on %s for domain %s", cfg.SMTP.ListenAddr, cfg.SMTP.Domain)
		serverErrors <- server.Serve(listener)
	}()

	// Wait for shutdown signal or server error
	select {
	case <-ctx.Done():
		logger.Sugar().Info("Shutting down SMTP server...")
		// Close the listener to stop accepting new connections
		listener.Close()
		// Give existing connections time to finish
		time.Sleep(2 * time.Second)
		logger.Sugar().Info("SMTP server stopped")
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			logger.Sugar().Fatalf("SMTP server error: %v", err)
		}
	}
}

func setupLogging(format string, verbose bool) (*zap.Logger, error) {
	var config zap.Config

	if format == "json" {
		config = zap.NewProductionConfig()
	} else {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		config.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000")
	}

	if !verbose {
		config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	} else {
		config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}

	logger, err := config.Build()
	if err != nil {
		return nil, err
	}

	// Replace global logger
	zap.ReplaceGlobals(logger)

	// Redirect standard log to zap
	zap.RedirectStdLog(logger)

	return logger, nil
}

// getTLSVersion converts a string TLS version to the corresponding tls constant
// Only TLS 1.2 and 1.3 are supported for security reasons
func getTLSVersion(version string) uint16 {
	switch version {
	case "1.2":
		return tls.VersionTLS12
	case "1.3":
		return tls.VersionTLS13
	default:
		// Default to TLS 1.2 for security - TLS 1.0 and 1.1 are deprecated
		return tls.VersionTLS12
	}
}
