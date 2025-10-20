package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"migadu/mizu/pkg/cluster"
	"migadu/mizu/pkg/config"
	"migadu/mizu/pkg/health"
	"migadu/mizu/pkg/logging"
	"migadu/mizu/pkg/metrics"
	"migadu/mizu/pkg/poster"
	"migadu/mizu/pkg/queue"
	"migadu/mizu/pkg/routing"
	"migadu/mizu/pkg/smtp"
	"migadu/mizu/pkg/srs"
	"migadu/mizu/pkg/stats"
	"migadu/mizu/pkg/storage"
	tlsmgr "migadu/mizu/pkg/tls"
	"migadu/mizu/pkg/validation"

	"github.com/caddyserver/certmagic"
	gosmtp "github.com/emersion/go-smtp"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Version information, injected at build time
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Handle special command-line arguments like 'generate-config' and '-version'
	handleCLIArgs()

	// Load configuration
	cfg, err := config.LoadConfig(os.Args[1:])
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Configuration validation failed: %v", err)
	}

	// Setup logging
	logFormat := cfg.Logging.Format
	if logFormat == "" {
		logFormat = "console" // Default to console format
	}
	logger, err := logging.Setup(logFormat, cfg.TLS.CertMagicVerbose)
	if err != nil {
		log.Fatalf("Failed to setup logging: %v", err)
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	logging.SafeGo(logger, "signal-handler", func() {
		<-sigChan
		logger.Info("Received shutdown signal")
		cancel()
	})

	// Initialize memberlist cluster first (required for TLS leader election)
	var clusterMgr *cluster.Cluster
	if cfg.Cluster.Enabled {
		clusterMgr, err = initCluster(cfg, logger)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to initialize cluster: %v", err))
			os.Exit(1)
		}
		defer clusterMgr.Shutdown()
	}

	// Initialize core components
	statsManager := initStatsManager(cfg, logger)

	// Initialize Prometheus metrics
	metricsInstance := metrics.New("mizu")
	logger.Info("Prometheus metrics initialized")

	var tlsConfig *tls.Config
	var s3Client *minio.Client
	var tlsMgr *tlsmgr.Manager
	if cfg.Local {
		logger.Info("Running in LOCAL mode - TLS disabled, messages will be dumped to terminal")
	} else {
		tlsConfig, s3Client, tlsMgr, err = initTLS(cfg, clusterMgr, logger)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to initialize TLS: %v", err))
			os.Exit(1)
		}

		// Start ACME challenge servers for autocert
		if cfg.TLS.EnableAutocert && tlsMgr != nil {
			// Start HTTPS server on port 443 for TLS-ALPN-01 challenges (primary method)
			logging.SafeGo(logger, "acme-tls-alpn-server", func() {
				logger.Info("Starting HTTPS server for ACME TLS-ALPN-01 challenges on :443")
				server := &http.Server{
					Addr:      ":443",
					TLSConfig: tlsMgr.TLSConfig(),
				}
				// Empty cert/key files - TLSConfig.GetCertificate handles everything
				if err := server.ListenAndServeTLS("", ""); err != nil {
					logger.Error("TLS-ALPN-01 challenge server failed", "error", err)
				}
			})

			// Start HTTP server on port 80 for HTTP-01 challenges (fallback)
			logging.SafeGo(logger, "acme-http-server", func() {
				logger.Info("Starting HTTP server for ACME HTTP-01 challenges on :80")
				if err := http.ListenAndServe(":80", tlsMgr.HTTPHandler()); err != nil {
					logger.Error("HTTP-01 challenge server failed", "error", err)
				}
			})
		}
	}
	circuitBreaker := initCircuitBreaker(cfg, logger, metricsInstance)

	// Create HTTP client with configured timeout for posting emails to destination
	httpClient := poster.NewHTTPClient(time.Duration(cfg.Delivery.HTTPTimeoutSeconds) * time.Second)

	// Initialize and start health check server (before starting SMTP servers)
	healthServer := startHealthServer(cfg, logger, statsManager, circuitBreaker, nil, nil, s3Client)

	// Set ACME handler on health server if autocert is enabled
	if healthServer != nil && tlsMgr != nil {
		healthServer.SetACMEHandler(tlsMgr.HTTPHandler())
	}

	// Start stats manager and sync/export loops
	statsManager.Start()
	startStatsLoops(ctx, statsManager, s3Client, cfg, logger)

	// --- SMTP Server Setup ---

	// Create shared DNS resolver with caching (used by all servers)
	dnsTimeout := time.Duration(cfg.DNS.TimeoutSeconds) * time.Second
	dnsCacheTTL := time.Duration(cfg.DNS.CacheTTLSeconds) * time.Second
	dnsResolver, dnsCache := smtp.NewDNSResolver(cfg.DNS.Resolvers, dnsTimeout, dnsCacheTTL)
	if len(cfg.DNS.Resolvers) > 0 {
		logger.Info("Using custom DNS resolvers: " + strings.Join(cfg.DNS.Resolvers, ", "))
	} else {
		logger.Info("Using system default DNS resolver")
	}
	_ = dnsCache // Cache wrapper for future use

	// Initialize routing client if enabled
	var routingClient smtp.RoutingClient
	if cfg.Routing.Enabled {
		// Create circuit breaker for routing endpoint
		var routingCircuitBreaker *poster.CircuitBreaker
		if cfg.Routing.CircuitBreaker.Enabled {
			cbConfig := poster.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: cfg.Routing.CircuitBreaker.FailureThreshold,
				SuccessThreshold: cfg.Routing.CircuitBreaker.SuccessThreshold,
				Timeout:          time.Duration(cfg.Routing.CircuitBreaker.TimeoutSeconds) * time.Second,
				HalfOpenMaxCalls: cfg.Routing.CircuitBreaker.HalfOpenMaxCalls,
				ResetTimeout:     time.Duration(cfg.Routing.CircuitBreaker.ResetTimeoutSeconds) * time.Second,
			}
			routingCircuitBreaker = poster.NewCircuitBreaker(cbConfig, logger, metricsInstance)
			logger.Info("Routing circuit breaker enabled",
				"failure_threshold", cfg.Routing.CircuitBreaker.FailureThreshold,
				"timeout_seconds", cfg.Routing.CircuitBreaker.TimeoutSeconds)
		}

		routingClient, err = routing.NewClient(routing.ClientConfig{
			Endpoint:                cfg.Routing.Endpoint,
			APIKey:                  cfg.Routing.APIKey,
			TimeoutMS:               cfg.Routing.TimeoutMS,
			MaxRetries:              cfg.Routing.RetryAttempts,
			CacheTTLSeconds:         cfg.Routing.CacheTTLSeconds,
			CacheNegativeTTLSeconds: cfg.Routing.CacheNegativeTTLSeconds,
			CacheMaxEntries:         cfg.Routing.CacheMaxEntries,
			FallbackOnError:         cfg.Routing.FallbackOnError,
			CircuitBreaker:          routingCircuitBreaker,
			Logger:                  logger,
		})
		if err != nil {
			logger.Error("Failed to initialize routing client", "error", err)
			os.Exit(1)
		}
		logger.Info("Routing client initialized",
			"endpoint", cfg.Routing.Endpoint,
			"cache_max_entries", cfg.Routing.CacheMaxEntries,
			"cache_ttl_seconds", cfg.Routing.CacheTTLSeconds)
	}

	// Initialize delivery queue if routing + queue enabled
	var deliveryQueue smtp.DeliveryQueue
	if cfg.Routing.Enabled && cfg.Queue.Enabled {
		// Create circuit breaker for forwarding endpoint if enabled
		var forwardingCircuitBreaker *poster.CircuitBreaker
		if cfg.Forwarding.Enabled && cfg.Forwarding.CircuitBreaker.Enabled {
			fwdCBConfig := poster.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: cfg.Forwarding.CircuitBreaker.FailureThreshold,
				SuccessThreshold: cfg.Forwarding.CircuitBreaker.SuccessThreshold,
				Timeout:          time.Duration(cfg.Forwarding.CircuitBreaker.TimeoutSeconds) * time.Second,
				HalfOpenMaxCalls: cfg.Forwarding.CircuitBreaker.HalfOpenMaxCalls,
				ResetTimeout:     time.Duration(cfg.Forwarding.CircuitBreaker.ResetTimeoutSeconds) * time.Second,
			}
			forwardingCircuitBreaker = poster.NewCircuitBreaker(fwdCBConfig, logger, metricsInstance)
			logger.Info("Forwarding circuit breaker enabled",
				"failure_threshold", cfg.Forwarding.CircuitBreaker.FailureThreshold,
				"timeout_seconds", cfg.Forwarding.CircuitBreaker.TimeoutSeconds)
		}

		// Use persistent queue with BadgerDB (48-hour retry window)
		dataDir := cfg.Queue.DataDir
		if dataDir == "" {
			dataDir = "./data/queue"
		}

		queueConfig := queue.QueueConfig{
			Workers:         cfg.Queue.Workers,
			MaxRetryHours:   cfg.Queue.MaxRetryHours,
			DeliveryTimeout: time.Duration(cfg.Delivery.HTTPTimeoutSeconds) * time.Second,
		}

		persistentQueue, err := queue.NewPersistentQueue(
			queueConfig,
			dataDir,
			httpClient,
			circuitBreaker,
			forwardingCircuitBreaker,
			logger,
			metricsInstance,
		)
		if err != nil {
			logger.Error("Failed to create persistent delivery queue", "error", err)
			os.Exit(1)
		}

		if err := persistentQueue.Start(); err != nil {
			logger.Error("Failed to start persistent delivery queue", "error", err)
			os.Exit(1)
		}

		deliveryQueue = persistentQueue

		logger.Info("Persistent delivery queue started",
			"workers", cfg.Queue.Workers,
			"max_retry_hours", cfg.Queue.MaxRetryHours,
			"data_dir", dataDir)
	}

	// Initialize SRS rewriter if forwarding + SRS enabled
	var srsRewriter smtp.SRSRewriter
	if cfg.Forwarding.Enabled && cfg.Forwarding.SRS.Enabled {
		srsSecret := cfg.Forwarding.SRS.Secret
		if srsSecret == "" {
			// Try environment variable
			srsSecret = os.Getenv("SRS_SECRET")
		}
		if srsSecret == "" {
			logger.Error("SRS enabled but no secret provided (set forwarding.srs.secret or SRS_SECRET env var)")
			os.Exit(1)
		}
		if cfg.Forwarding.SRS.Domain == "" {
			logger.Error("SRS enabled but no domain provided (set forwarding.srs.domain)")
			os.Exit(1)
		}

		srsRewriter = srs.NewRewriter(srsSecret, cfg.Forwarding.SRS.Domain)
		logger.Info("SRS (Sender Rewriting Scheme) enabled for forwarding",
			"domain", cfg.Forwarding.SRS.Domain)
	}

	// Track all server instances for coordinated shutdown
	var serverWg sync.WaitGroup
	type serverInstance struct {
		backend      *smtp.Backend
		cancelFunc   context.CancelFunc
		shutdownChan chan struct{}
	}
	servers := make([]serverInstance, 0)

	// Start each configured SMTP server instance
	for i := range cfg.Servers {
		serverCfg := &cfg.Servers[i]

		logger.Info("Initializing SMTP server",
			"name", serverCfg.Name,
			"type", serverCfg.Type,
			"listen_addr", serverCfg.ListenAddr,
			"tls", serverCfg.TLS.Mode)

		// Create server-specific backend
		backend := createServerBackend(
			serverCfg,
			cfg,
			statsManager,
			circuitBreaker,
			httpClient,
			dnsResolver,
			metricsInstance,
			routingClient,
			deliveryQueue,
			srsRewriter,
			clusterMgr,
			s3Client,
			logger,
		)

		// Create server-specific context
		serverCtx, serverCancel := context.WithCancel(ctx)

		// Store for shutdown coordination
		servers = append(servers, serverInstance{
			backend:      backend,
			cancelFunc:   serverCancel,
			shutdownChan: backend.ShutdownChan,
		})

		// Start server in background with panic recovery
		serverWg.Add(1)
		// Capture variables for closure
		srvCfg := serverCfg
		srvCtx := serverCtx
		be := backend
		logging.SafeGoWithWg(logger, fmt.Sprintf("smtp-server-%s", srvCfg.Name), &serverWg, func() {
			runSMTPServerInstance(srvCtx, srvCfg, be, tlsConfig, logger)
		})
	}

	if len(servers) == 0 {
		logger.Error("No SMTP servers enabled")
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Started %d SMTP server(s)", len(servers)))

	// Wait for shutdown signal
	<-ctx.Done()
	logger.Info("Initiating graceful shutdown of all servers...")

	// Phase 1: Stop accepting new connections on all servers
	for _, srv := range servers {
		close(srv.shutdownChan)
		srv.cancelFunc()
	}

	// Phase 2: Wait for all servers to shut down
	shutdownDone := make(chan struct{})
	logging.SafeGo(logger, "shutdown-wait", func() {
		serverWg.Wait()
		close(shutdownDone)
	})

	shutdownTimeout := time.Duration(cfg.Defaults.ShutdownTimeoutSeconds) * time.Second
	select {
	case <-shutdownDone:
		logger.Info("All servers shut down gracefully")
	case <-time.After(shutdownTimeout):
		logger.Warn("Shutdown timeout reached, forcing exit")
	}

	// Phase 3: Stop delivery queue
	if deliveryQueue != nil {
		logger.Info("Stopping delivery queue...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Queue.ShutdownTimeoutSeconds)*time.Second)
		defer cancel()
		if err := deliveryQueue.Shutdown(shutdownCtx); err != nil {
			logger.Warn("Queue shutdown error", "error", err)
		}
	}

	// Phase 4: Stop stats manager
	if statsManager != nil {
		logger.Info("Stopping stats manager...")
		statsManager.Stop()
	}

	// Phase 5: Stop health server
	if healthServer != nil {
		logger.Info("Stopping health server...")
		healthServer.Stop(context.Background())
	}

	logger.Info("Graceful shutdown complete")
}

// handleCLIArgs checks for special command-line arguments, like 'generate-config' and '-version'.
func handleCLIArgs() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "generate-config":
			if err := config.SaveExample("config.toml.example"); err != nil {
				log.Fatalf("Failed to generate example config: %v", err)
			}
			fmt.Println("Generated example configuration file: config.toml.example")
			os.Exit(0)
		case "-version", "--version", "version":
			fmt.Printf("mizu-server %s\n", version)
			fmt.Printf("  commit: %s\n", commit)
			fmt.Printf("  built:  %s\n", date)
			os.Exit(0)
		}
	}
}

