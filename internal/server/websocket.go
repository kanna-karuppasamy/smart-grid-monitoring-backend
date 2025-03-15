package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/kanna-karuppasamy/smart-grid-monitoring-backend/internal/database"
	"go.uber.org/zap"
)

// Client represents a connected WebSocket client
type Client struct {
	conn *websocket.Conn
	send chan []byte
}

// UpdateFrequency represents how often to send updates to clients
type UpdateFrequency int

const (
	// DefaultUpdateFrequency is the default update frequency in seconds
	DefaultUpdateFrequency UpdateFrequency = 1
)

// WebSocketManager manages WebSocket connections
type WebSocketManager struct {
	clients        map[*websocket.Conn]*Client
	clientsMutex   sync.Mutex
	logger         *zap.Logger
	stop           chan struct{}
	frequency      UpdateFrequency
	queryAPI       api.QueryAPI
	bucket         string
	org            string
	dataPushTicker *time.Ticker
}

// NewWebSocketManager creates a new WebSocket manager with InfluxDB connection
func NewWebSocketManager(logger *zap.Logger, influxClient *database.InfluxClient) *WebSocketManager {
	wsm := &WebSocketManager{
		clients:   make(map[*websocket.Conn]*Client),
		logger:    logger,
		stop:      make(chan struct{}),
		frequency: DefaultUpdateFrequency,
		queryAPI:  influxClient.QueryAPI,
		bucket:    influxClient.Bucket,
		org:       influxClient.Org,
	}

	// Start the automated data push
	wsm.startAutomatedDataPush()

	return wsm
}

// upgrader configures WebSocket connections
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for now
	},
}

// HandleWebSocket handles WebSocket connections
func (wsm *WebSocketManager) HandleWebSocket(c *gin.Context) {
	// Upgrade the HTTP connection to a WebSocket connection
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		wsm.logger.Error("Failed to upgrade connection", zap.Error(err))
		return
	}

	// Create a new client
	client := &Client{
		conn: conn,
		send: make(chan []byte, 256),
	}

	// Register the client
	wsm.clientsMutex.Lock()
	wsm.clients[conn] = client
	wsm.clientsMutex.Unlock()

	wsm.logger.Info("New WebSocket client connected")

	// Handle WebSocket communication
	go wsm.readPump(client)
	go wsm.writePump(client)
}

// readPump pumps messages from the WebSocket to the hub
func (wsm *WebSocketManager) readPump(client *Client) {
	defer func() {
		wsm.clientsMutex.Lock()
		delete(wsm.clients, client.conn)
		wsm.clientsMutex.Unlock()
		client.conn.Close()
		close(client.send)
		wsm.logger.Info("WebSocket client disconnected")
	}()

	// Set read deadline and pong handler
	client.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	client.conn.SetPongHandler(func(string) error {
		client.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				wsm.logger.Error("WebSocket read error", zap.Error(err))
			}
			break
		}
		// We're not processing any incoming messages since this is push-only
	}
}

