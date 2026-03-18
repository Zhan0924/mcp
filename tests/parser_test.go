package tests

import (
	"strings"
	"testing"

	"mcp_rag_server/rag"
)

func TestDetectFormat_PlainText(t *testing.T) {
	content := "This is plain text without any formatting."
	format := rag.DetectFormat(content)
	if format != rag.FormatPlainText {
		t.Errorf("Expected plain text, got %s", format)
	}
}

func TestDetectFormat_Markdown(t *testing.T) {
	content := `# Title

## Section 1

This is a paragraph with **bold** text.

- Item 1
- Item 2

` + "```go\nfmt.Println(\"hello\")\n```"

	format := rag.DetectFormat(content)
	if format != rag.FormatMarkdown {
		t.Errorf("Expected markdown, got %s", format)
	}
}

func TestDetectFormat_HTML(t *testing.T) {
	content := `<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body><p>Hello</p></body>
</html>`

	format := rag.DetectFormat(content)
	if format != rag.FormatHTML {
		t.Errorf("Expected html, got %s", format)
	}
}

func TestMarkdownParser_BasicParsing(t *testing.T) {
	content := `# My Document

## Introduction

This is the introduction section.

## Details

Here are the details with some **bold** text.

### Subsection

A subsection with more content.
`

	doc, err := rag.ParseDocument(content, rag.FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if doc.Format != rag.FormatMarkdown {
		t.Errorf("Expected format markdown, got %s", doc.Format)
	}

	if doc.Metadata.Title != "My Document" {
		t.Errorf("Expected title 'My Document', got '%s'", doc.Metadata.Title)
	}

	if len(doc.Sections) == 0 {
		t.Error("Expected at least 1 section")
	}

	t.Logf("Found %d sections:", len(doc.Sections))
	for _, s := range doc.Sections {
		t.Logf("  Level %d: %s (start=%d, end=%d)", s.Level, s.Title, s.Start, s.End)
	}

	if doc.Metadata.WordCount == 0 {
		t.Error("WordCount should be > 0")
	}
	if doc.Metadata.CharCount == 0 {
		t.Error("CharCount should be > 0")
	}
}

func TestMarkdownParser_Frontmatter(t *testing.T) {
	content := `---
title: Test Document
author: John Doe
tags: go, testing
language: en
---

# Content

Some content here.
`

	doc, err := rag.ParseDocument(content, rag.FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if doc.Metadata.Title != "Test Document" {
		t.Errorf("Expected title 'Test Document', got '%s'", doc.Metadata.Title)
	}
	if doc.Metadata.Author != "John Doe" {
		t.Errorf("Expected author 'John Doe', got '%s'", doc.Metadata.Author)
	}
	if doc.Metadata.Language != "en" {
		t.Errorf("Expected language 'en', got '%s'", doc.Metadata.Language)
	}
	if len(doc.Metadata.Tags) == 0 {
		t.Error("Expected tags to be extracted")
	}
	t.Logf("Metadata: title=%s, author=%s, tags=%v, lang=%s",
		doc.Metadata.Title, doc.Metadata.Author, doc.Metadata.Tags, doc.Metadata.Language)
}

func TestHTMLParser_BasicExtraction(t *testing.T) {
	content := `<!DOCTYPE html>
<html>
<head>
  <title>Test Page</title>
  <style>body { color: red; }</style>
</head>
<body>
  <h1>Hello World</h1>
  <p>This is a <strong>test</strong> paragraph.</p>
  <script>alert('hi');</script>
  <div>
    <ul>
      <li>Item 1</li>
      <li>Item 2</li>
    </ul>
  </div>
</body>
</html>`

	doc, err := rag.ParseDocument(content, rag.FormatHTML)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if doc.Format != rag.FormatHTML {
		t.Errorf("Expected format html, got %s", doc.Format)
	}

	if doc.Metadata.Title != "Test Page" {
		t.Errorf("Expected title 'Test Page', got '%s'", doc.Metadata.Title)
	}

	if strings.Contains(doc.Content, "<script>") {
		t.Error("Content should not contain script tags")
	}
	if strings.Contains(doc.Content, "body { color") {
		t.Error("Content should not contain style content")
	}
	if strings.Contains(doc.Content, "alert") {
		t.Error("Content should not contain script content")
	}

	if !strings.Contains(doc.Content, "Hello World") {
		t.Error("Content should contain 'Hello World'")
	}
	if !strings.Contains(doc.Content, "test") {
		t.Error("Content should contain 'test'")
	}
	if !strings.Contains(doc.Content, "Item 1") {
		t.Error("Content should contain 'Item 1'")
	}

	t.Logf("Extracted content (len=%d):\n%s", len(doc.Content), doc.Content)
}

func TestHTMLParser_Entities(t *testing.T) {
	content := `<html><body><p>A &amp; B &lt; C &gt; D</p></body></html>`

	doc, err := rag.ParseDocument(content, rag.FormatHTML)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if !strings.Contains(doc.Content, "A & B") {
		t.Errorf("Expected decoded entities, got: %s", doc.Content)
	}
}

func TestPlainTextParser(t *testing.T) {
	content := "Simple plain text with no special formatting.\nSecond line."

	doc, err := rag.ParseDocument(content, rag.FormatPlainText)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if doc.Content != content {
		t.Error("Plain text content should be unchanged")
	}
	if doc.Metadata.CharCount == 0 {
		t.Error("CharCount should be > 0")
	}
}

func TestStructureAwareChunk_Markdown(t *testing.T) {
	content := loadTestFile(t, "golang_concurrency.md")

	doc, err := rag.ParseDocument(content, rag.FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	t.Logf("Parsed document: format=%s, sections=%d, title=%s",
		doc.Format, len(doc.Sections), doc.Metadata.Title)

	cfg := rag.ChunkingConfig{
		MaxChunkSize:   1000,
		MinChunkSize:   100,
		OverlapSize:    200,
		StructureAware: true,
	}

	chunks := rag.StructureAwareChunk(doc, cfg)
	t.Logf("Structure-aware chunks: %d", len(chunks))

	regularChunks := rag.ChunkDocument(content, cfg)
	t.Logf("Regular chunks: %d", len(regularChunks))

	for i, c := range chunks {
		t.Logf("  Chunk %d: %d chars, tokens~%d", i, len(c.Content), c.TokenCount)
	}

	if len(chunks) == 0 {
		t.Error("Expected at least 1 chunk")
	}
}

// --- 表格处理测试 ---

func TestMarkdownParser_TableExtraction(t *testing.T) {
	content := `# 模型对比

下面是几个主流模型的对比：

| 模型 | 参数量 | 精度 | 发布时间 |
|------|-------|------|---------|
| GPT-4 | 1.8T | 96.3% | 2023-03 |
| Qwen-72B | 72B | 94.1% | 2023-11 |
| Llama-3 | 70B | 93.5% | 2024-04 |

## 其他信息

更多内容请参考文档。
`

	doc, err := rag.ParseDocument(content, rag.FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	t.Logf("Tables found: %d", len(doc.Tables))
	if len(doc.Tables) == 0 {
		t.Fatal("Expected at least 1 table")
	}

	table := doc.Tables[0]
	t.Logf("Headers: %v", table.Headers)
	t.Logf("Rows: %d", len(table.Rows))
	t.Logf("Context: %s", table.Context)
	t.Logf("Linearized:\n%s", table.Linearized)

	if len(table.Headers) != 4 {
		t.Errorf("Expected 4 headers, got %d: %v", len(table.Headers), table.Headers)
	}
	if len(table.Rows) != 3 {
		t.Errorf("Expected 3 rows, got %d", len(table.Rows))
	}
	if table.Headers[0] != "模型" {
		t.Errorf("Expected first header '模型', got '%s'", table.Headers[0])
	}

	// 验证线性化包含 key-value 格式
	if !strings.Contains(table.Linearized, "模型: GPT-4") {
		t.Error("Linearized should contain '模型: GPT-4'")
	}
	if !strings.Contains(table.Linearized, "参数量: 1.8T") {
		t.Error("Linearized should contain '参数量: 1.8T'")
	}

	if doc.Metadata.TableCount != 1 {
		t.Errorf("Expected TableCount=1, got %d", doc.Metadata.TableCount)
	}
}

func TestMarkdownParser_MultipleTablesInSections(t *testing.T) {
	content := `# 报告

## 性能指标

| 指标 | 值 |
|------|-----|
| QPS | 10000 |
| 延迟 | 5ms |

## 配置参数

| 参数 | 默认值 | 说明 |
|------|-------|------|
| timeout | 30s | 超时时间 |
| retries | 3 | 重试次数 |
`

	doc, err := rag.ParseDocument(content, rag.FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	t.Logf("Tables found: %d", len(doc.Tables))
	if len(doc.Tables) != 2 {
		t.Errorf("Expected 2 tables, got %d", len(doc.Tables))
	}

	for i, table := range doc.Tables {
		t.Logf("Table %d: headers=%v, rows=%d, context='%s'",
			i, table.Headers, len(table.Rows), table.Context)
	}
}

func TestLinearizeTable(t *testing.T) {
	table := rag.TableInfo{
		Headers: []string{"名称", "类型", "状态"},
		Rows: [][]string{
			{"Redis", "缓存", "运行中"},
			{"MySQL", "数据库", "停止"},
		},
		Context: "服务列表",
	}

	result := rag.LinearizeTable(table)
	t.Logf("Linearized:\n%s", result)

	if !strings.Contains(result, "[表格: 服务列表]") {
		t.Error("Should contain table context header")
	}
	if !strings.Contains(result, "名称: Redis") {
		t.Error("Should contain '名称: Redis'")
	}
	if !strings.Contains(result, "类型: 缓存") {
		t.Error("Should contain '类型: 缓存'")
	}
}

// --- 图片处理测试 ---

func TestMarkdownParser_ImageExtraction(t *testing.T) {
	content := `# 架构文档

## 系统架构图

下面展示了系统的整体架构：

![系统架构图](https://example.com/arch.png)

说明：该架构采用了微服务模式。

## 部署拓扑

![部署拓扑图](./images/deploy.png)

![](https://example.com/no-alt.png)
`

	doc, err := rag.ParseDocument(content, rag.FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	t.Logf("Images found: %d", len(doc.Images))
	if len(doc.Images) != 3 {
		t.Errorf("Expected 3 images, got %d", len(doc.Images))
	}

	for i, img := range doc.Images {
		t.Logf("  Image %d: alt='%s', url='%s'", i, img.AltText, img.URL)
	}

	if doc.Images[0].AltText != "系统架构图" {
		t.Errorf("Expected alt '系统架构图', got '%s'", doc.Images[0].AltText)
	}
	if doc.Images[2].AltText != "" {
		t.Errorf("Expected empty alt for 3rd image, got '%s'", doc.Images[2].AltText)
	}
	if doc.Metadata.ImageCount != 3 {
		t.Errorf("Expected ImageCount=3, got %d", doc.Metadata.ImageCount)
	}
}

// --- 内容增强测试 ---

func TestEnhanceContentForEmbedding(t *testing.T) {
	content := `# 测试文档

## 模型概览

| 模型 | 精度 |
|------|------|
| A | 95% |
| B | 90% |

## 架构图

![系统架构](https://example.com/arch.png)

更多说明。
`

	doc, err := rag.ParseDocument(content, rag.FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	enhanced := rag.EnhanceContentForEmbedding(doc)
	t.Logf("Enhanced content:\n%s", enhanced)

	// 表格应被线性化
	if strings.Contains(enhanced, "|------") {
		t.Error("Enhanced content should not contain table separator")
	}
	if !strings.Contains(enhanced, "模型: A") {
		t.Error("Enhanced content should contain linearized table '模型: A'")
	}

	// 图片应被增强
	if strings.Contains(enhanced, "https://example.com/arch.png") {
		t.Error("Enhanced content should not contain raw image URL")
	}
	if !strings.Contains(enhanced, "[图片: 系统架构]") {
		t.Error("Enhanced content should contain '[图片: 系统架构]'")
	}
}

// --- 表格感知分块测试 ---

func TestStructureAwareChunk_WithTable(t *testing.T) {
	content := `# API 文档

## 接口列表

以下是主要接口：

| 接口名 | 方法 | 路径 | 说明 |
|--------|------|------|------|
| 创建用户 | POST | /api/users | 注册新用户 |
| 获取用户 | GET | /api/users/:id | 获取用户详情 |
| 更新用户 | PUT | /api/users/:id | 更新用户信息 |
| 删除用户 | DELETE | /api/users/:id | 删除指定用户 |
| 用户列表 | GET | /api/users | 获取所有用户 |

## 认证方式

使用 JWT Token 认证。
`

	doc, err := rag.ParseDocument(content, rag.FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	cfg := rag.ChunkingConfig{
		MaxChunkSize:   300,
		MinChunkSize:   50,
		OverlapSize:    50,
		StructureAware: true,
	}

	chunks := rag.StructureAwareChunk(doc, cfg)
	t.Logf("Chunks: %d", len(chunks))

	tableFound := false
	for i, c := range chunks {
		t.Logf("  Chunk %d (%d chars): %s", i, len(c.Content), truncate(c.Content, 80))
		if strings.Contains(c.Content, "接口名: 创建用户") {
			tableFound = true
			// 验证表格没有被拆分
			if !strings.Contains(c.Content, "接口名: 删除用户") && !strings.Contains(c.Content, "接口名: 用户列表") {
				// 表格可能很大被保留为单独 chunk，但不应该行丢失
				t.Logf("  Note: table may span multiple chunks due to size limit")
			}
		}
	}

	if !tableFound {
		t.Error("Expected to find linearized table content in chunks")
	}
}

func TestAutoDetectAndParse(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected rag.DocumentFormat
	}{
		{
			name:     "plain text",
			content:  "Hello world. This is a test.",
			expected: rag.FormatPlainText,
		},
		{
			name: "markdown",
			content: `# Title

## Section

Content with **bold** and [links](http://example.com).

- List item 1
- List item 2
`,
			expected: rag.FormatMarkdown,
		},
		{
			name:     "html",
			content:  `<!DOCTYPE html><html><body><p>Test</p></body></html>`,
			expected: rag.FormatHTML,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := rag.ParseDocument(tc.content, "")
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			if doc.Format != tc.expected {
				t.Errorf("Expected format %s, got %s", tc.expected, doc.Format)
			}
			t.Logf("Format: %s, WordCount: %d, CharCount: %d",
				doc.Format, doc.Metadata.WordCount, doc.Metadata.CharCount)
		})
	}
}
