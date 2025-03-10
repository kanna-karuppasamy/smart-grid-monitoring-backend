// cmd/api/main.go
package main

import (
	"log"

	"github.com/joho/godotenv"
	"github.com/kanna-karuppasamy/smart-grid-monitoring-backend/internal/server"
)

func main() {
	// Load environment variables from .env file if it exists
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found or error loading it, using defaults")
	}

	// Initialize and start the server
	srv := server.NewServer()
	srv.Start()
}
