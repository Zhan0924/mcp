/*
config.go — TOML 配置加载、校验与领域配置转换

本文件采用"两层配置"架构：
  - TOML 层（ServerConfig 及其子 Section）: 直接映射 config.toml 文件结构，
    字段均为原始类型，负责反序列化。
  - 领域层（rag.XxxConfig）: 各子系统实际使用的配置，包含经过校验和默认值
    填充的运行时参数。

两层之间通过 To*Config() 系列方法桥接，使得 TOML 结构变更不会侵入业务逻辑。

=== 结构概览 ===

常量:

	DefaultMaxContentSize  — 文档内容大小上限（防止超大文件打爆内存）

类型:

	ServerConfig           — TOML 顶层配置，聚合所有子配置段
	ServerSection          — 服务器基础信息（端口/名称/实例标识）
	RedisSection           — Redis 连接配置，支持 standalone/sentinel/cluster 三种模式
	RetrieverSection       — 向量检索器配置（索引算法/批量参数/混合检索权重）
	ChunkingSection        — 文档分块策略参数
	EmbMgrSection          — Embedding 管理器配置（负载均衡/重试/熔断/健康检查）
	ProviderSection        — 单个 Embedding Provider 的连接与限流参数
	CacheSection           — 本地 + Redis 二级缓存策略
	RerankSection          — 重排序服务配置
	AsyncIndexSection      — 异步索引任务队列配置（基于 Redis Stream）
	MigrationSection       — Schema 版本迁移配置
	Duration               — time.Duration 的 TOML 友好包装（支持 "30s"/"5m" 等字符串）

函数:

	resolveEnvVar(value)           — 将 ${VAR} 占位符替换为环境变量值
	LoadConfig(path)               — 加载 TOML → 填充默认值 → 替换环境变量 → 校验
	NewConfigError(phase, detail)  — 统一构造配置错误（携带阶段标识便于定位）

方法（ServerConfig）:

	Validate()             — 快速失败校验：Redis 模式约束、Provider 必填项、参数合理性
	ToRetrieverConfig()    — 转换为 rag.RetrieverConfig（含 HNSW 参数条件组装）
	ToChunkingConfig()     — 转换为 rag.ChunkingConfig
	ToManagerConfig()      — 转换为 rag.ManagerConfig（以 DefaultManagerConfig 为基础覆盖）
	ToProviderConfigs()    — 转换为 []rag.ProviderConfig
	ToCacheConfig()        — 转换为 rag.CacheConfig
	ToTaskQueueConfig()    — 转换为 rag.TaskQueueConfig
	ToRerankConfig()       — 转换为 rag.RerankConfig
	ToMigrationConfig()    — 转换为 rag.MigrationConfig
*/
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"mcp_rag_server/rag"

	"github.com/BurntSushi/toml"
	"github.com/sirupsen/logrus"
)

const (
	DefaultMaxContentSize = 10 * 1024 * 1024 // 10MB
)

// ServerConfig 顶层配置结构。
// 字段与 config.toml 中的 section 一一对应，充当"TOML 反序列化目标"。
// 所有业务逻辑不应直接读取本结构，而应通过 To*Config() 转为领域配置使用，
// 这样 TOML schema 的演进不会影响下游子系统。
type ServerConfig struct {
	Server            ServerSection            `toml:"server"`
	Redis             RedisSection             `toml:"redis"`
	Retriever         RetrieverSection         `toml:"retriever"`
	Chunking          ChunkingSection          `toml:"chunking"`
	EmbMgr            EmbMgrSection            `toml:"embedding_manager"`
	Providers         []ProviderSection        `toml:"embedding_providers"`
	Cache             CacheSection             `toml:"cache"`
	Rerank            RerankSection            `toml:"rerank"`
	AsyncIndex        AsyncIndexSection        `toml:"async_index"`
	Migration         MigrationSection         `toml:"migration"`
	HyDE              HyDESection              `toml:"hyde"`
	VectorStore       *VectorStoreSection      `toml:"vector_store"`
	GraphRAG          GraphRAGSection          `toml:"graph_rag"`
	Upload            UploadSection            `toml:"upload"`
	MultiQuery        MultiQuerySection        `toml:"multi_query"`
	ContextCompressor ContextCompressorSection `toml:"context_compressor"`
}

// ServerSection 服务器配置
type ServerSection struct {
	Port           int    `toml:"port"`
	Name           string `toml:"name"`
	Version        string `toml:"version"`
	MaxContentSize int    `toml:"max_content_size"`
	InstanceID     string `toml:"instance_id"`
}

// RedisSection Redis 配置。
// 同时保留 Addr（单地址）和 Addrs（多地址）是为了向后兼容：
// 早期版本只有 standalone 模式使用 addr 字段，引入 sentinel/cluster 后新增 addrs，
// 而非破坏性地移除 addr，避免用户升级时配置报错。
type RedisSection struct {
	Mode         string   `toml:"mode"`        // "standalone" / "sentinel" / "cluster"
	Addr         string   `toml:"addr"`        // standalone 单地址（向后兼容）
	Addrs        []string `toml:"addrs"`       // sentinel/cluster 多地址
	MasterName   string   `toml:"master_name"` // sentinel master 名称
	Password     string   `toml:"password"`
	DB           int      `toml:"db"`
	DialTimeout  Duration `toml:"dial_timeout"`
	ReadTimeout  Duration `toml:"read_timeout"`
	WriteTimeout Duration `toml:"write_timeout"`
	PoolSize     int      `toml:"pool_size"`
}

// MultiQuerySection 多查询检索配置
type MultiQuerySection struct {
	Enabled     bool     `toml:"enabled"`
	BaseURL     string   `toml:"base_url"`
	APIKey      string   `toml:"api_key"`
	Model       string   `toml:"model"`
	NumVariants int      `toml:"num_variants"`
	Timeout     Duration `toml:"timeout"`
}

