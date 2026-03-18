package tests

import (
	"errors"
	"testing"

	"mcp_rag_server/rag"
)

func TestNewRAGError(t *testing.T) {
	err := rag.NewRAGError(rag.ErrCodeEmbeddingFailed, "test provider", nil)
	if err == nil {
		t.Fatal("Expected non-nil error")
	}
	if err.Code != rag.ErrCodeEmbeddingFailed {
		t.Errorf("Expected code %s, got %s", rag.ErrCodeEmbeddingFailed, err.Code)
	}
	if err.Detail != "test provider" {
		t.Errorf("Expected detail 'test provider', got '%s'", err.Detail)
	}

	errStr := err.Error()
	if errStr == "" {
		t.Error("Error string should not be empty")
	}
	t.Logf("Error: %s", errStr)

	if !containsStr(errStr, "RAG_002") {
		t.Error("Error string should contain error code")
	}
	if !containsStr(errStr, "test provider") {
		t.Error("Error string should contain detail")
	}
}

func TestNewRAGError_WithCause(t *testing.T) {
	cause := errors.New("connection refused")
	err := rag.NewRAGError(rag.ErrCodeSearchFailed, "redis search", cause)

	if err.Cause != cause {
		t.Error("Cause should be preserved")
	}

	unwrapped := errors.Unwrap(err)
	if unwrapped != cause {
		t.Error("Unwrap should return the cause")
	}

	errStr := err.Error()
	if !containsStr(errStr, "connection refused") {
		t.Error("Error string should contain cause")
	}
}

func TestNewRAGErrorf(t *testing.T) {
	err := rag.NewRAGErrorf(rag.ErrCodeContentTooLarge, nil, "size %d exceeds limit %d", 5000, 4000)
	if !containsStr(err.Detail, "5000") {
		t.Error("Detail should contain formatted values")
	}
}

func TestIsRAGError(t *testing.T) {
	ragErr := rag.NewRAGError(rag.ErrCodeIndexNotFound, "test", nil)

	extracted, ok := rag.IsRAGError(ragErr)
	if !ok {
		t.Error("Should recognize RAGError")
	}
	if extracted.Code != rag.ErrCodeIndexNotFound {
		t.Errorf("Expected code %s, got %s", rag.ErrCodeIndexNotFound, extracted.Code)
	}

	_, ok = rag.IsRAGError(errors.New("plain error"))
	if ok {
		t.Error("Should not recognize plain error as RAGError")
	}
}

func TestHasErrorCode(t *testing.T) {
	err := rag.NewRAGError(rag.ErrCodeCircuitOpen, "provider-1", nil)

	if !rag.HasErrorCode(err, rag.ErrCodeCircuitOpen) {
		t.Error("Should match correct code")
	}
	if rag.HasErrorCode(err, rag.ErrCodeEmbeddingFailed) {
		t.Error("Should not match wrong code")
	}
	if rag.HasErrorCode(errors.New("plain"), rag.ErrCodeCircuitOpen) {
		t.Error("Should not match plain error")
	}
}

func TestErrorCodeMessage(t *testing.T) {
	codes := []rag.ErrorCode{
		rag.ErrCodeIndexNotFound,
		rag.ErrCodeEmbeddingFailed,
		rag.ErrCodeInvalidInput,
		rag.ErrCodeNoProviders,
	}

	for _, code := range codes {
		msg := rag.ErrorCodeMessage(code)
		if msg == "" || msg == "Unknown error" {
			t.Errorf("Code %s should have a message", code)
		}
		t.Logf("%s -> %s", code, msg)
	}

	unknown := rag.ErrorCodeMessage("UNKNOWN_999")
	if unknown != "Unknown error" {
		t.Errorf("Unknown code should return 'Unknown error', got '%s'", unknown)
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
