package victorialogs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Client struct {
	config   *Config
	client   *http.Client
	buffer   chan *LogEntry
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	hostname string
}

type LogEntry struct {
	Timestamp   string                 `json:"timestamp"`
	Level       string                 `json:"level"`
	Message     string                 `json:"message"`
	Service     string                 `json:"service"`
	Environment string                 `json:"environment"`
	Hostname    string                 `json:"hostname"`
	ContainerID string                 `json:"container_id"`
	ServerUUID  string                 `json:"server_uuid"`
	ServerName  string                 `json:"server_name,omitempty"`
	Extra       map[string]interface{} `json:"extra,omitempty"`
}

func NewClient(config *Config) (*Client, error) {
	if !config.Enabled {
		return nil, nil
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	url := config.URL
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	config.URL = url

	ctx, cancel := context.WithCancel(context.Background())

	client := &Client{
		config:   config,
		client:   &http.Client{Timeout: 10 * time.Second},
		buffer:   make(chan *LogEntry, config.BatchSize*4),
		ctx:      ctx,
		cancel:   cancel,
		hostname: hostname,
	}

	client.wg.Add(1)
	go client.worker()

	return client, nil
}

func (c *Client) worker() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.config.FlushInterval)
	defer ticker.Stop()

	batch := make([]*LogEntry, 0, c.config.BatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}

		if err := c.sendBatch(batch); err != nil {
			fmt.Fprintf(os.Stderr, "[VictoriaLogs] Failed to send batch: %v\n", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-c.ctx.Done():
			flush()
			return
		case <-ticker.C:
			flush()
		case entry := <-c.buffer:
			batch = append(batch, entry)
			if len(batch) >= c.config.BatchSize {
				flush()
			}
		}
	}
}

func (c *Client) sendBatch(logs []*LogEntry) error {
	var buf bytes.Buffer
	for _, entry := range logs {
		data, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}

	req, err := http.NewRequest("POST", c.config.URL+"/insert/jsonline?_msg_field=message&_time_field=timestamp&_stream_fields=service,environment,container_id,server_uuid", &buf)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/stream+json")
	if c.config.Username != "" && c.config.Password != "" {
		req.SetBasicAuth(c.config.Username, c.config.Password)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("VictoriaLogs returned status %d", resp.StatusCode)
	}

	return nil
}

func (c *Client) Log(containerID, serverUUID, serverName, message string, extra map[string]interface{}) {
	if c == nil {
		return
	}

	entry := &LogEntry{
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		Level:       "info",
		Message:     message,
		Service:     "wings",
		Environment: c.config.Environment,
		Hostname:    c.hostname,
		ContainerID: containerID,
		ServerUUID:  serverUUID,
		ServerName:  serverName,
		Extra:       extra,
	}

	select {
	case c.buffer <- entry:
	default:
		fmt.Fprintf(os.Stderr, "[VictoriaLogs] Buffer full, dropping log for container %s\n", containerID)
	}
}

func (c *Client) Close() {
	if c == nil {
		return
	}

	c.cancel()
	c.wg.Wait()
	close(c.buffer)
}
