/*
┌─────────────────────────────────────────────────────────────────────────┐
│ main.go — RAG MCP Server 入口                                          │
│                                                                         │
│ 职责: 组件生命周期编排（初始化 → 运行 → 优雅关闭）                         │
│                                                                         │
│ 启动顺序 (有依赖关系，不可乱序):                                          │
│   1. LoadConfig          加载 TOML 配置                                  │
│   2. createRedisClient   创建 Redis 客户端 (standalone/sentinel/cluster)  │
│   3. InitEmbeddingManager 多 Provider 管理器 (熔断/负载均衡)              │
│   4. InitCache           L1 LRU + L2 Redis 二级缓存                     │
│   5. InitReranker        Rerank 精排器                                   │
│   6. TaskQueue + Worker  异步索引 (可选, 依赖 Redis + Store)              │
│   7. Migrator            Schema 版本迁移检查 (可选)                       │
│   8. StartServer         启动 MCP Streamable HTTP                       │
│                                                                         │
│ 关闭顺序 (与启动相反，确保数据安全):                                       │
│   1. IndexWorker.Stop()  先停 worker，等待 in-flight 任务完成              │
│   2. Manager.Stop()      停止 Embedding 健康检查                         │
│   3. RedisClient.Close() 最后关闭连接池                                   │
│                                                                         │
│ 函数:                                                                    │
│   main()                 程序入口，编排全部生命周期                         │
│   createRedisClient()    Redis 客户端工厂 (3 种部署模式)                   │
│   effectiveRedisMode()   获取实际 Redis 模式名称                          │
└─────────────────────────────────────────────────────────────────────────┘
*/
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mcp_rag_server/rag"

	redisCli "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

func main() {
	configPath := flag.String("config", "config.toml", "配置文件路径")
	flag.Parse()

	logrus.Info("========================================")
	logrus.Info("       RAG MCP Server 启动中")
	logrus.Info("========================================")

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		logrus.Fatalf("配置加载失败: %v", err)
	}
	logrus.Infof("配置加载成功: %s v%s (port: %d, instance: %s)",
		cfg.Server.Name, cfg.Server.Version, cfg.Server.Port, cfg.Server.InstanceID)

	// 使用工厂方法创建 Redis 客户端，通过 UniversalClient 接口统一三种部署模式
	redisClient := createRedisClient(&cfg.Redis)

	// Ping 验证连接可用，5s 超时防止启动时长期阻塞
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		logrus.Fatalf("Redis 连接失败: %v", err)
	}
	logrus.Infof("Redis 连接成功 (mode=%s)", effectiveRedisMode(&cfg.Redis))

	manager := InitEmbeddingManager(cfg)
	defer manager.Stop()

	stats := manager.GetStats()
	logrus.Infof("Embedding 管理器已初始化: %d 个 Provider", len(stats))
	for _, s := range stats {
		logrus.Infof("  - %s (type=%s, priority=%d, status=%s)", s.Name, s.CircuitState, s.Priority, s.Status)
	}

	cache := InitCache(cfg, redisClient)
	cacheStats := cache.Stats()
	logrus.Infof("Embedding 缓存已初始化: local_cap=%d, redis=%v", cacheStats.LocalCap, cfg.Cache.RedisEnabled)

	InitReranker(cfg)

	// 异步索引子系统（可选）：基于 Redis Streams 的分布式任务队列
	// 原理：多实例部署时，每个实例启动独立的消费者组 Worker，
	//       通过 XREADGROUP 竞争消费实现任务的负载均衡和故障转移
	var taskQueue *rag.TaskQueue
	var indexWorker *rag.IndexWorker
	asyncCfg := cfg.ToTaskQueueConfig()
	if asyncCfg.Enabled {
		taskQueue = rag.NewTaskQueue(redisClient, asyncCfg)
		retCfg := cfg.ToRetrieverConfig()
		chunkCfg := cfg.ToChunkingConfig()
		store := rag.NewRedisVectorStore(redisClient)
		// consumer 名称 = instanceID-PID，确保多实例多进程下全局唯一
		consumer := fmt.Sprintf("%s-%d", cfg.Server.InstanceID, os.Getpid())
		indexWorker = rag.NewIndexWorker(taskQueue, store, retCfg, chunkCfg, consumer, asyncCfg.WorkerCount)
		if err := indexWorker.Start(); err != nil {
			logrus.Fatalf("异步索引 Worker 启动失败: %v", err)
		}
		logrus.Infof("异步索引已启用: workers=%d, consumer=%s", asyncCfg.WorkerCount, consumer)
	}

	// Schema 迁移检查
	migCfg := cfg.ToMigrationConfig()
	if migCfg.Enabled && migCfg.AutoMigrateOnStartup {
		retCfg := cfg.ToRetrieverConfig()
		store := rag.NewRedisVectorStore(redisClient)
		migrator := rag.NewMigrator(store, redisClient, retCfg, migCfg)
		if err := migrator.MigrateAllOnStartup(context.Background()); err != nil {
			logrus.Warnf("Schema 迁移检查失败: %v", err)
		}
	}

	logrus.Infof("正在启动 RAG MCP Server (Port: %d)...", cfg.Server.Port)
	logrus.Infof("索引算法: %s | 混合检索: %v | 结构感知分块: %v | Rerank: %v | 异步索引: %v",
		cfg.Retriever.IndexAlgorithm, cfg.Retriever.HybridSearchEnabled,
		cfg.Chunking.StructureAware, cfg.Rerank.Enabled, asyncCfg.Enabled)

	// 在独立 goroutine 中启动 HTTP 服务器，主 goroutine 用于信号监听
	errCh := make(chan error, 1)
	go func() {
		errCh <- StartServer(cfg, redisClient, taskQueue)
	}()

	logrus.Info("----------------------------------------")
	logrus.Infof("RAG MCP Server 已启动: http://localhost:%d/mcp", cfg.Server.Port)
	logrus.Info("按 Ctrl+C 停止服务器")
	logrus.Info("========================================")

	// 优雅关闭：监听 SIGINT (Ctrl+C) 和 SIGTERM (docker stop / k8s)
	// 使用 select 同时监听信号和服务异常，先到先处理
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		logrus.Infof("收到信号 %v，正在关闭服务器...", sig)
	case err := <-errCh:
		logrus.Errorf("服务异常退出: %v", err)
	}

	// 关闭顺序：Worker → Manager → Redis，确保 in-flight 任务完成后再断开连接
	logrus.Info("正在清理资源...")
	if indexWorker != nil {
		indexWorker.Stop() // 内部调用 cancel + wg.Wait，等待所有 goroutine 退出
	}
	manager.Stop()
	if err := redisClient.Close(); err != nil {
		logrus.Warnf("关闭 Redis 连接时出错: %v", err)
	}
	logrus.Info("服务器已关闭")
}

