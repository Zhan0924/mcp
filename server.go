/*
┌─────────────────────────────────────────────────────────────────────────┐
│ server.go — MCP 服务器创建与组件装配                                     │
│                                                                         │
│ 职责: 将配置层 (config.go) 转换为领域层对象，完成依赖注入和工具注册         │
│                                                                         │
│ 装配流程:                                                                │
│   ServerConfig ──ToXxxConfig()──→ 领域配置 ──→ 领域对象 ──→ MCP Tools    │
│                                                                         │
│ 设计原理:                                                                │
│   - Config → Domain 转换: TOML 配置结构 ≠ 领域模型，通过 To*Config()      │
│     方法解耦，使领域层不依赖配置格式                                       │
│   - 全局单例: Manager/Cache/Reranker 使用全局初始化函数                    │
│     (InitGlobalXxx)，因为 Embedding 和 Rerank 是跨请求共享的有状态服务     │
│   - Registry 模式: 工具注册与 MCP Server 解耦，便于测试和扩展              │
│                                                                         │
│ 函数:                                                                    │
│   NewMCPServer()          创建 MCP 服务器并注册所有 RAG 工具              │
│   StartServer()           启动 Streamable HTTP 传输层                    │
│   InitEmbeddingManager()  初始化全局 Embedding 管理器 (多 Provider)       │
│   InitCache()             初始化全局 Embedding 缓存 (L1 LRU + L2 Redis)  │
│   InitReranker()          初始化全局 Rerank 精排器                        │
└─────────────────────────────────────────────────────────────────────────┘
*/
package main

import (
	"context"
	"fmt"
	"log"

	"mcp_rag_server/rag"
	"mcp_rag_server/tools"

	"github.com/mark3labs/mcp-go/server"
	redisCli "github.com/redis/go-redis/v9"
)

// NewMCPServer 创建 MCP 服务器
func NewMCPServer(cfg *ServerConfig, redisClient redisCli.UniversalClient, taskQueue *rag.TaskQueue) *server.MCPServer {
	return NewMCPServerWithGraphRAG(cfg, redisClient, taskQueue, nil, nil)
}

// NewMCPServerWithGraphRAG 创建 MCP 服务器（支持 Graph RAG）
func NewMCPServerWithGraphRAG(cfg *ServerConfig, redisClient redisCli.UniversalClient, taskQueue *rag.TaskQueue, graphStore rag.GraphStore, extractor rag.EntityExtractor) *server.MCPServer {
	mcpServer := server.NewMCPServer(
		cfg.Server.Name,
		cfg.Server.Version,
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true),
		server.WithPromptCapabilities(true),
		server.WithLogging(),
	)

	retCfg := cfg.ToRetrieverConfig()
	chunkCfg := cfg.ToChunkingConfig()
	rerankCfg := cfg.ToRerankConfig()

	// VectorStore 多后端工厂
	store := CreateVectorStore(cfg, redisClient)

	registry := tools.NewRegistry()
	tools.RegisterAllRAGTools(registry, store, retCfg, chunkCfg, rerankCfg, cfg.Server.MaxContentSize, taskQueue, graphStore, extractor)
	registry.ApplyToServer(mcpServer)

	return mcpServer
}

// CreateVectorStore 根据配置创建 VectorStore 实例
// 支持 redis / milvus / qdrant 三种后端
func CreateVectorStore(cfg *ServerConfig, redisClient redisCli.UniversalClient) rag.VectorStore {
	storeType := ""
	if cfg.VectorStore != nil {
		storeType = cfg.VectorStore.Type
	}

	switch storeType {
	case "milvus":
		milvusCfg := rag.DefaultMilvusConfig()
		if cfg.VectorStore != nil {
			if cfg.VectorStore.Milvus.Addr != "" {
				milvusCfg.Addr = cfg.VectorStore.Milvus.Addr
			}
			if cfg.VectorStore.Milvus.Token != "" {
				milvusCfg.Token = cfg.VectorStore.Milvus.Token
			}
			if cfg.VectorStore.Milvus.Database != "" {
				milvusCfg.Database = cfg.VectorStore.Milvus.Database
			}
		}
		log.Printf("Using Milvus VectorStore at %s", milvusCfg.Addr)
		return rag.NewMilvusVectorStore(milvusCfg)

	case "qdrant":
		qdrantCfg := rag.DefaultQdrantConfig()
		if cfg.VectorStore != nil {
			if cfg.VectorStore.Qdrant.Addr != "" {
				qdrantCfg.Addr = cfg.VectorStore.Qdrant.Addr
			}
			if cfg.VectorStore.Qdrant.APIKey != "" {
				qdrantCfg.APIKey = cfg.VectorStore.Qdrant.APIKey
			}
		}
		log.Printf("Using Qdrant VectorStore at %s", qdrantCfg.Addr)
		return rag.NewQdrantVectorStore(qdrantCfg)

	default:
		log.Printf("Using Redis VectorStore")
		return rag.NewRedisVectorStore(redisClient)
	}
}

