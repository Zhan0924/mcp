/*
rag/errors.go — RAG 子系统统一错误体系

设计原则:

 1. 类型化错误码（ErrorCode）: 以 string 为底层类型而非 int，
    使错误码在 JSON 响应中直接可读（如 "RAG_001"），无需额外映射表。
    调用方可通过 HasErrorCode() 做程序化分支判断，而非字符串匹配。

 2. 错误链（Error Chain）: RAGError 实现 Unwrap() 方法，
    支持 Go 1.13+ 的 errors.Is() / errors.As() 错误链遍历。
    例如: 当 Redis 超时触发 ErrCodeSearchFailed 时，Cause 保留原始 redis 错误，
    上层可通过 errors.Is(err, context.DeadlineExceeded) 识别超时根因。

 3. 三层错误信息: Code（机器可读）+ Message（人类可读摘要）+ Detail（现场上下文），
    tools 层直接将 RAGError 格式化后返回给 MCP 客户端，无需额外包装。

导出类型:
  - ErrorCode          — 错误码字面量类型（string 底层）
  - RAGError           — 统一错误结构体，实现 error + Unwrap 接口

导出常量（18 个错误码）:

	ErrCodeIndexNotFound  .. ErrCodeManagerNotReady  (RAG_001 ~ RAG_018)

导出函数:
  - NewRAGError(code, detail, cause)   — 标准构造器
  - NewRAGErrorf(code, cause, fmt...)  — 格式化 detail 构造器
  - IsRAGError(err) (*RAGError, bool)  — 类型断言提取
  - HasErrorCode(err, code) bool       — 错误码匹配判断
  - ErrorCodeMessage(code) string      — 查询错误码默认消息

内部变量:
  - errorMessages      — 错误码 → 默认消息映射表
*/
package rag

import (
	"fmt"
	"time"
)

// ErrorCategory 错误分类，用于自动化响应策略
type ErrorCategory string

const (
	CategoryInput      ErrorCategory = "input"      // 客户端输入错误（4xx）
	CategoryTransient  ErrorCategory = "transient"  // 瞬时故障，可重试（5xx retryable）
	CategoryPermanent  ErrorCategory = "permanent"  // 永久性故障（5xx not retryable）
	CategoryDependency ErrorCategory = "dependency" // 依赖服务故障
	CategoryQuota      ErrorCategory = "quota"      // 配额/限流
)

// ErrorCode RAG 错误码。
// 底层类型为 string 而非 int，这样在 JSON 序列化时直接输出 "RAG_001" 等可读文本，
// 无需客户端维护数字到含义的映射表。
type ErrorCode string

// 错误码常量: RAG_001 ~ RAG_018
// 按故障域分组:
//
//	001-006: 核心读写路径（索引 / embedding / 检索 / 输入校验）
//	007-009: Embedding Provider 可用性（无 provider / 超时 / 熔断）
//	010-012: 辅助功能（重排序 / 解析 / 缓存）
//	013-018: 系统级（配置 / 文档不存在 / 批量 / 混合检索 / 格式 / 就绪状态）
const (
	ErrCodeIndexNotFound     ErrorCode = "RAG_001" // Redis 中找不到指定用户的索引
	ErrCodeEmbeddingFailed   ErrorCode = "RAG_002" // 调用 Embedding API 失败
	ErrCodeIndexCreateFailed ErrorCode = "RAG_003" // FT.CREATE 创建索引失败
	ErrCodeSearchFailed      ErrorCode = "RAG_004" // FT.SEARCH 检索失败
	ErrCodeInvalidInput      ErrorCode = "RAG_005" // 请求参数校验不通过
	ErrCodeContentTooLarge   ErrorCode = "RAG_006" // 文档内容超过大小上限
	ErrCodeNoProviders       ErrorCode = "RAG_007" // 所有 Embedding Provider 均不可用
	ErrCodeProviderTimeout   ErrorCode = "RAG_008" // Embedding Provider 请求超时
	ErrCodeCircuitOpen       ErrorCode = "RAG_009" // 熔断器已打开，拒绝请求
	ErrCodeRerankFailed      ErrorCode = "RAG_010" // Reranker 重排序失败
	ErrCodeParseFailed       ErrorCode = "RAG_011" // 文档解析失败（格式损坏等）
	ErrCodeCacheFailed       ErrorCode = "RAG_012" // 缓存读写异常
	ErrCodeConfigInvalid     ErrorCode = "RAG_013" // 配置校验不通过
	ErrCodeDocumentNotFound  ErrorCode = "RAG_014" // 指定文档 ID 不存在
	ErrCodeBatchFailed       ErrorCode = "RAG_015" // 批量操作部分或全部失败
	ErrCodeHybridMergeFailed ErrorCode = "RAG_016" // 混合检索结果合并失败
	ErrCodeUnsupportedFormat ErrorCode = "RAG_017" // 不支持的文档格式
	ErrCodeManagerNotReady   ErrorCode = "RAG_018" // EmbeddingManager 尚未初始化完成
)

