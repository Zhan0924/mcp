// Package middleware provides HTTP middleware for authentication, rate limiting, and audit logging.
package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ──────────────────────────────────────────────────────────────────────────────
//  Context Keys
// ──────────────────────────────────────────────────────────────────────────────

type contextKey string

const (
	ContextKeyUserID    contextKey = "authenticated_user_id"
	ContextKeySessionID contextKey = "session_id"
	ContextKeyRequestID contextKey = "request_id"
)

// UserIDFromContext extracts the authenticated user ID from context.
func UserIDFromContext(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(ContextKeyUserID).(int64)
	return v, ok
}

// ──────────────────────────────────────────────────────────────────────────────
//  Auth Middleware — JWT / API Key
// ──────────────────────────────────────────────────────────────────────────────

// AuthConfig holds authentication configuration.
type AuthConfig struct {
	Enabled   bool             `toml:"enabled"`
	Mode      string           `toml:"mode"` // "api_key", "jwt", "both"
	APIKeys   map[string]int64 // key -> user_id mapping
	JWTSecret string           `toml:"jwt_secret"`
	SkipPaths []string         `toml:"skip_paths"` // e.g., ["/health", "/metrics"]
}

// AuthMiddleware provides authentication for HTTP requests.
type AuthMiddleware struct {
	config AuthConfig
	logger *slog.Logger
}

// NewAuthMiddleware creates a new authentication middleware.
func NewAuthMiddleware(config AuthConfig, logger *slog.Logger) *AuthMiddleware {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthMiddleware{config: config, logger: logger}
}

// Handler wraps an http.Handler with authentication.
func (m *AuthMiddleware) Handler(next http.Handler) http.Handler {
	if !m.config.Enabled {
		return next // auth disabled, pass through
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for certain paths
		for _, p := range m.config.SkipPaths {
			if strings.HasPrefix(r.URL.Path, p) {
				next.ServeHTTP(w, r)
				return
			}
		}

		userID, err := m.authenticate(r)
		if err != nil {
			m.logger.Warn("authentication failed",
				slog.String("path", r.URL.Path),
				slog.String("remote", r.RemoteAddr),
				slog.String("error", err.Error()),
			)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{
				"error":   "unauthorized",
				"message": err.Error(),
			})
			return
		}

		ctx := context.WithValue(r.Context(), ContextKeyUserID, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *AuthMiddleware) authenticate(r *http.Request) (int64, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		// Also check X-API-Key header
		apiKey := r.Header.Get("X-API-Key")
		if apiKey != "" {
			return m.validateAPIKey(apiKey)
		}
		return 0, fmt.Errorf("missing authorization header")
	}

	if strings.HasPrefix(auth, "Bearer ") {
		token := strings.TrimPrefix(auth, "Bearer ")
		switch m.config.Mode {
		case "api_key", "both":
			if uid, err := m.validateAPIKey(token); err == nil {
				return uid, nil
			}
		}
		// JWT validation placeholder (implement with your JWT library)
		return 0, fmt.Errorf("invalid token")
	}

	return 0, fmt.Errorf("unsupported authorization scheme")
}

func (m *AuthMiddleware) validateAPIKey(key string) (int64, error) {
	// Constant-time comparison for security
	for storedKey, userID := range m.config.APIKeys {
		if subtle.ConstantTimeCompare([]byte(key), []byte(storedKey)) == 1 {
			return userID, nil
		}
	}
	return 0, fmt.Errorf("invalid api key")
}

// GenerateAPIKey generates a secure API key with HMAC.
func GenerateAPIKey(secret string, userID int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("user:%d:%d", userID, time.Now().UnixNano())))
	return hex.EncodeToString(mac.Sum(nil))
}

// ──────────────────────────────────────────────────────────────────────────────
//  Rate Limiter Middleware
// ──────────────────────────────────────────────────────────────────────────────

// RateLimitConfig holds rate limiting configuration.
type RateLimitConfig struct {
	Enabled         bool          `toml:"enabled"`
	GlobalRPS       float64       `toml:"global_rps"`   // global requests per second
	PerUserRPS      float64       `toml:"per_user_rps"` // per-user requests per second
	BurstSize       int           `toml:"burst_size"`   // burst allowance
	CleanupInterval time.Duration `toml:"cleanup_interval"`
}

// DefaultRateLimitConfig returns sensible defaults.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Enabled:         false,
		GlobalRPS:       100,
		PerUserRPS:      20,
		BurstSize:       50,
		CleanupInterval: 10 * time.Minute,
	}
}

// RateLimitMiddleware provides per-user and global rate limiting.
type RateLimitMiddleware struct {
	config RateLimitConfig
	global *rate.Limiter
	users  map[string]*rate.Limiter
	mu     sync.RWMutex
	logger *slog.Logger
}

