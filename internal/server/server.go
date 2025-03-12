// internal/server/server.go
package server

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kanna-karuppasamy/smart-grid-monitoring-backend/internal/database"
	"go.uber.org/zap"
)

// Server represents the HTTP server
type Server struct {
	router       *gin.Engine
	logger       *zap.Logger
	influxClient *database.InfluxClient
	wsManager    *WebSocketManager
}

// NewServer creates a new server instance
func NewServer(logger *zap.Logger, influxClient *database.InfluxClient) *Server {
	// Set Gin mode based on environment
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	// Add middleware
	router.Use(gin.Recovery())
	router.Use(LoggerMiddleware(logger))

	// Create WebSocket manager
	wsManager := NewWebSocketManager(logger)

	// Setup routes
	router.GET("/health", healthCheckHandler(influxClient, logger))
	router.GET("/ws", wsManager.HandleConnection)

	return &Server{
		router:       router,
		logger:       logger,
		influxClient: influxClient,
		wsManager:    wsManager,
	}
}

// Start starts the HTTP server
func (s *Server) Start() {
	s.wsManager.Start()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: s.router,
	}

	// Start server in a goroutine
	go func() {
		s.logger.Info("Server starting", zap.String("port", port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Fatal("Failed to start server", zap.Error(err))
		}
	}()

	// Wait for interrupt signal to gracefully shut down the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	s.logger.Info("Server shutting down...")

	// Create context with timeout for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		s.logger.Fatal("Server forced to shutdown", zap.Error(err))
	}

	s.logger.Info("Server exited properly")
}

// LoggerMiddleware logs HTTP requests
func LoggerMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		// Process request
		c.Next()

		// Log request details
		latency := time.Since(start)
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method

		logger.Info("Request",
			zap.String("method", method),
			zap.String("path", path),
			zap.Int("status", statusCode),
			zap.String("ip", clientIP),
			zap.Duration("latency", latency),
		)
	}
}

// healthCheckHandler handles the health check endpoint
func healthCheckHandler(influxClient *database.InfluxClient, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Write health check event to InfluxDB
		influxClient.WriteHealthCheck("ok")

		// Try to get system metrics from InfluxDB
		metrics, err := influxClient.QueryHealthMetrics(time.Hour)
		if err != nil {
			logger.Warn("Failed to query InfluxDB metrics", zap.Error(err))
		}

		// Prepare response
		response := gin.H{
			"status": "ok",
			"time":   time.Now().Format(time.RFC3339),
		}

		// Add metrics if available
		if err == nil && metrics != nil {
			response["metrics"] = metrics
		}

		c.JSON(http.StatusOK, response)
	}
}
