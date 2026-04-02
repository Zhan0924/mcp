package rag

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// isRecoverableError 判断是否为可恢复的业务错误（不应计入熔断）
// 这些错误是业务层面的"找不到"或"参数错误"，不代表后端不可用
func isRecoverableError(err error) bool {
	msg := err.Error()
	recoverable := []string{
		"collection not found",
		"not found",
		"not exist",
		"already exists",
		"invalid parameter",
	}
	for _, r := range recoverable {
		if strings.Contains(strings.ToLower(msg), r) {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────────────────────
//  Circuit Breaker for VectorStore — P0 Reliability
//  Reuses CircuitState from embedding_manager.go (CircuitStateClosed/Open/HalfOpen)
// ──────────────────────────────────────────────────────────────────────────────

// StoreCircuitBreakerConfig configures the circuit breaker behavior.
type StoreCircuitBreakerConfig struct {
	FailureThreshold int           `toml:"failure_threshold"` // failures before opening
	SuccessThreshold int           `toml:"success_threshold"` // successes before closing
	Timeout          time.Duration `toml:"timeout"`           // time to wait before half-open
}

// DefaultStoreCircuitBreakerConfig returns sensible defaults.
func DefaultStoreCircuitBreakerConfig() StoreCircuitBreakerConfig {
	return StoreCircuitBreakerConfig{
		FailureThreshold: 10, // 从 5 提升到 10，避免高并发下误触发
		SuccessThreshold: 3,
		Timeout:          30 * time.Second,
	}
}

// StoreCircuitBreaker wraps a VectorStore with circuit breaker protection.
type StoreCircuitBreaker struct {
	inner  VectorStore
	config StoreCircuitBreakerConfig
	logger *slog.Logger

	mu                sync.RWMutex
	state             CircuitState
	consecutiveFails  int
	consecutiveSucc   int
	lastFailTime      time.Time
	totalRequests     int64
	totalFailures     int64
	totalCircuitOpens int64
}

// Compile-time interface check
var _ VectorStore = (*StoreCircuitBreaker)(nil)

// NewStoreCircuitBreaker wraps a VectorStore with circuit breaker protection.
func NewStoreCircuitBreaker(inner VectorStore, config StoreCircuitBreakerConfig, logger *slog.Logger) *StoreCircuitBreaker {
	if logger == nil {
		logger = slog.Default()
	}
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = 5
	}
	if config.SuccessThreshold <= 0 {
		config.SuccessThreshold = 3
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	return &StoreCircuitBreaker{
		inner:  inner,
		config: config,
		logger: logger,
		state:  CircuitStateClosed,
	}
}

func (cb *StoreCircuitBreaker) allow() error {
	cb.mu.RLock()
	state := cb.state
	lastFail := cb.lastFailTime
	cb.mu.RUnlock()

	switch state {
	case CircuitStateClosed:
		return nil
	case CircuitStateOpen:
		if time.Since(lastFail) > cb.config.Timeout {
			cb.mu.Lock()
			if cb.state == CircuitStateOpen {
				cb.state = CircuitStateHalfOpen
				cb.consecutiveSucc = 0
				cb.logger.Info("store circuit breaker: transitioning to half-open")
			}
			cb.mu.Unlock()
			return nil
		}
		return fmt.Errorf("circuit breaker is open (vector store unavailable, retry after %v)", cb.config.Timeout-time.Since(lastFail))
	case CircuitStateHalfOpen:
		return nil
	}
	return nil
}

func (cb *StoreCircuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.totalRequests++
	cb.consecutiveFails = 0
	cb.consecutiveSucc++
	if cb.state == CircuitStateHalfOpen && cb.consecutiveSucc >= cb.config.SuccessThreshold {
		cb.state = CircuitStateClosed
		cb.logger.Info("store circuit breaker: recovered, closing circuit")
	}
}

func (cb *StoreCircuitBreaker) recordFailure(err error) {
	// 可恢复的业务错误（如 collection not found）不计入熔断
	if isRecoverableError(err) {
		cb.mu.Lock()
		cb.totalRequests++
		cb.mu.Unlock()
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.totalRequests++
	cb.totalFailures++
	cb.consecutiveFails++
	cb.consecutiveSucc = 0
	cb.lastFailTime = time.Now()

	if cb.state == CircuitStateHalfOpen {
		cb.state = CircuitStateOpen
		cb.totalCircuitOpens++
		cb.logger.Warn("store circuit breaker: half-open probe failed, reopening", slog.String("error", err.Error()))
		return
	}
	if cb.state == CircuitStateClosed && cb.consecutiveFails >= cb.config.FailureThreshold {
		cb.state = CircuitStateOpen
		cb.totalCircuitOpens++
		cb.logger.Error("store circuit breaker: opened", slog.Int("failures", cb.consecutiveFails), slog.String("error", err.Error()))
	}
}

// CBState returns the current circuit breaker state.
func (cb *StoreCircuitBreaker) CBState() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// CBStats returns circuit breaker statistics.
func (cb *StoreCircuitBreaker) CBStats() map[string]interface{} {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return map[string]interface{}{
		"state":               string(cb.state),
		"consecutive_fails":   cb.consecutiveFails,
		"consecutive_success": cb.consecutiveSucc,
		"total_requests":      cb.totalRequests,
		"total_failures":      cb.totalFailures,
		"total_circuit_opens": cb.totalCircuitOpens,
	}
}

// ── VectorStore interface delegation with circuit breaker ────────────────────

func (cb *StoreCircuitBreaker) EnsureIndex(ctx context.Context, config IndexConfig) error {
	if err := cb.allow(); err != nil {
		return err
	}
	err := cb.inner.EnsureIndex(ctx, config)
	if err != nil {
		cb.recordFailure(err)
		return err
	}
	cb.recordSuccess()
	return nil
}

func (cb *StoreCircuitBreaker) UpsertVectors(ctx context.Context, entries []VectorEntry) (int, error) {
	if err := cb.allow(); err != nil {
		return 0, err
	}
	n, err := cb.inner.UpsertVectors(ctx, entries)
	if err != nil {
		cb.recordFailure(err)
		return 0, err
	}
	cb.recordSuccess()
	return n, nil
}

func (cb *StoreCircuitBreaker) SearchVectors(ctx context.Context, query VectorQuery) ([]VectorSearchResult, error) {
	if err := cb.allow(); err != nil {
		return nil, err
	}
	result, err := cb.inner.SearchVectors(ctx, query)
	if err != nil {
		cb.recordFailure(err)
		return nil, err
	}
	cb.recordSuccess()
	return result, nil
}

func (cb *StoreCircuitBreaker) HybridSearch(ctx context.Context, query HybridQuery) ([]VectorSearchResult, error) {
	if err := cb.allow(); err != nil {
		return nil, err
	}
	result, err := cb.inner.HybridSearch(ctx, query)
	if err != nil {
		cb.recordFailure(err)
		return nil, err
	}
	cb.recordSuccess()
	return result, nil
}

func (cb *StoreCircuitBreaker) DeleteByFileID(ctx context.Context, indexName, prefix, fileID string) (int64, error) {
	if err := cb.allow(); err != nil {
		return 0, err
	}
	n, err := cb.inner.DeleteByFileID(ctx, indexName, prefix, fileID)
	if err != nil {
		cb.recordFailure(err)
		return 0, err
	}
	cb.recordSuccess()
	return n, nil
}

func (cb *StoreCircuitBreaker) GetDocumentChunks(ctx context.Context, indexName, prefix, fileID string) ([]string, error) {
	if err := cb.allow(); err != nil {
		return nil, err
	}
	result, err := cb.inner.GetDocumentChunks(ctx, indexName, prefix, fileID)
	if err != nil {
		cb.recordFailure(err)
		return nil, err
	}
	cb.recordSuccess()
	return result, nil
}

func (cb *StoreCircuitBreaker) ListDocuments(ctx context.Context, indexName string) ([]DocumentMeta, error) {
	if err := cb.allow(); err != nil {
		return nil, err
	}
	result, err := cb.inner.ListDocuments(ctx, indexName)
	if err != nil {
		cb.recordFailure(err)
		return nil, err
	}
	cb.recordSuccess()
	return result, nil
}

func (cb *StoreCircuitBreaker) Close() error {
	return cb.inner.Close()
}