// initStatsManager initializes the statistics manager based on the configuration.
func initStatsManager(cfg *config.Config, logger *slog.Logger) *stats.Manager {
	if !cfg.Stats.Enabled {
		logger.Info("Stats tracking disabled")
		return stats.NewManager(false, 0, "", false, 0, nil, 0, 0, logger)
	}

	// Stats sync still uses HTTP (not migrated to memberlist yet)
	// Build peer URLs from seed nodes for stats HTTP sync
	var syncServers []string
	if cfg.Stats.SyncEnabled && cfg.Cluster.Enabled {
		// Convert peers to HTTP URLs (assuming health server port 8080)
		syncServers = make([]string, len(cfg.Cluster.Peers))
		for i, peer := range cfg.Cluster.Peers {
			// Extract hostname from "hostname:port" format
			host, _, _ := net.SplitHostPort(peer)
			if host == "" {
				host = peer // No port specified
			}
			syncServers[i] = fmt.Sprintf("http://%s:8080", host)
		}
	}

	// Use node name for stats identification
	hostname := cfg.Cluster.NodeName
	if hostname == "" {
		// Auto-detect hostname if not configured
		if h, err := os.Hostname(); err == nil {
			hostname = h
		}
	}

	statsManager := stats.NewManager(true, time.Duration(cfg.Stats.RetentionSeconds)*time.Second, hostname,
		cfg.Stats.SyncEnabled, time.Duration(cfg.Stats.SyncIntervalSeconds)*time.Second, syncServers,
		cfg.Stats.MaxIPEntries, cfg.Stats.MaxDomainEntries, logger)
	logger.Info(fmt.Sprintf("Stats tracking enabled with %v retention, max entries: IPs=%d, Domains=%d",
		time.Duration(cfg.Stats.RetentionSeconds)*time.Second, cfg.Stats.MaxIPEntries, cfg.Stats.MaxDomainEntries))

	if cfg.Stats.SyncEnabled {
		logger.Info(fmt.Sprintf("Stats sync enabled with %v interval, syncing with %d servers",
			time.Duration(cfg.Stats.SyncIntervalSeconds)*time.Second, len(syncServers)))
	}
	return statsManager
}

