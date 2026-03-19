/*
┌──────────────────────────────────────────────────────────────────────┐
│                  parser.go — 多格式文档解析引擎                        │
├──────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  核心思想：格式自动检测 → 专用解析器 → 统一 ParsedDocument 输出          │
│  解析器通过注册表模式管理，新增格式只需实现 DocumentParser 接口并注册       │
│                                                                      │
│  导出类型:                                                            │
│    DocumentFormat     — 文档格式枚举 (text/markdown/html/pdf/docx)     │
│    ParsedDocument     — 解析结果：正文、元数据、章节、表格、图片引用       │
│    DocumentMetadata   — 元数据：字数/字符数/表格数/图片数（内容质量评估）  │
│    DocumentSection    — 章节结构：标题、层级、内容、位置区间              │
│    TableInfo          — 表格：表头、行数据、原始文本、线性化文本           │
│    ImageRef           — 图片引用：alt文本、URL、位置区间                 │
│    DocumentParser     — 解析器接口（Parse + SupportedFormat）           │
│                                                                      │
│  导出函数:                                                            │
│    RegisterParser(parser)                 — 注册解析器到全局注册表       │
│    GetParser(format) (parser, bool)       — 按格式查找解析器            │
│    ParseDocument(content, format) (*doc)  — 自动检测格式并解析           │
│    DetectFormat(content) DocumentFormat   — 格式自动检测                │
│    LinearizeTable(table) string           — 表格→检索友好的线性化文本     │
│    EnhanceContentForEmbedding(doc) string — embedding 预处理增强        │
│    StructureAwareChunk(doc, config) []Chunk — 结构感知分块              │
│                                                                      │
│  内置解析器:                                                          │
│    PlainTextParser  — 纯文本（透传 + 统计元数据）                       │
│    MarkdownParser   — Markdown（基于正则提取标题/表格/图片/章节层级）     │
│    HTMLParser        — HTML（标签剥离 + 内容保留 + 实体解码）            │
│                                                                      │
│  章节提取的意义: 下游 StructureAwareChunk 利用章节边界作为自然切分点       │
│  元数据的意义: char_count/table_count/image_count 用于内容质量评估和过滤  │
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// DocumentFormat 文档格式
type DocumentFormat string

const (
	FormatPlainText DocumentFormat = "text"
	FormatMarkdown  DocumentFormat = "markdown"
	FormatHTML      DocumentFormat = "html"
	FormatPDF       DocumentFormat = "pdf"
	FormatDOCX      DocumentFormat = "docx"
)

// ParsedDocument 解析后的文档
// RawContent 保留原始内容，Content 可能经过清洗（如 HTML 去标签后的纯文本）
type ParsedDocument struct {
	Content    string            `json:"content"`
	Format     DocumentFormat    `json:"format"`
	Metadata   DocumentMetadata  `json:"metadata"`
	Sections   []DocumentSection `json:"sections,omitempty"`
	Tables     []TableInfo       `json:"tables,omitempty"`
	Images     []ImageRef        `json:"images,omitempty"`
	RawContent string            `json:"-"`
}

// DocumentMetadata 文档元数据
// 统计指标用于内容质量评估：过短文档可能无实质内容，表格/图片计数帮助判断文档结构复杂度
type DocumentMetadata struct {
	Title      string   `json:"title,omitempty"`
	Author     string   `json:"author,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Language   string   `json:"language,omitempty"`
	WordCount  int      `json:"word_count"`
	CharCount  int      `json:"char_count"`
	TableCount int      `json:"table_count,omitempty"`
	ImageCount int      `json:"image_count,omitempty"`
}

// DocumentSection 文档章节
// Start/End 是字符偏移，供 StructureAwareChunk 按章节边界切分时使用
type DocumentSection struct {
	Title   string `json:"title"`
	Level   int    `json:"level"`
	Content string `json:"content"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
}

// TableInfo 表格信息
// Linearized 是表格的线性化文本（"列名: 值, 列名: 值" 格式），对 embedding 模型更友好
// Context 是表格上方最近的标题或描述文字，为线性化文本提供语义锚点
type TableInfo struct {
	Headers    []string   `json:"headers"`
	Rows       [][]string `json:"rows"`
	RawText    string     `json:"raw_text"`
	Linearized string     `json:"linearized"`
	Context    string     `json:"context,omitempty"`
	StartPos   int        `json:"start_pos"`
	EndPos     int        `json:"end_pos"`
}

// ImageRef 图片引用
type ImageRef struct {
	AltText  string `json:"alt_text"`
	URL      string `json:"url"`
	StartPos int    `json:"start_pos"`
	EndPos   int    `json:"end_pos"`
}

// DocumentParser 文档解析器接口
// 新增格式只需实现此接口并在 init() 中调用 RegisterParser 即可接入
type DocumentParser interface {
	Parse(content string) (*ParsedDocument, error)
	SupportedFormat() DocumentFormat
}

// --- 解析器注册表（策略模式） ---

var parsers = make(map[DocumentFormat]DocumentParser)

// RegisterParser 注册解析器到全局注册表
func RegisterParser(parser DocumentParser) {
	parsers[parser.SupportedFormat()] = parser
}

// GetParser 按格式查找解析器
func GetParser(format DocumentFormat) (DocumentParser, bool) {
	p, ok := parsers[format]
	return p, ok
}

// ParseDocument 统一入口：格式检测 → 查找解析器 → 解析
// 当 format 为空时自动检测；当找不到对应解析器时，降级为纯文本处理而非报错，
// 保证未知格式也能被索引（虽然结构信息会丢失）
func ParseDocument(content string, format DocumentFormat) (*ParsedDocument, error) {
	if format == "" {
		format = DetectFormat(content)
	}

	parser, ok := GetParser(format)
	if !ok {
		// 降级为纯文本：丢失结构信息但保证内容可被索引
		return &ParsedDocument{
			Content:    content,
			Format:     FormatPlainText,
			RawContent: content,
			Metadata: DocumentMetadata{
				WordCount: estimateWordCount(content),
				CharCount: utf8.RuneCountInString(content),
			},
		}, nil
	}

	return parser.Parse(content)
}

// DetectFormat 基于内容特征自动检测文档格式
// 检测顺序：HTML（明确标签前缀）→ Markdown（特征计数 ≥ 2）→ 纯文本（兜底）
func DetectFormat(content string) DocumentFormat {
	trimmed := strings.TrimSpace(content)

	// HTML 检测：文档必须以声明或标签开头才被识别，避免正文中偶尔出现的 HTML 片段误判
	if strings.HasPrefix(trimmed, "<!DOCTYPE") || strings.HasPrefix(trimmed, "<html") || strings.HasPrefix(trimmed, "<HTML") {
		return FormatHTML
	}

	if hasMarkdownFeatures(trimmed) {
		return FormatMarkdown
	}

	return FormatPlainText
}

// hasMarkdownFeatures 通过多特征投票判定 Markdown
// 要求至少匹配 2 个特征，避免仅含单个 # 或 * 的纯文本被误判
func hasMarkdownFeatures(content string) bool {
	mdPatterns := []string{
		"(?m)^#{1,6}\\s",
		"(?m)^\\x60\\x60\\x60",
		"(?m)^\\*\\s",
		"(?m)^-\\s",
		"(?m)^\\d+\\.\\s",
		"\\[.*\\]\\(.*\\)",
		"\\*\\*.*\\*\\*",
		"__.*__",
	}
	matches := 0
	for _, pattern := range mdPatterns {
		if matched, _ := regexp.MatchString(pattern, content); matched {
			matches++
		}
	}
	// 阈值 2：至少同时存在两种 Markdown 语法特征才认定为 Markdown
	return matches >= 2
}

// --- PlainText Parser（纯文本透传，仅统计元数据） ---

type PlainTextParser struct{}

// init 在包加载时自动注册所有内置解析器
func init() {
	RegisterParser(&PlainTextParser{})
	RegisterParser(&MarkdownParser{})
	RegisterParser(&HTMLParser{})
}

func (p *PlainTextParser) SupportedFormat() DocumentFormat { return FormatPlainText }

func (p *PlainTextParser) Parse(content string) (*ParsedDocument, error) {
	return &ParsedDocument{
		Content:    content,
		Format:     FormatPlainText,
		RawContent: content,
		Metadata: DocumentMetadata{
			WordCount: estimateWordCount(content),
			CharCount: utf8.RuneCountInString(content),
		},
	}, nil
}

// --- Markdown Parser（结构感知：提取标题层级、表格、图片，供下游结构感知分块使用） ---

type MarkdownParser struct{}

func (p *MarkdownParser) SupportedFormat() DocumentFormat { return FormatMarkdown }

// 预编译正则表达式，避免每次解析时重复编译
var mdHeaderRegex = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)
var mdCodeBlockRegex = regexp.MustCompile("(?s)\\x60\\x60\\x60[^\\x60]*\\x60\\x60\\x60")
var mdFrontmatterRegex = regexp.MustCompile(`(?s)^---\n(.+?)\n---`)
var mdImageRegex = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
var mdTableRowRegex = regexp.MustCompile(`(?m)^\|(.+)\|$`)
var mdTableSepRegex = regexp.MustCompile(`(?m)^\|[\s:]*[-]+[\s:]*(\|[\s:]*[-]+[\s:]*)*\|$`)

// Parse 解析 Markdown 文档
// 提取流程: frontmatter 元数据 → 文档标题 → 章节层级 → 表格 → 图片引用 → 统计指标
func (p *MarkdownParser) Parse(content string) (*ParsedDocument, error) {
	doc := &ParsedDocument{
		Content:    content,
		Format:     FormatMarkdown,
		RawContent: content,
	}

	body := content

	// 提取 frontmatter 元数据
	if fm := mdFrontmatterRegex.FindStringSubmatch(content); len(fm) > 1 {
		doc.Metadata = parseFrontmatter(fm[1])
		body = content[len(fm[0]):]
	}

	// 提取标题作为 title
	if doc.Metadata.Title == "" {
		if matches := mdHeaderRegex.FindStringSubmatch(body); len(matches) > 2 {
			if len(matches[1]) == 1 {
				doc.Metadata.Title = strings.TrimSpace(matches[2])
			}
		}
	}

	// 按标题分节
	doc.Sections = extractMarkdownSections(body)

	// 提取表格
	doc.Tables = extractMarkdownTables(body)
	doc.Metadata.TableCount = len(doc.Tables)

	// 提取图片引用
	doc.Images = extractMarkdownImages(body)
	doc.Metadata.ImageCount = len(doc.Images)

	doc.Metadata.WordCount = estimateWordCount(content)
	doc.Metadata.CharCount = utf8.RuneCountInString(content)

	return doc, nil
}

// --- Markdown 表格提取与线性化 ---
// 表格在 Markdown 中是纯文本格式 (| col | col |)，embedding 模型难以理解其语义。
// 提取后线性化为 "列名: 值, 列名: 值" 格式，显著提升表格内容的检索召回率。

// extractMarkdownTables 从 Markdown 内容中提取所有表格
func extractMarkdownTables(content string) []TableInfo {
	lines := strings.Split(content, "\n")
	var tables []TableInfo
	i := 0

	for i < len(lines) {
		// 寻找表格起始行: 包含 | 的行
		if !isTableRow(lines[i]) {
			i++
			continue
		}

		// 检查下一行是否是分隔符行 (|---|---|)
		if i+1 >= len(lines) || !isTableSeparator(lines[i+1]) {
			i++
			continue
		}

		// 提取表头
		headers := parseTableCells(lines[i])
		if len(headers) == 0 {
			i++
			continue
		}

		tableStartLine := i
		i += 2 // 跳过表头和分隔符

		// 提取数据行
		var rows [][]string
		for i < len(lines) && isTableRow(lines[i]) {
			cells := parseTableCells(lines[i])
			// 补齐或截断到与表头同长度
			for len(cells) < len(headers) {
				cells = append(cells, "")
			}
			if len(cells) > len(headers) {
				cells = cells[:len(headers)]
			}
			rows = append(rows, cells)
			i++
		}

		// 计算原始文本范围
		rawLines := lines[tableStartLine:i]
		rawText := strings.Join(rawLines, "\n")

		startPos := posOfLine(content, tableStartLine)
		endPos := posOfLine(content, i)

		// 查找表格上方最近的上下文（标题或说明）
		tableContext := findTableContext(lines, tableStartLine)

		table := TableInfo{
			Headers:  headers,
			Rows:     rows,
			RawText:  rawText,
			Context:  tableContext,
			StartPos: startPos,
			EndPos:   endPos,
		}
		table.Linearized = LinearizeTable(table)
		tables = append(tables, table)
	}

	return tables
}

func isTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") && strings.Count(trimmed, "|") >= 2
}

func isTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return false
	}
	return mdTableSepRegex.MatchString(trimmed)
}

func parseTableCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.Trim(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	var cells []string
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

// posOfLine 将行号转换为字符偏移量
// O(n) 线性扫描而非预建行偏移索引：此函数仅在表格提取时低频调用，不值得维护缓存
func posOfLine(content string, lineIndex int) int {
	pos := 0
	for i, c := range content {
		if lineIndex == 0 {
			return i
		}
		if c == '\n' {
			lineIndex--
			pos = i + 1
		}
	}
	if lineIndex == 0 {
		return pos
	}
	return len(content)
}

// findTableContext 向上最多回溯 3 行寻找表格的语义上下文（标题或描述文字）
// 限制 3 行: 表格通常紧跟其说明，超过 3 行大概率已越过话题边界，会引入不相关上下文
func findTableContext(lines []string, tableStartLine int) string {
	for i := tableStartLine - 1; i >= 0 && i >= tableStartLine-3; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		// 如果是标题行
		if strings.HasPrefix(line, "#") {
			return strings.TrimLeft(line, "# ")
		}
		// 如果是描述文字（非空行）
		if len(line) > 0 && !strings.HasPrefix(line, "|") {
			return line
		}
	}
	return ""
}

// LinearizeTable 将表格线性化为适合 embedding 的键值对文本
// 例: | 模型 | 参数量 | 精度 | → "模型: GPT-4, 参数量: 1.8T, 精度: 96.3%"
// 键值对格式让 embedding 模型能捕获列名与值的关联语义，而非仅看到管道符分隔的文本
func LinearizeTable(table TableInfo) string {
	var sb strings.Builder

	if table.Context != "" {
		sb.WriteString("[表格: ")
		sb.WriteString(table.Context)
		sb.WriteString("]\n")
	}

	sb.WriteString("列: ")
	sb.WriteString(strings.Join(table.Headers, " | "))
	sb.WriteString("\n")

	for _, row := range table.Rows {
		for j, cell := range row {
			if j > 0 {
				sb.WriteString(", ")
			}
			if j < len(table.Headers) {
				sb.WriteString(table.Headers[j])
				sb.WriteString(": ")
			}
			sb.WriteString(cell)
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}

// --- Markdown 图片引用提取 ---

// extractMarkdownImages 从 Markdown 内容中提取所有图片引用
func extractMarkdownImages(content string) []ImageRef {
	matches := mdImageRegex.FindAllStringSubmatchIndex(content, -1)
	var images []ImageRef
	for _, match := range matches {
		altText := content[match[2]:match[3]]
		url := content[match[4]:match[5]]
		images = append(images, ImageRef{
			AltText:  altText,
			URL:      url,
			StartPos: match[0],
			EndPos:   match[1],
		})
	}
	return images
}

// EnhanceContentForEmbedding 将文档内容中的表格和图片转换为对 embedding 更友好的格式
// 表格 → 线性化键值对，图片 → [图片: alt描述]，使 embedding 向量更好地捕获语义
func EnhanceContentForEmbedding(doc *ParsedDocument) string {
	if doc == nil {
		return ""
	}

	content := doc.Content

	// 1) 替换表格为线性化文本（使用字符串匹配，避免位置偏移问题）
	for _, table := range doc.Tables {
		if table.RawText != "" && table.Linearized != "" {
			content = strings.Replace(content, table.RawText, table.Linearized, 1)
		}
	}

	// 2) 增强图片引用
	for _, img := range doc.Images {
		original := fmt.Sprintf("![%s](%s)", img.AltText, img.URL)
		enhanced := fmt.Sprintf("[图片: %s]", img.AltText)
		if img.AltText == "" {
			enhanced = "[图片]"
		}
		content = strings.Replace(content, original, enhanced, 1)
	}

	return content
}

// extractMarkdownSections 按 Markdown 标题行切分文档为章节列表
// 算法：找到所有标题行位置 → 每个标题的"管辖范围"延伸到下一个标题之前
// 这种"标题-到-标题"切分与 Markdown 的层级语义一致，是结构感知分块的基础
func extractMarkdownSections(content string) []DocumentSection {
	matches := mdHeaderRegex.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return nil
	}

	var sections []DocumentSection
	for i, match := range matches {
		level := match[3] - match[2] // submatch[1] 的长度 = # 的数量 = 标题层级
		title := content[match[4]:match[5]]

		start := match[0]
		end := len(content)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}

		sectionContent := strings.TrimSpace(content[match[1]:end])

		sections = append(sections, DocumentSection{
			Title:   title,
			Level:   level,
			Content: sectionContent,
			Start:   start,
			End:     end,
		})
	}

	return sections
}

// parseFrontmatter 解析 YAML frontmatter 中的已知字段
// 使用简单的 key:value 行解析而非完整 YAML 库：仅需提取 title/author/tags/language 四个字段，
// 避免为此引入额外依赖
func parseFrontmatter(fm string) DocumentMetadata {
	meta := DocumentMetadata{}
	for _, line := range strings.Split(fm, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch strings.ToLower(key) {
		case "title":
			meta.Title = value
		case "author":
			meta.Author = value
		case "tags":
			for _, tag := range strings.Split(value, ",") {
				tag = strings.TrimSpace(strings.Trim(tag, "[]"))
				if tag != "" {
					meta.Tags = append(meta.Tags, tag)
				}
			}
		case "language", "lang":
			meta.Language = value
		}
	}
	return meta
}

// --- HTML Parser ---
// 设计思路：剥离标签但保留内容结构（块级标签 → 换行，内联标签 → 透明移除）
// 不使用完整的 DOM 解析器（如 golang.org/x/net/html），因为 RAG 场景只需要纯文本，
// 正则方案足够且零外部依赖

type HTMLParser struct{}

func (p *HTMLParser) SupportedFormat() DocumentFormat { return FormatHTML }

var htmlTagRegex = regexp.MustCompile(`<[^>]+>`)
var htmlEntityRegex = regexp.MustCompile(`&[a-zA-Z]+;|&#\d+;`)
var htmlTitleRegex = regexp.MustCompile(`(?i)<title[^>]*>(.*?)</title>`)
var htmlScriptRegex = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
var htmlStyleRegex = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
var htmlNoscriptRegex = regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`)
var multiSpaceRegex = regexp.MustCompile(`\s{3,}`)
var multiNewlineRegex = regexp.MustCompile(`\n{3,}`)

// Parse 解析 HTML 文档
// 流程: 提取 title → 移除不可见内容(script/style) → 块级标签转换行 → 剥离剩余标签 → 实体解码 → 清理空白
func (p *HTMLParser) Parse(content string) (*ParsedDocument, error) {
	doc := &ParsedDocument{
		Format:     FormatHTML,
		RawContent: content,
	}

	if matches := htmlTitleRegex.FindStringSubmatch(content); len(matches) > 1 {
		doc.Metadata.Title = strings.TrimSpace(matches[1])
	}

	// 先移除 script/style/noscript，这些内容对检索无意义且会引入大量噪声
	cleaned := htmlScriptRegex.ReplaceAllString(content, "")
	cleaned = htmlStyleRegex.ReplaceAllString(cleaned, "")
	cleaned = htmlNoscriptRegex.ReplaceAllString(cleaned, "")

	// 块级标签 → 换行符，保留文档的段落结构
	// 内联标签（如 span, a, em）直接移除，不影响文本流
	blockTags := []string{"div", "p", "br", "h1", "h2", "h3", "h4", "h5", "h6",
		"li", "tr", "td", "th", "blockquote", "pre", "hr", "section", "article"}
	for _, tag := range blockTags {
		re := regexp.MustCompile(fmt.Sprintf(`(?i)</?%s[^>]*>`, tag))
		cleaned = re.ReplaceAllString(cleaned, "\n")
	}

	cleaned = htmlTagRegex.ReplaceAllString(cleaned, "")
	cleaned = decodeHTMLEntities(cleaned)

	cleaned = multiSpaceRegex.ReplaceAllString(cleaned, " ")
	cleaned = multiNewlineRegex.ReplaceAllString(cleaned, "\n\n")
	cleaned = strings.TrimSpace(cleaned)

	doc.Content = cleaned
	doc.Metadata.WordCount = estimateWordCount(cleaned)
	doc.Metadata.CharCount = utf8.RuneCountInString(cleaned)

	return doc, nil
}

func decodeHTMLEntities(s string) string {
	entities := map[string]string{
		"&amp;":  "&",
		"&lt;":   "<",
		"&gt;":   ">",
		"&quot;": "\"",
		"&apos;": "'",
		"&nbsp;": " ",
		"&mdash;": "—",
		"&ndash;": "–",
		"&hellip;": "…",
	}
	result := s
	for entity, char := range entities {
		result = strings.ReplaceAll(result, entity, char)
	}
	return result
}

// estimateWordCount 中英文混合字数估算
// 中文：每个 CJK 字符计 1 词（中文无空格分隔）
// 英文：空白分隔的连续字母数字串计 1 词
func estimateWordCount(text string) int {
	words := 0
	inWord := false
	for _, r := range text {
		if r >= 0x4e00 && r <= 0x9fff {
			// CJK 字符各自独立成词
			words++
			inWord = false
		} else if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			if inWord {
				words++
				inWord = false
			}
		} else if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			inWord = true
		}
	}
	if inWord {
		words++
	}
	return words
}

// StructureAwareChunk 结构感知分块 — 利用 Markdown 章节标题作为自然切分点
// 优势：章节边界通常是语义边界，比固定窗口更能保持话题一致性
// 同时支持表格感知（不拆分表格）和图片引用增强
// 非 Markdown 或无章节结构的文档降级为普通 ChunkDocument
func StructureAwareChunk(doc *ParsedDocument, config ChunkingConfig) []Chunk {
	if doc.Format != FormatMarkdown || len(doc.Sections) == 0 {
		// 无结构信息可利用，降级为普通滑动窗口分块
		content := EnhanceContentForEmbedding(doc)
		return ChunkDocument(content, config)
	}

	enhancedDoc := enhanceSectionsContent(doc)

	var allChunks []Chunk
	chunkIndex := 0

	for _, section := range enhancedDoc.Sections {
		// 将标题重新拼入内容，使每个 chunk 自带标题上下文
		// 这样即使 chunk 被单独检索，也能从标题判断其所属主题
		text := section.Content
		if section.Title != "" {
			text = strings.Repeat("#", section.Level) + " " + section.Title + "\n\n" + text
		}

		sectionChunks := chunkSectionWithTableAwareness(text, section, config)

		for i := range sectionChunks {
			sectionChunks[i].ChunkIndex = chunkIndex
			chunkIndex++
		}
		allChunks = append(allChunks, sectionChunks...)
	}

	if len(allChunks) == 0 {
		content := EnhanceContentForEmbedding(doc)
		return ChunkDocument(content, config)
	}

	return allChunks
}

// enhanceSectionsContent 对文档的每个 section 应用表格线性化和图片增强
// 浅拷贝 doc + 深拷贝 Sections 切片：避免修改原始 ParsedDocument，
// 因为原始文档可能被缓存层持有或其他流程并发引用
func enhanceSectionsContent(doc *ParsedDocument) *ParsedDocument {
	enhanced := *doc
	enhanced.Sections = make([]DocumentSection, len(doc.Sections))
	copy(enhanced.Sections, doc.Sections)

	for i, section := range enhanced.Sections {
		content := section.Content

		// 线性化 section 内的表格
		for _, table := range doc.Tables {
			if table.RawText != "" && strings.Contains(content, table.RawText) {
				content = strings.Replace(content, table.RawText, table.Linearized, 1)
			}
		}

		// 增强图片引用
		for _, img := range doc.Images {
			original := fmt.Sprintf("![%s](%s)", img.AltText, img.URL)
			if strings.Contains(content, original) {
				enhanced := "[图片: " + img.AltText + "]"
				if img.AltText == "" {
					enhanced = "[图片]"
				}
				content = strings.Replace(content, original, enhanced, 1)
			}
		}

		enhanced.Sections[i].Content = content
	}

	return &enhanced
}

// chunkSectionWithTableAwareness 对单个 section 进行表格感知分块
// 核心约束：表格是原子单元，即使超过 MaxChunkSize 也不拆分
// 因为拆分后的半张表格对检索毫无意义，反而会成为噪声
func chunkSectionWithTableAwareness(text string, section DocumentSection, config ChunkingConfig) []Chunk {
	// 如果整个 section 够小，直接作为单个 chunk
	if utf8.RuneCountInString(text) <= config.MaxChunkSize {
		return []Chunk{{
			ChunkID:    generateChunkID(),
			Content:    text,
			StartPos:   section.Start,
			EndPos:     section.End,
			TokenCount: estimateTokenCount(text),
		}}
	}

	// 检测是否包含线性化的表格标记 "[表格:" 或 "列:"
	if !strings.Contains(text, "[表格:") && !strings.Contains(text, "列: ") {
		return ChunkDocument(text, config)
	}

	// 将内容拆分为文本块和表格块
	blocks := splitIntoBlocks(text)
	var chunks []Chunk

	for _, block := range blocks {
		if block.isTable {
			// 表格作为原子 chunk，不拆分
			chunks = append(chunks, Chunk{
				ChunkID:    generateChunkID(),
				Content:    block.content,
				StartPos:   section.Start,
				EndPos:     section.End,
				TokenCount: estimateTokenCount(block.content),
			})
		} else {
			trimmed := strings.TrimSpace(block.content)
			if trimmed == "" {
				continue
			}
			subChunks := ChunkDocument(trimmed, config)
			chunks = append(chunks, subChunks...)
		}
	}

	if len(chunks) == 0 {
		return ChunkDocument(text, config)
	}

	return chunks
}

type contentBlock struct {
	content string
	isTable bool
}

// splitIntoBlocks 将内容拆分为普通文本块和表格块
// 实现为两状态有限状态机（inTable = true/false），逐行扫描并在状态转换时输出累积块
// 这样表格内容被完整保留为单个块，不会被后续的 ChunkDocument 拆散
func splitIntoBlocks(text string) []contentBlock {
	lines := strings.Split(text, "\n")
	var blocks []contentBlock
	var current strings.Builder
	inTable := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 检测表格块的开始: "[表格:" 标记或 "列:" 行
		isTableLine := strings.HasPrefix(trimmed, "[表格:") ||
			strings.HasPrefix(trimmed, "列: ") ||
			(inTable && trimmed != "" && !strings.HasPrefix(trimmed, "#"))

		if isTableLine && !inTable {
			// 从普通文本进入表格
			if current.Len() > 0 {
				blocks = append(blocks, contentBlock{content: current.String(), isTable: false})
				current.Reset()
			}
			inTable = true
		} else if !isTableLine && inTable {
			// 从表格回到普通文本（空行或标题行）
			if current.Len() > 0 {
				blocks = append(blocks, contentBlock{content: current.String(), isTable: true})
				current.Reset()
			}
			inTable = false
		}

		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}

	if current.Len() > 0 {
		blocks = append(blocks, contentBlock{content: current.String(), isTable: inTable})
	}

	return blocks
}

func generateChunkID() string {
	return newUUID()
}
