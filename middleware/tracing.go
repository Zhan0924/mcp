package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
//  Distributed Tracing — P1 可观测性
//
//  轻量级追踪抽象层，提供 Span 概念用于追踪请求链路。
//  当前使用内置实现（零外部依赖），可无缝升级到 OpenTelemetry：
//    1. go get go.opentelemetry.io/otel
//    2. 替换 NewTracer() 实现为 otel.Tracer()
//    3. 所有 StartSpan/EndSpan 调用签名不变
// ──────────────────────────────────────────────────────────────────────────────

// SpanContext 跨服务传播的追踪上下文
type SpanContext struct {
	TraceID  string `json:"trace_id"`
	SpanID   string `json:"span_id"`
	ParentID string `json:"parent_id,omitempty"`
}

// Span 表示一个操作的追踪单元
type Span struct {
	Name       string            `json:"name"`
	Context    SpanContext       `json:"context"`
	StartTime  time.Time         `json:"start_time"`
	EndTime    time.Time         `json:"end_time,omitempty"`
	Duration   time.Duration     `json:"duration,omitempty"`
	Status     string            `json:"status"` // ok, error
	Attributes map[string]string `json:"attributes,omitempty"`
	mu         sync.Mutex
}

// SetAttribute 设置 span 属性
func (s *Span) SetAttribute(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Attributes == nil {
		s.Attributes = make(map[string]string)
	}
	s.Attributes[key] = value
}

// SetError 标记 span 为错误状态
func (s *Span) SetError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = "error"
	if s.Attributes == nil {
		s.Attributes = make(map[string]string)
	}
	s.Attributes["error.message"] = err.Error()
}

// End 结束 span 并记录持续时间
func (s *Span) End() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.EndTime = time.Now()
	s.Duration = s.EndTime.Sub(s.StartTime)
	if s.Status == "" {
		s.Status = "ok"
	}
}

// Tracer 追踪器接口
type Tracer struct {
	serviceName string
	logger      *slog.Logger
	enabled     bool
	// 追踪数据收集（可选，用于 /traces 端点或导出到 Jaeger）
	mu     sync.Mutex
	traces []Span
	maxLen int
}

// TracerConfig 追踪器配置
type TracerConfig struct {
	Enabled     bool   `toml:"enabled"`
	ServiceName string `toml:"service_name"`
	MaxTraces   int    `toml:"max_traces"` // 内存中保留的最大 trace 数量
}

// DefaultTracerConfig 默认追踪器配置
func DefaultTracerConfig() TracerConfig {
	return TracerConfig{
		Enabled:     true,
		ServiceName: "rag-mcp-server",
		MaxTraces:   1000,
	}
}

// NewTracer 创建追踪器
// 升级到 OpenTelemetry 时，替换此函数的实现即可
func NewTracer(cfg TracerConfig, logger *slog.Logger) *Tracer {
	if cfg.MaxTraces <= 0 {
		cfg.MaxTraces = 1000
	}
	return &Tracer{
		serviceName: cfg.ServiceName,
		logger:      logger,
		enabled:     cfg.Enabled,
		traces:      make([]Span, 0, cfg.MaxTraces),
		maxLen:      cfg.MaxTraces,
	}
}

// ── Context Keys ─────────────────────────────────────────────────────────────

type ctxKey string

const (
	spanCtxKey ctxKey = "trace_span"
	traceIDKey ctxKey = "trace_id"
	spanIDKey  ctxKey = "span_id"
)

// StartSpan 开始一个新 span，自动继承父 span 的 trace_id
func (t *Tracer) StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	span := &Span{
		Name:      name,
		StartTime: time.Now(),
		Status:    "ok",
	}

	// 继承父 span 的 trace_id
	if parentSpan, ok := ctx.Value(spanCtxKey).(*Span); ok {
		span.Context = SpanContext{
			TraceID:  parentSpan.Context.TraceID,
			SpanID:   generateID(8),
			ParentID: parentSpan.Context.SpanID,
		}
	} else if traceID, ok := ctx.Value(traceIDKey).(string); ok {
		span.Context = SpanContext{
			TraceID: traceID,
			SpanID:  generateID(8),
		}
	} else {
		span.Context = SpanContext{
			TraceID: generateID(16),
			SpanID:  generateID(8),
		}
	}

	ctx = context.WithValue(ctx, spanCtxKey, span)
	ctx = context.WithValue(ctx, traceIDKey, span.Context.TraceID)
	ctx = context.WithValue(ctx, spanIDKey, span.Context.SpanID)
	return ctx, span
}

// EndSpan 结束 span 并记录到追踪存储
func (t *Tracer) EndSpan(span *Span) {
	if span == nil || !t.enabled {
		return
	}
	span.End()

	// 记录到日志
	if t.logger != nil {
		attrs := []slog.Attr{
			slog.String("trace_id", span.Context.TraceID),
			slog.String("span_id", span.Context.SpanID),
			slog.String("span", span.Name),
			slog.Duration("duration", span.Duration),
			slog.String("status", span.Status),
		}
		if span.Context.ParentID != "" {
			attrs = append(attrs, slog.String("parent_id", span.Context.ParentID))
		}
		t.logger.LogAttrs(context.Background(), slog.LevelInfo, "trace", attrs...)
	}

	// 保存到内存环形缓冲
	t.mu.Lock()
	if len(t.traces) >= t.maxLen {
		t.traces = t.traces[1:]
	}
	t.traces = append(t.traces, *span)
	t.mu.Unlock()
}

// GetRecentTraces 获取最近的 traces（用于 /traces 端点）
func (t *Tracer) GetRecentTraces(limit int) []Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	if limit <= 0 || limit > len(t.traces) {
		limit = len(t.traces)
	}
	start := len(t.traces) - limit
	result := make([]Span, limit)
	copy(result, t.traces[start:])
	return result
}

// ── HTTP 中间件 ──────────────────────────────────────────────────────────────

// TracingMiddleware 为每个 HTTP 请求创建根 span
func (t *Tracer) Handler(next http.Handler) http.Handler {
	if !t.enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spanName := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
		ctx, span := t.StartSpan(r.Context(), spanName)
		span.SetAttribute("http.method", r.Method)
		span.SetAttribute("http.url", r.URL.String())
		span.SetAttribute("http.remote", r.RemoteAddr)

		// 从请求头提取 trace_id（跨服务传播）
		if incomingTraceID := r.Header.Get("X-Trace-ID"); incomingTraceID != "" {
			span.Context.TraceID = incomingTraceID
		}

		// 响应头注入 trace_id（方便调试）
		w.Header().Set("X-Trace-ID", span.Context.TraceID)

		defer func() {
			t.EndSpan(span)
		}()

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ── 工具函数 ─────────────────────────────────────────────────────────────────

// GetTraceID 从 context 获取 trace_id
func GetTraceID(ctx context.Context) string {
	if id, ok := ctx.Value(traceIDKey).(string); ok {
		return id
	}
	return ""
}

// GetSpan 从 context 获取当前 span
func GetSpan(ctx context.Context) *Span {
	if span, ok := ctx.Value(spanCtxKey).(*Span); ok {
		return span
	}
	return nil
}

func generateID(byteLen int) string {
	b := make([]byte, byteLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