// initStorageBackend initializes the storage backend based on configuration (S3 or filesystem)
func initStorageBackend(cfg *config.Config, logger *slog.Logger) (storage.Backend, *minio.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var backend storage.Backend
	var s3Client *minio.Client

	switch cfg.Storage.Backend {
	case "filesystem":
		logger.Info("Using filesystem storage backend", "path", cfg.Storage.FilesystemPath)
		fsBackend, err := storage.NewFilesystemBackend(cfg.Storage.FilesystemPath, logger)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to init filesystem backend: %w", err)
		}

		// Ensure storage directory exists
		exists, err := fsBackend.BucketExists(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to check storage directory: %w", err)
		}
		if !exists {
			logger.Info("Creating storage directory", "path", cfg.Storage.FilesystemPath)
			if err := fsBackend.MakeBucket(ctx); err != nil {
				return nil, nil, fmt.Errorf("failed to create storage directory: %w", err)
			}
		}

		backend = fsBackend

	case "s3":
		logger.Info("Using S3 storage backend", "bucket", cfg.Storage.Bucket)
		// Initialize S3 client for MinIO (S3-compatible)
		var err error
		s3Client, err = minio.New(cfg.Storage.Endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.Storage.AccessKeyID, cfg.Storage.SecretAccessKey, ""),
			Region: cfg.Storage.Region,
			Secure: true, // Use HTTPS
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to init S3 client: %w", err)
		}

		s3Backend := storage.NewS3Backend(s3Client, cfg.Storage.Bucket, logger)

		// Validate S3 credentials early by checking bucket access
		logger.Info(fmt.Sprintf("Validating S3 access to bucket '%s'...", cfg.Storage.Bucket))
		exists, err := s3Backend.BucketExists(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to validate S3 credentials/access: %w (check S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY)", err)
		}
		if !exists {
			// Bucket doesn't exist - try to create it
			logger.Info(fmt.Sprintf("Bucket '%s' does not exist, attempting to create it...", cfg.Storage.Bucket))
			if err := s3Backend.MakeBucket(ctx); err != nil {
				return nil, nil, fmt.Errorf("S3 bucket '%s' does not exist and could not be created: %w (ensure credentials have s3:CreateBucket permission)", cfg.Storage.Bucket, err)
			}
			logger.Info(fmt.Sprintf("Successfully created S3 bucket '%s'", cfg.Storage.Bucket))
		} else {
			logger.Info(fmt.Sprintf("Successfully validated S3 access to bucket '%s'", cfg.Storage.Bucket))
		}

		backend = s3Backend

	default:
		return nil, nil, fmt.Errorf("invalid storage backend: %s", cfg.Storage.Backend)
	}

	return backend, s3Client, nil
}

