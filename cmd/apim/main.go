// main is the entry point for the VedaDB API Manager (VAPIM).
// It initializes all components including the gateway server, admin server,
// database connection, and handles graceful shutdown on OS signals.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/tiennesdm/vedadb-apim/internal/admin"
	"github.com/tiennesdm/vedadb-apim/internal/gateway"
	"github.com/tiennesdm/vedadb-apim/pkg/config"
	"github.com/tiennesdm/vedadb-apim/pkg/store"
)

const (
	// Version is the current version of VAPIM.
	Version = "1.0.0"
	// BuildTime is set during build.
	BuildTime = "dev"
	// CommitHash is set during build.
	CommitHash = "dev"
)

func main() {
	// Print startup banner
	printBanner()

	// Parse CLI flags
	var (
		configPath = flag.String("config", "", "path to configuration file")
		logLevel   = flag.String("log-level", "", "log level (debug, info, warn, error)")
		version    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *version {
		fmt.Printf("VedaDB API Manager v%s (built: %s, commit: %s)\n", Version, BuildTime, CommitHash)
		os.Exit(0)
	}

	// Determine config path
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = config.FindConfigFile()
	}

	// Load configuration
	cfgManager := config.NewManager()
	if err := cfgManager.Load(cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to load config from %s: %v, using defaults\n", cfgPath, err)
	}
	cfg := cfgManager.Get()

	// Override log level from CLI
	if *logLevel != "" {
		cfg.Log.Level = *logLevel
	}

	// Initialize logger
	logger := initLogger(cfg.Log)
	defer logger.Sync()

	logger.Info("VedaDB API Manager starting",
		zap.String("version", Version),
		zap.String("build_time", BuildTime),
		zap.String("commit", CommitHash),
		zap.String("config_path", cfgPath),
	)

	// Create root context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize VedaDB client
	var dbClient *store.VedaDBClient
	if cfg.Database.Host != "" {
		dbClient = store.NewVedaDBClient(
			cfg.Database.Host,
			cfg.Database.Port,
			cfg.Database.Database,
		).WithTimeout(cfg.Database.Timeout).WithPoolSize(cfg.Database.PoolSize)

		if err := dbClient.Connect(ctx); err != nil {
			logger.Warn("failed to connect to VedaDB, continuing with limited functionality",
				zap.String("host", cfg.Database.Host),
				zap.Int("port", cfg.Database.Port),
				zap.Error(err),
			)
		} else {
			logger.Info("connected to VedaDB",
				zap.String("host", cfg.Database.Host),
				zap.Int("port", cfg.Database.Port),
			)
		}
	}

	// Set Gin mode
	if cfg.Log.Level == "debug" || cfg.Log.Level == "DEBUG" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// Create route store and router
	routeStore := gateway.NewVedaDBRouteStore(dbClient)
	router := gateway.NewRouter(routeStore)

	// Load initial routes
	if err := router.LoadRoutes(ctx); err != nil {
		logger.Warn("failed to load initial routes, will retry", zap.Error(err))
	} else {
		logger.Info("routes loaded", zap.Int("count", router.GetRouteCount()))
	}

	// Start route refresh
	if err := router.Start(ctx); err != nil {
		logger.Warn("failed to start route refresher", zap.Error(err))
	}

	// Create reverse proxy
	proxyConfig := gateway.ProxyConfig{
		RequestTimeout:      cfg.Gateway.RequestTimeout,
		RetryCount:          cfg.Gateway.RetryCount,
		RetryBackoff:        cfg.Gateway.RetryBackoff,
		PreserveHost:        cfg.Gateway.ProxyPreserveHost,
		MaxRequestBodySize:  cfg.Gateway.MaxRequestBodySize,
		MaxResponseBodySize: cfg.Gateway.MaxResponseBodySize,
	}
	proxy := gateway.NewProxy(proxyConfig, logger)

	// Create rate limiter
	var rateLimiter *gateway.RateLimiterMiddleware
	if cfg.RateLimit.Enabled {
		localLimiter := gateway.NewLocalRateLimiter()
		var limiter gateway.RateLimiter
		if cfg.RateLimit.Distributed && dbClient != nil {
			limiter = gateway.NewDistributedRateLimiter(dbClient, gateway.RateLimitConfig{
				Enabled:             cfg.RateLimit.Enabled,
				DefaultLimit:        cfg.RateLimit.DefaultLimit,
				DefaultWindow:       cfg.RateLimit.DefaultWindow,
				HeaderLimitName:     cfg.RateLimit.HeaderLimitName,
				HeaderRemainingName: cfg.RateLimit.HeaderRemainingName,
				HeaderResetName:     cfg.RateLimit.HeaderResetName,
			})
		} else {
			limiter = localLimiter
		}
		rateLimiter = gateway.NewRateLimiterMiddleware(limiter, gateway.RateLimitConfig{
			Enabled:              cfg.RateLimit.Enabled,
			DefaultLimit:         cfg.RateLimit.DefaultLimit,
			DefaultWindow:        cfg.RateLimit.DefaultWindow,
			HeaderLimitName:      cfg.RateLimit.HeaderLimitName,
			HeaderRemainingName:  cfg.RateLimit.HeaderRemainingName,
			HeaderResetName:      cfg.RateLimit.HeaderResetName,
			HeaderRetryAfterName: cfg.RateLimit.HeaderRetryAfterName,
			PerAppLimits: map[string]int{
				"Bronze":    100,
				"Silver":    500,
				"Gold":      2000,
				"Unlimited": 100000,
			},
			PerUserLimits: map[string]int{
				"admin":      100000,
				"publisher":  5000,
				"subscriber": 1000,
			},
		})
	}

	// Create cache middleware
	var cacheMiddleware *gateway.CacheMiddleware
	if cfg.Cache.Enabled {
		cacheStore := gateway.NewInMemoryCache(cfg.Cache.MaxSize, cfg.Cache.MaxEntrySize)
		cacheMiddleware = gateway.NewCacheMiddleware(cacheStore, gateway.CacheMiddlewareConfig{
			Enabled:              cfg.Cache.Enabled,
			DefaultTTL:           cfg.Cache.DefaultTTL,
			MaxEntrySize:         cfg.Cache.MaxEntrySize,
			CacheBypassHeader:    cfg.Cache.CacheBypassHeader,
			CacheBypassValue:     cfg.Cache.CacheBypassValue,
			CacheableMethods:     cfg.Cache.CacheableMethods,
			CacheableStatusCodes: cfg.Cache.CacheableStatusCodes,
			VaryByHeaders:        cfg.Cache.VaryByHeaders,
			StaleWhileRevalidate: cfg.Cache.StaleWhileRevalidate,
		})
	}

	// Create auth middleware
	var authMiddleware *gateway.AuthMiddleware
	if cfg.Auth.Enabled {
		authMiddleware = gateway.NewAuthMiddleware(gateway.AuthMiddlewareConfig{
			Enabled:            cfg.Auth.Enabled,
			JWTSecret:          cfg.Auth.JWTSecret,
			AccessTokenExpiry:  cfg.Auth.AccessTokenExpiry,
			TokenIssuer:        cfg.Auth.TokenIssuer,
			TokenAudience:      cfg.Auth.TokenAudience,
			HeaderName:         cfg.Auth.HeaderName,
			QueryParamName:     cfg.Auth.QueryParamName,
			SkipPaths:          cfg.Auth.SkipPaths,
			SkipAuthForOptions: cfg.Auth.SkipAuthForOptions,
			RevocationCheck:    cfg.Auth.RevocationCheck,
			CacheTokens:        cfg.Auth.CacheTokens,
			TokenCacheTTL:      cfg.Auth.TokenCacheTTL,
		}, logger)
	}

	// Create subscription middleware
	subscriptionMiddleware := gateway.NewSubscriptionMiddleware(logger)

	// Create transform middleware
	transformMiddleware := gateway.NewTransformMiddleware(gateway.TransformMiddlewareConfig{
		Enabled: cfg.Gateway.SchemaValidation,
	})

	// Create analytics middleware
	analyticsMiddleware := gateway.NewAnalyticsMiddleware(gateway.AnalyticsMiddlewareConfig{
		Enabled: cfg.Analytics.Enabled,
		Logger:  logger,
	})

	// Create gateway server
	gatewayOpts := gateway.GatewayOptions{
		Config:                 cfg,
		Logger:                 logger,
		Router:                 router,
		Proxy:                  proxy,
		RateLimiter:            rateLimiter,
		Cache:                  cacheMiddleware,
		Auth:                   authMiddleware,
		Analytics:              analyticsMiddleware,
		Transform:              transformMiddleware,
		Subscription:           subscriptionMiddleware,
	}
	gatewayServer := gateway.NewGatewayServer(gatewayOpts)

	// Create admin server
	adminOpts := admin.AdminServerOptions{
		Config:        cfg,
		ConfigManager: cfgManager,
		Logger:        logger,
		DBClient:      dbClient,
		GatewayAddr:   fmt.Sprintf(":%d", cfg.Server.Port),
	}
	adminServer := admin.NewAdminServer(adminOpts)

	// Start admin server (on a separate port if configured)
	adminPort := cfg.Server.Port + 1
	go func() {
		logger.Info("starting admin server", zap.Int("port", adminPort))
		if err := adminServer.Run(adminPort); err != nil {
			logger.Error("admin server error", zap.Error(err))
		}
	}()

	// Start key manager server (if enabled)
	if cfg.KeyManager.Enabled {
		go func() {
			logger.Info("starting key manager server", zap.Int("port", cfg.KeyManager.Port))
			if err := startKeyManager(cfg.KeyManager, logger); err != nil {
				logger.Error("key manager error", zap.Error(err))
			}
		}()
	}

	// Start publisher API server (if enabled)
	if cfg.Publisher.Enabled {
		go func() {
			logger.Info("starting publisher API server", zap.Int("port", cfg.Publisher.Port))
			if err := startPublisher(cfg.Publisher, logger); err != nil {
				logger.Error("publisher API error", zap.Error(err))
			}
		}()
	}

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// Start gateway server in a goroutine
	gatewayErrChan := make(chan error, 1)
	go func() {
		logger.Info("starting gateway server", zap.Int("port", cfg.Server.Port))
		if err := gatewayServer.Run(); err != nil {
			gatewayErrChan <- err
		}
	}()

	// Wait for signal or error
	select {
	case sig := <-sigChan:
		logger.Info("received signal, initiating graceful shutdown", zap.String("signal", sig.String()))

		// Cancel context to stop background tasks
		cancel()

		// Shutdown components with timeout
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer shutdownCancel()

		// Shutdown gateway
		if err := gatewayServer.Shutdown(); err != nil {
			logger.Error("gateway shutdown error", zap.Error(err))
		}

		// Shutdown admin
		if err := adminServer.Shutdown(); err != nil {
			logger.Error("admin shutdown error", zap.Error(err))
		}

		// Close database connection
		if dbClient != nil {
			if err := dbClient.Close(); err != nil {
				logger.Error("database close error", zap.Error(err))
			}
		}

		logger.Info("shutdown complete")
		os.Exit(0)

	case err := <-gatewayErrChan:
		logger.Fatal("gateway server failed", zap.Error(err))
	}
}

