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
	mcpServer := server.NewMCPServer(
		cfg.Server.Name,
		cfg.Server.Version,
		server.WithToolCapabilities(true),
		server.WithLogging(),
	)

	retCfg := cfg.ToRetrieverConfig()
	chunkCfg := cfg.ToChunkingConfig()
	rerankCfg := cfg.ToRerankConfig()
	store := rag.NewRedisVectorStore(redisClient)

	registry := tools.NewRegistry()
	tools.RegisterAllRAGTools(registry, store, retCfg, chunkCfg, rerankCfg, cfg.Server.MaxContentSize, taskQueue)
	registry.ApplyToServer(mcpServer)

	return mcpServer
}

// StartServer 启动 MCP 服务器（Streamable HTTP）
func StartServer(cfg *ServerConfig, redisClient redisCli.UniversalClient, taskQueue *rag.TaskQueue) error {
	mcpServer := NewMCPServer(cfg, redisClient, taskQueue)
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