// initTLS sets up the storage backend and TLS certificate management (autocert or certmagic).
func initTLS(cfg *config.Config, clusterMgr *cluster.Cluster, logger *slog.Logger) (*tls.Config, *minio.Client, *tlsmgr.Manager, error) {
	storageBackend, s3Client, err := initStorageBackend(cfg, logger)
	if err != nil {
		return nil, nil, nil, err
	}

	_ = storageBackend // TODO: Update TLS manager to use storage abstraction

	var tlsConfig *tls.Config
	var tlsMgr *tlsmgr.Manager

	// Use autocert if enabled (requires cluster for leader election)
	if cfg.TLS.EnableAutocert {
		logger.Info("Using autocert for TLS certificate management")

		// Get leader function from cluster
		var isLeaderF func() bool
		if clusterMgr != nil {
			isLeaderF = clusterMgr.IsLeader
		} else {
			// Fallback: always return true if no cluster (single-node mode)
			isLeaderF = func() bool { return true }
			logger.Warn("No cluster configured - autocert running in single-node mode")
		}

		// Create TLS manager with autocert
		tlsMgr, err = tlsmgr.NewManager(tlsmgr.Config{
			Enabled:   true,
			Email:     cfg.TLS.Email,
			Domains:   cfg.TLS.Domains,
			S3Client:  s3Client,
			S3Bucket:  cfg.Storage.Bucket,
			S3Prefix:  cfg.Storage.Prefix,
			IsLeaderF: isLeaderF,
			Staging:   !cfg.TLS.UseProduction,
		}, logger)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to create TLS manager: %w", err)
		}

		tlsConfig = tlsMgr.TLSConfig()
		if tlsConfig == nil {
			return nil, nil, nil, fmt.Errorf("TLS manager returned nil config")
		}

		logger.Info(fmt.Sprintf("Autocert initialized for domains: %v", cfg.TLS.Domains))
	} else {
		// Use certmagic (existing behavior)
		logger.Info("Using certmagic for TLS certificate management")

		// Configure certmagic logging for debugging TLS certificate issues
		// Note: certmagic expects zap.Logger, not slog.Logger
		// TODO: Create adapter if verbose logging is needed
		_ = cfg.TLS.CertMagicVerbose

		// Set up Certmagic storage to use S3
		certmagic.Default.Storage = storage.NewS3CertStorage(s3Client, cfg.Storage.Bucket, cfg.Storage.Prefix, logger)

		// Configure Certmagic for ACME (Let's Encrypt)
		if cfg.TLS.Email != "" {
			certmagic.DefaultACME.Email = cfg.TLS.Email
		}

		if !cfg.TLS.UseProduction || cfg.TLS.UseLocalCA {
			certmagic.DefaultACME.CA = certmagic.LetsEncryptStagingCA
			logger.Info("Using Let's Encrypt staging CA")
		} else {
			certmagic.DefaultACME.CA = certmagic.LetsEncryptProductionCA
			logger.Info("Using Let's Encrypt production CA")
		}

		certmagic.Default.OnDemand = &certmagic.OnDemandConfig{} // Enable on-demand certs

		// Load or issue initial certificate
		// Get certificate for the first enabled server's domain
		var primaryDomain string
		for _, srv := range cfg.Servers {
			primaryDomain = srv.Domain
			break
		}
		if primaryDomain == "" {
			primaryDomain = cfg.Defaults.Domain
		}

		logger.Info(fmt.Sprintf("Attempting to get TLS certificate for %s...", primaryDomain))
		tlsConfig, err = certmagic.TLS([]string{primaryDomain})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to get initial TLS certificate: %w", err)
		}

		logger.Info(fmt.Sprintf("Successfully configured TLS certificate for %s", primaryDomain))
	}

	// Set default minimum TLS version (can be overridden per-server)
	tlsConfig.MinVersion = tls.VersionTLS12
	logger.Info("Default TLS configuration ready (per-server min version can be set in [server.tls])")

	return tlsConfig, s3Client, tlsMgr, nil
}

