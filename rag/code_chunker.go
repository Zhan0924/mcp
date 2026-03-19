/*
┌─────────────────────────────────────────────────────────────────────────────┐
│                code_chunker.go — 代码感知分块器                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  核心思想: 以函数/类/方法为单位切分代码文件，保持代码语义完整性。             │
│  基于正则模式匹配（非 AST），零外部依赖，支持主流编程语言。                  │
│                                                                             │
│  支持语言: Go / Python / JavaScript/TypeScript / Java / Rust / C/C++        │
│                                                                             │
│  分块策略:                                                                   │
│    1. 按语言特征正则识别函数/类/方法的定义行                                  │
│    2. 通过缩进/花括号匹配找到函数体结束位置                                  │
│    3. 每个函数/类作为一个 chunk，包含其文档注释                              │
│    4. 超大函数二次切割                                                       │
│                                                                             │
│  导出函数:                                                                   │
│    DetectCodeLanguage(content, fileName) string                             │
│    CodeChunking(content, language, maxChunkSize) []Chunk                    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// languagePattern 语言特定的函数/类定义模式
type languagePattern struct {
	Language    string
	Extensions  []string
	FuncPattern *regexp.Regexp // 函数定义正则
	ClassPattern *regexp.Regexp // 类/结构体定义正则
	UseBraces   bool           // 是否使用花括号定界
}

// 预编译各语言的正则模式
var codePatterns = []languagePattern{
	{
		Language:    "go",
		Extensions:  []string{".go"},
		FuncPattern: regexp.MustCompile(`(?m)^func\s+(\([^)]*\)\s+)?[A-Za-z_]\w*\s*\(`),
		ClassPattern: regexp.MustCompile(`(?m)^type\s+[A-Z]\w*\s+(struct|interface)\s*\{`),
		UseBraces:   true,
	},
	{
		Language:    "python",
		Extensions:  []string{".py"},
		FuncPattern: regexp.MustCompile(`(?m)^(\s*)def\s+\w+\s*\(`),
		ClassPattern: regexp.MustCompile(`(?m)^(\s*)class\s+\w+`),
		UseBraces:   false, // Python 用缩进
	},
	{
		Language:    "javascript",
		Extensions:  []string{".js", ".ts", ".jsx", ".tsx"},
		FuncPattern: regexp.MustCompile(`(?m)^(export\s+)?(async\s+)?function\s+\w+|^(export\s+)?(const|let|var)\s+\w+\s*=\s*(async\s+)?\(`),
		ClassPattern: regexp.MustCompile(`(?m)^(export\s+)?class\s+\w+`),
		UseBraces:   true,
	},
	{
		Language:    "java",
		Extensions:  []string{".java"},
		FuncPattern: regexp.MustCompile(`(?m)^\s*(public|private|protected|static|\s)+[\w<>\[\]]+\s+\w+\s*\(`),
		ClassPattern: regexp.MustCompile(`(?m)^(public\s+|private\s+|protected\s+)?(abstract\s+|final\s+)?class\s+\w+`),
		UseBraces:   true,
	},
	{
		Language:    "rust",
		Extensions:  []string{".rs"},
		FuncPattern: regexp.MustCompile(`(?m)^(\s*)(pub(\(.*\))?\s+)?(async\s+)?fn\s+\w+`),
		ClassPattern: regexp.MustCompile(`(?m)^(\s*)(pub(\(.*\))?\s+)?(struct|enum|trait|impl)\s+\w+`),
		UseBraces:   true,
	},
	{
		Language:    "cpp",
		Extensions:  []string{".c", ".cpp", ".cc", ".h", ".hpp"},
		FuncPattern: regexp.MustCompile(`(?m)^[\w:*&<>\s]+\s+\w+\s*\([^;]*\)\s*\{`),
		ClassPattern: regexp.MustCompile(`(?m)^(class|struct)\s+\w+`),
		UseBraces:   true,
	},
}

// DetectCodeLanguage 从文件名或内容特征检测编程语言
func DetectCodeLanguage(content, fileName string) string {
	// 优先从文件扩展名判断
	if fileName != "" {
		lowerName := strings.ToLower(fileName)
		for _, p := range codePatterns {
			for _, ext := range p.Extensions {
				if strings.HasSuffix(lowerName, ext) {
					return p.Language
				}
			}
		}
	}

	// 从内容特征检测
	if strings.Contains(content, "package ") && strings.Contains(content, "func ") {
		return "go"
	}
	if strings.Contains(content, "def ") && strings.Contains(content, "import ") {
		return "python"
	}
	if strings.Contains(content, "function ") || strings.Contains(content, "const ") ||
		strings.Contains(content, "export ") {
		return "javascript"
	}

	return ""
}

// CodeChunking 代码感知分块
// 按函数/类边界切分，保持代码语义完整性
func CodeChunking(content, language string, maxChunkSize int) []Chunk {
	if maxChunkSize <= 0 {
		maxChunkSize = 1500
	}

	// 查找对应语言的模式
	var pattern *languagePattern
	for i := range codePatterns {
		if codePatterns[i].Language == language {
			pattern = &codePatterns[i]
			break
		}
	}

	// 未知语言降级为通用分块
	if pattern == nil {
		logrus.Debugf("[CodeChunker] Unknown language %s, falling back to generic chunking", language)
		return ChunkDocument(content, ChunkingConfig{
			MaxChunkSize: maxChunkSize,
			MinChunkSize: 50,
			OverlapSize:  100,
		})
	}

	// 找到所有函数/类定义的位置
	definitions := findCodeDefinitions(content, pattern)

	if len(definitions) == 0 {
		return ChunkDocument(content, ChunkingConfig{
			MaxChunkSize: maxChunkSize,
			MinChunkSize: 50,
			OverlapSize:  100,
		})
	}

	var chunks []Chunk
	chunkIndex := 0

	// 处理第一个定义之前的内容（imports, package 等）
	if definitions[0].startPos > 0 {
		preamble := strings.TrimSpace(content[:definitions[0].startPos])
		if preamble != "" {
			chunks = append(chunks, Chunk{
				ChunkID:    uuid.New().String(),
				Content:    preamble,
				ChunkIndex: chunkIndex,
				StartPos:   0,
				EndPos:     definitions[0].startPos,
				TokenCount: estimateTokenCount(preamble),
			})
			chunkIndex++
		}
	}

	// 每个定义作为一个 chunk
	for _, def := range definitions {
		text := def.content

		// 包含定义前的文档注释
		if def.docComment != "" {
			text = def.docComment + "\n" + text
		}

		if utf8.RuneCountInString(text) > maxChunkSize {
			// 超大函数二次切割
			subChunks := ChunkDocument(text, ChunkingConfig{
				MaxChunkSize: maxChunkSize,
				MinChunkSize: 50,
				OverlapSize:  100,
			})
			for _, sc := range subChunks {
				sc.ChunkIndex = chunkIndex
				chunks = append(chunks, sc)
				chunkIndex++
			}
		} else {
			chunks = append(chunks, Chunk{
				ChunkID:    uuid.New().String(),
				Content:    text,
				ChunkIndex: chunkIndex,
				StartPos:   def.startPos,
				EndPos:     def.endPos,
				TokenCount: estimateTokenCount(text),
			})
			chunkIndex++
		}
	}

	logrus.Infof("[CodeChunker] Split %s code into %d chunks (%d definitions found)",
		language, len(chunks), len(definitions))
	return chunks
}

// codeDefinition 代码定义的位置信息
type codeDefinition struct {
	content    string
	docComment string
	startPos   int
	endPos     int
	defType    string // "func" or "class"
}

// findCodeDefinitions 查找所有函数/类定义
func findCodeDefinitions(content string, pattern *languagePattern) []codeDefinition {
	lines := strings.Split(content, "\n")
	var defs []codeDefinition

	i := 0
	for i < len(lines) {
		line := lines[i]

		// 检查是否匹配函数或类定义
		isFunc := pattern.FuncPattern.MatchString(line)
		isClass := pattern.ClassPattern != nil && pattern.ClassPattern.MatchString(line)

		if !isFunc && !isClass {
			i++
			continue
		}

		defType := "func"
		if isClass {
			defType = "class"
		}

		// 向上回溯收集文档注释
		docComment := collectDocComment(lines, i)

		// 向下找到定义体结束位置
		startLine := i
		endLine := i

		if pattern.UseBraces {
			endLine = findBraceEnd(lines, i)
		} else {
			// Python: 用缩进判断
			endLine = findIndentEnd(lines, i)
		}

		// 提取定义内容
		defLines := lines[startLine : endLine+1]
		defContent := strings.Join(defLines, "\n")

		// 计算字符位置
		startPos := posOfLineInContent(content, startLine)
		endPos := posOfLineInContent(content, endLine+1)

		defs = append(defs, codeDefinition{
			content:    defContent,
			docComment: docComment,
			startPos:   startPos,
			endPos:     endPos,
			defType:    defType,
		})

		i = endLine + 1
	}

	return defs
}

// collectDocComment 向上收集文档注释
func collectDocComment(lines []string, defLine int) string {
	var commentLines []string
	for i := defLine - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") ||
			strings.HasPrefix(trimmed, "*/") || strings.HasPrefix(trimmed, "\"\"\"") ||
			strings.HasPrefix(trimmed, "///") {
			commentLines = append([]string{lines[i]}, commentLines...)
		} else if trimmed == "" {
			// 空行继续回溯
			continue
		} else {
			break
		}
	}
	return strings.Join(commentLines, "\n")
}

