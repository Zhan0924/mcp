package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ──────────────────────────────────────────────────────────────────────────────
//  Prometheus Metrics — P1 可观测性
//
//  提供标准 Prometheus 指标，替代之前的 JSON /metrics 端点。
//  指标覆盖：HTTP 请求、Embedding、搜索、索引、缓存、熔断器、任务队列。
//
//  使用方式：
//    prom := NewPrometheusMetrics()
//    mux.Handle("/metrics", prom.Handler())            // Prometheus 格式
//    handler := prom.HTTPMiddleware(next)               // 自动记录 HTTP 指标
//
//  Grafana Dashboard 推荐 panels：
//    - rate(rag_http_requests_total[5m])                // 请求 QPS
//    - histogram_quantile(0.95, rag_http_duration_seconds) // P95 延迟
//    - rag_embedding_cache_hits_total / rag_embedding_requests_total  // 缓存命中率
// ──────────────────────────────────────────────────────────────────────────────

// PrometheusMetrics 统一 Prometheus 指标注册
type PrometheusMetrics struct {
	// HTTP 指标
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec
	HTTPActiveRequests  prometheus.Gauge

	// Embedding 指标
	EmbeddingRequestsTotal *prometheus.CounterVec
	EmbeddingDuration      *prometheus.HistogramVec
	EmbeddingCacheHits     *prometheus.CounterVec

	// 搜索指标
	SearchRequestsTotal *prometheus.CounterVec
	SearchDuration      *prometheus.HistogramVec
	SearchResultCount   *prometheus.HistogramVec

	// 索引指标
	IndexRequestsTotal *prometheus.CounterVec
	IndexChunksTotal   prometheus.Counter
	IndexDuration      *prometheus.HistogramVec

	// 基础设施指标
	CircuitBreakerOpens prometheus.Counter
	TaskQueueDepth      prometheus.Gauge
	ActiveSessions      prometheus.Gauge

	registry *prometheus.Registry
}

// NewPrometheusMetrics 创建并注册所有 Prometheus 指标
func NewPrometheusMetrics() *PrometheusMetrics {
	reg := prometheus.NewRegistry()
	// 注册 Go 运行时收集器（GC、goroutine、内存等）
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))

	factory := promauto.With(reg)

	m := &PrometheusMetrics{
		registry: reg,

		// ── HTTP ────────────────────────────────────────────
		HTTPRequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "rag_http_requests_total",
			Help: "Total HTTP requests by method, path, and status code",
		}, []string{"method", "path", "status"}),

		HTTPRequestDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "rag_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"method", "path"}),

		HTTPActiveRequests: factory.NewGauge(prometheus.GaugeOpts{
			Name: "rag_http_active_requests",
			Help: "Number of currently active HTTP requests",
		}),

		// ── Embedding ──────────────────────────────────────
		EmbeddingRequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "rag_embedding_requests_total",
			Help: "Total embedding requests by provider and status",
		}, []string{"provider", "status"}),

		EmbeddingDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "rag_embedding_duration_seconds",
			Help:    "Embedding generation duration in seconds",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
		}, []string{"provider"}),

		EmbeddingCacheHits: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "rag_embedding_cache_hits_total",
			Help: "Embedding cache hits by layer (local/redis)",
		}, []string{"layer"}),

		// ── Search ─────────────────────────────────────────
		SearchRequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "rag_search_requests_total",
			Help: "Total search requests by method and status",
		}, []string{"method", "status"}),

		SearchDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "rag_search_duration_seconds",
			Help:    "Search request duration in seconds",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		}, []string{"method"}),

		SearchResultCount: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "rag_search_result_count",
			Help:    "Number of results returned per search",
			Buckets: []float64{0, 1, 2, 5, 10, 20, 50},
		}, []string{"method"}),

		// ── Index ──────────────────────────────────────────
		IndexRequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "rag_index_requests_total",
			Help: "Total index requests by format and status",
		}, []string{"format", "status"}),

		IndexChunksTotal: factory.NewCounter(prometheus.CounterOpts{
			Name: "rag_index_chunks_total",
			Help: "Total chunks indexed",
		}),

		IndexDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "rag_index_duration_seconds",
			Help:    "Document indexing duration in seconds",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
		}, []string{"format"}),

		// ── Infrastructure ─────────────────────────────────
		CircuitBreakerOpens: factory.NewCounter(prometheus.CounterOpts{
			Name: "rag_circuit_breaker_opens_total",
			Help: "Total circuit breaker open events",
		}),

		TaskQueueDepth: factory.NewGauge(prometheus.GaugeOpts{
			Name: "rag_task_queue_depth",
			Help: "Current task queue depth",
		}),

		ActiveSessions: factory.NewGauge(prometheus.GaugeOpts{
			Name: "rag_active_sessions",
			Help: "Number of active MCP sessions",
		}),
	}

	return m
}

// Handler 返回 Prometheus HTTP handler（用于 /metrics 端点）
func (m *PrometheusMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// HTTPMiddleware 自动记录 HTTP 请求指标
func (m *PrometheusMetrics) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		m.HTTPActiveRequests.Inc()

		// 包装 ResponseWriter 以捕获状态码
		rw := &promResponseWriter{ResponseWriter: w, statusCode: 200}

		next.ServeHTTP(rw, r)

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(rw.statusCode)
		path := normalizePath(r.URL.Path)

		m.HTTPRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
		m.HTTPRequestDuration.WithLabelValues(r.Method, path).Observe(duration)
		m.HTTPActiveRequests.Dec()
	})
}

// promResponseWriter 包装器，捕获 HTTP 状态码（Prometheus 专用，避免与 auth.go 冲突）
type promResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *promResponseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

// normalizePath 路径归一化，防止高基数标签
func normalizePath(path string) string {
	switch path {
	case "/mcp", "/health", "/metrics", "/traces", "/upload":
		return path
	default:
		return "/other"
	}
}
