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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"mcp_rag_server/middleware"
	"mcp_rag_server/rag"
	"mcp_rag_server/tools"

	"github.com/mark3labs/mcp-go/server"
	redisCli "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
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
	uploadCfg := cfg.ToUploadConfig()

	// VectorStore 多后端工厂
	store := CreateVectorStore(cfg, redisClient)

	// UploadStore：如果启用，创建并传入工具层
	var uploadStore *rag.UploadStore
	if uploadCfg.Enabled {
		uploadStore = rag.NewUploadStore(redisClient, uploadCfg)
	}

	registry := tools.NewRegistry()
	tools.RegisterAllRAGTools(registry, store, retCfg, chunkCfg, rerankCfg, cfg.Server.MaxContentSize, taskQueue, uploadStore, uploadCfg, graphStore, extractor)
	registry.ApplyToServer(mcpServer)

	return mcpServer
}

// CreateVectorStore 根据配置创建 VectorStore 实例
// 支持 redis / milvus / qdrant 三种后端，统一包裹 CircuitBreaker
func CreateVectorStore(cfg *ServerConfig, redisClient redisCli.UniversalClient) rag.VectorStore {
	storeType := ""
	if cfg.VectorStore != nil {
		storeType = cfg.VectorStore.Type
	}

	var inner rag.VectorStore

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
		inner = rag.NewMilvusVectorStore(milvusCfg)

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
		inner = rag.NewQdrantVectorStore(qdrantCfg)

	default:
		log.Printf("Using Redis VectorStore")
		inner = rag.NewRedisVectorStore(redisClient)
	}

	// 包裹 CircuitBreaker 保护层
	cbCfg := rag.DefaultStoreCircuitBreakerConfig()
	store := rag.NewStoreCircuitBreaker(inner, cbCfg, nil)
	log.Printf("VectorStore circuit breaker enabled (threshold=%d, timeout=%v)", cbCfg.FailureThreshold, cbCfg.Timeout)
	return store
}

// StartServer 启动 MCP 服务器（Streamable HTTP）
func StartServer(cfg *ServerConfig, redisClient redisCli.UniversalClient, taskQueue *rag.TaskQueue) error {
	return StartServerWithGraphRAG(cfg, redisClient, taskQueue, nil, nil)
}

// StartServerWithGraphRAG 启动 MCP 服务器（支持 Graph RAG + 文件上传）
func StartServerWithGraphRAG(cfg *ServerConfig, redisClient redisCli.UniversalClient, taskQueue *rag.TaskQueue, graphStore rag.GraphStore, extractor rag.EntityExtractor) error {
	return StartServerFull(cfg, redisClient, taskQueue, graphStore, extractor, nil)
}

// BuildServerFull 构建 HTTP Server（不启动），用于外部控制优雅关闭。
// 返回 *http.Server，调用方可通过 srv.ListenAndServe() 启动，
// 再通过 srv.Shutdown(ctx) 优雅关闭已有连接。
func BuildServerFull(cfg *ServerConfig, redisClient redisCli.UniversalClient, taskQueue *rag.TaskQueue, graphStore rag.GraphStore, extractor rag.EntityExtractor, uploadStore *rag.UploadStore) *http.Server {
	mcpServer := NewMCPServerWithGraphRAG(cfg, redisClient, taskQueue, graphStore, extractor)

	// Redis Session Manager：支持多实例部署
	sessionMgr := middleware.NewRedisSessionManager(redisClient, middleware.DefaultRedisSessionConfig())
	httpServer := server.NewStreamableHTTPServer(mcpServer,
		server.WithSessionIdManager(sessionMgr),
	)

	// 组合路由：MCP 端点 + 健康检查 + 文件上传端点
	mux := http.NewServeMux()
	mux.Handle("/mcp", httpServer)
	mux.HandleFunc("/health", handleHealthCheck(cfg, redisClient))

	if uploadStore != nil {
		mux.HandleFunc("/upload", handleFileUpload(uploadStore))
		logrus.Infof("[Upload] POST /upload enabled (max=%dMB)",
			uploadStore.GetConfig().MaxUploadSize/(1024*1024))
	}

	// ── 企业级中间件链 ──────────────────────────────────────────────
	slogger := middleware.NewLogger(middleware.LogConfig{Level: "info", Format: "json"})

	// 限流中间件
	rlCfg := middleware.DefaultRateLimitConfig()
	rateLimiter := middleware.NewRateLimitMiddleware(rlCfg, slogger)

	// 审计日志中间件
	auditMW := middleware.NewAuditMiddleware(middleware.AuditConfig{Enabled: true}, slogger)

	// 分布式追踪中间件
	tracerCfg := middleware.DefaultTracerConfig()
	tracerCfg.ServiceName = cfg.Server.Name
	tracer := middleware.NewTracer(tracerCfg, slogger)

	// Prometheus 指标
	prom := middleware.NewPrometheusMetrics()

	// 认证中间件（默认关闭，生产环境通过 config.toml [auth] enabled=true 启用）
	authCfg := middleware.AuthConfig{
		Enabled:   false,
		Mode:      "api_key",
		SkipPaths: []string{"/health", "/metrics"},
		APIKeys:   make(map[string]int64),
	}
	// 从环境变量 AUTH_API_KEYS 读取 API Keys（格式: "key1:uid1,key2:uid2"）
	if keys := os.Getenv("AUTH_API_KEYS"); keys != "" {
		authCfg.Enabled = true
		for _, pair := range strings.Split(keys, ",") {
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) == 2 {
				if uid, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
					authCfg.APIKeys[parts[0]] = uid
				}
			}
		}
	}
	if secret := os.Getenv("AUTH_JWT_SECRET"); secret != "" {
		authCfg.JWTSecret = secret
	}
	authMW := middleware.NewAuthMiddleware(authCfg, slogger)

	// 中间件链：Prometheus → 追踪 → 认证 → 限流 → 审计 → 路由
	// 注意：不使用 TimeoutMiddleware，因为 /mcp 是 SSE 长连接，超时会断开连接
	handler := middleware.Chain(mux,
		prom.HTTPMiddleware,
		tracer.Handler,
		authMW.Handler,
		rateLimiter.Handler,
		auditMW.Handler,
	)

	// Prometheus /metrics 端点（替代旧的 JSON metrics）
	mux.Handle("/metrics", prom.Handler())

	// JSON /metrics-json 端点（向后兼容）
	jsonMetrics := middleware.NewMetrics(slogger)
	mux.Handle("/metrics-json", jsonMetrics.MetricsHandler())

	// Traces 端点
	mux.HandleFunc("/traces", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		traces := tracer.GetRecentTraces(50)
		json.NewEncoder(w).Encode(traces)
	})

	log.Printf("Middleware enabled: prometheus, tracing, rate_limit(%.0f rps), audit", rlCfg.GlobalRPS)
	// ─────────────────────────────────────────────────────────────────

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("RAG MCP Server listening on %s/mcp", addr)
	log.Printf("Health check available at %s/health", addr)
	log.Printf("Metrics (Prometheus) at %s/metrics", addr)
	log.Printf("Metrics (JSON) at %s/metrics-json", addr)
	log.Printf("Traces available at %s/traces", addr)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	if cfg.Server.TLS.Enabled {
		log.Printf("TLS enabled: cert=%s, key=%s", cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
	}
	return srv
}