// errorMessages 错误码 → 默认人类可读消息。
// Message 字段用英文，保证国际化日志可读性；Detail 字段由调用方填入现场上下文。
var errorMessages = map[ErrorCode]string{
	ErrCodeIndexNotFound:     "Index not found",
	ErrCodeEmbeddingFailed:   "Embedding generation failed",
	ErrCodeIndexCreateFailed: "Index creation failed",
	ErrCodeSearchFailed:      "Search operation failed",
	ErrCodeInvalidInput:      "Invalid input parameter",
	ErrCodeContentTooLarge:   "Content exceeds size limit",
	ErrCodeNoProviders:       "No embedding providers available",
	ErrCodeProviderTimeout:   "Embedding provider timed out",
	ErrCodeCircuitOpen:       "Circuit breaker is open",
	ErrCodeRerankFailed:      "Rerank operation failed",
	ErrCodeParseFailed:       "Document parsing failed",
	ErrCodeCacheFailed:       "Cache operation failed",
	ErrCodeConfigInvalid:     "Configuration is invalid",
	ErrCodeDocumentNotFound:  "Document not found",
	ErrCodeBatchFailed:       "Batch operation failed",
	ErrCodeHybridMergeFailed: "Hybrid search merge failed",
	ErrCodeUnsupportedFormat: "Unsupported document format",
	ErrCodeManagerNotReady:   "Embedding manager not initialized",
}

// RAGError 统一 RAG 错误类型。
// 实现了 error 接口和 Unwrap() 方法，支持:
//   - fmt.Errorf / log 直接打印（通过 Error()）
//   - errors.Is(err, target) 沿 Cause 链查找特定错误
//   - errors.As(err, &target) 沿 Cause 链做类型断言
type RAGError struct {
	Code       ErrorCode     // 机器可读错误码，用于程序化分支
	Category   ErrorCategory // 错误分类（input/transient/permanent/dependency/quota）
	Message    string        // 该错误码对应的通用描述（来自 errorMessages）
	UserMsg    string        // 面向终端用户的友好提示（可直接展示到 UI）
	Detail     string        // 本次调用的具体上下文信息
	Cause      error         // 原始底层错误，构成错误链
	RetryAfter time.Duration // 可重试时的建议等待时间（0 表示不可重试）
	HTTPStatus int           // 建议的 HTTP 状态码（0 使用默认映射）
}

// Error 格式: [RAG_XXX] 通用描述: 具体细节 (caused by: 底层错误)
func (e *RAGError) Error() string {
	msg := fmt.Sprintf("[%s] %s", e.Code, e.Message)
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	if e.Cause != nil {
		msg += fmt.Sprintf(" (caused by: %v)", e.Cause)
	}
	return msg
}

// Unwrap 返回被包装的底层错误，使 errors.Is / errors.As 能沿链向下遍历。
func (e *RAGError) Unwrap() error {
	return e.Cause
}

// NewRAGError 根据错误码创建 RAGError。
// Message 自动从 errorMessages 映射表查询；detail 由调用方提供现场信息；
// cause 为触发此错误的底层 error（可为 nil）。
func NewRAGError(code ErrorCode, detail string, cause error) *RAGError {
	msg, ok := errorMessages[code]
	if !ok {
		msg = "Unknown error"
	}
	meta := errorMeta[code]
	return &RAGError{
		Code:       code,
		Category:   meta.Category,
		Message:    msg,
		UserMsg:    meta.UserMsg,
		Detail:     detail,
		Cause:      cause,
		RetryAfter: meta.RetryAfter,
		HTTPStatus: meta.HTTPStatus,
	}
}

// NewRAGErrorf 与 NewRAGError 等价，但 detail 支持 fmt.Sprintf 风格的格式化。
func NewRAGErrorf(code ErrorCode, cause error, format string, args ...interface{}) *RAGError {
	return NewRAGError(code, fmt.Sprintf(format, args...), cause)
}

// IsRAGError 尝试将 err 断言为 *RAGError。
// 适用于需要同时获取错误码和错误体的场景。
// 注意: 此函数只做直接类型断言，不沿错误链遍历；
// 若需沿链查找，应使用 errors.As(err, &ragErr)。
func IsRAGError(err error) (*RAGError, bool) {
	if ragErr, ok := err.(*RAGError); ok {
		return ragErr, true
	}
	return nil, false
}