// writePump pumps messages from the hub to the WebSocket connection
func (wsm *WebSocketManager) writePump(client *Client) {
	pingTicker := time.NewTicker(30 * time.Second)
	defer func() {
		pingTicker.Stop()
		client.conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.send:
			client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// Channel was closed
				client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := client.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued messages
			n := len(client.send)
			for i := 0; i < n; i++ {
				w.Write(<-client.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-pingTicker.C:
			client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// startAutomatedDataPush begins the periodic data pushing to all connected clients
func (wsm *WebSocketManager) startAutomatedDataPush() {
	wsm.logger.Info("Starting automated data push",
		zap.Int("frequencySeconds", int(wsm.frequency)))

	wsm.dataPushTicker = time.NewTicker(time.Duration(wsm.frequency) * time.Second)

	go func() {
		for {
			select {
			case <-wsm.dataPushTicker.C:
				wsm.pushDataToAllClients()
			case <-wsm.stop:
				wsm.dataPushTicker.Stop()
				return
			}
		}
	}()
}

// pushDataToAllClients queries the latest data and pushes to all connected clients
func (wsm *WebSocketManager) pushDataToAllClients() {
	wsm.clientsMutex.Lock()
	clientCount := len(wsm.clients)
	wsm.clientsMutex.Unlock()

	if clientCount == 0 {
		return // No clients connected, skip the update
	}

	wsm.logger.Info("Pushing data updates to clients", zap.Int("clientCount", clientCount))

	// Get energy by region data
	energyByRegion := wsm.getEnergyByRegion(nil)
	regionData, err := json.Marshal(map[string]interface{}{
		"type":      "energyByRegion",
		"data":      energyByRegion,
		"timestamp": time.Now().Unix(),
	})
	if err == nil {
		wsm.broadcastMessage(regionData)
	}

	// Get faulty meters data
	faultyMeters := wsm.getFaultyMeters(nil)
	faultyData, err := json.Marshal(map[string]interface{}{
		"type":      "faultyMeters",
		"data":      faultyMeters,
		"timestamp": time.Now().Unix(),
	})
	if err == nil {
		wsm.broadcastMessage(faultyData)
	}

	// Get energy by building type data
	buildingTypeData := wsm.getEnergyByBuildingType(nil)
	buildingData, err := json.Marshal(map[string]interface{}{
		"type":      "energyByBuildingType",
		"data":      buildingTypeData,
		"timestamp": time.Now().Unix(),
	})
	if err == nil {
		wsm.broadcastMessage(buildingData)
	}

	// Get peak load meters data
	peakLoadData := wsm.getPeakLoadMeters(nil)
	peakData, err := json.Marshal(map[string]interface{}{
		"type":      "peakLoadMeters",
		"data":      peakLoadData,
		"timestamp": time.Now().Unix(),
	})
	if err == nil {
		wsm.broadcastMessage(peakData)
	}
}

// broadcastMessage sends a message to all connected clients
func (wsm *WebSocketManager) broadcastMessage(message []byte) {
	wsm.clientsMutex.Lock()
	defer wsm.clientsMutex.Unlock()

	for _, client := range wsm.clients {
		select {
		case client.send <- message:
			// Message sent successfully
		default:
			// Client's buffer is full, log and continue
			wsm.logger.Warn("Client buffer full, dropping update")
		}
	}
}

// SetUpdateFrequency changes how often data is pushed to clients
func (wsm *WebSocketManager) SetUpdateFrequency(seconds int) {
	wsm.frequency = UpdateFrequency(seconds)
	if wsm.dataPushTicker != nil {
		wsm.dataPushTicker.Reset(time.Duration(seconds) * time.Second)
	}
	wsm.logger.Info("Update frequency changed", zap.Int("seconds", seconds))
}

// Stop closes the WebSocket manager and all client connections
func (wsm *WebSocketManager) Stop() {
	close(wsm.stop)

	if wsm.dataPushTicker != nil {
		wsm.dataPushTicker.Stop()
	}

	wsm.clientsMutex.Lock()
	defer wsm.clientsMutex.Unlock()

	for conn, client := range wsm.clients {
		conn.Close()
		close(client.send)
	}

	wsm.logger.Info("WebSocket manager stopped")
}

// getEnergyByRegion returns the current energy consumption by region from InfluxDB
func (wsm *WebSocketManager) getEnergyByRegion(params map[string]interface{}) interface{} {
	// Create the Flux query for current energy consumption by region
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -1m)
		|> filter(fn: (r) => r._measurement == "energy_consumption")
		|> filter(fn: (r) => r._field == "consumption_kwh")
		|> group(columns: ["region"])
		|> sum()
	`, wsm.bucket)

	// Execute the query
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := wsm.queryAPI.Query(ctx, query)
	if err != nil {
		wsm.logger.Error("Error querying InfluxDB for energy by region",
			zap.Error(err))
		return []map[string]interface{}{}
	}
	defer result.Close()

	if result.Err() != nil {
		wsm.logger.Error("Error processing InfluxDB results for energy by region",
			zap.Error(result.Err()))
		return []map[string]interface{}{}
	}

	return "getEnergyRegion"
}

// getFaultyMeters returns the number and details of faulty meter recordings from InfluxDB
func (wsm *WebSocketManager) getFaultyMeters(params map[string]interface{}) interface{} {
	// Create the Flux query for faulty meters
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -1h)
		|> filter(fn: (r) => r._measurement == "energy_consumption")
		|> filter(fn: (r) => r.status == "fault")
		|> group(columns: ["meter_id"])
		|> count()
		|> group()
		|> count()
	`, wsm.bucket)

	// Execute the query
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := wsm.queryAPI.Query(ctx, query)
	if err != nil {
		wsm.logger.Error("Error querying InfluxDB for faulty meter count",
			zap.Error(err))
		return []map[string]interface{}{}
	}
	defer result.Close()

	return "getFaultyMeters"
}

// getEnergyByBuildingType returns the mean energy consumption by building type from InfluxDB
func (wsm *WebSocketManager) getEnergyByBuildingType(params map[string]interface{}) interface{} {
	// Create the Flux query for energy by building type
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -1h)
		|> filter(fn: (r) => r._measurement == "energy_consumption")
		|> filter(fn: (r) => r._field == "consumption_kwh")
		|> group(columns: ["building_type"])
		|> mean()
	`, wsm.bucket)

	// Execute the query
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := wsm.queryAPI.Query(ctx, query)
	if err != nil {
		wsm.logger.Error("Error querying InfluxDB for peak load meters",
			zap.Error(err))
		return []map[string]interface{}{}
	}
	defer result.Close()

	return "getEnergyByBuildingType"
}

// getPeakLoadMeters returns the peak load consuming meters and their regions from InfluxDB
func (wsm *WebSocketManager) getPeakLoadMeters(params map[string]interface{}) interface{} {
	// Create the Flux query for peak load meters
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -5m)
		|> filter(fn: (r) => r._measurement == "energy_consumption")
		|> filter(fn: (r) => r._field == "peak_load" and r._value == true)
		|> group(columns: ["region", "meter_id"])
		|> keep(columns: ["region", "meter_id", "_time"])
		|> distinct(column: "meter_id")
	`, wsm.bucket)

	// Execute the query
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := wsm.queryAPI.Query(ctx, query)
	if err != nil {
		wsm.logger.Error("Error querying InfluxDB for peak load meters",
			zap.Error(err))
		return []map[string]interface{}{}
	}
	defer result.Close()

	return "getPeakLoadMeters"
}