// initCircuitBreaker initializes the circuit breaker for the destination endpoint.
func initCircuitBreaker(cfg *config.Config, logger *slog.Logger, metricsInstance *metrics.Metrics) *poster.CircuitBreaker {
	if cfg.Local || !cfg.Delivery.CircuitBreaker.Enabled {
		return nil
	}

	cb := poster.NewCircuitBreaker(poster.CircuitBreakerConfig{
		Enabled:          cfg.Delivery.CircuitBreaker.Enabled,
		FailureThreshold: cfg.Delivery.CircuitBreaker.FailureThreshold,
		SuccessThreshold: cfg.Delivery.CircuitBreaker.SuccessThreshold,
		Timeout:          time.Duration(cfg.Delivery.CircuitBreaker.TimeoutSeconds) * time.Second,
		HalfOpenMaxCalls: cfg.Delivery.CircuitBreaker.HalfOpenMaxCalls,
		ResetTimeout:     time.Duration(cfg.Delivery.CircuitBreaker.ResetTimeoutSeconds) * time.Second,
	}, logger, metricsInstance)
	logger.Info(fmt.Sprintf("Circuit breaker enabled: failure_threshold=%d, timeout=%v",
		cfg.Delivery.CircuitBreaker.FailureThreshold,
		time.Duration(cfg.Delivery.CircuitBreaker.TimeoutSeconds)*time.Second))
	return cb
}

// initCluster initializes the memberlist cluster for distributed operations
func initCluster(cfg *config.Config, logger *slog.Logger) (*cluster.Cluster, error) {
	// Determine node name
	nodeName := cfg.Cluster.NodeName
	if nodeName == "" {
		// Auto-detect from OS hostname
		if h, err := os.Hostname(); err == nil {
			nodeName = h
		} else {
			return nil, fmt.Errorf("failed to auto-detect hostname: %w", err)
		}
	}

	// Read secret key from environment variable (preferred) or config file
	var secretKey []byte
	secretKeyStr := os.Getenv("CLUSTER_SECRET_KEY")
	if secretKeyStr != "" {
		decoded, err := base64.StdEncoding.DecodeString(secretKeyStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode CLUSTER_SECRET_KEY: %w", err)
		}
		if len(decoded) != 32 {
			return nil, fmt.Errorf("CLUSTER_SECRET_KEY must be 32 bytes when decoded, got %d", len(decoded))
		}
		secretKey = decoded
		logger.Info("Using cluster secret key from CLUSTER_SECRET_KEY environment variable")
	} else if cfg.Cluster.SecretKey != "" {
		// Fallback to config file (not recommended for production)
		decoded, err := base64.StdEncoding.DecodeString(cfg.Cluster.SecretKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode cluster.secret_key from config: %w", err)
		}
		if len(decoded) != 32 {
			return nil, fmt.Errorf("cluster.secret_key must be 32 bytes when decoded, got %d", len(decoded))
		}
		secretKey = decoded
		logger.Warn("Using cluster secret key from config file - use CLUSTER_SECRET_KEY env var instead")
	}

	clusterCfg := cluster.Config{
		NodeName:  nodeName,
		BindAddr:  cfg.Cluster.BindAddr,
		BindPort:  cfg.Cluster.BindPort,
		Peers:     cfg.Cluster.Peers,
		SecretKey: secretKey,
		Logger:    logger,
	}

	logger.Info("Initializing memberlist cluster",
		"node_name", nodeName,
		"bind_addr", cfg.Cluster.BindAddr,
		"bind_port", cfg.Cluster.BindPort,
		"peers", len(cfg.Cluster.Peers))

	return cluster.NewCluster(clusterCfg)
}