// initLogger creates a new Zap logger based on configuration.
func initLogger(cfg config.LogConfig) *zap.Logger {
	level := zap.InfoLevel
	switch cfg.Level {
	case "debug", "DEBUG":
		level = zap.DebugLevel
	case "warn", "WARN":
		level = zap.WarnLevel
	case "error", "ERROR":
		level = zap.ErrorLevel
	case "fatal", "FATAL":
		level = zap.FatalLevel
	}

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	var zapConfig zap.Config
	if cfg.Format == "json" {
		zapConfig = zap.Config{
			Level:         zap.NewAtomicLevelAt(level),
			Development:   level == zap.DebugLevel,
			Sampling:      nil,
			Encoding:      "json",
			EncoderConfig: encoderConfig,
			OutputPaths:   []string{"stdout"},
			ErrorOutputPaths: []string{"stderr"},
		}
	} else {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoderConfig.ConsoleSeparator = " | "
		zapConfig = zap.Config{
			Level:         zap.NewAtomicLevelAt(level),
			Development:   level == zap.DebugLevel,
			Encoding:      "console",
			EncoderConfig: encoderConfig,
			OutputPaths:   []string{"stdout"},
			ErrorOutputPaths: []string{"stderr"},
		}
	}

	logger, err := zapConfig.Build()
	if err != nil {
		// Fallback to basic logger
		logger = zap.Must(zap.NewDevelopment())
	}

	return logger
}

