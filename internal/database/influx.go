// internal/database/influx.go
package database

import (
	"context"
	"fmt"
	"os"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
)

// InfluxClient wraps the InfluxDB client
type InfluxClient struct {
	client   influxdb2.Client
	queryAPI api.QueryAPI
	writeAPI api.WriteAPI
	org      string
	bucket   string
}

// NewInfluxClient creates a new InfluxDB client
func NewInfluxClient() (*InfluxClient, error) {
	url := os.Getenv("INFLUXDB_URL")
	if url == "" {
		url = "http://localhost:8086"
	}

	token := os.Getenv("INFLUXDB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("INFLUXDB_TOKEN environment variable not set")
	}

	org := os.Getenv("INFLUXDB_ORG")
	if org == "" {
		org = "myorg"
	}

	bucket := os.Getenv("INFLUXDB_BUCKET")
	if bucket == "" {
		bucket = "mybucket"
	}

	client := influxdb2.NewClient(url, token)

	// Ping the InfluxDB server to verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok, err := client.Ping(ctx)
	if !ok || err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to connect to InfluxDB: %v", err)
	}

	return &InfluxClient{
		client:   client,
		queryAPI: client.QueryAPI(org),
		writeAPI: client.WriteAPI(org, bucket),
		org:      org,
		bucket:   bucket,
	}, nil
}

// Close closes the InfluxDB client
func (c *InfluxClient) Close() {
	c.client.Close()
}

// QueryHealthMetrics retrieves health metrics from InfluxDB
func (c *InfluxClient) QueryHealthMetrics(rangeStart time.Duration) (interface{}, error) {
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -%s)
		|> filter(fn: (r) => r._measurement == "system")
		|> filter(fn: (r) => r._field == "uptime" or r._field == "load")
		|> last()
	`, c.bucket, rangeStart.String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := c.queryAPI.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query InfluxDB: %v", err)
	}

	// Collect the results
	metrics := make(map[string]interface{})
	for result.Next() {
		record := result.Record()
		metrics[record.Field()] = record.Value()
	}

	if result.Err() != nil {
		return nil, fmt.Errorf("error in query result: %v", result.Err())
	}

	return metrics, nil
}

// WriteHealthCheck records a health check event
func (c *InfluxClient) WriteHealthCheck(status string) {
	point := influxdb2.NewPoint(
		"api_health",
		map[string]string{"service": "mygoapi"},
		map[string]interface{}{
			"status": status,
			"up":     status == "ok",
		},
		time.Now(),
	)

	c.writeAPI.WritePoint(point)
	c.writeAPI.Flush()
}