// StartServer 启动 MCP 服务器（Streamable HTTP）
func StartServer(cfg *ServerConfig, redisClient redisCli.UniversalClient, taskQueue *rag.TaskQueue) error {
	return StartServerWithGraphRAG(cfg, redisClient, taskQueue, nil, nil)
}

// StartServerWithGraphRAG 启动 MCP 服务器（支持 Graph RAG）
func StartServerWithGraphRAG(cfg *ServerConfig, redisClient redisCli.UniversalClient, taskQueue *rag.TaskQueue, graphStore rag.GraphStore, extractor rag.EntityExtractor) error {
	mcpServer := NewMCPServerWithGraphRAG(cfg, redisClient, taskQueue, graphStore, extractor)
	httpServer := server.NewStreamableHTTPServer(mcpServer)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("RAG MCP Server listening on %s/mcp", addr)
	return httpServer.Start(addr)
}

// InitEmbeddingManager 初始化 Embedding 管理器
func InitEmbeddingManager(cfg *ServerConfig) *rag.Manager {
	managerCfg := cfg.ToManagerConfig()
	manager := rag.InitGlobalManager(managerCfg)

	providerConfigs := cfg.ToProviderConfigs()
	ctx := context.Background()
	for _, pc := range providerConfigs {
		if err := manager.AddProvider(ctx, pc); err != nil {
			log.Printf("Warning: failed to add embedding provider %s: %v", pc.Name, err)
		}
	}

	manager.Start()
	return manager
}

// InitCache 初始化 Embedding 缓存
func InitCache(cfg *ServerConfig, redisClient redisCli.UniversalClient) *rag.EmbeddingCache {
	cacheCfg := cfg.ToCacheConfig()
	var cacheRedis redisCli.UniversalClient
	if cacheCfg.RedisEnabled {
		cacheRedis = redisClient
	}
	return rag.InitGlobalCache(cacheCfg, cacheRedis)
}

// InitReranker 初始化 Reranker
func InitReranker(cfg *ServerConfig) rag.Reranker {
	rerankCfg := cfg.ToRerankConfig()
	return rag.InitGlobalReranker(rerankCfg)
}

// InitCompressor 初始化上下文压缩器
func InitCompressor(cfg *ServerConfig) rag.ContextCompressor {
	compCfg := rag.DefaultCompressorConfig()
	// 从 RetrieverConfig 中读取压缩器配置
	retCfg := cfg.ToRetrieverConfig()
	if retCfg.CompressorEnabled && retCfg.CompressorConfig != nil {
		compCfg = *retCfg.CompressorConfig
		compCfg.Enabled = true
	}
	return rag.InitGlobalCompressor(compCfg)
}

// InitGraphRAG 初始化 Graph RAG 组件
func InitGraphRAG(cfg *ServerConfig) (rag.GraphStore, rag.EntityExtractor) {
	graphCfg := cfg.ToGraphRAGConfig()
	if !graphCfg.Enabled {
		log.Println("Graph RAG disabled")
		return nil, nil
	}

	var graphStore rag.GraphStore
	var err error

	switch graphCfg.GraphStoreType {
	case "neo4j":
		graphStore, err = rag.NewNeo4jGraphStore(context.Background(), graphCfg.Neo4j)
		if err != nil {
			log.Printf("Warning: Failed to connect to Neo4j: %v, falling back to in-memory", err)
			graphStore = rag.NewInMemoryGraphStore()
		}
	default:
		graphStore = rag.NewInMemoryGraphStore()
	}

	var extractor rag.EntityExtractor
	switch graphCfg.EntityExtractor {
	case "llm":
		extractor = rag.NewLLMEntityExtractor(graphCfg.LLMExtractor)
		log.Printf("Using LLM entity extractor (model=%s)", graphCfg.LLMExtractor.Model)
	default:
		extractor = rag.NewSimpleEntityExtractor()
		log.Println("Using simple rule-based entity extractor")
	}

	log.Printf("Graph RAG initialized (store=%s, extractor=%s)", graphCfg.GraphStoreType, graphCfg.EntityExtractor)
	return graphStore, extractor
}