// printBanner prints the VAPIM startup banner.
func printBanner() {
	banner := `
 _   _____________  ______  ____  ____   _____  __  ___________  __  ____________
| | / / ___/_  __/ / __/\ \/ /  |/  / | / / _ |/ / / /_  __/\ \/ / <  / ___/ _ \
| |/ / /__  / /   / _/   \  / /|_/ /| |/ / __ / /_/ / / /    \  /  / / /__/ // /
|___/\___/ /_/   /___/   /_/_/  /_/ |___/_/ |_\\____/ /_/     /_/  /_/\___/____/

                    VedaDB API Manager v%s
                    High-Performance API Gateway

`
	fmt.Printf(banner, Version)
}

// startKeyManager starts the embedded key manager server.
func startKeyManager(cfg config.KeyManagerConfig, logger *zap.Logger) error {
	engine := gin.New()
	engine.Use(gin.Recovery())

	// OAuth2 token endpoints
	engine.POST("/token", func(c *gin.Context) {
		c.JSON(http.StatusOK, map[string]interface{}{
			"access_token":  "placeholder-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "placeholder-refresh",
		})
	})

	engine.POST("/token/refresh", func(c *gin.Context) {
		c.JSON(http.StatusOK, map[string]interface{}{
			"access_token": "placeholder-refreshed",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	engine.POST("/token/revoke", func(c *gin.Context) {
		c.JSON(http.StatusOK, map[string]interface{}{
			"revoked": true,
		})
	})

	engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, map[string]interface{}{
			"status": "healthy",
		})
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	logger.Info("key manager listening", zap.String("addr", addr))
	return engine.Run(addr)
}

// startPublisher starts the publisher API server.
func startPublisher(cfg config.PublisherConfig, logger *zap.Logger) error {
	engine := gin.New()
	engine.Use(gin.Recovery())

	// API CRUD endpoints
	engine.GET("/apis", func(c *gin.Context) {
		c.JSON(http.StatusOK, map[string]interface{}{
			"count": 0,
			"data":  []interface{}{},
		})
	})

	engine.GET("/apis/:id", func(c *gin.Context) {
		c.JSON(http.StatusNotFound, map[string]interface{}{
			"error": "API not found",
		})
	})

	engine.POST("/apis", func(c *gin.Context) {
		c.JSON(http.StatusCreated, map[string]interface{}{
			"id":     "placeholder",
			"status": "CREATED",
		})
	})

	engine.PUT("/apis/:id", func(c *gin.Context) {
		c.JSON(http.StatusOK, map[string]interface{}{
			"status": "UPDATED",
		})
	})

	engine.DELETE("/apis/:id", func(c *gin.Context) {
		c.JSON(http.StatusNoContent, nil)
	})

	// Application endpoints
	engine.GET("/applications", func(c *gin.Context) {
		c.JSON(http.StatusOK, map[string]interface{}{
			"count": 0,
			"data":  []interface{}{},
		})
	})

	// Subscription endpoints
	engine.GET("/subscriptions", func(c *gin.Context) {
		c.JSON(http.StatusOK, map[string]interface{}{
			"count": 0,
			"data":  []interface{}{},
		})
	})

	engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, map[string]interface{}{
			"status": "healthy",
		})
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	logger.Info("publisher API listening", zap.String("addr", addr))
	return engine.Run(addr)
}
