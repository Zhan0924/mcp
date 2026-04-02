// Package middleware — Prometheus metrics and structured logging for production observability.
package middleware

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
//  Structured Logger (slog) — P1 Observability
// ──────────────────────────────────────────────────────────────────────────────

// LogConfig holds logging configuration.
type LogConfig struct {
	Level  string `toml:"level"`  // debug, info, warn, error
	Format string `toml:"format"` // json, text
}

// NewLogger creates a structured slog.Logger from config.
func NewLogger(config LogConfig) *slog.Logger {
	var level slog.Level
	switch config.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if config.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler).With(
		slog.String("service", "rag-mcp-server"),
	)
}

// ──────────────────────────────────────────────────────────────────────────────
//  Lightweight Metrics (no Prometheus dependency) — P1 Observability
//  Uses atomic counters, exposable via /metrics as JSON or text
// ──────────────────────────────────────────────────────────────────────────────

// Metrics holds application-level counters and histograms.
type Metrics struct {
	// Request counters
	TotalRequests  atomic.Int64
	TotalErrors    atomic.Int64
	ActiveRequests atomic.Int64

	// Tool call counters
	ToolCalls    map[string]*atomic.Int64
	ToolErrors   map[string]*atomic.Int64
	ToolDuration map[string]*atomic.Int64 // total microseconds

	// Embedding metrics
	EmbeddingRequests    atomic.Int64
	EmbeddingErrors      atomic.Int64
	EmbeddingCacheHits   atomic.Int64
	EmbeddingCacheMisses atomic.Int64

	// Search metrics
	SearchRequests  atomic.Int64
	SearchLatencyUs atomic.Int64 // total microseconds

	// Index metrics
	IndexRequests atomic.Int64
	IndexedChunks atomic.Int64

	// Circuit breaker
	CircuitBreakerOpens atomic.Int64

	logger *slog.Logger
}

// NewMetrics creates a new metrics collector.
func NewMetrics(logger *slog.Logger) *Metrics {
	tools := []string{
		"rag_status", "rag_parse_document", "rag_chunk_text",
		"rag_index_document", "rag_list_documents", "rag_search",
		"rag_build_prompt", "rag_export_data", "rag_graph_search",
		"rag_index_url", "rag_task_status", "rag_delete_document",
	}

	m := &Metrics{
		ToolCalls:    make(map[string]*atomic.Int64),
		ToolErrors:   make(map[string]*atomic.Int64),
		ToolDuration: make(map[string]*atomic.Int64),
		logger:       logger,
	}

	for _, t := range tools {
		m.ToolCalls[t] = &atomic.Int64{}
		m.ToolErrors[t] = &atomic.Int64{}
		m.ToolDuration[t] = &atomic.Int64{}
	}

	return m
}

// RecordToolCall records a tool invocation.
func (m *Metrics) RecordToolCall(tool string, duration time.Duration, err error) {
	m.TotalRequests.Add(1)
	if c, ok := m.ToolCalls[tool]; ok {
		c.Add(1)
	}
	if d, ok := m.ToolDuration[tool]; ok {
		d.Add(duration.Microseconds())
	}
	if err != nil {
		m.TotalErrors.Add(1)
		if e, ok := m.ToolErrors[tool]; ok {
			e.Add(1)
		}
	}
}

// Snapshot returns a JSON-serializable snapshot of all metrics.
func (m *Metrics) Snapshot() map[string]interface{} {
	toolStats := make(map[string]interface{})
	for name, calls := range m.ToolCalls {
		c := calls.Load()
		if c > 0 {
			errs := m.ToolErrors[name].Load()
			dur := m.ToolDuration[name].Load()
			avgUs := int64(0)
			if c > 0 {
				avgUs = dur / c
			}
			toolStats[name] = map[string]interface{}{
				"calls":       c,
				"errors":      errs,
				"avg_latency": time.Duration(avgUs * int64(time.Microsecond)).String(),
			}
		}
	}

	return map[string]interface{}{
		"requests": map[string]int64{
			"total":  m.TotalRequests.Load(),
			"errors": m.TotalErrors.Load(),
			"active": m.ActiveRequests.Load(),
		},
		"tools": toolStats,
		"embedding": map[string]int64{
			"requests":     m.EmbeddingRequests.Load(),
			"errors":       m.EmbeddingErrors.Load(),
			"cache_hits":   m.EmbeddingCacheHits.Load(),
			"cache_misses": m.EmbeddingCacheMisses.Load(),
		},
		"search": map[string]interface{}{
			"requests":    m.SearchRequests.Load(),
			"avg_latency": avgDuration(m.SearchLatencyUs.Load(), m.SearchRequests.Load()),
		},
		"index": map[string]int64{
			"requests":       m.IndexRequests.Load(),
			"indexed_chunks": m.IndexedChunks.Load(),
		},
		"circuit_breaker_opens": m.CircuitBreakerOpens.Load(),
	}
}

func avgDuration(totalUs, count int64) string {
	if count == 0 {
		return "0s"
	}
	return time.Duration(totalUs / count * int64(time.Microsecond)).String()
}

// MetricsHandler returns an HTTP handler that serves metrics as JSON.
func (m *Metrics) MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		snapshot := m.Snapshot()

		// Simple JSON serialization without encoding/json import overhead
		w.Write([]byte("{\n"))
		first := true
		for k, v := range snapshot {
			if !first {
				w.Write([]byte(",\n"))
			}
			first = false
			w.Write([]byte("  \"" + k + "\": "))
			writeJSON(w, v)
		}
		w.Write([]byte("\n}\n"))
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	switch val := v.(type) {
	case map[string]int64:
		w.Write([]byte("{"))
		first := true
		for k, n := range val {
			if !first {
				w.Write([]byte(", "))
			}
			first = false
			w.Write([]byte("\"" + k + "\": " + strconv.FormatInt(n, 10)))
		}
		w.Write([]byte("}"))
	case map[string]interface{}:
		w.Write([]byte("{"))
		first := true
		for k, inner := range val {
			if !first {
				w.Write([]byte(", "))
			}
			first = false
			w.Write([]byte("\"" + k + "\": "))
			writeJSON(w, inner)
		}
		w.Write([]byte("}"))
	case int64:
		w.Write([]byte(strconv.FormatInt(val, 10)))
	case string:
		w.Write([]byte("\"" + val + "\""))
	default:
		w.Write([]byte("null"))
	}
}
