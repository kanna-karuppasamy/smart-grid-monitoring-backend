// cmd/api/main.go
package main

import (
	"github.com/joho/godotenv"
	"github.com/kanna-karuppasamy/smart-grid-monitoring-backend/internal/database"
	"github.com/kanna-karuppasamy/smart-grid-monitoring-backend/internal/server"
	"github.com/kanna-karuppasamy/smart-grid-monitoring-backend/pkg/logger"
	"go.uber.org/zap"
)

func main() {
	// Initialize logger
	log := logger.NewLogger()
	defer log.Sync()

	// Load environment variables from .env file if it exists
	if err := godotenv.Load(); err != nil {
		log.Info("No .env file found or error loading it, using defaults")
	}

	// Initialize InfluxDB client
	influxClient, err := database.NewInfluxClient()
	if err != nil {
		log.Fatal("Failed to create InfluxDB client", zap.Error(err))
	}
	defer influxClient.Close()

	// Initialize and start the server
	srv := server.NewServer(log, influxClient)
	if err := srv.Start(); err != nil {
		log.Fatal("Failed to start server", zap.Error(err))
	}
}