// startHealthServer initializes and starts the health check server.
func startHealthServer(cfg *config.Config, logger *slog.Logger, statsManager *stats.Manager, cb *poster.CircuitBreaker, connTracker *smtp.ConnectionTracker, distTracker *smtp.DistributedTracker, s3Client *minio.Client) *health.Server {
	if !cfg.Health.Enabled {
		return nil
	}

	var checkers []health.Checker
	if statsManager != nil {
		checkers = append(checkers, statsManager)
	}
	if cb != nil {
		checkers = append(checkers, cb)
	}
	if distTracker != nil {
		checkers = append(checkers, distTracker)
	} else if connTracker != nil {
		checkers = append(checkers, connTracker)
	}
	if s3Client != nil {
		checkers = append(checkers, health.NewCheckS3Connection(s3Client, cfg.Storage.Bucket))
	}
	if !cfg.Local && cfg.Delivery.URL != "" {
		checkers = append(checkers, health.NewCheckDestination(cfg.Delivery.URL, 5*time.Second))
	}
	// Check TLS certificates for all enabled servers
	if !cfg.Local {
		for _, srv := range cfg.Servers {
			if srv.Domain != "" && srv.Domain != "mail.yourdomain.com" {
				_, portStr, err := net.SplitHostPort(srv.ListenAddr)
				if err != nil {
					logger.Warn("Could not parse listen address for health check",
						"server", srv.Name,
						"error", err)
					continue
				}
				port, _ := net.LookupPort("tcp", portStr)
				if port > 0 {
					checkers = append(checkers, health.NewCheckTLSCertificate(srv.Domain, port, 14*24*time.Hour))
				}
			}
		}
	}

	healthServer := health.NewServer(cfg.Health.ListenAddr, logger, checkers...)

	// Configure HTTP Basic Auth for health endpoint
	if cfg.Health.Username != "" {
		healthServer.SetBasicAuth(cfg.Health.Username, cfg.Health.Password)
	}

	// Configure Prometheus metrics endpoint
	healthServer.SetMetricsConfig(
		cfg.Metrics.Enabled,
		cfg.Metrics.Path,
		cfg.Metrics.Username,
		cfg.Metrics.Password,
	)

	// Set stats provider
	healthServer.SetStatsProvider(statsManager)

	// Set cache flusher (if distributed tracker exists)
	if distTracker != nil {
		healthServer.SetCacheFlusher(distTracker)
	}

	healthServer.Start()
	return healthServer
}

// startStatsLoops starts the background loops for exporting and syncing stats data.
func startStatsLoops(ctx context.Context, statsMgr *stats.Manager, s3Client *minio.Client, cfg *config.Config, logger *slog.Logger) {
	if !cfg.Stats.Enabled || !cfg.Stats.SyncEnabled || s3Client == nil {
		return
	}

	// Use node name for stats identification
	hostname := cfg.Cluster.NodeName
	if hostname == "" {
		// Auto-detect hostname if not configured
		if h, err := os.Hostname(); err == nil {
			hostname = h
		}
	}

	// Start export loop
	logging.SafeGo(logger, "stats-export-loop", func() {
		statsMgr.StartExportLoop(ctx, s3Client, cfg.Storage.Bucket, cfg.Storage.Prefix,
			hostname, time.Duration(cfg.Stats.SyncIntervalSeconds)*time.Second)
	})

	// Start sync loop
	logging.SafeGo(logger, "stats-sync-loop", func() {
		statsMgr.StartSyncLoop(ctx, s3Client, cfg.Storage.Bucket, cfg.Storage.Prefix,
			time.Duration(cfg.Stats.SyncIntervalSeconds)*time.Second)
	})
}

