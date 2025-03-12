// internal/server/websocket.go
package server

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for testing
	},
}

// WebSocketManager manages WebSocket connections
type WebSocketManager struct {
	clients      map[*websocket.Conn]bool
	clientsMutex sync.Mutex
	logger       *zap.Logger
	stop         chan struct{}
}

// NewWebSocketManager creates a new WebSocket manager
func NewWebSocketManager(logger *zap.Logger) *WebSocketManager {
	return &WebSocketManager{
		clients: make(map[*websocket.Conn]bool),
		logger:  logger,
		stop:    make(chan struct{}),
	}
}

// Start begins sending periodic updates to clients
func (wsm *WebSocketManager) Start() {
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				wsm.broadcastUpdate()
			case <-wsm.stop:
				ticker.Stop()
				return
			}
		}
	}()
}

// Stop stops the WebSocket manager
func (wsm *WebSocketManager) Stop() {
	close(wsm.stop)

	wsm.clientsMutex.Lock()
	defer wsm.clientsMutex.Unlock()

	for client := range wsm.clients {
		client.Close()
	}
}

// HandleConnection handles a new WebSocket connection
func (wsm *WebSocketManager) HandleConnection(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		wsm.logger.Error("Failed to upgrade connection", zap.Error(err))
		return
	}

	// Register new client
	wsm.clientsMutex.Lock()
	wsm.clients[ws] = true
	wsm.clientsMutex.Unlock()

	wsm.logger.Info("New WebSocket connection established",
		zap.String("remote", ws.RemoteAddr().String()))

	// Send initial message
	initMsg := map[string]interface{}{
		"type":    "connection_established",
		"message": "WebSocket connection established",
		"time":    time.Now().Format(time.RFC3339),
	}

	if err := ws.WriteJSON(initMsg); err != nil {
		wsm.logger.Error("Failed to send initial message", zap.Error(err))
	}

	go wsm.readPump(ws)
}

// readPump handles messages from clients and removes closed connections
func (wsm *WebSocketManager) readPump(ws *websocket.Conn) {
	defer func() {
		wsm.clientsMutex.Lock()
		delete(wsm.clients, ws)
		wsm.clientsMutex.Unlock()
		ws.Close()
		wsm.logger.Info("WebSocket connection closed",
			zap.String("remote", ws.RemoteAddr().String()))
	}()

	// Set read deadline and pong handler
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Read messages in a loop
	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				wsm.logger.Warn("WebSocket read error", zap.Error(err))
			}
			break
		}
		// Reset read deadline with each successful read
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
	}
}

// broadcastUpdate sends a message to all connected clients
func (wsm *WebSocketManager) broadcastUpdate() {
	message := map[string]interface{}{
		"type":      "update",
		"timestamp": time.Now().Format(time.RFC3339),
		"data": map[string]interface{}{
			"cpu":    generateRandomMetric(10, 90), // Random CPU usage between 10-90%
			"memory": generateRandomMetric(20, 80), // Random memory usage between 20-80%
			"temp":   generateRandomMetric(30, 70), // Random temperature between 30-70
		},
	}

	wsm.clientsMutex.Lock()
	defer wsm.clientsMutex.Unlock()

	for client := range wsm.clients {
		err := client.WriteJSON(message)
		if err != nil {
			wsm.logger.Warn("Error broadcasting to client",
				zap.Error(err),
				zap.String("remote", client.RemoteAddr().String()))
			client.Close()
			delete(wsm.clients, client)
		}
	}

	wsm.logger.Debug("Broadcasted updates",
		zap.Int("client_count", len(wsm.clients)))
}

// generateRandomMetric generates a random metric value
func generateRandomMetric(min, max float64) float64 {
	return min + (max-min)*float64(time.Now().UnixNano()%100)/100.0
}
