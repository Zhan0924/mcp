package rag

// ──────────────────────────────────────────────────────────────────────────────
//  Compile-time Interface Compliance Checks — P2 Code Quality
//
//  确保所有实现类型在编译期满足接口约束，防止接口方法签名变更后
//  某个实现忘记更新而导致运行时 panic。
// ──────────────────────────────────────────────────────────────────────────────

// VectorStore 实现检查
var _ VectorStore = (*RedisVectorStore)(nil)
var _ VectorStore = (*MilvusVectorStore)(nil)
var _ VectorStore = (*QdrantVectorStore)(nil)
var _ VectorStore = (*StoreCircuitBreaker)(nil)

// GraphStore 实现检查
var _ GraphStore = (*Neo4jGraphStore)(nil)
var _ GraphStore = (*InMemoryGraphStore)(nil)
