package rag

import (
	"context"
	"log"
	"unsafe"
)

// ──────────────────────────────────────────────────────────────────────────────
//  Embedding float32 优化层 — P2 性能优化
//
//  问题：eino 库 Embedding API 返回 []float64（8 字节/维度），但 Redis/Milvus/Qdrant
//  全部使用 []float32（4 字节/维度）。当前在 retriever.go 每次索引和搜索时
//  都需要 float64→float32 逐元素转换，内存占用翻倍。
//
//  方案：
//    1. EmbedF32Adapter — 包装 EmbedStrings()，在 API 返回后立即转换为 float32
//    2. 缓存层使用 float32（减少 L1 LRU 内存 50%）
//    3. 提供 Float64ToFloat32Batch / Float32ToFloat64Batch 批量转换工具
//
//  内存节省（以 1536 维 text-embedding-v4 为例）：
//    float64: 1536 * 8 = 12,288 字节/向量
//    float32: 1536 * 4 =  6,144 字节/向量 (节省 50%)
//    1000 个文档 × 50 个 chunk: 600MB → 300MB
//
//  精度影响：
//    float32 有效精度 ~7 位十进制，余弦相似度误差 < 1e-6，对 RAG 无影响。
// ──────────────────────────────────────────────────────────────────────────────

// Embedding32 float32 类型的 embedding 向量
type Embedding32 = []float32

// EmbedF32Func float32 Embedding 函数签名
type EmbedF32Func func(ctx context.Context, texts []string) ([]Embedding32, error)

// EmbedF32Adapter 将 float64 Embedding API 适配为 float32 输出
// 在 API 调用后立即转换，后续管线全部使用 float32
type EmbedF32Adapter struct {
	embedFn func(ctx context.Context, texts []string) ([][]float64, error)
}

// NewEmbedF32Adapter 创建 float32 适配器
func NewEmbedF32Adapter(embedFn func(ctx context.Context, texts []string) ([][]float64, error)) *EmbedF32Adapter {
	return &EmbedF32Adapter{embedFn: embedFn}
}

// EmbedStrings 调用底层 float64 API 并立即转换为 float32
func (a *EmbedF32Adapter) EmbedStrings(ctx context.Context, texts []string) ([]Embedding32, error) {
	f64Results, err := a.embedFn(ctx, texts)
	if err != nil {
		return nil, err
	}

	f32Results := make([]Embedding32, len(f64Results))
	for i, vec := range f64Results {
		f32Results[i] = Float64ToFloat32(vec)
	}
	return f32Results, nil
}

// ── 批量转换工具 ────────────────────────────────────────────────────────────

// Float64ToFloat32 将 float64 切片转换为 float32
// 逐元素转换，精度损失 < 1e-6，对向量相似度计算无影响
func Float64ToFloat32(f64 []float64) []float32 {
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}

// Float32ToFloat64 将 float32 切片转换为 float64（用于兼容旧接口）
func Float32ToFloat64(f32 []float32) []float64 {
	f64 := make([]float64, len(f32))
	for i, v := range f32 {
		f64[i] = float64(v)
	}
	return f64
}

// Float64ToFloat32Batch 批量转换 float64 向量为 float32
func Float64ToFloat32Batch(batch [][]float64) []Embedding32 {
	result := make([]Embedding32, len(batch))
	for i, vec := range batch {
		result[i] = Float64ToFloat32(vec)
	}
	return result
}

// Float32ToFloat64Batch 批量转换 float32 向量为 float64（向后兼容）
func Float32ToFloat64Batch(batch []Embedding32) [][]float64 {
	result := make([][]float64, len(batch))
	for i, vec := range batch {
		result[i] = Float32ToFloat64(vec)
	}
	return result
}

// ── 内存统计 ────────────────────────────────────────────────────────────────

// EmbeddingMemoryStats 向量内存统计
type EmbeddingMemoryStats struct {
	VectorCount  int    `json:"vector_count"`
	Dimensions   int    `json:"dimensions"`
	Float64Bytes int64  `json:"float64_bytes"` // 如果用 float64 的内存
	Float32Bytes int64  `json:"float32_bytes"` // 实际 float32 的内存
	SavedBytes   int64  `json:"saved_bytes"`   // 节省的内存
	SavedPercent string `json:"saved_percent"` // 节省百分比
}

// CalculateMemorySavings 计算 float32 优化的内存节省
func CalculateMemorySavings(vectorCount, dimensions int) EmbeddingMemoryStats {
	f64Size := int64(vectorCount) * int64(dimensions) * int64(unsafe.Sizeof(float64(0)))
	f32Size := int64(vectorCount) * int64(dimensions) * int64(unsafe.Sizeof(float32(0)))
	saved := f64Size - f32Size

	return EmbeddingMemoryStats{
		VectorCount:  vectorCount,
		Dimensions:   dimensions,
		Float64Bytes: f64Size,
		Float32Bytes: f32Size,
		SavedBytes:   saved,
		SavedPercent: "50%",
	}
}

// LogMemorySavings 日志输出内存节省信息
func LogMemorySavings(vectorCount, dimensions int) {
	stats := CalculateMemorySavings(vectorCount, dimensions)
	log.Printf("[EmbeddingF32] Memory savings: %d vectors × %d dims — float64: %.1fMB, float32: %.1fMB, saved: %.1fMB (%s)",
		stats.VectorCount, stats.Dimensions,
		float64(stats.Float64Bytes)/(1024*1024),
		float64(stats.Float32Bytes)/(1024*1024),
		float64(stats.SavedBytes)/(1024*1024),
		stats.SavedPercent,
	)
}
