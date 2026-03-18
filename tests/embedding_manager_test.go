package tests

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mcp_rag_server/rag"

	"github.com/cloudwego/eino/components/embedding"
)

// --- Mock Embedder (implements embedding.Embedder) ---

type mockEmbedder struct {
	dim        int
	shouldFail bool
	failCount  int64
	callCount  int64
	delay      time.Duration
	mu         sync.Mutex
}

func newMockEmbedder(dim int) *mockEmbedder {
	return &mockEmbedder{dim: dim}
}

func (m *mockEmbedder) EmbedStrings(ctx context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	atomic.AddInt64(&m.callCount, 1)

	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.delay):
		}
	}

	m.mu.Lock()
	shouldFail := m.shouldFail
	if shouldFail {
		m.failCount++
	}
	m.mu.Unlock()

	if shouldFail {
		return nil, errors.New("mock embedding failed")
	}

	result := make([][]float64, len(texts))
	for i := range texts {
		vec := make([]float64, m.dim)
		for j := range vec {
			vec[j] = float64(i+1) * 0.01
		}
		result[i] = vec
	}
	return result, nil
}

func (m *mockEmbedder) setFail(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shouldFail = fail
}

func (m *mockEmbedder) getCallCount() int64 {
	return atomic.LoadInt64(&m.callCount)
}

// --- Tests ---

func TestManager_BasicEmbedding(t *testing.T) {
	cfg := rag.DefaultManagerConfig()
	cfg.Strategy = rag.LoadBalancePriority
	manager := rag.NewManager(cfg)

	mock := newMockEmbedder(128)

	rag.RegisterFactory("mock", func(ctx context.Context, config rag.ProviderConfig) (embedding.Embedder, error) {
		return mock, nil
	})

	ctx := context.Background()
	providerCfg := rag.ProviderConfig{
		Name:      "test-mock",
		Type:      "mock",
		Dimension: 128,
		Priority:  1,
		Weight:    100,
		Timeout:   5 * time.Second,
		Enabled:   true,
	}

	if err := manager.AddProvider(ctx, providerCfg); err != nil {
		t.Fatalf("AddProvider failed: %v", err)
	}
	manager.Start()
	defer manager.Stop()

	vectors, err := manager.EmbedStrings(ctx, []string{"hello", "world"})
	if err != nil {
		t.Fatalf("EmbedStrings failed: %v", err)
	}
	if len(vectors) != 2 {
		t.Errorf("Expected 2 vectors, got %d", len(vectors))
	}
	if len(vectors[0]) != 128 {
		t.Errorf("Expected dimension 128, got %d", len(vectors[0]))
	}

	stats := manager.GetStats()
	if len(stats) != 1 {
		t.Errorf("Expected 1 provider stats, got %d", len(stats))
	}
	if stats[0].TotalRequests != 1 {
		t.Errorf("Expected 1 total request, got %d", stats[0].TotalRequests)
	}
}

func TestManager_CircuitBreaker(t *testing.T) {
	cfg := rag.DefaultManagerConfig()
	cfg.CircuitThreshold = 3
	cfg.CircuitTimeout = 100 * time.Millisecond
	cfg.MaxRetries = 0
	manager := rag.NewManager(cfg)

	mock := newMockEmbedder(128)
	mock.setFail(true)

	rag.RegisterFactory("mock", func(ctx context.Context, config rag.ProviderConfig) (embedding.Embedder, error) {
		return mock, nil
	})

	ctx := context.Background()
	providerCfg := rag.ProviderConfig{
		Name:      "breaker-test",
		Type:      "mock",
		Dimension: 128,
		Priority:  1,
		Weight:    100,
		Timeout:   5 * time.Second,
		Enabled:   true,
	}
	if err := manager.AddProvider(ctx, providerCfg); err != nil {
		t.Fatalf("AddProvider failed: %v", err)
	}
	manager.Start()
	defer manager.Stop()

	for i := 0; i < cfg.CircuitThreshold+1; i++ {
		_, _ = manager.EmbedStrings(ctx, []string{"test"})
	}

	stats := manager.GetStats()
	if len(stats) == 0 {
		t.Fatal("Expected stats")
	}
	t.Logf("Circuit state: %s, failed: %d", stats[0].CircuitState, stats[0].FailedRequests)

	if stats[0].CircuitState != rag.CircuitStateOpen {
		t.Errorf("Expected circuit open, got %s", stats[0].CircuitState)
	}

	time.Sleep(200 * time.Millisecond)

	mock.setFail(false)
	vectors, err := manager.EmbedStrings(ctx, []string{"recovery"})
	if err != nil {
		t.Logf("Recovery attempt: %v (may need more time for half-open)", err)
	} else {
		t.Logf("Recovery succeeded: got %d vectors", len(vectors))
	}
}

