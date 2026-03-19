/*
+------------------------------------------------------------------------------+
|                  docx_parser.go --- DOCX 文档解析器                            |
+------------------------------------------------------------------------------+
|                                                                              |
|  核心思想：base64 解码 -> ZIP 解压 -> 提取 word/document.xml -> XML 解析       |
|                                                                              |
|  为什么输入是 base64？                                                        |
|    与 pdf_parser.go 相同：MCP 协议基于 JSON 传输，二进制需 base64 编码。         |
|                                                                              |
|  DOCX 文件格式简介：                                                          |
|    DOCX 是 Office Open XML 标准，本质是一个 ZIP 压缩包，内部结构：              |
|      word/document.xml  — 文档正文（段落、表格、图片引用）                       |
|      word/styles.xml    — 样式定义（标题级别等）                                |
|      word/media/        — 嵌入的图片文件                                       |
|      [Content_Types].xml — MIME 类型清单                                      |
|                                                                              |
|  提取策略：                                                                    |
|    1. 解析 word/document.xml 中的 <w:p> (段落) 和 <w:tbl> (表格) 元素          |
|    2. 通过 <w:pStyle w:val="HeadingN"> 识别标题级别，构建 Section 层级          |
|    3. 表格按行列提取并线性化，与 parser.go 的 LinearizeTable 复用               |
|    4. 图片通过 <w:drawing> / <wp:docPr> 提取 alt 描述文本                      |
|                                                                              |
|  零外部依赖：仅使用 Go 标准库 (archive/zip + encoding/xml)                     |
|                                                                              |
|  降级策略：如果 XML 结构解析失败，返回逐段拼接的纯文本                           |
|                                                                              |
+------------------------------------------------------------------------------+
*/
package rag

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// DOCXParser DOCX 文档解析器
type DOCXParser struct{}

func init() {
	RegisterParser(&DOCXParser{})
}

func (p *DOCXParser) SupportedFormat() DocumentFormat { return FormatDOCX }

// Parse 解析 DOCX 内容
// 流程: base64 字符串 -> 解码为字节 -> ZIP 解压 -> 提取 word/document.xml -> XML 遍历
func (p *DOCXParser) Parse(content string) (*ParsedDocument, error) {
	data, err := decodeDOCXContent(content)
	if err != nil {
		return nil, fmt.Errorf("decode DOCX content: %w", err)
	}

	return ParseDOCXFromBytes(data)
}

// ParseDOCXFromBytes 从字节切片解析 DOCX 文档（供外部直接调用，跳过 base64）
func ParseDOCXFromBytes(data []byte) (*ParsedDocument, error) {
	reader := bytes.NewReader(data)
	zipReader, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open ZIP reader: %w", err)
	}

	// 查找 word/document.xml —— DOCX 正文所在的核心文件
	var docXML []byte
	for _, f := range zipReader.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open word/document.xml: %w", err)
			}
			docXML, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("read word/document.xml: %w", err)
			}
			break
		}
	}

	if docXML == nil {
		return nil, fmt.Errorf("word/document.xml not found in DOCX archive")
	}

	return parseDocumentXML(docXML)
}