// createRedisClient 根据配置创建 Redis 客户端，支持 standalone/sentinel/cluster 三种部署模式。
//
// 设计原理：使用 go-redis 的 UniversalClient 接口作为返回类型，上层代码（VectorStore、Cache、
// TaskQueue 等）无需关心底层是哪种部署模式，实现了依赖倒置。
//
// 三种模式对应不同的 go-redis 客户端实现：
//   - standalone → redis.Client：单节点直连，支持 DB 选择
//   - sentinel   → redis.FailoverClient：通过 Sentinel 哨兵发现主节点，自动故障转移
//   - cluster    → redis.ClusterClient：Redis Cluster 分片集群，key 按 CRC16 哈希槽路由
func createRedisClient(cfg *RedisSection) redisCli.UniversalClient {
	mode := strings.ToLower(cfg.Mode)

	switch mode {
	case "sentinel":
		opts := &redisCli.FailoverOptions{
			MasterName:    cfg.MasterName,
			SentinelAddrs: cfg.Addrs,
			Password:      cfg.Password,
			DB:            cfg.DB,
		}
		if cfg.DialTimeout.Duration > 0 {
			opts.DialTimeout = cfg.DialTimeout.Duration
		}
		if cfg.ReadTimeout.Duration > 0 {
			opts.ReadTimeout = cfg.ReadTimeout.Duration
		}
		if cfg.WriteTimeout.Duration > 0 {
			opts.WriteTimeout = cfg.WriteTimeout.Duration
		}
		if cfg.PoolSize > 0 {
			opts.PoolSize = cfg.PoolSize
		}
		logrus.Infof("[Redis] Connecting in sentinel mode: master=%s, sentinels=%v", cfg.MasterName, cfg.Addrs)
		return redisCli.NewFailoverClient(opts)

	case "cluster":
		opts := &redisCli.ClusterOptions{
			Addrs:    cfg.Addrs,
			Password: cfg.Password,
		}
		if cfg.DialTimeout.Duration > 0 {
			opts.DialTimeout = cfg.DialTimeout.Duration
		}
		if cfg.ReadTimeout.Duration > 0 {
			opts.ReadTimeout = cfg.ReadTimeout.Duration
		}
		if cfg.WriteTimeout.Duration > 0 {
			opts.WriteTimeout = cfg.WriteTimeout.Duration
		}
		if cfg.PoolSize > 0 {
			opts.PoolSize = cfg.PoolSize
		}
		logrus.Infof("[Redis] Connecting in cluster mode: addrs=%v", cfg.Addrs)
		return redisCli.NewClusterClient(opts)

	default:
		addr := cfg.Addr
		if addr == "" && len(cfg.Addrs) > 0 {
			addr = cfg.Addrs[0]
		}
		opts := &redisCli.Options{
			Addr:     addr,
			Password: cfg.Password,
			DB:       cfg.DB,
		}
		if cfg.DialTimeout.Duration > 0 {
			opts.DialTimeout = cfg.DialTimeout.Duration
		}
		if cfg.ReadTimeout.Duration > 0 {
			opts.ReadTimeout = cfg.ReadTimeout.Duration
		}
		if cfg.WriteTimeout.Duration > 0 {
			opts.WriteTimeout = cfg.WriteTimeout.Duration
		}
		if cfg.PoolSize > 0 {
			opts.PoolSize = cfg.PoolSize
		}
		logrus.Infof("[Redis] Connecting in standalone mode: addr=%s", addr)
		return redisCli.NewClient(opts)
	}
}

func effectiveRedisMode(cfg *RedisSection) string {
	mode := strings.ToLower(cfg.Mode)
	if mode == "" {
		return "standalone"
	}
	return mode
}
