package tools

import (
	"testing"
)

// =============================================================================
// parseDocumentURI Tests (internal - same package)
// =============================================================================

func TestParseDocumentURI_NewFormat(t *testing.T) {
	tests := []struct {
		name       string
		uri        string
		wantUserID uint
		wantFileID string
		wantErr    bool
	}{
		{
			name:       "valid new format",
			uri:        "rag://users/42/documents/my-file-001",
			wantUserID: 42,
			wantFileID: "my-file-001",
		},
		{
			name:       "user_id=1",
			uri:        "rag://users/1/documents/doc-abc",
			wantUserID: 1,
			wantFileID: "doc-abc",
		},
		{
			name:       "large user_id",
			uri:        "rag://users/999999/documents/big-doc",
			wantUserID: 999999,
			wantFileID: "big-doc",
		},
		{
			name:       "file_id with dashes and dots",
			uri:        "rag://users/5/documents/my.file-v2.3",
			wantUserID: 5,
			wantFileID: "my.file-v2.3",
		},
		{
			name:    "missing file_id",
			uri:     "rag://users/1/documents/",
			wantErr: true,
		},
		{
			name:    "missing user_id",
			uri:     "rag://users//documents/file1",
			wantErr: true,
		},
		{
			name:    "invalid user_id (non-numeric)",
			uri:     "rag://users/abc/documents/file1",
			wantErr: true,
		},
		{
			name:    "negative user_id string",
			uri:     "rag://users/-1/documents/file1",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			userID, fileID, err := parseDocumentURI(tc.uri)
			if tc.wantErr {
				if err == nil {
					t.Errorf("Expected error for URI: %s", tc.uri)
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if userID != tc.wantUserID {
				t.Errorf("Expected userID=%d, got %d", tc.wantUserID, userID)
			}
			if fileID != tc.wantFileID {
				t.Errorf("Expected fileID=%s, got %s", tc.wantFileID, fileID)
			}
		})
	}
}

func TestParseDocumentURI_BackwardCompatibility(t *testing.T) {
	tests := []struct {
		name       string
		uri        string
		wantUserID uint
		wantFileID string
		wantErr    bool
	}{
		{
			name:       "old format - defaults to userID=1",
			uri:        "rag://documents/old-file-001",
			wantUserID: 1,
			wantFileID: "old-file-001",
		},
		{
			name:       "old format with complex file_id",
			uri:        "rag://documents/my-complex.file_id-v3",
			wantUserID: 1,
			wantFileID: "my-complex.file_id-v3",
		},
		{
			name:    "old format empty file_id",
			uri:     "rag://documents/",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			userID, fileID, err := parseDocumentURI(tc.uri)
			if tc.wantErr {
				if err == nil {
					t.Errorf("Expected error for URI: %s", tc.uri)
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if userID != tc.wantUserID {
				t.Errorf("Expected userID=%d, got %d", tc.wantUserID, userID)
			}
			if fileID != tc.wantFileID {
				t.Errorf("Expected fileID=%s, got %s", tc.wantFileID, fileID)
			}
		})
	}
}

func TestParseDocumentURI_InvalidScheme(t *testing.T) {
	invalidURIs := []string{
		"http://example.com/documents/file1",
		"ftp://users/1/documents/file1",
		"rag://other/path",
		"",
		"rag://",
		"random-string",
	}

	for _, uri := range invalidURIs {
		_, _, err := parseDocumentURI(uri)
		if err == nil {
			t.Errorf("Expected error for invalid URI: '%s'", uri)
		}
	}
}

// =============================================================================
// splitAndTrim Tests
// =============================================================================

func TestSplitAndTrim_Basic(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{"  a , b , c  ", []string{"a", "b", "c"}},
		{"single", []string{"single"}},
		{"", nil},    // empty string returns nil
		{",,,", nil}, // all empty parts
		{"a,,b", []string{"a", "b"}},
		{"  ,  , a ,  ", []string{"a"}},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := splitAndTrim(tc.input)
			if len(result) != len(tc.expected) {
				t.Errorf("Input %q: expected %d items, got %d: %v", tc.input, len(tc.expected), len(result), result)
				return
			}
			for i, v := range result {
				if v != tc.expected[i] {
					t.Errorf("Input %q: item[%d] expected %q, got %q", tc.input, i, tc.expected[i], v)
				}
			}
		})
	}
}

func TestSplitAndTrim_FileIDsScenario(t *testing.T) {
	// Simulate the file_ids argument from RAG_QA prompt
	input := "doc-001, doc-002, doc-003"
	result := splitAndTrim(input)

	if len(result) != 3 {
		t.Fatalf("Expected 3 file IDs, got %d: %v", len(result), result)
	}

	expected := []string{"doc-001", "doc-002", "doc-003"}
	for i, v := range result {
		if v != expected[i] {
			t.Errorf("Expected %q, got %q", expected[i], v)
		}
	}
}

// =============================================================================
// parseUserID Tests
// =============================================================================

func TestParseUserID_Valid(t *testing.T) {
	tests := []struct {
		input    string
		expected uint
	}{
		{"1", 1},
		{"42", 42},
		{"999999", 999999},
		{"0", 0}, // valid parse, returns 0
	}

	for _, tc := range tests {
		result := parseUserID(tc.input)
		if result != tc.expected {
			t.Errorf("parseUserID(%q) = %d, want %d", tc.input, result, tc.expected)
		}
	}
}

func TestParseUserID_Invalid(t *testing.T) {
	// Invalid strings should default to 1
	tests := []struct {
		input    string
		expected uint
	}{
		{"", 1},
		{"abc", 1},
		{"-1", 1},
		{"3.14", 1},
	}

	for _, tc := range tests {
		result := parseUserID(tc.input)
		if result != tc.expected {
			t.Errorf("parseUserID(%q) = %d, want %d (default)", tc.input, result, tc.expected)
		}
	}
}
