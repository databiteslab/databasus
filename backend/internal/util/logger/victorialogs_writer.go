package logger

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

type logEntry struct {
	Time    string         `json:"_time"`
	Message string         `json:"_msg"`
	Level   string         `json:"level"`
	Attrs   map[string]any `json:",inline"`
}

type VictoriaLogsWriter struct {
	url        string
	username   string
	password   string
	httpClient *http.Client
	logChannel chan logEntry
	wg         sync.WaitGroup
	shutdown   func()
	ctx        context.Context
	cancel     context.CancelFunc
	logger     *slog.Logger
}

func NewVictoriaLogsWriter(url, username, password string) *VictoriaLogsWriter {
	ctx, cancel := context.WithCancel(context.Background())

	writer := &VictoriaLogsWriter{
		url:      url,
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logChannel: make(chan logEntry, 1000),
		ctx:        ctx,
		cancel:     cancel,
		logger:     slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	writer.shutdown = sync.OnceFunc(func() {
		// Stop accepting new logs
		writer.cancel()

		// Wait for workers to finish with timeout
		done := make(chan struct{})
		go func() {
			writer.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			writer.logger.Info("VictoriaLogs writer shutdown gracefully")
		case <-time.After(5 * time.Second):
			writer.logger.Warn("VictoriaLogs writer shutdown timeout, some logs may be lost")
		}
	})

	// Start 3 worker goroutines
	for range 3 {
		writer.wg.Go(writer.worker)
	}

	return writer
}

func (w *VictoriaLogsWriter) Write(level, message string, attrs map[string]any) {
	entry := logEntry{
		Time:    time.Now().UTC().Format(time.RFC3339Nano),
		Message: message,
		Level:   level,
		Attrs:   attrs,
	}

	select {
	case w.logChannel <- entry:
		// Successfully queued
	default:
		// Channel is full, drop log with warning
		w.logger.Warn("VictoriaLogs channel buffer full, dropping log entry")
	}
}

func (w *VictoriaLogsWriter) Shutdown(timeout time.Duration) {
	w.shutdown()
}

func (w *VictoriaLogsWriter) worker() {
	batch := make([]logEntry, 0, 100)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			w.flushBatch(batch)
			return

		case entry, ok := <-w.logChannel:
			if !ok {
				w.flushBatch(batch)
				return
			}

			batch = append(batch, entry)

			// Send batch if it reaches 100 entries
			if len(batch) >= 100 {
				w.sendBatch(batch)
				batch = make([]logEntry, 0, 100)
			}

		case <-ticker.C:
			if len(batch) > 0 {
				w.sendBatch(batch)
				batch = make([]logEntry, 0, 100)
			}
		}
	}
}

func (w *VictoriaLogsWriter) sendBatch(entries []logEntry) {
	backoffs := []time.Duration{0, 5 * time.Second, 30 * time.Second, 1 * time.Minute}

	for attempt := range 4 {
		if backoffs[attempt] > 0 {
			time.Sleep(backoffs[attempt])
		}

		if err := w.sendHTTP(entries); err == nil {
			return
		} else if attempt == 3 {
			w.logger.Error("VictoriaLogs failed to send logs after 4 attempts",
				"error", err,
				"entries_count", len(entries))
		}
	}
}

func (w *VictoriaLogsWriter) sendHTTP(entries []logEntry) error {
	// Build JSON Lines payload
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)

	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			return fmt.Errorf("failed to encode log entry: %w", err)
		}
	}

	// Build request
	url := fmt.Sprintf("%s/insert/jsonline?_stream_fields=level&_msg_field=_msg", w.url)
	req, err := http.NewRequestWithContext(w.ctx, "POST", url, &buf)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/x-ndjson")

	// Set Basic Auth (username:password)
	if w.password != "" {
		auth := base64.StdEncoding.EncodeToString([]byte(w.username + ":" + w.password))
		req.Header.Set("Authorization", "Basic "+auth)
	}

	// Send request
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Check response
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("VictoriaLogs returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (w *VictoriaLogsWriter) flushBatch(batch []logEntry) {
	if len(batch) > 0 {
		w.sendBatch(batch)
	}
}