// decodeDOCXContent 解码 DOCX base64 内容
// 支持多种 base64 变体和 data URI 前缀（与 pdf_parser.go 一致）
func decodeDOCXContent(content string) ([]byte, error) {
	raw := strings.TrimPrefix(content, "base64:")

	// 兼容 data URI: "data:application/vnd.openxmlformats-officedocument.wordprocessingml.document;base64,..."
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

	// 校验 ZIP 魔数签名 (PK\x03\x04)
	if len(data) < 4 || string(data[:2]) != "PK" {
		return nil, fmt.Errorf("decoded data is not a valid ZIP/DOCX file (missing PK header)")
	}

	return data, nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// Office Open XML 解析
// ═══════════════════════════════════════════════════════════════════════════════

// parseDocumentXML 解析 word/document.xml 的 XML 内容
// 使用流式 XML Decoder 遍历，提取段落文本、标题样式、表格和图片引用
func parseDocumentXML(data []byte) (*ParsedDocument, error) {
	doc := &ParsedDocument{
		Format: FormatDOCX,
	}

	var textBuilder strings.Builder
	var sections []DocumentSection
	var tables []TableInfo
	var images []ImageRef

	// 当前解析状态
	var currentParagraphTexts []string // 当前 <w:p> 内累积的文本片段
	var currentHeadingLevel int        // 当前段落的标题级别 (0 = 正文)
	var currentTableRows [][]string    // 当前 <w:tbl> 内累积的行
	var currentRowCells []string       // 当前 <w:tr> 内累积的单元格
	var currentCellTexts []string      // 当前 <w:tc> 内累积的文本

	inParagraph := false
	inTable := false
	inTableRow := false
	inTableCell := false
	inRun := false // <w:r> 文本运行块
	inText := false // <w:t> 文本内容

	decoder := xml.NewDecoder(bytes.NewReader(data))

	// 标题样式正则: Heading1, Heading2, ... 或 heading 1, heading 2 (不同 Word 版本)
	headingRegex := regexp.MustCompile(`(?i)^heading\s*(\d)$`)

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// XML 解析错误时降级返回已提取的内容
			break
		}

		switch t := token.(type) {
		case xml.StartElement:
			localName := t.Name.Local

			switch localName {
			case "p": // <w:p> 段落开始
				if !inTable {
					inParagraph = true
					currentParagraphTexts = nil
					currentHeadingLevel = 0
				}

			case "pStyle": // <w:pStyle w:val="Heading1"> 段落样式
				if inParagraph || inTableCell {
					for _, attr := range t.Attr {
						if attr.Name.Local == "val" {
							if m := headingRegex.FindStringSubmatch(attr.Value); len(m) > 1 {
								level := int(m[1][0] - '0')
								if level >= 1 && level <= 6 {
									currentHeadingLevel = level
								}
							}
						}
					}
				}

			case "r": // <w:r> 文本运行块
				inRun = true

			case "t": // <w:t> 文本内容
				if inRun {
					inText = true
				}

			case "tbl": // <w:tbl> 表格开始
				inTable = true
				currentTableRows = nil

			case "tr": // <w:tr> 表格行开始
				if inTable {
					inTableRow = true
					currentRowCells = nil
				}

			case "tc": // <w:tc> 表格单元格开始
				if inTableRow {
					inTableCell = true
					currentCellTexts = nil
				}

			case "drawing", "pict": // <w:drawing> 或 <w:pict> 图片
				// 尝试从子元素中提取 alt 文本
				altText := extractImageAlt(decoder, localName)
				if altText != "" {
					images = append(images, ImageRef{
						AltText:  altText,
						StartPos: textBuilder.Len(),
						EndPos:   textBuilder.Len(),
					})
					// 在正文中插入图片占位符
					if inParagraph {
						currentParagraphTexts = append(currentParagraphTexts, "[图片: "+altText+"]")
					}
				}
			}

		case xml.CharData:
			if inText {
				text := string(t)
				if inTableCell {
					currentCellTexts = append(currentCellTexts, text)
				} else if inParagraph {
					currentParagraphTexts = append(currentParagraphTexts, text)
				}
			}

		case xml.EndElement:
			localName := t.Name.Local

			switch localName {
			case "t":
				inText = false

			case "r":
				inRun = false
				inText = false

			case "p": // </w:p> 段落结束
				if inTableCell {
					// 表格单元格内的段落 —— 累积到单元格文本
					cellText := strings.Join(currentCellTexts, "")
					if cellText != "" {
						// 如果单元格内有多个段落，用换行分隔
						if len(currentCellTexts) > 0 {
							currentCellTexts = nil
						}
					}
				} else if inParagraph {
					paraText := strings.Join(currentParagraphTexts, "")
					paraText = strings.TrimSpace(paraText)

					if paraText != "" {
						startPos := textBuilder.Len()

						if textBuilder.Len() > 0 {
							textBuilder.WriteString("\n\n")
						}

						// 标题段落：插入 Markdown 风格标记，便于下游结构感知分块
						if currentHeadingLevel > 0 {
							textBuilder.WriteString(strings.Repeat("#", currentHeadingLevel))
							textBuilder.WriteString(" ")
						}
						textBuilder.WriteString(paraText)

						endPos := textBuilder.Len()

						// 记录标题作为 Section
						if currentHeadingLevel > 0 {
							sections = append(sections, DocumentSection{
								Title: paraText,
								Level: currentHeadingLevel,
								Start: startPos,
								End:   endPos,
							})
						}
					}

					inParagraph = false
					currentParagraphTexts = nil
					currentHeadingLevel = 0
				}

			case "tc": // </w:tc> 单元格结束
				if inTableCell {
					cellText := strings.Join(currentCellTexts, "")
					currentRowCells = append(currentRowCells, strings.TrimSpace(cellText))
					inTableCell = false
					currentCellTexts = nil
				}

			case "tr": // </w:tr> 行结束
				if inTableRow {
					currentTableRows = append(currentTableRows, currentRowCells)
					inTableRow = false
					currentRowCells = nil
				}

			case "tbl": // </w:tbl> 表格结束
				if inTable && len(currentTableRows) >= 2 {
					table := buildDOCXTable(currentTableRows, textBuilder.Len())
					if table != nil {
						tables = append(tables, *table)
						// 将线性化表格文本插入正文
						if textBuilder.Len() > 0 {
							textBuilder.WriteString("\n\n")
						}
						textBuilder.WriteString(table.Linearized)
					}
				}
				inTable = false
				currentTableRows = nil
			}
		}
	}

	fullText := textBuilder.String()
	fullText = cleanDOCXText(fullText)

	doc.Content = fullText
	doc.RawContent = fullText
	doc.Tables = tables
	doc.Images = images

	// 回填 Section 内容: 每个 section 的 content 是从当前标题到下一个标题之间的文本
	for i := range sections {
		start := sections[i].Start
		end := len(fullText)
		if i+1 < len(sections) {
			end = sections[i+1].Start
		}
		if start < len(fullText) && end <= len(fullText) {
			sectionContent := strings.TrimSpace(fullText[start:end])
			// 去掉标题行本身，只保留内容
			if idx := strings.Index(sectionContent, "\n"); idx != -1 {
				sections[i].Content = strings.TrimSpace(sectionContent[idx+1:])
			}
		}
		sections[i].End = end
	}
	doc.Sections = sections

	// 提取文档标题（第一个 Heading1，或首个非空段落）
	if len(sections) > 0 {
		doc.Metadata.Title = sections[0].Title
	} else {
		for _, line := range strings.Split(fullText, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				doc.Metadata.Title = trimmed
				break
			}
		}
	}

	doc.Metadata.WordCount = estimateWordCount(fullText)
	doc.Metadata.CharCount = utf8.RuneCountInString(fullText)
	doc.Metadata.TableCount = len(tables)
	doc.Metadata.ImageCount = len(images)

	return doc, nil
}