// ContextCompressorSection 上下文压缩配置
type ContextCompressorSection struct {
	Enabled        bool     `toml:"enabled"`
	Type           string   `toml:"type"` // llm / embedding
	BaseURL        string   `toml:"base_url"`
	APIKey         string   `toml:"api_key"`
	Model          string   `toml:"model"`
	Timeout        Duration `toml:"timeout"`
	SimilarityTopN int      `toml:"similarity_top_n"`
	MinSimilarity  float64  `toml:"min_similarity"`
}

// RetrieverSection 检索器配置
type RetrieverSection struct {
	UserIndexNameTemplate   string   `toml:"user_index_name_template"`
	UserIndexPrefixTemplate string   `toml:"user_index_prefix_template"`
	Dimension               int      `toml:"dimension"`
	VectorFieldName         string   `toml:"vector_field_name"`
	ReturnFields            []string `toml:"return_fields"`
	SearchDialect           int      `toml:"search_dialect"`
	DefaultTopK             int      `toml:"default_top_k"`
	MaxTopK                 int      `toml:"max_top_k"`
	MinScore                float64  `toml:"min_score"`

	// 索引算法
	IndexAlgorithm  string `toml:"index_algorithm"`
	HNSWM           int    `toml:"hnsw_m"`
	HNSWEFConstruct int    `toml:"hnsw_ef_construction"`
	HNSWEFRuntime   int    `toml:"hnsw_ef_runtime"`

	// 批量操作
	EmbeddingBatchSize int `toml:"embedding_batch_size"`
	PipelineBatchSize  int `toml:"pipeline_batch_size"`

	// 混合检索
	HybridSearchEnabled bool    `toml:"hybrid_search_enabled"`
	VectorWeight        float64 `toml:"vector_weight"`
	KeywordWeight       float64 `toml:"keyword_weight"`
}

// ChunkingSection 分块配置
type ChunkingSection struct {
	MaxChunkSize        int                     `toml:"max_chunk_size"`
	MinChunkSize        int                     `toml:"min_chunk_size"`
	OverlapSize         int                     `toml:"overlap_size"`
	StructureAware      bool                    `toml:"structure_aware"`
	ParentChildEnabled  bool                    `toml:"parent_child_enabled"`
	ParentChunkSize     int                     `toml:"parent_chunk_size"`
	ChildChunkSize      int                     `toml:"child_chunk_size"`
	SemanticChunking    SemanticChunkingSection `toml:"semantic_chunking"`
	CodeChunkingEnabled bool                    `toml:"code_chunking_enabled"`
}

// SemanticChunkingSection 语义分块配置
type SemanticChunkingSection struct {
	Enabled             bool    `toml:"enabled"`
	WindowSize          int     `toml:"window_size"`
	BreakpointThreshold float64 `toml:"breakpoint_threshold"`
	MaxChunkSize        int     `toml:"max_chunk_size"`
	MinChunkSize        int     `toml:"min_chunk_size"`
}

// EmbMgrSection Embedding 管理器配置
type EmbMgrSection struct {
	Strategy            string   `toml:"strategy"`
	MaxRetries          int      `toml:"max_retries"`
	RetryDelay          Duration `toml:"retry_delay"`
	RetryMaxDelay       Duration `toml:"retry_max_delay"`
	RetryMultiplier     float64  `toml:"retry_multiplier"`
	CircuitThreshold    int      `toml:"circuit_threshold"`
	CircuitTimeout      Duration `toml:"circuit_timeout"`
	CircuitHalfOpenMax  int      `toml:"circuit_half_open_max"`
	HealthCheckInterval Duration `toml:"health_check_interval"`
	HealthCheckTimeout  Duration `toml:"health_check_timeout"`
}

// ProviderSection Embedding Provider 配置
type ProviderSection struct {
	Name      string   `toml:"name"`
	Type      string   `toml:"type"`
	BaseURL   string   `toml:"base_url"`
	APIKey    string   `toml:"api_key"`
	Model     string   `toml:"model"`
	Dimension int      `toml:"dimension"`
	Priority  int      `toml:"priority"`
	Weight    int      `toml:"weight"`
	MaxQPS    float64  `toml:"max_qps"`
	Timeout   Duration `toml:"timeout"`
	Enabled   bool     `toml:"enabled"`
}

// CacheSection 缓存配置
type CacheSection struct {
	Enabled       bool     `toml:"enabled"`
	LocalMaxSize  int      `toml:"local_max_size"`
	LocalTTL      Duration `toml:"local_ttl"`
	RedisEnabled  bool     `toml:"redis_enabled"`
	RedisTTL      Duration `toml:"redis_ttl"`
	RedisPrefix   string   `toml:"redis_prefix"`
	DeduplicateOn bool     `toml:"deduplicate_on"`
}

// RerankSection 重排序配置
type RerankSection struct {
	Enabled    bool     `toml:"enabled"`
	Provider   string   `toml:"provider"`
	BaseURL    string   `toml:"base_url"`
	APIKey     string   `toml:"api_key"`
	Model      string   `toml:"model"`
	TopN       int      `toml:"top_n"`
	RecallTopK int      `toml:"recall_top_k"`
	Timeout    Duration `toml:"timeout"`
	Instruct   string   `toml:"instruct"`
}

// AsyncIndexSection 异步索引配置
type AsyncIndexSection struct {
	Enabled      bool     `toml:"enabled"`
	StreamKey    string   `toml:"stream_key"`
	GroupName    string   `toml:"group_name"`
	StatusPrefix string   `toml:"status_prefix"`
	WorkerCount  int      `toml:"worker_count"`
	TaskTTL      Duration `toml:"task_ttl"`
	ClaimTimeout Duration `toml:"claim_timeout"`
}

// MigrationSection Schema 迁移配置
type MigrationSection struct {
	Enabled              bool   `toml:"enabled"`
	AutoMigrateOnStartup bool   `toml:"auto_migrate_on_startup"`
	MetaPrefix           string `toml:"meta_prefix"`
	BatchSize            int    `toml:"batch_size"`
}

// HyDESection 查询扩展 (HyDE) 配置
type HyDESection struct {
	Enabled     bool     `toml:"enabled"`
	BaseURL     string   `toml:"base_url"`
	APIKey      string   `toml:"api_key"`
	Model       string   `toml:"model"`
	MaxTokens   int      `toml:"max_tokens"`
	Temperature float64  `toml:"temperature"`
	Timeout     Duration `toml:"timeout"`
}

