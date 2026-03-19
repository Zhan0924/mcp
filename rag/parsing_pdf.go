/*
┌──────────────────────────────────────────────────────────────────────────┐
│                  pdf_parser.go — PDF 文档解析器                           │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  核心思想：base64 解码 → 内存 PDF 解析 → 逐页提取文本和表格                 │
│                                                                          │
│  为什么输入是 base64？                                                    │
│    MCP 工具链的传输协议基于 JSON，无法直接携带二进制数据，                    │
│    因此 PDF 内容以 base64 编码字符串的形式传入。                             │
│                                                                          │
│  导出类型:                                                                │
│    PDFParser — 实现 DocumentParser 接口，通过 init() 自动注册              │
│                                                                          │
│  导出函数/方法:                                                           │
│    PDFParser.Parse(content) (*ParsedDocument, error)                     │
│        base64 解码 → bytes.Reader 内存解析 → 逐页提取 → 清洗 → 统一输出    │
│    ParsePDFFromReader(r, size) (*ParsedDocument, error)                  │
│        从 io.ReaderAt 直接解析（用于本地文件路径输入场景）                   │
│                                                                          │
│  内部函数:                                                                │
│    decodePDFContent    — base64 多编码方案解码 + PDF 魔数校验               │
│    extractPageContent  — 单页文本/表格提取（基于行列坐标）                   │
│    buildPDFTable       — 从连续多列行构建结构化表格                         │
│    extractPDFSections  — 按 [第N页] 标记切分为章节                         │
│    cleanPDFText        — 清理 PDF 提取文本中的多余空白                      │
│                                                                          │
│  表格检测启发式：                                                         │
│    连续 ≥ 3 行且每行 ≥ 3 个文本单元 → 识别为表格候选                        │
│    PDF 没有显式表格标记，只能靠空间布局推断                                  │
│                                                                          │
│  降级策略：如果结构化解析失败，返回逐页拼接的原始文本                         │
│                                                                          │
│  依赖: github.com/ledongthuc/pdf (纯 Go PDF 文本提取)                    │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// PDFParser PDF 文档解析器
type PDFParser struct{}

func init() {
	RegisterParser(&PDFParser{})
}

func (p *PDFParser) SupportedFormat() DocumentFormat { return FormatPDF }

// Parse 解析 PDF 内容
// 流程: base64 字符串 → 解码为字节 → bytes.Reader 内存解析（无需临时文件） → 逐页提取
func (p *PDFParser) Parse(content string) (*ParsedDocument, error) {
	pdfData, err := decodePDFContent(content)
	if err != nil {
		return nil, fmt.Errorf("decode PDF content: %w", err)
	}

	// 使用 bytes.Reader 避免写临时文件，减少 I/O 开销和清理负担
	reader := bytes.NewReader(pdfData)
	pdfReader, err := pdf.NewReader(reader, int64(len(pdfData)))
	if err != nil {
		return nil, fmt.Errorf("open PDF reader: %w", err)
	}

	var textBuilder strings.Builder
	var tables []TableInfo
	numPages := pdfReader.NumPage()

	for pageNum := 1; pageNum <= numPages; pageNum++ {
		page := pdfReader.Page(pageNum)
		// PDF 中可能存在占位空页或损坏页面（扫描件常见），其底层 V 对象为 null
		if page.V.IsNull() {
			continue
		}

		pageText, pageTables := extractPageContent(page, pageNum)
		if pageText != "" {
			if textBuilder.Len() > 0 {
				textBuilder.WriteString("\n\n")
			}
			// 插入页码标记，下游 extractPDFSections 以此为切分锚点
			textBuilder.WriteString(fmt.Sprintf("[第%d页]\n", pageNum))
			textBuilder.WriteString(pageText)
		}
		tables = append(tables, pageTables...)
	}

	fullText := textBuilder.String()
	fullText = cleanPDFText(fullText)

	doc := &ParsedDocument{
		Content:    fullText,
		Format:     FormatPDF,
		RawContent: fullText,
		Tables:     tables,
		Metadata: DocumentMetadata{
			WordCount:  estimateWordCount(fullText),
			CharCount:  utf8.RuneCountInString(fullText),
			TableCount: len(tables),
		},
	}

	// 标题提取启发式：取第一个非空、非页码标记的行
	// PDF 没有结构化标题元数据，只能靠内容推断
	for _, line := range strings.Split(fullText, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "[第") {
			doc.Metadata.Title = trimmed
			break
		}
	}

	doc.Sections = extractPDFSections(fullText)

	return doc, nil
}

// decodePDFContent 解码 PDF 内容
// 支持三种 base64 变体（标准、URL-safe、无填充），因为不同客户端的编码方式不统一
// 解码后验证 %PDF- 魔数签名，防止将非 PDF 数据传入解析器导致 panic
func decodePDFContent(content string) ([]byte, error) {
	raw := strings.TrimPrefix(content, "base64:")

	// 兼容 data URI scheme: "data:application/pdf;base64,..."
	if idx := strings.Index(raw, "base64,"); idx != -1 {
		raw = raw[idx+7:]
	}

	raw = strings.TrimSpace(raw)

	// 逐级尝试三种 base64 编码变体
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(raw)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(raw)
			if err != nil {
				return nil, fmt.Errorf("content is not valid base64: %w", err)
			}
		}
	}

	// 校验 PDF 魔数签名，防止非 PDF 数据进入解析器
	if len(data) < 5 || string(data[:5]) != "%PDF-" {
		return nil, fmt.Errorf("decoded data is not a valid PDF (missing %%PDF- header)")
	}

	return data, nil
}

// extractPageContent 从单页提取文本和表格
// 使用 GetTextByRow() 按行获取文本单元，保留空间布局信息用于表格检测
func extractPageContent(page pdf.Page, pageNum int) (string, []TableInfo) {
	rows, err := page.GetTextByRow()
	if err != nil || len(rows) == 0 {
		return "", nil
	}

	var lines []string
	var tableCandidate [][]string
	var tables []TableInfo

	for _, row := range rows {
		var cells []string
		for _, word := range row.Content {
			cells = append(cells, word.S)
		}
		lineText := strings.Join(cells, " ")
		lines = append(lines, lineText)

		// 表格检测启发式：一行内 ≥ 3 个独立文本单元 → 可能是表格行
		// 阈值 3 列: 低于 3 列与普通多词文本难以区分，误报率过高
		// PDF 没有显式表格标签，只能靠空间布局中的列对齐来推断
		if len(cells) >= 3 {
			tableCandidate = append(tableCandidate, cells)
		} else {
			// 非表格行打断了连续性：如果已累积 ≥ 3 行候选，确认为表格
			// 阈值 3 行: 1~2 行的"表格"更可能是巧合排列，连续 3 行以上才有统计置信度
			if len(tableCandidate) >= 3 {
				table := buildPDFTable(tableCandidate, pageNum)
				if table != nil {
					tables = append(tables, *table)
				}
			}
			tableCandidate = nil
		}
	}

	// 页面末尾的表格候选也需要处理
	if len(tableCandidate) >= 3 {
		table := buildPDFTable(tableCandidate, pageNum)
		if table != nil {
			tables = append(tables, *table)
		}
	}

	return strings.Join(lines, "\n"), tables
}

// buildPDFTable 从连续多列行构建结构化表格
// 第一行作为表头，后续行作为数据行，自动补齐/截断到表头列数
func buildPDFTable(rows [][]string, pageNum int) *TableInfo {
	// 至少需要 2 行（1 行表头 + 1 行数据），仅有表头的单行无法构成有意义的表格
	if len(rows) < 2 {
		return nil
	}

	headers := rows[0]
	var dataRows [][]string
	for _, row := range rows[1:] {
		for len(row) < len(headers) {
			row = append(row, "")
		}
		if len(row) > len(headers) {
			row = row[:len(headers)]
		}
		dataRows = append(dataRows, row)
	}

	table := &TableInfo{
		Headers: headers,
		Rows:    dataRows,
		Context: fmt.Sprintf("PDF 第%d页表格", pageNum),
	}

	// 生成 Markdown 格式的原始文本，供 LinearizeTable 和日志使用
	var rawBuf strings.Builder
	rawBuf.WriteString("| " + strings.Join(headers, " | ") + " |\n")
	for _, row := range dataRows {
		rawBuf.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
	table.RawText = rawBuf.String()
	table.Linearized = LinearizeTable(*table)

	return table
}

// extractPDFSections 按 [第N页] 标记切分内容为章节
// PDF 缺少结构化目录，用页码标记作为章节边界是最可靠的降级方案
func extractPDFSections(content string) []DocumentSection {
	pageMarker := regexp.MustCompile(`(?m)^\[第(\d+)页\]$`)
	matches := pageMarker.FindAllStringIndex(content, -1)

	if len(matches) == 0 {
		return nil
	}

	var sections []DocumentSection
	for i, match := range matches {
		start := match[0]
		end := len(content)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}

		sectionContent := strings.TrimSpace(content[match[1]:end])
		title := strings.TrimSpace(content[match[0]:match[1]])
		title = strings.Trim(title, "[]")

		if sectionContent == "" {
			continue
		}

		sections = append(sections, DocumentSection{
			Title:   title,
			Level:   1,
			Content: sectionContent,
			Start:   start,
			End:     end,
		})
	}

	return sections
}

// cleanPDFText 清理 PDF 提取文本
// PDF 文本提取常产生大量连续空白（因为 PDF 用空间坐标排版而非语义标记）
func cleanPDFText(text string) string {
	// 3+ 连续换行 → 双换行: 保留段落间距但消除 PDF 排版产生的大片空白区域
	re := regexp.MustCompile(`\n{3,}`)
	text = re.ReplaceAllString(text, "\n\n")

	// 3+ 连续空格/制表符 → 双空格: PDF 用坐标定位文本，提取后列间距变成大量空格
	re2 := regexp.MustCompile(`[ \t]{3,}`)
	text = re2.ReplaceAllString(text, "  ")

	return strings.TrimSpace(text)
}

// ParsePDFFromReader 从 io.ReaderAt 直接解析 PDF
// 用于本地文件路径输入场景，跳过 base64 编解码环节
// 与 Parse 方法共享逐页提取逻辑（extractPageContent），仅入口不同：io.ReaderAt vs base64 字符串
func ParsePDFFromReader(r io.ReaderAt, size int64) (*ParsedDocument, error) {
	pdfReader, err := pdf.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("open PDF reader: %w", err)
	}

	var textBuilder strings.Builder
	var tables []TableInfo
	numPages := pdfReader.NumPage()

	for pageNum := 1; pageNum <= numPages; pageNum++ {
		page := pdfReader.Page(pageNum)
		if page.V.IsNull() {
			continue
		}

		pageText, pageTables := extractPageContent(page, pageNum)
		if pageText != "" {
			if textBuilder.Len() > 0 {
				textBuilder.WriteString("\n\n")
			}
			textBuilder.WriteString(fmt.Sprintf("[第%d页]\n", pageNum))
			textBuilder.WriteString(pageText)
		}
		tables = append(tables, pageTables...)
	}

	fullText := cleanPDFText(textBuilder.String())

	doc := &ParsedDocument{
		Content:    fullText,
		Format:     FormatPDF,
		RawContent: fullText,
		Tables:     tables,
		Metadata: DocumentMetadata{
			WordCount:  estimateWordCount(fullText),
			CharCount:  utf8.RuneCountInString(fullText),
			TableCount: len(tables),
		},
	}

	doc.Sections = extractPDFSections(fullText)

	return doc, nil
}