// StartServerFull 启动 MCP 服务器（完整版，向后兼容）
func StartServerFull(cfg *ServerConfig, redisClient redisCli.UniversalClient, taskQueue *rag.TaskQueue, graphStore rag.GraphStore, extractor rag.EntityExtractor, uploadStore *rag.UploadStore) error {
	srv := BuildServerFull(cfg, redisClient, taskQueue, graphStore, extractor, uploadStore)
	if cfg.Server.TLS.Enabled {
		return srv.ListenAndServeTLS(cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
	}
	return srv.ListenAndServe()
}

// handleHealthCheck 健康检查端点
// GET /health
//
// 返回服务状态、版本信息和依赖组件连通性，用于 Docker HEALTHCHECK、
// 负载均衡探针和运维 curl 快速验证，避免直接 GET /mcp 挂在 SSE 长连接上。
//
// 响应示例:
//
//	{
//	  "status": "healthy",
//	  "server": "rag-mcp-server",
//	  "version": "2.0.0",
//	  "redis": "ok",
//	  "uptime": "2h15m30s"
//	}
func handleHealthCheck(cfg *ServerConfig, redisClient redisCli.UniversalClient) http.HandlerFunc {
	startTime := time.Now()

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		result := map[string]interface{}{
			"status":  "healthy",
			"server":  cfg.Server.Name,
			"version": cfg.Server.Version,
			"uptime":  time.Since(startTime).Round(time.Second).String(),
		}

		// 检测 Redis 连通性（2s 超时，防止阻塞健康检查）
		httpStatus := http.StatusOK
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := redisClient.Ping(ctx).Err(); err != nil {
			result["status"] = "degraded"
			result["redis"] = fmt.Sprintf("error: %v", err)
			httpStatus = http.StatusServiceUnavailable
		} else {
			result["redis"] = "ok"
		}

		// 检测 Embedding Manager 状态
		manager := rag.GetGlobalManager()
		if manager != nil {
			stats := manager.GetStats()
			activeProviders := 0
			for _, s := range stats {
				if s.Status == "active" {
					activeProviders++
				}
			}
			result["embedding_providers"] = fmt.Sprintf("%d/%d active", activeProviders, len(stats))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		json.NewEncoder(w).Encode(result)
	}
}

// handleFileUpload 处理文件上传请求
// POST /upload  (multipart/form-data)
//   - file: 文件二进制 (必须)
//   - file_name: 文件名 (可选，默认从 multipart header 取)
//
// 响应: {"upload_id": "upl_xxxx", "size": 15728640, "expires_in": 3600}
func handleFileUpload(uploadStore *rag.UploadStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		maxSize := uploadStore.GetConfig().MaxUploadSize
		r.Body = http.MaxBytesReader(w, r.Body, maxSize)

		if err := r.ParseMultipartForm(32 << 20); err != nil {
			logrus.Warnf("[Upload] Parse multipart failed: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("invalid multipart form or file too large (max %dMB)",
					maxSize/(1024*1024)),
			})
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "file field is required"})
			return
		}
		defer file.Close()

		data, err := io.ReadAll(file)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to read file"})
			return
		}

		// 文件名：优先用表单字段，其次用 multipart header
		fileName := r.FormValue("file_name")
		if fileName == "" {
			fileName = header.Filename
		}

		format := string(rag.DetectFormatByFileName(fileName))

		uploadID := uploadStore.GenerateID()
		meta := rag.UploadMeta{
			FileName: fileName,
			Format:   format,
		}

		ctx := r.Context()
		if err := uploadStore.Save(ctx, uploadID, data, meta); err != nil {
			logrus.Errorf("[Upload] Save failed: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to save upload"})
			return
		}

		ttlSeconds := int(uploadStore.GetConfig().TTL.Seconds())

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"upload_id":  uploadID,
			"file_name":  fileName,
			"format":     format,
			"size":       len(data),
			"expires_in": ttlSeconds,
		})
	}
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
