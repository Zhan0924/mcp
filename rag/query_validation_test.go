package rag

import (
	"testing"
)

func TestIsValidQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		// 应该拦截的垃圾查询
		{"empty string", "", false},
		{"only spaces", "   ", false},
		{"single char", "a", false},
		{"unknown task", "Unknown task", false},
		{"unknown topic", "unknown topic", false},
		{"unknown question", "UNKNOWN QUESTION", false},
		{"compare and (double space)", "Compare  and ", false},
		{"compare   and", "compare   and", false},
		{"pure punctuation", "!!??...", false},
		{"pure symbols", "@#$%^&*()", false},
		{"tabs and newlines", "\t\n\r", false},

		// 应该放行的正常查询
		{"normal chinese", "什么是 goroutine", true},
		{"normal english", "how to use channels", true},
		{"short but valid", "Go", true},
		{"code query", "sync.Mutex 用法", true},
		{"compare valid", "Compare Go and Rust performance", true},
		{"single CJK char x2", "互斥", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidQuery(tt.query)
			if got != tt.want {
				t.Errorf("isValidQuery(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestEscapeRedisQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(string) bool // 验证输出满足条件
	}{
		{
			name:  "normal text stays readable",
			input: "hello world",
			check: func(s string) bool { return len(s) > 0 },
		},
		{
			name:  "code snippet with parens",
			input: "def handle_unknown_question(self):",
			check: func(s string) bool { return !containsUnescaped(s, "(") && !containsUnescaped(s, ")") },
		},
		{
			name:  "code with backslash",
			input: `class UnknownTaskError\nraise`,
			check: func(s string) bool { return !containsUnescaped(s, `\n`) },
		},
		{
			name:  "multiline HyDE output",
			input: "line1\nline2\nline3",
			check: func(s string) bool { return !containsRaw(s, "\n") },
		},
		{
			name:  "empty query protection",
			input: "",
			check: func(s string) bool { return s == "__empty_query__" },
		},
		{
			name:  "long HyDE output truncated",
			input: generateLongString(2000),
			check: func(s string) bool { return len(s) <= 600 }, // 500 + escape overhead
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeRedisQuery(tt.input)
			if !tt.check(result) {
				t.Errorf("escapeRedisQuery(%q) = %q, failed check", tt.input[:min(50, len(tt.input))], result[:min(100, len(result))])
			}
		})
	}
}

// containsUnescaped checks if char exists without a preceding backslash
func containsUnescaped(s, char string) bool {
	escaped := `\` + char
	// Remove escaped versions, then check for raw char
	cleaned := replaceAll(s, escaped, "")
	return containsRaw(cleaned, char)
}

func containsRaw(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func replaceAll(s, old, new string) string {
	result := s
	for {
		i := 0
		found := false
		for i <= len(result)-len(old) {
			if result[i:i+len(old)] == old {
				result = result[:i] + new + result[i+len(old):]
				found = true
				i += len(new)
			} else {
				i++
			}
		}
		if !found {
			break
		}
	}
	return result
}

func generateLongString(n int) string {
	s := make([]byte, n)
	for i := range s {
		s[i] = byte('a' + (i % 26))
	}
	return string(s)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
