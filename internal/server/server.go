// internal/server/server.go
package server

import (
	"net/http"
	"os"

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

	wsManager := NewWebSocketManager(logger, influxClient)

	// Setup routes
	router.GET("/health", healthCheckHandler(influxClient, logger))
	router.GET("/ws", wsManager.HandleWebSocket)

	return &Server{
		router:       router,
		logger:       logger,
		influxClient: influxClient,
		wsManager:    wsManager,
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	s.logger.Info("Starting server", zap.String("port", port))
	return http.ListenAndServe(":"+port, s.router)
}

// LoggerMiddleware is a Gin middleware that logs requests
func LoggerMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		logger.Info("Request received",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.String("ip", c.ClientIP()),
		)

		c.Next()

		logger.Info("Request completed",
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
		)
	}
}

// healthCheckHandler handles health check requests
func healthCheckHandler(influxClient *database.InfluxClient, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check InfluxDB connection
		if err := influxClient.Ping(); err != nil {
			logger.Error("Health check failed: InfluxDB connection error", zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status":  "unhealthy",
				"message": "InfluxDB connection error",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "healthy",
			"message": "All systems operational",
		})
	}
}