func TestManager_LoadBalance_RoundRobin(t *testing.T) {
	cfg := rag.DefaultManagerConfig()
	cfg.Strategy = rag.LoadBalanceRoundRobin
	cfg.MaxRetries = 0
	manager := rag.NewManager(cfg)

	mock1 := newMockEmbedder(128)
	mock2 := newMockEmbedder(128)
	callIdx := int64(0)

	rag.RegisterFactory("mock_rr", func(ctx context.Context, config rag.ProviderConfig) (embedding.Embedder, error) {
		idx := atomic.AddInt64(&callIdx, 1)
		if idx == 1 {
			return mock1, nil
		}
		return mock2, nil
	})

	ctx := context.Background()
	for i, name := range []string{"provider-a", "provider-b"} {
		err := manager.AddProvider(ctx, rag.ProviderConfig{
			Name:      name,
			Type:      "mock_rr",
			Dimension: 128,
			Priority:  i + 1,
			Weight:    100,
			Timeout:   5 * time.Second,
			Enabled:   true,
		})
		if err != nil {
			t.Fatalf("AddProvider %s failed: %v", name, err)
		}
	}
	manager.Start()
	defer manager.Stop()

	for i := 0; i < 4; i++ {
		_, err := manager.EmbedStrings(ctx, []string{"test"})
		if err != nil {
			t.Fatalf("Request %d failed: %v", i, err)
		}
	}

	c1 := mock1.getCallCount()
	c2 := mock2.getCallCount()
	t.Logf("Provider A calls: %d, Provider B calls: %d", c1, c2)

	if c1 == 0 || c2 == 0 {
		t.Error("Both providers should have been called in round-robin")
	}
}

func TestManager_ResetStats(t *testing.T) {
	cfg := rag.DefaultManagerConfig()
	manager := rag.NewManager(cfg)

	rag.RegisterFactory("mock_reset", func(ctx context.Context, config rag.ProviderConfig) (embedding.Embedder, error) {
		return newMockEmbedder(config.Dimension), nil
	})

	ctx := context.Background()
	manager.AddProvider(ctx, rag.ProviderConfig{
		Name: "reset-test", Type: "mock_reset", Dimension: 128,
		Priority: 1, Weight: 100, Timeout: 5 * time.Second, Enabled: true,
	})
	manager.Start()
	defer manager.Stop()

	manager.EmbedStrings(ctx, []string{"test"})
	stats := manager.GetStats()
	if stats[0].TotalRequests != 1 {
		t.Error("Expected 1 request")
	}

	manager.ResetStats()
	stats = manager.GetStats()
	if stats[0].TotalRequests != 0 {
		t.Errorf("Expected 0 requests after reset, got %d", stats[0].TotalRequests)
	}
}

func TestManager_NoProviders(t *testing.T) {
	cfg := rag.DefaultManagerConfig()
	manager := rag.NewManager(cfg)
	manager.Start()
	defer manager.Stop()

	_, err := manager.EmbedStrings(context.Background(), []string{"test"})
	if err == nil {
		t.Error("Expected error with no providers")
	}
}
