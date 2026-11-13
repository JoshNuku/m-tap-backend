package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/boronlabs/m-tap-backend/internal/config"
	"github.com/boronlabs/m-tap-backend/internal/db"
	"github.com/boronlabs/m-tap-backend/internal/handlers"
	"github.com/boronlabs/m-tap-backend/internal/middleware"
	mtapredis "github.com/boronlabs/m-tap-backend/internal/redis"
	"github.com/boronlabs/m-tap-backend/internal/services"
	"github.com/boronlabs/m-tap-backend/internal/worker"
)

func main() {
	// 1. Structured Logging with slog
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(jsonHandler))

	slog.Info("Initializing M-Tap Proximity Switching Backend", "author", "Boron Labs")

	// 2. Load Configuration
	cfg := config.Load()

	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// 3. PostgreSQL 16 Pool via pgxpool
	dbPool, err := db.InitPool(rootCtx, cfg)
	if err != nil {
		slog.Error("Fatal database initialization error", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	// Run idempotent schema migrations
	if err := db.RunMigrations(rootCtx, dbPool); err != nil {
		slog.Error("Failed to apply database migrations", "error", err)
		os.Exit(1)
	}

	// 4. Redis Client
	rdb, err := mtapredis.InitClient(rootCtx, cfg)
	if err != nil {
		slog.Error("Fatal Redis initialization error", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()

	// 5. Asynq Client
	asynqClient := asynq.NewClient(worker.AsynqRedisOpt(cfg))
	defer asynqClient.Close()

	// 6. Core Domain Services
	auditService := services.NewAuditService(dbPool)
	smsService := services.NewAfricaTalkingService(cfg)
	tagService := services.NewTagService(dbPool, rdb, asynqClient, auditService)
	paystackClient := services.NewPaystackClient(cfg)
	paymentService := services.NewPaymentService(cfg, dbPool, rdb, tagService, paystackClient, auditService, asynqClient)

	// 7. Asynq Background Worker Engine
	paymentProcessor := worker.NewPaymentWebhookProcessor(dbPool, rdb, auditService)
	smsProcessor := worker.NewSMSProcessor(smsService)
	asynqServer, asynqMux := worker.NewWorkerServer(cfg, paymentProcessor, smsProcessor)

	go func() {
		slog.Info("Starting Asynq background task worker", "queues", "payments (6), notifications (3), default (1)")
		if err := asynqServer.Run(asynqMux); err != nil {
			slog.Error("Asynq background worker stopped unexpectedly", "error", err)
		}
	}()

	// 8. HTTP Routing (Gin)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.RequestLogger())

	// Handlers
	healthHandler := handlers.NewHealthHandler(dbPool, rdb)
	terminalHandler := handlers.NewTerminalHandler(paymentService, rdb)
	tagHandler := handlers.NewTagHandler(tagService)
	webhookHandler := handlers.NewWebhookHandler(asynqClient)
	swaggerHandler := handlers.NewSwaggerHandler()

	// Swagger / OpenAPI documentation
	router.GET("/swagger", swaggerHandler.ServeUI)
	router.GET("/docs", swaggerHandler.ServeUI)
	router.GET("/swagger/openapi.yaml", swaggerHandler.ServeSpec)

	// Health probes
	router.GET("/health", healthHandler.Health)
	router.GET("/ready", healthHandler.Ready)

	// API v1
	v1 := router.Group("/api/v1")
	{
		// A. Terminal Operations
		terminalGroup := v1.Group("/terminal")
		{
			terminalGroup.POST("/tap",
				middleware.TerminalHMACAuth(dbPool, cfg),
				middleware.DistributedIdempotencyMiddleware(rdb),
				middleware.TerminalRateLimit(rdb),
				terminalHandler.HandleTap,
			)
		}

		// B. Self-Service Tag Onboarding
		tagGroup := v1.Group("/tags")
		{
			tagGroup.POST("/initiate-pairing", middleware.SMSRateLimit(rdb), tagHandler.InitiatePairing)
			tagGroup.POST("/verify-otp", tagHandler.VerifyOTP)
			tagGroup.POST("/revoke", tagHandler.Revoke)
		}

		// C. Paystack Clearing Rail Webhook Engine
		paystackGroup := v1.Group("/payments/paystack")
		{
			paystackGroup.POST("/webhook",
				middleware.PaystackSecurityDefense(cfg),
				webhookHandler.HandlePaystackWebhook,
			)
		}
	}

	// 9. Graceful HTTP Server Execution
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	go func() {
		slog.Info("M-Tap HTTP Switching Engine listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server failed to bind/listen", "error", err)
			os.Exit(1)
		}
	}()

	// 10. Graceful Shutdown on SIGINT/SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down M-Tap switching service gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server forced to shutdown", "error", err)
	}

	asynqServer.Shutdown()

	slog.Info("M-Tap switching backend gracefully stopped")
}

