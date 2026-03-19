/*
┌─────────────────────────────────────────────────────────────────────────────┐
│           document_service.go — 增强文档预处理服务                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  统一的文档处理入口，整合多种解析和分块策略：                                 │
│    1. 格式自动检测 + 多格式解析                                              │
│    2. 代码文件智能识别 + 代码感知分块                                        │
│    3. 语义分块 (可选)                                                        │
│    4. 结构感知分块 (Markdown)                                                │
│    5. 通用固定窗口分块 (兜底)                                                │
│                                                                             │
│  分块策略选择优先级:                                                         │
│    代码文件 → CodeChunking                                                  │
│    语义分块启用 → SemanticChunking                                          │
│    Markdown 文档 → StructureAwareChunk                                      │
│    其他 → ChunkDocument                                                     │
│                                                                             │
│  导出类型:                                                                   │
│    DocumentService       — 文档处理服务                                     │
│    DocumentServiceConfig — 服务配置                                         │
│    ProcessedDocument     — 处理结果                                         │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"context"
	"strings"

	"github.com/sirupsen/logrus"
)

// DocumentServiceConfig 文档处理服务配置
type DocumentServiceConfig struct {
	ChunkingConfig         ChunkingConfig          `toml:"chunking"`
	SemanticChunkingConfig SemanticChunkingConfig   `toml:"semantic_chunking"`
	CodeChunkingEnabled    bool                     `toml:"code_chunking_enabled"`
	MaxChunkSize           int                      `toml:"max_chunk_size"`
}

// ProcessedDocument 处理后的文档
type ProcessedDocument struct {
	Content  string           // 清洗后的文本内容
	Chunks   []Chunk          // 分块结果
	Metadata DocumentMetadata // 文档元数据
	Format   DocumentFormat   // 检测到的格式
	Language string           // 检测到的代码语言 (若为代码文件)
}

// DocumentService 增强文档预处理服务
// 统一调度多种解析和分块策略
type DocumentService struct {
	config DocumentServiceConfig
}

// NewDocumentService 创建文档处理服务
func NewDocumentService(cfg DocumentServiceConfig) *DocumentService {
	if cfg.MaxChunkSize <= 0 {
		cfg.MaxChunkSize = 1000
	}
	return &DocumentService{config: cfg}
}

// Process 处理文档: 解析 → 检测类型 → 选择分块策略 → 返回结果
func (s *DocumentService) Process(ctx context.Context, content, fileName, format string, embedFn EmbedFunc) (*ProcessedDocument, error) {
	result := &ProcessedDocument{}

	// Step 1: 解析文档
	docFormat := DocumentFormat(format)
	doc, err := ParseDocument(content, docFormat)
	if err != nil {
		logrus.Warnf("[DocService] Parse failed, using raw content: %v", err)
		doc = &ParsedDocument{
			Content:    content,
			Format:     FormatPlainText,
			RawContent: content,
		}
	}

	result.Content = doc.Content
	result.Metadata = doc.Metadata
	result.Format = doc.Format

	// Step 2: 检测是否为代码文件
	if s.config.CodeChunkingEnabled {
		language := DetectCodeLanguage(content, fileName)
		if language != "" {
			result.Language = language
			logrus.Infof("[DocService] Detected code language: %s", language)
			result.Chunks = CodeChunking(doc.Content, language, s.config.MaxChunkSize)
			return result, nil
		}
	}

	// Step 3: 尝试语义分块（需要 Embedding 支持）
	if s.config.SemanticChunkingConfig.Enabled && embedFn != nil {
		logrus.Info("[DocService] Using semantic chunking")
		result.Chunks = SemanticChunking(ctx, doc.Content, s.config.SemanticChunkingConfig, embedFn)
		if len(result.Chunks) > 0 {
			return result, nil
		}
		logrus.Warn("[DocService] Semantic chunking produced no chunks, falling back")
	}

	// Step 4: Markdown 结构感知分块
	if doc.Format == FormatMarkdown && len(doc.Sections) > 0 && s.config.ChunkingConfig.StructureAware {
		logrus.Info("[DocService] Using structure-aware chunking")
		result.Chunks = StructureAwareChunk(doc, s.config.ChunkingConfig)
		if len(result.Chunks) > 0 {
			return result, nil
		}
	}

	// Step 5: 通用固定窗口分块（兜底）
	logrus.Info("[DocService] Using generic chunking")
	result.Chunks = ChunkDocument(doc.Content, s.config.ChunkingConfig)

	return result, nil
}

// ProcessForIndex 为索引优化的处理流程
// 返回清洗后的内容和分块结果
func (s *DocumentService) ProcessForIndex(ctx context.Context, content, fileName, format string) (string, []Chunk) {
	// 创建一个 embedFn 如果语义分块启用
	var embedFn EmbedFunc
	if s.config.SemanticChunkingConfig.Enabled {
		embedFn = func(ctx context.Context, texts []string) ([][]float64, error) {
			return CachedEmbedStrings(ctx, texts)
		}
	}

	result, err := s.Process(ctx, content, fileName, format, embedFn)
	if err != nil || result == nil {
		return content, ChunkDocument(content, s.config.ChunkingConfig)
	}

	return result.Content, result.Chunks
}

// DetectAndParseDocument 检测格式并解析文档，返回清洗后的内容
// 这是供外部简单调用的便捷方法
func DetectAndParseDocument(content, fileName, formatHint string) (string, DocumentFormat) {
	format := DocumentFormat(formatHint)

	// 自动检测格式
	if format == "" {
		format = DetectFormat(content)

		// 检测代码文件
		if lang := DetectCodeLanguage(content, fileName); lang != "" {
			// 代码文件不需要额外解析，直接返回
			return content, FormatPlainText
		}
	}

	// 解析文档
	doc, err := ParseDocument(content, format)
	if err != nil {
		return content, FormatPlainText
	}

	// 对 Markdown 文档做 embedding 增强
	if doc.Format == FormatMarkdown {
		enhanced := EnhanceContentForEmbedding(doc)
		return enhanced, doc.Format
	}

	return doc.Content, doc.Format
}

// IsCodeFile 判断是否为代码文件
func IsCodeFile(fileName string) bool {
	lowerName := strings.ToLower(fileName)
	codeExtensions := []string{
		".go", ".py", ".js", ".ts", ".jsx", ".tsx",
		".java", ".rs", ".c", ".cpp", ".cc", ".h", ".hpp",
		".rb", ".php", ".swift", ".kt", ".scala", ".cs",
		".sh", ".bash", ".zsh", ".ps1",
		".sql", ".yaml", ".yml", ".json", ".toml", ".xml",
		".dockerfile", ".tf", ".hcl",
	}
	for _, ext := range codeExtensions {
		if strings.HasSuffix(lowerName, ext) {
			return true
		}
	}
	return false
}