// VectorStoreSection VectorStore 后端配置
type VectorStoreSection struct {
	Type   string        `toml:"type"` // redis / milvus / qdrant
	Milvus MilvusSection `toml:"milvus"`
	Qdrant QdrantSection `toml:"qdrant"`
}

// MilvusSection Milvus 配置
type MilvusSection struct {
	Addr       string   `toml:"addr"`
	Token      string   `toml:"token"`
	Database   string   `toml:"database"`
	Timeout    Duration `toml:"timeout"`
	MetricType string   `toml:"metric_type"`
}

// QdrantSection Qdrant 配置
type QdrantSection struct {
	Addr    string   `toml:"addr"`
	APIKey  string   `toml:"api_key"`
	Timeout Duration `toml:"timeout"`
}

// UploadSection 文件上传配置
type UploadSection struct {
	Enabled            bool     `toml:"enabled"`
	MaxUploadSize      int64    `toml:"max_upload_size"`
	DiskPath           string   `toml:"disk_path"`
	TTL                Duration `toml:"ttl"`
	AutoAsyncThreshold int      `toml:"auto_async_threshold"`
}

// GraphRAGSection Graph RAG 配置
type GraphRAGSection struct {
	Enabled         bool                `toml:"enabled"`
	GraphStoreType  string              `toml:"graph_store_type"` // neo4j / memory
	EntityExtractor string              `toml:"entity_extractor"` // llm / simple
	AutoExtract     bool                `toml:"auto_extract"`
	SearchDepth     int                 `toml:"search_depth"`
	MergeWithVector bool                `toml:"merge_with_vector"`
	Neo4j           Neo4jSection        `toml:"neo4j"`
	LLMExtractor    LLMExtractorSection `toml:"llm_extractor"`
}

// Neo4jSection Neo4j 配置
type Neo4jSection struct {
	URI            string   `toml:"uri"`
	Username       string   `toml:"username"`
	Password       string   `toml:"password"`
	Database       string   `toml:"database"`
	MaxConnPool    int      `toml:"max_conn_pool"`
	ConnectTimeout Duration `toml:"connect_timeout"`
}

// LLMExtractorSection LLM 实体提取器配置
type LLMExtractorSection struct {
	BaseURL     string   `toml:"base_url"`
	APIKey      string   `toml:"api_key"`
	Model       string   `toml:"model"`
	MaxTokens   int      `toml:"max_tokens"`
	Temperature float64  `toml:"temperature"`
	Timeout     Duration `toml:"timeout"`
}

// Duration 是 time.Duration 的 TOML 友好包装。
// Go 原生 time.Duration 的 JSON/TOML 序列化结果是纳秒整数（如 5000000000），
// 可读性极差且易误配。通过实现 encoding.TextUnmarshaler 接口，
// 允许在 TOML 中直接写 "5s"、"200ms" 等人类可读格式。
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
}

// envVarPattern 匹配 ${VAR_NAME} 占位符语法。
// 选择 ${} 而非 $VAR 是为了避免与 shell 变量展开冲突，
// 且大括号明确界定了变量名边界，适合出现在 URL 等复杂字符串中。
var envVarPattern = regexp.MustCompile(`\$\{([^}]+)}`)

// resolveEnvVar 将 ${VAR_NAME} 替换为环境变量值。
// 返回替换后的字符串和未找到的变量名列表（用于统一告警，而非立即报错，
// 因为某些可选字段缺少环境变量不应阻断启动）。
func resolveEnvVar(value string) (string, []string) {
	var missing []string
	resolved := envVarPattern.ReplaceAllStringFunc(value, func(match string) string {
		varName := envVarPattern.FindStringSubmatch(match)[1]
		if envVal := os.Getenv(varName); envVal != "" {
			return envVal
		}
		missing = append(missing, varName)
		return ""
	})
	return resolved, missing
}