// extractImageAlt 从 <w:drawing> 或 <w:pict> 元素中提取图片的 alt 文本
// 在 OOXML 中，alt 文本位于 <wp:docPr name="..." descr="..."> 的 descr 属性
// 使用递归下降解析：跳过直到遇到闭合标签，沿途检查 docPr 元素
func extractImageAlt(decoder *xml.Decoder, parentName string) string {
	var altText string
	depth := 1

	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := token.(type) {
		case xml.StartElement:
			depth++
			// <wp:docPr> 或 <v:imagedata> 中可能包含 alt 描述
			if t.Name.Local == "docPr" || t.Name.Local == "cNvPr" {
				for _, attr := range t.Attr {
					if attr.Name.Local == "descr" && attr.Value != "" {
						altText = attr.Value
					}
					// name 属性作为 alt 的兜底
					if attr.Name.Local == "name" && altText == "" {
						altText = attr.Value
					}
				}
			}
		case xml.EndElement:
			depth--
		}
	}

	return altText
}

// buildDOCXTable 从 DOCX 表格行数据构建 TableInfo
// 第一行视为表头，后续行为数据
func buildDOCXTable(rows [][]string, textPos int) *TableInfo {
	if len(rows) < 2 {
		return nil
	}

	headers := rows[0]
	// 检查表头是否全为空（无意义的空表格）
	allEmpty := true
	for _, h := range headers {
		if strings.TrimSpace(h) != "" {
			allEmpty = false
			break
		}
	}
	if allEmpty {
		return nil
	}

	var dataRows [][]string
	for _, row := range rows[1:] {
		// 补齐或截断到表头列数
		for len(row) < len(headers) {
			row = append(row, "")
		}
		if len(row) > len(headers) {
			row = row[:len(headers)]
		}
		dataRows = append(dataRows, row)
	}

	// 构建 Markdown 格式原始文本
	var rawBuf strings.Builder
	rawBuf.WriteString("| " + strings.Join(headers, " | ") + " |\n")
	sep := make([]string, len(headers))
	for i := range sep {
		sep[i] = "---"
	}
	rawBuf.WriteString("| " + strings.Join(sep, " | ") + " |\n")
	for _, row := range dataRows {
		rawBuf.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}

	table := &TableInfo{
		Headers:  headers,
		Rows:     dataRows,
		RawText:  rawBuf.String(),
		StartPos: textPos,
		EndPos:   textPos,
		Context:  "DOCX 文档表格",
	}
	table.Linearized = LinearizeTable(*table)

	return table
}

// cleanDOCXText 清理 DOCX 提取文本中的多余空白
func cleanDOCXText(text string) string {
	// 3+ 连续换行 -> 双换行
	re := regexp.MustCompile(`\n{3,}`)
	text = re.ReplaceAllString(text, "\n\n")

	// 3+ 连续空格 -> 单空格
	re2 := regexp.MustCompile(`[ \t]{3,}`)
	text = re2.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}