// createServerBackend creates a Backend instance for a specific server
func createServerBackend(
	serverCfg *config.ServerConfig,
	globalCfg *config.Config,
	statsManager *stats.Manager,
	circuitBreaker *poster.CircuitBreaker,
	httpClient *http.Client,
	dnsResolver *net.Resolver,
	metricsInstance *metrics.Metrics,
	routingClient smtp.RoutingClient,
	deliveryQueue smtp.DeliveryQueue,
	srsRewriter smtp.SRSRewriter,
	clusterMgr *cluster.Cluster,
	s3Client *minio.Client,
	logger *slog.Logger,
) *smtp.Backend {
	// Create server-specific logger
	serverLogger := logger.With(
		"server_name", serverCfg.Name,
		"server_type", serverCfg.Type,
	)

	// Create per-server connection tracker
	connTracker := smtp.NewConnectionTracker(serverCfg.Limits.MaxConnections, serverCfg.Limits.MaxConnectionsPerIP)

	// Create distributed tracker if enabled for this server
	var distTracker *smtp.DistributedTracker
	if serverCfg.Distributed.Enabled {
		if !globalCfg.Cluster.Enabled || clusterMgr == nil {
			serverLogger.Error("Distributed tracking requires cluster.enabled=true")
			os.Exit(1)
		}

		nodeName := globalCfg.Cluster.NodeName
		if nodeName == "" {
			if h, err := os.Hostname(); err == nil {
				nodeName = h
			}
		}

		distTracker = smtp.NewDistributedTracker(
			connTracker,
			s3Client,
			globalCfg.Storage.Bucket,
			globalCfg.Storage.Prefix,
			smtp.DistributedConfig{
				Hostname:          nodeName,
				Cluster:           clusterMgr,
				GossipInterval:    time.Duration(serverCfg.Distributed.GossipIntervalSeconds) * time.Second,
				S3SyncInterval:    time.Duration(serverCfg.Distributed.S3SyncIntervalSeconds) * time.Second,
				GlobalMaxPerIP:    serverCfg.Distributed.GlobalMaxPerIP,
				RecipientCacheTTL: time.Duration(serverCfg.Distributed.RecipientCacheTTLSeconds) * time.Second,
			},
			serverLogger,
		)
		distTracker.Start()
		serverLogger.Info("Distributed tracking enabled", "global_max_per_ip", serverCfg.Distributed.GlobalMaxPerIP)
	}

	// Create per-server rate limiter if configured
	var rateLimiter *smtp.RateLimiter
	if serverCfg.RateLimit.Enabled {
		if serverCfg.RateLimit.GossipEnabled && (!globalCfg.Cluster.Enabled || clusterMgr == nil) {
			serverLogger.Error("Rate limit gossip requires cluster.enabled=true")
			os.Exit(1)
		}

		rateLimiter = smtp.NewRateLimiter(serverCfg.RateLimit, clusterMgr, serverLogger)
		serverLogger.Info("Rate limiting enabled",
			"dimensions", len(serverCfg.RateLimit.Dimensions),
			"gossip_enabled", serverCfg.RateLimit.GossipEnabled)
	}

	// Initialize ARC signer if enabled
	var arcSigner *validation.ARCSigner
	if serverCfg.ARC.Enabled {
		var err error
		arcSigner, err = validation.NewARCSigner(
			serverCfg.ARC.Domain,
			serverCfg.ARC.Selector,
			serverCfg.ARC.PrivateKeyPath,
			serverLogger,
		)
		if err != nil {
			serverLogger.Error("Failed to initialize ARC signer", "error", err)
			os.Exit(1)
		}
		serverLogger.Info("ARC signing enabled",
			"domain", serverCfg.ARC.Domain,
			"selector", serverCfg.ARC.Selector)
	}

	// Initialize authenticator for submission servers
	var authenticator smtp.Authenticator
	if serverCfg.IsSubmission() && serverCfg.Auth.Required {
		authenticator = initAuthenticator(serverCfg, serverLogger)
	}

	// Initialize DKIM signer for submission servers
	var dkimSigner smtp.DKIMSigner
	if serverCfg.DKIM.Enabled {
		dkimSigner = initDKIMSigner(serverCfg, serverLogger)
	}

	// Create Backend
	var activeSessionsWg sync.WaitGroup
	var activeSessionCount atomic.Int64
	shutdownChan := make(chan struct{})

	return &smtp.Backend{
		ServerConfig:       serverCfg,
		GlobalConfig:       globalCfg,
		StatsManager:       statsManager,
		CircuitBreaker:     circuitBreaker,
		HTTPClient:         httpClient,
		DNSResolver:        dnsResolver,
		Metrics:            metricsInstance,
		Logger:             serverLogger,
		ActiveSessionsWg:   &activeSessionsWg,
		ActiveSessionCount: &activeSessionCount,
		ShutdownChan:       shutdownChan,
		ConnTracker:        connTracker,
		DistTracker:        distTracker,
		RateLimiter:        rateLimiter,
		Authenticator:      authenticator,
		DKIMSigner:         dkimSigner,
		ARCSigner:          arcSigner,
		RoutingClient:      routingClient,
		DeliveryQueue:      deliveryQueue,
		SRSRewriter:        srsRewriter,
	}
}

// initAuthenticator initializes an HTTP authenticator for submission servers
func initAuthenticator(serverCfg *config.ServerConfig, logger *slog.Logger) smtp.Authenticator {
	if serverCfg.Auth.Endpoint == "" {
		logger.Error("Authentication requires auth.endpoint")
		os.Exit(1)
	}
	logger.Info("Using HTTP authenticator", "endpoint", serverCfg.Auth.Endpoint)
	return smtp.NewHTTPAuthenticator(serverCfg.Auth.Endpoint, serverCfg.Auth.APIKey, logger)
}