// LoadConfig 从 TOML 文件加载配置。
// 处理流程：TOML 反序列化 → 服务器默认值填充 → 环境变量替换 → 校验。
// 这个顺序很重要：必须先填充默认值（确保后续校验有完整数据），
// 再做环境变量替换（因为默认值中不含 ${} 占位符），最后校验全量配置。
func LoadConfig(path string) (*ServerConfig, error) {
	var cfg ServerConfig
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, NewConfigError("load", fmt.Sprintf("failed to load config from %s", path), err)
	}

	// 服务器基础字段默认值：仅在 TOML 未显式配置时生效（零值检测），
	// 使用户可以用最小配置快速启动。
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8082
	}
	if cfg.Server.Name == "" {
		cfg.Server.Name = "rag-mcp-server"
	}
	if cfg.Server.Version == "" {
		cfg.Server.Version = "1.0.0"
	}
	if cfg.Server.MaxContentSize == 0 {
		cfg.Server.MaxContentSize = DefaultMaxContentSize
	}
	// InstanceID 自动生成策略：优先使用主机名实现跨重启稳定标识，
	// 若主机名不可用则回退到 PID（至少在同一机器上保证唯一）。
	// 支持 ${VAR} 语法让容器环境通过 POD_NAME 等注入实例标识。
	if cfg.Server.InstanceID == "" {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "instance-" + fmt.Sprintf("%d", os.Getpid())
		}
		cfg.Server.InstanceID = hostname
	} else {
		resolved, _ := resolveEnvVar(cfg.Server.InstanceID)
		if resolved != "" {
			cfg.Server.InstanceID = resolved
		}
	}

	// 环境变量替换：只对安全敏感字段（API Key、密码）和运行时变化字段（BaseURL）执行替换，
	// 避免全量遍历带来的性能开销和意外替换风险。
	// 仅替换已启用的 Provider，跳过禁用项以减少不必要的环境变量依赖。
	var allMissing []string
	for i := range cfg.Providers {
		if cfg.Providers[i].Enabled {
			resolved, missing := resolveEnvVar(cfg.Providers[i].BaseURL)
			cfg.Providers[i].BaseURL = resolved
			allMissing = append(allMissing, missing...)
			resolved, missing = resolveEnvVar(cfg.Providers[i].APIKey)
			cfg.Providers[i].APIKey = resolved
			allMissing = append(allMissing, missing...)
			resolved, missing = resolveEnvVar(cfg.Providers[i].Model)
			cfg.Providers[i].Model = resolved
			allMissing = append(allMissing, missing...)
		}
	}
	resolved, missing := resolveEnvVar(cfg.Redis.Password)
	cfg.Redis.Password = resolved
	allMissing = append(allMissing, missing...)

	// Rerank — base_url / api_key / model 环境变量解析
	if cfg.Rerank.BaseURL != "" {
		resolved, missing := resolveEnvVar(cfg.Rerank.BaseURL)
		cfg.Rerank.BaseURL = resolved
		allMissing = append(allMissing, missing...)
	}
	if cfg.Rerank.APIKey != "" {
		resolved, missing := resolveEnvVar(cfg.Rerank.APIKey)
		cfg.Rerank.APIKey = resolved
		allMissing = append(allMissing, missing...)
	}
	if cfg.Rerank.Model != "" {
		resolved, missing := resolveEnvVar(cfg.Rerank.Model)
		cfg.Rerank.Model = resolved
		allMissing = append(allMissing, missing...)
	}

	// HyDE — base_url / api_key / model 环境变量解析
	if cfg.HyDE.BaseURL != "" {
		resolved, missing := resolveEnvVar(cfg.HyDE.BaseURL)
		cfg.HyDE.BaseURL = resolved
		allMissing = append(allMissing, missing...)
	}
	if cfg.HyDE.APIKey != "" {
		resolved, missing := resolveEnvVar(cfg.HyDE.APIKey)
		cfg.HyDE.APIKey = resolved
		allMissing = append(allMissing, missing...)
	}
	if cfg.HyDE.Model != "" {
		resolved, missing := resolveEnvVar(cfg.HyDE.Model)
		cfg.HyDE.Model = resolved
		allMissing = append(allMissing, missing...)
	}

	// Multi-Query — base_url / api_key / model 环境变量解析
	if cfg.MultiQuery.BaseURL != "" {
		resolved, missing := resolveEnvVar(cfg.MultiQuery.BaseURL)
		cfg.MultiQuery.BaseURL = resolved
		allMissing = append(allMissing, missing...)
	}
	if cfg.MultiQuery.APIKey != "" {
		resolved, missing := resolveEnvVar(cfg.MultiQuery.APIKey)
		cfg.MultiQuery.APIKey = resolved
		allMissing = append(allMissing, missing...)
	}
	if cfg.MultiQuery.Model != "" {
		resolved, missing := resolveEnvVar(cfg.MultiQuery.Model)
		cfg.MultiQuery.Model = resolved
		allMissing = append(allMissing, missing...)
	}

	// Context Compressor — base_url / api_key / model 环境变量解析
	if cfg.ContextCompressor.BaseURL != "" {
		resolved, missing := resolveEnvVar(cfg.ContextCompressor.BaseURL)
		cfg.ContextCompressor.BaseURL = resolved
		allMissing = append(allMissing, missing...)
	}
	if cfg.ContextCompressor.APIKey != "" {
		resolved, missing := resolveEnvVar(cfg.ContextCompressor.APIKey)
		cfg.ContextCompressor.APIKey = resolved
		allMissing = append(allMissing, missing...)
	}
	if cfg.ContextCompressor.Model != "" {
		resolved, missing := resolveEnvVar(cfg.ContextCompressor.Model)
		cfg.ContextCompressor.Model = resolved
		allMissing = append(allMissing, missing...)
	}

	// Graph RAG — Neo4j 密码 + LLM Extractor (base_url / api_key / model) 环境变量解析
	if cfg.GraphRAG.Neo4j.Password != "" {
		resolved, missing := resolveEnvVar(cfg.GraphRAG.Neo4j.Password)
		cfg.GraphRAG.Neo4j.Password = resolved
		allMissing = append(allMissing, missing...)
	}
	if cfg.GraphRAG.LLMExtractor.BaseURL != "" {
		resolved, missing := resolveEnvVar(cfg.GraphRAG.LLMExtractor.BaseURL)
		cfg.GraphRAG.LLMExtractor.BaseURL = resolved
		allMissing = append(allMissing, missing...)
	}
	if cfg.GraphRAG.LLMExtractor.APIKey != "" {
		resolved, missing := resolveEnvVar(cfg.GraphRAG.LLMExtractor.APIKey)
		cfg.GraphRAG.LLMExtractor.APIKey = resolved
		allMissing = append(allMissing, missing...)
	}
	if cfg.GraphRAG.LLMExtractor.Model != "" {
		resolved, missing := resolveEnvVar(cfg.GraphRAG.LLMExtractor.Model)
		cfg.GraphRAG.LLMExtractor.Model = resolved
		allMissing = append(allMissing, missing...)
	}

	// VectorStore — Milvus token + Qdrant api_key 环境变量解析
	if cfg.VectorStore != nil {
		if cfg.VectorStore.Milvus.Token != "" {
			resolved, missing := resolveEnvVar(cfg.VectorStore.Milvus.Token)
			cfg.VectorStore.Milvus.Token = resolved
			allMissing = append(allMissing, missing...)
		}
		if cfg.VectorStore.Qdrant.APIKey != "" {
			resolved, missing := resolveEnvVar(cfg.VectorStore.Qdrant.APIKey)
			cfg.VectorStore.Qdrant.APIKey = resolved
			allMissing = append(allMissing, missing...)
		}
	}

	// 缺失的环境变量只告警不报错：某些变量可能是可选的（如 Rerank APIKey），
	// 后续 Validate() 会对真正必填的字段执行严格校验。
	if len(allMissing) > 0 {
		logrus.Warnf("[Config] Unresolved environment variables: %v", allMissing)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate 校验配置必填项和合理性。
// 采用"快速失败"策略：遇到第一个错误即返回，并在错误信息中标注阶段（validate）
// 和具体原因，使运维人员无需阅读代码即可定位配置问题。
func (c *ServerConfig) Validate() error {
	// Redis 高可用模式校验：不同模式有不同的必填字段约束。
	// sentinel 必须提供多地址和 master_name，否则客户端无法发现主节点。
	// cluster 模式下 Redis 协议限制只能使用 DB 0，配置非零值必将导致运行时错误，
	// 因此在启动阶段即拦截，避免数据写入错误的逻辑库。
	mode := strings.ToLower(c.Redis.Mode)
	switch mode {
	case "sentinel":
		if len(c.Redis.Addrs) == 0 {
			return NewConfigError("validate", "redis.addrs is required for sentinel mode", nil)
		}
		if c.Redis.MasterName == "" {
			return NewConfigError("validate", "redis.master_name is required for sentinel mode", nil)
		}
	case "cluster":
		if len(c.Redis.Addrs) == 0 {
			return NewConfigError("validate", "redis.addrs is required for cluster mode", nil)
		}
		if c.Redis.DB != 0 {
			return NewConfigError("validate", "redis.db must be 0 for cluster mode (cluster only supports DB 0)", nil)
		}
	default:
		if c.Redis.Addr == "" && len(c.Redis.Addrs) == 0 {
			return NewConfigError("validate", "redis.addr (or redis.addrs) is required", nil)
		}
	}

	// Provider 校验：系统核心依赖 Embedding 服务，零可用 Provider 意味着无法生成向量，
	// 所有检索功能都会失败，因此必须至少启用一个。
	// 仅校验 Enabled=true 的条目，允许用户在 TOML 中保留已禁用的备用配置。
	enabledProviders := 0
	for _, p := range c.Providers {
		if p.Enabled {
			enabledProviders++
			if p.Name == "" {
				return NewConfigError("validate", "enabled provider must have a name", nil)
			}
			if p.Type == "" {
				return NewConfigError("validate", fmt.Sprintf("enabled provider %s must have a type", p.Name), nil)
			}
			if p.APIKey == "" {
				return NewConfigError("validate",
					fmt.Sprintf("enabled provider %s must have an api_key (or set via env var)", p.Name), nil)
			}
		}
	}
	if enabledProviders == 0 {
		return NewConfigError("validate", "at least one embedding provider must be enabled", nil)
	}

	if c.Retriever.DefaultTopK < 0 {
		return NewConfigError("validate", "retriever.default_top_k must be >= 0", nil)
	}
	if c.Retriever.MaxTopK < 0 {
		return NewConfigError("validate", "retriever.max_top_k must be >= 0", nil)
	}
	if c.Retriever.MaxTopK > 0 && c.Retriever.DefaultTopK > c.Retriever.MaxTopK {
		return NewConfigError("validate",
			fmt.Sprintf("retriever.default_top_k (%d) must be <= max_top_k (%d)", c.Retriever.DefaultTopK, c.Retriever.MaxTopK), nil)
	}

	algo := strings.ToUpper(c.Retriever.IndexAlgorithm)
	if algo != "" && algo != "FLAT" && algo != "HNSW" {
		return NewConfigError("validate",
			fmt.Sprintf("retriever.index_algorithm must be FLAT or HNSW, got %s", algo), nil)
	}

	if c.Retriever.HybridSearchEnabled {
		vw := c.Retriever.VectorWeight
		kw := c.Retriever.KeywordWeight
		if vw < 0 || vw > 1 || kw < 0 || kw > 1 {
			return NewConfigError("validate", "hybrid weights must be between 0 and 1", nil)
		}
	}

	// Rerank 逻辑约束：recall_top_k 是初次召回的文档数，top_n 是重排后保留的文档数，
	// top_n > recall_top_k 意味着要求的输出比输入还多，在语义上不合理。
	if c.Rerank.Enabled {
		if c.Rerank.TopN > 0 && c.Rerank.RecallTopK > 0 && c.Rerank.TopN > c.Rerank.RecallTopK {
			return NewConfigError("validate",
				fmt.Sprintf("rerank.top_n (%d) must be <= rerank.recall_top_k (%d)", c.Rerank.TopN, c.Rerank.RecallTopK), nil)
		}
	}

	return nil
}

// NewConfigError 创建配置错误。
// phase 参数（如 "load"/"validate"）标记错误产生的阶段，
// 拼入错误信息后用户可直接从日志区分是文件解析失败还是业务校验失败。
func NewConfigError(phase, detail string, cause error) *rag.RAGError {
	return rag.NewRAGError(rag.ErrCodeConfigInvalid, fmt.Sprintf("[%s] %s", phase, detail), cause)
}

// ToRetrieverConfig 转换为 rag.RetrieverConfig。
// 转换策略：先用合理的硬编码默认值（适合大多数场景），
// 再用 TOML 中非零值覆盖。这样用户只需配置与默认值不同的字段。
func (c *ServerConfig) ToRetrieverConfig() *rag.RetrieverConfig {
	// 维度推断：若用户未在 retriever 段显式指定 dimension，
	// 则从第一个启用的 Provider 获取，避免 TOML 中重复配置同一数值。
	dim := c.Retriever.Dimension
	if dim == 0 {
		for _, p := range c.Providers {
			if p.Enabled && p.Dimension > 0 {
				dim = p.Dimension
				break
			}
		}
	}

	defaultTopK := c.Retriever.DefaultTopK
	if defaultTopK == 0 {
		defaultTopK = 5
	}
	maxTopK := c.Retriever.MaxTopK
	if maxTopK == 0 {
		maxTopK = 20
	}
	vectorField := c.Retriever.VectorFieldName
	if vectorField == "" {
		vectorField = "vector"
	}
	returnFields := c.Retriever.ReturnFields
	if len(returnFields) == 0 {
		returnFields = []string{"content", "file_id", "file_name", "chunk_id", "chunk_index", "distance"}
	}
	searchDialect := c.Retriever.SearchDialect
	if searchDialect == 0 {
		searchDialect = 2
	}

	algo := strings.ToUpper(c.Retriever.IndexAlgorithm)
	if algo == "" {
		algo = "FLAT"
	}

	embBatchSize := c.Retriever.EmbeddingBatchSize
	if embBatchSize <= 0 {
		embBatchSize = 10
	}
	pipeBatchSize := c.Retriever.PipelineBatchSize
	if pipeBatchSize <= 0 {
		pipeBatchSize = 500
	}

	vw := c.Retriever.VectorWeight
	if vw == 0 {
		vw = 0.7
	}
	kw := c.Retriever.KeywordWeight
	if kw == 0 {
		kw = 0.3
	}

	retCfg := &rag.RetrieverConfig{
		UserIndexNameTemplate:   c.Retriever.UserIndexNameTemplate,
		UserIndexPrefixTemplate: c.Retriever.UserIndexPrefixTemplate,
		Dimension:               dim,
		VectorFieldName:         vectorField,
		ReturnFields:            returnFields,
		SearchDialect:           searchDialect,
		DefaultTopK:             defaultTopK,
		MaxTopK:                 maxTopK,
		MinScore:                c.Retriever.MinScore,
		IndexAlgorithm:          algo,
		EmbeddingBatchSize:      embBatchSize,
		PipelineBatchSize:       pipeBatchSize,
		HybridSearchEnabled:     c.Retriever.HybridSearchEnabled,
		VectorWeight:            vw,
		KeywordWeight:           kw,
	}

	// HNSW 参数仅在选择 HNSW 算法时组装：使用指针类型 *HNSWParams，
	// 当算法为 FLAT 时保持 nil，下游可据此判断是否需要创建 HNSW 索引。
	if algo == "HNSW" {
		m := c.Retriever.HNSWM
		if m <= 0 {
			m = 16
		}
		efC := c.Retriever.HNSWEFConstruct
		if efC <= 0 {
			efC = 200
		}
		efR := c.Retriever.HNSWEFRuntime
		if efR <= 0 {
			efR = 10
		}
		retCfg.HNSWParams = &rag.HNSWParams{
			M:              m,
			EFConstruction: efC,
			EFRuntime:      efR,
		}
	}

	// 多查询检索配置桥接
	retCfg.MultiQueryEnabled = c.MultiQuery.Enabled
	if c.MultiQuery.Enabled {
		mqModel := c.MultiQuery.Model
		if mqModel == "" {
			mqModel = "qwen-turbo"
		}
		mqBaseURL := c.MultiQuery.BaseURL
		if mqBaseURL == "" {
			mqBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
		}
		mqNumVariants := c.MultiQuery.NumVariants
		if mqNumVariants <= 0 {
			mqNumVariants = 3
		}
		mqTimeout := c.MultiQuery.Timeout.Duration
		if mqTimeout == 0 {
			mqTimeout = 15 * time.Second
		}
		retCfg.MultiQueryConfig = &rag.MultiQueryConfig{
			Enabled:     true,
			BaseURL:     mqBaseURL,
			APIKey:      c.MultiQuery.APIKey,
			Model:       mqModel,
			NumVariants: mqNumVariants,
			Timeout:     mqTimeout,
		}
	}

	// 上下文压缩配置桥接
	retCfg.CompressorEnabled = c.ContextCompressor.Enabled
	if c.ContextCompressor.Enabled {
		compType := c.ContextCompressor.Type
		if compType == "" {
			compType = "embedding"
		}
		compTimeout := c.ContextCompressor.Timeout.Duration
		if compTimeout == 0 {
			compTimeout = 15 * time.Second
		}
		simTopN := c.ContextCompressor.SimilarityTopN
		if simTopN <= 0 {
			simTopN = 5
		}
		minSim := c.ContextCompressor.MinSimilarity
		if minSim <= 0 {
			minSim = 0.3
		}
		retCfg.CompressorConfig = &rag.CompressorConfig{
			Enabled:        true,
			Type:           compType,
			BaseURL:        c.ContextCompressor.BaseURL,
			APIKey:         c.ContextCompressor.APIKey,
			Model:          c.ContextCompressor.Model,
			Timeout:        compTimeout,
			SimilarityTopN: simTopN,
			MinSimilarity:  minSim,
		}
	}

	// HyDE 配置桥接
	retCfg.HyDEEnabled = c.HyDE.Enabled
	if c.HyDE.Enabled {
		model := c.HyDE.Model
		if model == "" {
			model = "gpt-3.5-turbo"
		}
		baseURL := c.HyDE.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		maxTokens := c.HyDE.MaxTokens
		if maxTokens == 0 {
			maxTokens = 256
		}
		temp := c.HyDE.Temperature
		if temp == 0 {
			temp = 0.3
		}
		timeout := c.HyDE.Timeout.Duration
		if timeout == 0 {
			timeout = 10 * time.Second
		}
		retCfg.HyDEConfig = &rag.HyDEConfig{
			BaseURL:     baseURL,
			APIKey:      c.HyDE.APIKey,
			Model:       model,
			MaxTokens:   maxTokens,
			Temperature: temp,
			Timeout:     timeout,
		}
	}

	return retCfg
}

// ToVectorStoreConfig 转换为 rag.VectorStoreConfig
func (c *ServerConfig) ToVectorStoreConfig() rag.VectorStoreConfig {
	typ := ""
	if c.VectorStore != nil {
		typ = c.VectorStore.Type
	}
	if typ == "" {
		typ = "redis"
	}

	vsCfg := rag.VectorStoreConfig{Type: typ}

	if c.VectorStore != nil {
		vsCfg.Milvus = rag.MilvusConfig{
			Addr:     c.VectorStore.Milvus.Addr,
			Token:    c.VectorStore.Milvus.Token,
			Database: c.VectorStore.Milvus.Database,
			Timeout:  c.VectorStore.Milvus.Timeout.Duration,
		}
		vsCfg.Qdrant = rag.QdrantConfig{
			Addr:    c.VectorStore.Qdrant.Addr,
			APIKey:  c.VectorStore.Qdrant.APIKey,
			Timeout: c.VectorStore.Qdrant.Timeout.Duration,
		}
	}

	return vsCfg
}

// ToGraphRAGConfig 转换为 rag.GraphRAGConfig
func (c *ServerConfig) ToGraphRAGConfig() rag.GraphRAGConfig {
	cfg := rag.DefaultGraphRAGConfig()
	cfg.Enabled = c.GraphRAG.Enabled

	if c.GraphRAG.GraphStoreType != "" {
		cfg.GraphStoreType = c.GraphRAG.GraphStoreType
	}
	if c.GraphRAG.EntityExtractor != "" {
		cfg.EntityExtractor = c.GraphRAG.EntityExtractor
	}
	cfg.AutoExtract = c.GraphRAG.AutoExtract
	if c.GraphRAG.SearchDepth > 0 {
		cfg.SearchDepth = c.GraphRAG.SearchDepth
	}
	cfg.MergeWithVector = c.GraphRAG.MergeWithVector

	// Neo4j 配置
	if c.GraphRAG.Neo4j.URI != "" {
		cfg.Neo4j.URI = c.GraphRAG.Neo4j.URI
	}
	if c.GraphRAG.Neo4j.Username != "" {
		cfg.Neo4j.Username = c.GraphRAG.Neo4j.Username
	}
	if c.GraphRAG.Neo4j.Password != "" {
		cfg.Neo4j.Password = c.GraphRAG.Neo4j.Password
	}
	if c.GraphRAG.Neo4j.Database != "" {
		cfg.Neo4j.Database = c.GraphRAG.Neo4j.Database
	}

	// LLM 实体提取器配置
	if c.GraphRAG.LLMExtractor.BaseURL != "" {
		cfg.LLMExtractor.BaseURL = c.GraphRAG.LLMExtractor.BaseURL
	}
	if c.GraphRAG.LLMExtractor.APIKey != "" {
		cfg.LLMExtractor.APIKey = c.GraphRAG.LLMExtractor.APIKey
	}
	if c.GraphRAG.LLMExtractor.Model != "" {
		cfg.LLMExtractor.Model = c.GraphRAG.LLMExtractor.Model
	}

	return cfg
}

// ToChunkingConfig 转换为 rag.ChunkingConfig
func (c *ServerConfig) ToChunkingConfig() *rag.ChunkingConfig {
	maxChunk := c.Chunking.MaxChunkSize
	if maxChunk == 0 {
		maxChunk = 1000
	}
	minChunk := c.Chunking.MinChunkSize
	if minChunk == 0 {
		minChunk = 100
	}
	overlap := c.Chunking.OverlapSize
	if overlap == 0 {
		overlap = 200
	}
	parentSize := c.Chunking.ParentChunkSize
	if parentSize == 0 {
		parentSize = 1000
	}
	childSize := c.Chunking.ChildChunkSize
	if childSize == 0 {
		childSize = 200
	}
	// 语义分块配置
	scCfg := rag.DefaultSemanticChunkingConfig()
	scCfg.Enabled = c.Chunking.SemanticChunking.Enabled
	if c.Chunking.SemanticChunking.WindowSize > 0 {
		scCfg.WindowSize = c.Chunking.SemanticChunking.WindowSize
	}
	if c.Chunking.SemanticChunking.BreakpointThreshold > 0 {
		scCfg.BreakpointThreshold = c.Chunking.SemanticChunking.BreakpointThreshold
	}
	if c.Chunking.SemanticChunking.MaxChunkSize > 0 {
		scCfg.MaxChunkSize = c.Chunking.SemanticChunking.MaxChunkSize
	}
	if c.Chunking.SemanticChunking.MinChunkSize > 0 {
		scCfg.MinChunkSize = c.Chunking.SemanticChunking.MinChunkSize
	}

	return &rag.ChunkingConfig{
		MaxChunkSize:        maxChunk,
		MinChunkSize:        minChunk,
		OverlapSize:         overlap,
		StructureAware:      c.Chunking.StructureAware,
		ParentChildEnabled:  c.Chunking.ParentChildEnabled,
		ParentChunkSize:     parentSize,
		ChildChunkSize:      childSize,
		SemanticChunking:    scCfg,
		CodeChunkingEnabled: c.Chunking.CodeChunkingEnabled,
	}
}

// ToManagerConfig 转换为 rag.ManagerConfig。
// 以 DefaultManagerConfig() 为基础，仅覆盖 TOML 中显式设置的字段（非零值检测）。
// 这种"默认值在领域层、覆盖在配置层"的模式确保即使 TOML 中完全省略 [embedding_manager] 段，
// 系统也能以安全的默认参数运行。
func (c *ServerConfig) ToManagerConfig() rag.ManagerConfig {
	cfg := rag.DefaultManagerConfig()

	strategy := strings.ToLower(c.EmbMgr.Strategy)
	switch strategy {
	case "round_robin":
		cfg.Strategy = rag.LoadBalanceRoundRobin
	case "random":
		cfg.Strategy = rag.LoadBalanceRandom
	case "weighted":
		cfg.Strategy = rag.LoadBalanceWeighted
	case "priority":
		cfg.Strategy = rag.LoadBalancePriority
	}

	if c.EmbMgr.MaxRetries > 0 {
		cfg.MaxRetries = c.EmbMgr.MaxRetries
	}
	if c.EmbMgr.RetryDelay.Duration > 0 {
		cfg.RetryDelay = c.EmbMgr.RetryDelay.Duration
	}
	if c.EmbMgr.RetryMaxDelay.Duration > 0 {
		cfg.RetryMaxDelay = c.EmbMgr.RetryMaxDelay.Duration
	}
	if c.EmbMgr.RetryMultiplier > 0 {
		cfg.RetryMultiplier = c.EmbMgr.RetryMultiplier
	}
	if c.EmbMgr.CircuitThreshold > 0 {
		cfg.CircuitThreshold = c.EmbMgr.CircuitThreshold
	}
	if c.EmbMgr.CircuitTimeout.Duration > 0 {
		cfg.CircuitTimeout = c.EmbMgr.CircuitTimeout.Duration
	}
	if c.EmbMgr.CircuitHalfOpenMax > 0 {
		cfg.CircuitHalfOpenMax = c.EmbMgr.CircuitHalfOpenMax
	}
	if c.EmbMgr.HealthCheckInterval.Duration > 0 {
		cfg.HealthCheckInterval = c.EmbMgr.HealthCheckInterval.Duration
	}
	if c.EmbMgr.HealthCheckTimeout.Duration > 0 {
		cfg.HealthCheckTimeout = c.EmbMgr.HealthCheckTimeout.Duration
	}

	return cfg
}

// ToProviderConfigs 转换为 []rag.ProviderConfig。
// 此处转换所有 Provider（包括 Enabled=false 的），将启用状态判断留给下游 Manager，
// 使其能在运行时动态启停 Provider 而无需重新加载配置文件。
func (c *ServerConfig) ToProviderConfigs() []rag.ProviderConfig {
	configs := make([]rag.ProviderConfig, len(c.Providers))
	for i, p := range c.Providers {
		configs[i] = rag.ProviderConfig{
			Name:      p.Name,
			Type:      p.Type,
			BaseURL:   p.BaseURL,
			APIKey:    p.APIKey,
			Model:     p.Model,
			Dimension: p.Dimension,
			Priority:  p.Priority,
			Weight:    p.Weight,
			MaxQPS:    p.MaxQPS,
			Timeout:   p.Timeout.Duration,
			Enabled:   p.Enabled,
		}
	}
	return configs
}

// ToCacheConfig 转换为 rag.CacheConfig。
// 与其他 To*Config 方法一致，以 DefaultCacheConfig() 为基底再覆盖。
func (c *ServerConfig) ToCacheConfig() rag.CacheConfig {
	cfg := rag.DefaultCacheConfig()

	if c.Cache.LocalMaxSize > 0 {
		cfg.LocalMaxSize = c.Cache.LocalMaxSize
	}
	if c.Cache.LocalTTL.Duration > 0 {
		cfg.LocalTTL = c.Cache.LocalTTL.Duration
	}
	cfg.Enabled = c.Cache.Enabled
	cfg.RedisEnabled = c.Cache.RedisEnabled
	if c.Cache.RedisTTL.Duration > 0 {
		cfg.RedisTTL = c.Cache.RedisTTL.Duration
	}
	if c.Cache.RedisPrefix != "" {
		cfg.RedisPrefix = c.Cache.RedisPrefix
	}
	cfg.DeduplicateOn = c.Cache.DeduplicateOn

	return cfg
}

// ToTaskQueueConfig 转换为 rag.TaskQueueConfig
func (c *ServerConfig) ToTaskQueueConfig() rag.TaskQueueConfig {
	cfg := rag.DefaultTaskQueueConfig()
	cfg.Enabled = c.AsyncIndex.Enabled
	if c.AsyncIndex.StreamKey != "" {
		cfg.StreamKey = c.AsyncIndex.StreamKey
	}
	if c.AsyncIndex.GroupName != "" {
		cfg.GroupName = c.AsyncIndex.GroupName
	}
	if c.AsyncIndex.StatusPrefix != "" {
		cfg.StatusPrefix = c.AsyncIndex.StatusPrefix
	}
	if c.AsyncIndex.WorkerCount > 0 {
		cfg.WorkerCount = c.AsyncIndex.WorkerCount
	}
	if c.AsyncIndex.TaskTTL.Duration > 0 {
		cfg.TaskTTL = c.AsyncIndex.TaskTTL.Duration
	}
	if c.AsyncIndex.ClaimTimeout.Duration > 0 {
		cfg.ClaimTimeout = c.AsyncIndex.ClaimTimeout.Duration
	}
	return cfg
}

// ToMigrationConfig 转换为 rag.MigrationConfig
func (c *ServerConfig) ToMigrationConfig() rag.MigrationConfig {
	cfg := rag.DefaultMigrationConfig()
	cfg.Enabled = c.Migration.Enabled
	cfg.AutoMigrateOnStartup = c.Migration.AutoMigrateOnStartup
	if c.Migration.MetaPrefix != "" {
		cfg.MetaPrefix = c.Migration.MetaPrefix
	}
	if c.Migration.BatchSize > 0 {
		cfg.BatchSize = c.Migration.BatchSize
	}
	return cfg
}

// ToUploadConfig 转换为 rag.UploadConfig
func (c *ServerConfig) ToUploadConfig() rag.UploadConfig {
	cfg := rag.DefaultUploadConfig()
	cfg.Enabled = c.Upload.Enabled
	if c.Upload.MaxUploadSize > 0 {
		cfg.MaxUploadSize = c.Upload.MaxUploadSize
	}
	if c.Upload.DiskPath != "" {
		cfg.DiskPath = c.Upload.DiskPath
	}
	if c.Upload.TTL.Duration > 0 {
		cfg.TTL = c.Upload.TTL.Duration
	}
	if c.Upload.AutoAsyncThreshold > 0 {
		cfg.AutoAsyncThreshold = c.Upload.AutoAsyncThreshold
	}
	return cfg
}

// ToRerankConfig 转换为 rag.RerankConfig
func (c *ServerConfig) ToRerankConfig() rag.RerankConfig {
	cfg := rag.DefaultRerankConfig()

	cfg.Enabled = c.Rerank.Enabled
	if c.Rerank.Provider != "" {
		cfg.Provider = c.Rerank.Provider
	}
	if c.Rerank.BaseURL != "" {
		cfg.BaseURL = c.Rerank.BaseURL
	}
	cfg.APIKey = c.Rerank.APIKey
	if c.Rerank.Model != "" {
		cfg.Model = c.Rerank.Model
	}
	if c.Rerank.TopN > 0 {
		cfg.TopN = c.Rerank.TopN
	}
	if c.Rerank.RecallTopK > 0 {
		cfg.RecallTopK = c.Rerank.RecallTopK
	}
	if c.Rerank.Timeout.Duration > 0 {
		cfg.Timeout = c.Rerank.Timeout.Duration
	}
	if c.Rerank.Instruct != "" {
		cfg.Instruct = c.Rerank.Instruct
	}

	return cfg
}
