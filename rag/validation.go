package rag

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ──────────────────────────────────────────────────────────────────────────────
//  Input Validation — P0 Security
// ──────────────────────────────────────────────────────────────────────────────

var (
	fileIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]{1,256}$`)
	urlRegex    = regexp.MustCompile(`^https?://[^\s]+$`)
)

// ValidationError represents an input validation failure.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s — %s", e.Field, e.Message)
}

// ValidateUserID checks that user_id is within valid range.
func ValidateUserID(id int64) error {
	if id <= 0 {
		return &ValidationError{Field: "user_id", Message: "must be positive"}
	}
	if id > 1<<32 {
		return &ValidationError{Field: "user_id", Message: "exceeds maximum value (4294967296)"}
	}
	return nil
}

// ValidateFileID checks that file_id is safe and within length bounds.
func ValidateFileID(id string) error {
	if id == "" {
		return &ValidationError{Field: "file_id", Message: "cannot be empty"}
	}
	if len(id) > 256 {
		return &ValidationError{Field: "file_id", Message: "exceeds maximum length (256)"}
	}
	if !fileIDRegex.MatchString(id) {
		return &ValidationError{Field: "file_id", Message: "contains invalid characters (allowed: a-z, A-Z, 0-9, _, -, .)"}
	}
	return nil
}

// ValidateQuery checks search query input.
func ValidateQuery(query string) error {
	if strings.TrimSpace(query) == "" {
		return &ValidationError{Field: "query", Message: "cannot be empty"}
	}
	if utf8.RuneCountInString(query) > 10000 {
		return &ValidationError{Field: "query", Message: "exceeds maximum length (10000 characters)"}
	}
	return nil
}

// ValidateContent checks document content input.
func ValidateContent(content string) error {
	if strings.TrimSpace(content) == "" {
		return &ValidationError{Field: "content", Message: "cannot be empty"}
	}
	if len(content) > 50*1024*1024 { // 50MB
		return &ValidationError{Field: "content", Message: "exceeds maximum size (50MB)"}
	}
	return nil
}

// ValidateTopK checks that top_k is within valid range.
func ValidateTopK(topK int) error {
	if topK < 1 {
		return &ValidationError{Field: "top_k", Message: "must be at least 1"}
	}
	if topK > 100 {
		return &ValidationError{Field: "top_k", Message: "exceeds maximum (100)"}
	}
	return nil
}

// ValidateURL checks that a URL is valid.
func ValidateURL(u string) error {
	if u == "" {
		return &ValidationError{Field: "url", Message: "cannot be empty"}
	}
	if !urlRegex.MatchString(u) {
		return &ValidationError{Field: "url", Message: "invalid URL format (must start with http:// or https://)"}
	}
	return nil
}

// ValidateChunkSize checks chunking parameters.
func ValidateChunkSize(maxSize, overlap int) error {
	if maxSize < 50 {
		return &ValidationError{Field: "max_chunk_size", Message: "must be at least 50"}
	}
	if maxSize > 100000 {
		return &ValidationError{Field: "max_chunk_size", Message: "exceeds maximum (100000)"}
	}
	if overlap < 0 {
		return &ValidationError{Field: "overlap_size", Message: "cannot be negative"}
	}
	if overlap >= maxSize {
		return &ValidationError{Field: "overlap_size", Message: "must be less than max_chunk_size"}
	}
	return nil
}

// ValidateFileName checks file name safety.
func ValidateFileName(name string) error {
	if name == "" {
		return nil // optional field
	}
	if len(name) > 512 {
		return &ValidationError{Field: "file_name", Message: "exceeds maximum length (512)"}
	}
	// Prevent path traversal
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return &ValidationError{Field: "file_name", Message: "contains forbidden characters (path traversal)"}
	}
	return nil
}

// ValidateSearchDepth checks graph search depth.
func ValidateSearchDepth(depth int) error {
	if depth < 1 {
		return &ValidationError{Field: "search_depth", Message: "must be at least 1"}
	}
	if depth > 5 {
		return &ValidationError{Field: "search_depth", Message: "exceeds maximum (5)"}
	}
	return nil
}
