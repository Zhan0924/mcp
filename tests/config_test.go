package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp_rag_server/rag"
)

func TestChunkingConfig_Defaults(t *testing.T) {
	cfg := rag.DefaultChunkingConfig()

	if cfg.MaxChunkSize != 1000 {
		t.Errorf("Expected MaxChunkSize=1000, got %d", cfg.MaxChunkSize)
	}
	if cfg.MinChunkSize != 100 {
		t.Errorf("Expected MinChunkSize=100, got %d", cfg.MinChunkSize)
	}
	if cfg.OverlapSize != 200 {
		t.Errorf("Expected OverlapSize=200, got %d", cfg.OverlapSize)
	}
	if !cfg.StructureAware {
		t.Error("Expected StructureAware=true")
	}
}

func TestRetrieverConfig_Defaults(t *testing.T) {
	cfg := rag.DefaultRetrieverConfig()

	if cfg.DefaultTopK != 5 {
		t.Errorf("Expected DefaultTopK=5, got %d", cfg.DefaultTopK)
	}
	if cfg.MaxTopK != 20 {
		t.Errorf("Expected MaxTopK=20, got %d", cfg.MaxTopK)
	}
	if cfg.IndexAlgorithm != "FLAT" {
		t.Errorf("Expected IndexAlgorithm=FLAT, got %s", cfg.IndexAlgorithm)
	}
	if cfg.EmbeddingBatchSize != 10 {
		t.Errorf("Expected EmbeddingBatchSize=10, got %d", cfg.EmbeddingBatchSize)
	}
	if cfg.VectorWeight != 0.7 {
		t.Errorf("Expected VectorWeight=0.7, got %f", cfg.VectorWeight)
	}
}

func TestManagerConfig_Defaults(t *testing.T) {
	cfg := rag.DefaultManagerConfig()

	if cfg.Strategy != rag.LoadBalancePriority {
		t.Errorf("Expected strategy priority, got %s", cfg.Strategy)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("Expected MaxRetries=3, got %d", cfg.MaxRetries)
	}
	if cfg.CircuitThreshold != 5 {
		t.Errorf("Expected CircuitThreshold=5, got %d", cfg.CircuitThreshold)
	}
}

func TestCacheConfig_Defaults(t *testing.T) {
	cfg := rag.DefaultCacheConfig()

	if !cfg.Enabled {
		t.Error("Expected Enabled=true")
	}
	if cfg.LocalMaxSize != 10000 {
		t.Errorf("Expected LocalMaxSize=10000, got %d", cfg.LocalMaxSize)
	}
	if !cfg.DeduplicateOn {
		t.Error("Expected DeduplicateOn=true")
	}
}

func TestRerankConfig_Defaults(t *testing.T) {
	cfg := rag.DefaultRerankConfig()

	if cfg.Enabled {
		t.Error("Expected Enabled=false by default")
	}
	if cfg.TopN != 5 {
		t.Errorf("Expected TopN=5, got %d", cfg.TopN)
	}
	if cfg.RecallTopK != 20 {
		t.Errorf("Expected RecallTopK=20, got %d", cfg.RecallTopK)
	}
}

func TestHNSWParams_Defaults(t *testing.T) {
	params := rag.DefaultHNSWParams()

	if params.M != 16 {
		t.Errorf("Expected M=16, got %d", params.M)
	}
	if params.EFConstruction != 200 {
		t.Errorf("Expected EFConstruction=200, got %d", params.EFConstruction)
	}
	if params.EFRuntime != 10 {
		t.Errorf("Expected EFRuntime=10, got %d", params.EFRuntime)
	}
}

func TestRAGError_AllCodes(t *testing.T) {
	codes := []rag.ErrorCode{
		rag.ErrCodeIndexNotFound,
		rag.ErrCodeEmbeddingFailed,
		rag.ErrCodeIndexCreateFailed,
		rag.ErrCodeSearchFailed,
		rag.ErrCodeInvalidInput,
		rag.ErrCodeContentTooLarge,
		rag.ErrCodeNoProviders,
		rag.ErrCodeProviderTimeout,
		rag.ErrCodeCircuitOpen,
		rag.ErrCodeRerankFailed,
		rag.ErrCodeParseFailed,
		rag.ErrCodeCacheFailed,
		rag.ErrCodeConfigInvalid,
		rag.ErrCodeDocumentNotFound,
		rag.ErrCodeBatchFailed,
		rag.ErrCodeHybridMergeFailed,
		rag.ErrCodeUnsupportedFormat,
		rag.ErrCodeManagerNotReady,
	}

	for _, code := range codes {
		msg := rag.ErrorCodeMessage(code)
		if msg == "" || msg == "Unknown error" {
			t.Errorf("Code %s has no registered message", code)
		}

		err := rag.NewRAGError(code, "test", nil)
		errStr := err.Error()
		if !strings.Contains(errStr, string(code)) {
			t.Errorf("Error for code %s should contain code in string", code)
		}
	}
}

func TestEffectiveRecallTopK(t *testing.T) {
	// Rerank disabled
	cfg := rag.DefaultRerankConfig()
	cfg.Enabled = false
	got := rag.GetEffectiveRecallTopK(cfg, 5)
	if got != 5 {
		t.Errorf("Expected 5 when rerank disabled, got %d", got)
	}

	// Rerank enabled with recall_top_k
	cfg.Enabled = true
	cfg.RecallTopK = 20
	got = rag.GetEffectiveRecallTopK(cfg, 5)
	if got != 20 {
		t.Errorf("Expected 20 when rerank enabled, got %d", got)
	}

	// Rerank enabled without explicit recall_top_k
	cfg.RecallTopK = 0
	got = rag.GetEffectiveRecallTopK(cfg, 5)
	if got != 20 { // 5 * 4
		t.Errorf("Expected 20 (5*4) when no recall_top_k, got %d", got)
	}
}

func TestConfigToml_Exists(t *testing.T) {
	path := filepath.Join("..", "config.toml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("config.toml not found")
	}
}