// findBraceEnd 使用花括号匹配找到代码块结束行
func findBraceEnd(lines []string, startLine int) int {
	depth := 0
	started := false

	for i := startLine; i < len(lines); i++ {
		for _, c := range lines[i] {
			if c == '{' {
				depth++
				started = true
			} else if c == '}' {
				depth--
				if started && depth == 0 {
					return i
				}
			}
		}
	}

	// 未找到匹配的闭合花括号，返回下一个函数定义前一行或文件末尾
	return len(lines) - 1
}

// findIndentEnd 使用缩进判断 Python 代码块结束行
func findIndentEnd(lines []string, startLine int) int {
	if startLine >= len(lines) {
		return startLine
	}

	// 获取定义行的缩进级别
	defIndent := countLeadingSpaces(lines[startLine])

	for i := startLine + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue // 跳过空行
		}
		currentIndent := countLeadingSpaces(lines[i])
		if currentIndent <= defIndent {
			return i - 1
		}
	}

	return len(lines) - 1
}

// countLeadingSpaces 计算行首空格数
func countLeadingSpaces(line string) int {
	count := 0
	for _, c := range line {
		if c == ' ' {
			count++
		} else if c == '\t' {
			count += 4
		} else {
			break
		}
	}
	return count
}

// posOfLineInContent 计算第 N 行在原始内容中的字符偏移
func posOfLineInContent(content string, lineIndex int) int {
	pos := 0
	currentLine := 0
	for i, c := range content {
		if currentLine == lineIndex {
			return i
		}
		if c == '\n' {
			currentLine++
			pos = i + 1
		}
	}
	if currentLine == lineIndex {
		return pos
	}
	return len(content)
}