// NewRateLimitMiddleware creates a new rate limiter.
func NewRateLimitMiddleware(config RateLimitConfig, logger *slog.Logger) *RateLimitMiddleware {
	if logger == nil {
		logger = slog.Default()
	}
	rl := &RateLimitMiddleware{
		config: config,
		global: rate.NewLimiter(rate.Limit(config.GlobalRPS), config.BurstSize),
		users:  make(map[string]*rate.Limiter),
		logger: logger,
	}
	// Background cleanup of stale user limiters
	go rl.cleanup()
	return rl
}

// Handler wraps an http.Handler with rate limiting.
func (rl *RateLimitMiddleware) Handler(next http.Handler) http.Handler {
	if !rl.config.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Global rate limit
		if !rl.global.Allow() {
			rl.logger.Warn("global rate limit exceeded", slog.String("remote", r.RemoteAddr))
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate_limit_exceeded","message":"too many requests"}`, http.StatusTooManyRequests)
			return
		}

		// Per-user rate limit (by authenticated user or IP)
		key := r.RemoteAddr
		if uid, ok := UserIDFromContext(r.Context()); ok {
			key = fmt.Sprintf("user:%d", uid)
		}

		limiter := rl.getUserLimiter(key)
		if !limiter.Allow() {
			rl.logger.Warn("per-user rate limit exceeded", slog.String("key", key))
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate_limit_exceeded","message":"too many requests for user"}`, http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimitMiddleware) getUserLimiter(key string) *rate.Limiter {
	rl.mu.RLock()
	limiter, exists := rl.users[key]
	rl.mu.RUnlock()
	if exists {
		return limiter
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()
	// Double check
	if limiter, exists = rl.users[key]; exists {
		return limiter
	}
	limiter = rate.NewLimiter(rate.Limit(rl.config.PerUserRPS), rl.config.BurstSize)
	rl.users[key] = limiter
	return limiter
}

func (rl *RateLimitMiddleware) cleanup() {
	ticker := time.NewTicker(rl.config.CleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		rl.users = make(map[string]*rate.Limiter)
		rl.mu.Unlock()
	}
}

// ──────────────────────────────────────────────────────────────────────────────
//  Audit Logger Middleware
// ──────────────────────────────────────────────────────────────────────────────

// AuditEvent represents an auditable operation.
type AuditEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	RequestID  string    `json:"request_id"`
	UserID     int64     `json:"user_id,omitempty"`
	Action     string    `json:"action"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	RemoteAddr string    `json:"remote_addr"`
	StatusCode int       `json:"status_code"`
	Duration   int64     `json:"duration_ms"`
	SessionID  string    `json:"session_id,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// AuditConfig holds audit logging configuration.
type AuditConfig struct {
	Enabled bool `toml:"enabled"`
}

// AuditMiddleware logs all API operations for compliance.
type AuditMiddleware struct {
	config AuditConfig
	logger *slog.Logger
}

// NewAuditMiddleware creates audit logging middleware.
func NewAuditMiddleware(config AuditConfig, logger *slog.Logger) *AuditMiddleware {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuditMiddleware{config: config, logger: logger}
}

// Handler wraps an http.Handler with audit logging.
func (m *AuditMiddleware) Handler(next http.Handler) http.Handler {
	if !m.config.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Generate request ID
		reqID := fmt.Sprintf("%d", time.Now().UnixNano())
		ctx := context.WithValue(r.Context(), ContextKeyRequestID, reqID)
		r = r.WithContext(ctx)

		// Wrap response writer to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		// Log audit event
		event := AuditEvent{
			Timestamp:  start,
			RequestID:  reqID,
			Action:     r.Method + " " + r.URL.Path,
			Method:     r.Method,
			Path:       r.URL.Path,
			RemoteAddr: r.RemoteAddr,
			StatusCode: rw.statusCode,
			Duration:   time.Since(start).Milliseconds(),
			SessionID:  r.Header.Get("Mcp-Session-Id"),
		}
		if uid, ok := UserIDFromContext(r.Context()); ok {
			event.UserID = uid
		}

		m.logger.Info("audit",
			slog.String("request_id", event.RequestID),
			slog.Int64("user_id", event.UserID),
			slog.String("action", event.Action),
			slog.String("remote", event.RemoteAddr),
			slog.Int("status", event.StatusCode),
			slog.Int64("duration_ms", event.Duration),
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// ──────────────────────────────────────────────────────────────────────────────
//  Timeout Middleware
// ──────────────────────────────────────────────────────────────────────────────

// TimeoutMiddleware adds request timeout.
func TimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, timeout, `{"error":"request_timeout","message":"request processing exceeded time limit"}`)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
//  Chain helper — compose middlewares
// ──────────────────────────────────────────────────────────────────────────────

// Chain composes multiple middleware into a single handler wrapper.
func Chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}