// HasErrorCode 快捷判断: err 是否为 *RAGError 且错误码等于 code。
// 典型用法: if HasErrorCode(err, ErrCodeCircuitOpen) { /* 触发降级 */ }
func HasErrorCode(err error, code ErrorCode) bool {
	ragErr, ok := IsRAGError(err)
	if !ok {
		return false
	}
	return ragErr.Code == code
}

// ErrorCodeMessage 查询指定错误码的默认消息文本。
// 用于在不构造完整 RAGError 的场景下获取人类可读描述。
func ErrorCodeMessage(code ErrorCode) string {
	if msg, ok := errorMessages[code]; ok {
		return msg
	}
	return "Unknown error"
}

// IsRetryable 判断此错误是否可以重试
func (e *RAGError) IsRetryable() bool {
	return e.Category == CategoryTransient || e.Category == CategoryDependency
}

// GetHTTPStatus 返回建议的 HTTP 状态码
func (e *RAGError) GetHTTPStatus() int {
	if e.HTTPStatus > 0 {
		return e.HTTPStatus
	}
	switch e.Category {
	case CategoryInput:
		return 400
	case CategoryQuota:
		return 429
	case CategoryTransient, CategoryDependency:
		return 503
	default:
		return 500
	}
}

// GetUserMessage 返回面向用户的友好消息
func (e *RAGError) GetUserMessage() string {
	if e.UserMsg != "" {
		return e.UserMsg
	}
	return e.Message
}

// WithUserMsg 设置用户友好消息（链式调用）
func (e *RAGError) WithUserMsg(msg string) *RAGError {
	e.UserMsg = msg
	return e
}

// WithRetryAfter 设置重试等待时间（链式调用）
func (e *RAGError) WithRetryAfter(d time.Duration) *RAGError {
	e.RetryAfter = d
	return e
}

// ── 错误码元数据 ─────────────────────────────────────────────────────────────

type errorCodeMeta struct {
	Category   ErrorCategory
	UserMsg    string
	RetryAfter time.Duration
	HTTPStatus int
}

// errorMeta 错误码 → 自动化元数据
var errorMeta = map[ErrorCode]errorCodeMeta{
	ErrCodeIndexNotFound:     {CategoryInput, "请求的索引不存在，请先索引文档", 0, 404},
	ErrCodeEmbeddingFailed:   {CategoryDependency, "文本向量化服务暂时不可用，请稍后重试", 5 * time.Second, 503},
	ErrCodeIndexCreateFailed: {CategoryTransient, "创建索引失败，请稍后重试", 3 * time.Second, 503},
	ErrCodeSearchFailed:      {CategoryTransient, "搜索服务暂时不可用，请稍后重试", 3 * time.Second, 503},
	ErrCodeInvalidInput:      {CategoryInput, "请求参数无效，请检查输入", 0, 400},
	ErrCodeContentTooLarge:   {CategoryInput, "文档内容过大，请缩小文件后重试", 0, 413},
	ErrCodeNoProviders:       {CategoryDependency, "AI 服务暂时不可用，请稍后重试", 10 * time.Second, 503},
	ErrCodeProviderTimeout:   {CategoryTransient, "AI 服务响应超时，请稍后重试", 5 * time.Second, 504},
	ErrCodeCircuitOpen:       {CategoryDependency, "服务正在恢复中，请稍后重试", 30 * time.Second, 503},
	ErrCodeRerankFailed:      {CategoryTransient, "搜索结果优化失败，返回未优化结果", 0, 200},
	ErrCodeParseFailed:       {CategoryInput, "文档格式无法解析，请检查文件是否损坏", 0, 422},
	ErrCodeCacheFailed:       {CategoryTransient, "缓存服务异常，已降级处理", 0, 200},
	ErrCodeConfigInvalid:     {CategoryPermanent, "服务配置异常，请联系管理员", 0, 500},
	ErrCodeDocumentNotFound:  {CategoryInput, "指定的文档不存在", 0, 404},
	ErrCodeBatchFailed:       {CategoryTransient, "批量操作部分失败，请重试", 3 * time.Second, 207},
	ErrCodeHybridMergeFailed: {CategoryTransient, "搜索结果合并失败，已降级处理", 0, 200},
	ErrCodeUnsupportedFormat: {CategoryInput, "不支持的文件格式", 0, 415},
	ErrCodeManagerNotReady:   {CategoryTransient, "服务正在启动中，请稍后重试", 5 * time.Second, 503},
}