// initDKIMSigner initializes a DKIM signer based on server config
func initDKIMSigner(serverCfg *config.ServerConfig, logger *slog.Logger) smtp.DKIMSigner {
	signer, err := validation.NewDKIMSigner(
		serverCfg.DKIM.Domain,
		serverCfg.DKIM.Selector,
		serverCfg.DKIM.PrivateKeyPath,
		logger,
	)
	if err != nil {
		logger.Error("Failed to initialize DKIM signer",
			"domain", serverCfg.DKIM.Domain,
			"selector", serverCfg.DKIM.Selector,
			"key_path", serverCfg.DKIM.PrivateKeyPath,
			"error", err)
		os.Exit(1)
	}

	logger.Info("DKIM signer initialized",
		"domain", serverCfg.DKIM.Domain,
		"selector", serverCfg.DKIM.Selector)

	return signer
}

// runSMTPServerInstance runs a single SMTP server instance
func runSMTPServerInstance(ctx context.Context, serverCfg *config.ServerConfig, be *smtp.Backend, tlsConfig *tls.Config, logger *slog.Logger) {
	server := gosmtp.NewServer(be)
	server.Addr = serverCfg.ListenAddr
	server.Domain = serverCfg.Domain
	server.ReadTimeout = time.Duration(serverCfg.TimeoutSeconds) * time.Second
	server.WriteTimeout = time.Duration(serverCfg.TimeoutSeconds) * time.Second
	server.MaxMessageBytes = int64(serverCfg.MaxMessageSize)
	server.EnableSMTPUTF8 = true

	// Configure authentication for submission servers
	// Note: Authentication is handled in the Backend.Auth() method
	if serverCfg.IsSubmission() {
		server.AllowInsecureAuth = false // Always require TLS for AUTH
	}

	// Configure TLS mode
	// Note: STARTTLS is always available if TLSConfig is set in go-smtp
	if serverCfg.IsTLSEnabled() {
		// Clone TLS config for per-server settings
		serverTLSConfig := tlsConfig.Clone()

		// Apply per-server min TLS version if specified
		if serverCfg.TLS.MinTLSVersion != "" {
			serverTLSConfig.MinVersion = getTLSVersion(serverCfg.TLS.MinTLSVersion)
			logger.Info("Server-specific minimum TLS version",
				"server", serverCfg.Name,
				"min_tls_version", serverCfg.TLS.MinTLSVersion)
		}

		server.TLSConfig = serverTLSConfig
	} else {
		// No TLS (only for testing/internal use)
		server.TLSConfig = nil
	}

	// Create listener
	listener, err := net.Listen("tcp", serverCfg.ListenAddr)
	if err != nil {
		logger.Error("Failed to create listener",
			"server", serverCfg.Name,
			"addr", serverCfg.ListenAddr,
			"error", err)
		return
	}
	defer listener.Close()

	// Start server in background
	serverErrors := make(chan error, 1)
	logging.SafeGo(logger, "smtp-server", func() {
		logger.Info("SMTP server listening",
			"server", serverCfg.Name,
			"type", serverCfg.Type,
			"addr", serverCfg.ListenAddr,
			"tls", serverCfg.TLS.Mode,
			"deferred_handshake", serverCfg.TLS.DeferredHandshake)

		// For implicit TLS (port 465), wrap listener with TLS
		if serverCfg.UsesImplicitTLS() && tlsConfig != nil {
			// Use deferred TLS handshake if enabled to prevent head-of-line blocking
			if serverCfg.TLS.DeferredHandshake {
				// Determine handshake timeout
				handshakeTimeout := time.Duration(serverCfg.TLS.HandshakeTimeoutSecs) * time.Second
				if handshakeTimeout == 0 {
					handshakeTimeout = 10 * time.Second // Default 10 seconds
				}

				tlsListener := tlsmgr.NewDeferredTLSListener(listener, tlsConfig, handshakeTimeout, logger)
				logger.Info("Using deferred TLS handshake for implicit TLS",
					"server", serverCfg.Name,
					"handshake_timeout", handshakeTimeout)
				serverErrors <- server.Serve(tlsListener)
			} else {
				// Traditional synchronous TLS handshake in Accept()
				tlsListener := tls.NewListener(listener, tlsConfig)
				serverErrors <- server.Serve(tlsListener)
			}
		} else {
			serverErrors <- server.Serve(listener)
		}
	})

	// Wait for shutdown or error
	select {
	case <-ctx.Done():
		logger.Info("Shutting down server", "server", serverCfg.Name)

		// Graceful shutdown
		close(be.ShutdownChan)
		listener.Close()

		// Wait for active sessions
		waitDone := make(chan struct{})
		logging.SafeGo(logger, "shutdown-wait", func() {
			be.ActiveSessionsWg.Wait()
			close(waitDone)
		})

		select {
		case <-waitDone:
			logger.Info("Server shut down gracefully", "server", serverCfg.Name)
		case <-time.After(time.Duration(serverCfg.ShutdownTimeoutSeconds) * time.Second):
			logger.Warn("Server shutdown timeout", "server", serverCfg.Name)
		}

	case err := <-serverErrors:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			logger.Error("Server error",
				"server", serverCfg.Name,
				"error", err)
		}
	}
}

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
