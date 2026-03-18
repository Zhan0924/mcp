/*
┌─────────────────────────────────────────────────────────────────────────────┐
│                    embedding_manager.go 结构总览                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  核心设计：全局单例 Manager 统一调度多个 Embedding Provider                  │
│    - 每个 Provider 拥有独立的熔断器，避免单点故障拖垮整个系统                 │
│    - 四种负载均衡策略可选，Priority 策略实现「主备自动降级」                   │
│    - 指数退避重试 + 随机抖动，防止多客户端同时重试导致「惊群效应」             │
│    - 后台健康检查协程主动探测，使故障 Provider 能被更快发现和恢复             │
│                                                                             │
│  常量/枚举                                                                  │
│    ProviderStatus      — Provider 健康状态 (Healthy/Unhealthy/Degraded)     │
│    CircuitState        — 熔断器状态 (Closed/Open/HalfOpen)                  │
│    LoadBalanceStrategy — 负载均衡策略 (RoundRobin/Random/Weighted/Priority) │
│                                                                             │
│  配置结构体                                                                 │
│    ProviderConfig      — 单个 Provider 的连接与行为配置                      │
│    ManagerConfig       — 管理器全局配置（策略、重试、熔断、健康检查）          │
│    DefaultManagerConfig() — 返回经验值默认配置                               │
│                                                                             │
│  核心结构体                                                                 │
│    Provider            — 封装 eino Embedder + 熔断器状态 + 统计计数器        │
│    ProviderStats       — Provider 运行时统计的只读快照（无锁读取安全）        │
│    Manager             — 管理多个 Provider 的调度器（负载均衡+故障转移）      │
│                                                                             │
│  工厂注册（开闭原则）                                                       │
│    EmbedderFactory     — 创建 Embedder 的工厂函数签名                       │
│    RegisterFactory()   — 按 type 注册工厂（新增类型无需改 Manager）          │
│    GetFactory()        — 按 type 查找工厂                                   │
│                                                                             │
│  Manager 方法                                                               │
│    NewManager()        — 构造 Manager（创建独立 context 管理生命周期）       │
│    AddProvider()       — 通过工厂创建并注册 Provider（乐观初始化为 Closed）  │
│    Start()             — 幂等启动健康检查协程                                │
│    Stop()              — 优雅停止（cancel context → 等待协程退出）           │
│    EmbedStrings()      — 核心入口：选择 Provider → 调用 → 重试/故障转移     │
│    GetStats()          — 返回所有 Provider 统计快照（基于读锁，不阻塞写入）  │
│    ResetStats()        — 清零所有统计计数器                                  │
│                                                                             │
│  全局单例                                                                   │
│    InitGlobalManager() — 初始化/替换全局 Manager（热替换：先 Stop 旧实例）   │
│    GetGlobalManager()  — 获取全局 Manager                                   │
│    EmbedStrings()      — 包级别便捷函数，委托给全局 Manager                  │
│    GetStats()          — 包级别便捷函数，委托给全局 Manager                  │
│                                                                             │
│  设计模式                                                                   │
│    ┌────────────────────────────────────────────────────────────────────┐   │
│    │ 熔断器状态机 (每个 Provider 独立，避免级联故障):                     │   │
│    │   Closed ──连续失败≥阈值──→ Open ──冷却超时──→ HalfOpen            │   │
│    │     ↑                                            │                 │   │
│    │     └────────── 探测成功 ─────────────────────────┘                │   │
│    │                            探测失败 → 回到 Open                    │   │
│    │                                                                    │   │
│    │ 为什么每个 Provider 独立熔断而非全局熔断？                          │   │
│    │   → 单个后端故障时，其他健康后端仍可正常服务                        │   │
│    │   → 全局熔断会导致一个后端故障就完全中断服务                        │   │
│    └────────────────────────────────────────────────────────────────────┘   │
│    ┌────────────────────────────────────────────────────────────────────┐   │
│    │ 重试策略: 指数退避 + 随机抖动 (jitter = delay/4)                   │   │
│    │   delay(n) = min(base * multiplier^n, max_delay) + rand jitter     │   │
│    │                                                                    │   │
│    │ 为什么用指数退避而非固定间隔？                                      │   │
│    │   → 后端过载时，固定间隔重试会持续加压，指数退避自动「让步」        │   │
│    │   → 抖动使多客户端的重试时间分散，避免同步重试放大峰值              │   │
│    └────────────────────────────────────────────────────────────────────┘   │
│    ┌────────────────────────────────────────────────────────────────────┐   │
│    │ 并发安全模型:                                                      │   │
│    │   Manager.mu (RWMutex)  — 保护 providers 列表和 isRunning 状态     │   │
│    │   Provider.mu (RWMutex) — 保护每个 Provider 的熔断器状态和统计     │   │
│    │   rrIndex (atomic)      — RoundRobin 热路径，无锁递增避免争用      │   │
│    │   globalMu (RWMutex)    — 保护全局单例的读写                       │   │
│    └────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/sirupsen/logrus"
)

// ProviderStatus Provider 健康状态，由熔断器状态和成功率综合判定
type ProviderStatus string

const (
	ProviderStatusHealthy   ProviderStatus = "healthy"
	ProviderStatusUnhealthy ProviderStatus = "unhealthy"
	ProviderStatusDegraded  ProviderStatus = "degraded"
)

// CircuitState 熔断器三态：Closed（正常放行）→ Open（快速失败）→ HalfOpen（有限探测）
type CircuitState string

const (
	CircuitStateClosed   CircuitState = "closed"
	CircuitStateOpen     CircuitState = "open"
	CircuitStateHalfOpen CircuitState = "half_open"
)

// LoadBalanceStrategy 负载均衡策略
type LoadBalanceStrategy string

const (
	LoadBalanceRoundRobin LoadBalanceStrategy = "round_robin"
	LoadBalanceRandom     LoadBalanceStrategy = "random"
	LoadBalanceWeighted   LoadBalanceStrategy = "weighted"
	LoadBalancePriority   LoadBalanceStrategy = "priority" // 按优先级故障转移，attempt=0 选最高优先级，失败后依次降级
)

// ProviderConfig 单个 Embedding Provider 的配置
type ProviderConfig struct {
	Name      string        `toml:"name"`
	Type      string        `toml:"type"`    // 对应 RegisterFactory 注册的 type key
	BaseURL   string        `toml:"base_url"`
	APIKey    string        `toml:"api_key"`
	Model     string        `toml:"model"`
	Dimension int           `toml:"dimension"` // 仅用于 Redis 索引创建时声明维度，实际向量维度由 API 返回决定
	Priority  int           `toml:"priority"`  // Priority 策略下数值越小优先级越高
	Weight    int           `toml:"weight"`    // Weighted 策略下的流量权重
	MaxQPS    float64       `toml:"max_qps"`
	Timeout   time.Duration `toml:"timeout"`
	Enabled   bool          `toml:"enabled"`
}

// ManagerConfig 管理器全局配置
type ManagerConfig struct {
	Strategy            LoadBalanceStrategy `toml:"strategy"`
	MaxRetries          int                 `toml:"max_retries"`          // 最大重试次数（不含首次）
	RetryDelay          time.Duration       `toml:"retry_delay"`          // 首次重试基础延迟
	RetryMaxDelay       time.Duration       `toml:"retry_max_delay"`      // 指数退避上限
	RetryMultiplier     float64             `toml:"retry_multiplier"`     // 退避倍数，每次重试 delay *= multiplier
	CircuitThreshold    int                 `toml:"circuit_threshold"`    // 连续失败多少次触发熔断
	CircuitTimeout      time.Duration       `toml:"circuit_timeout"`      // Open 态持续多久后进入 HalfOpen
	CircuitHalfOpenMax  int                 `toml:"circuit_half_open_max"` // HalfOpen 态允许的最大探测请求数
	HealthCheckInterval time.Duration       `toml:"health_check_interval"`
	HealthCheckTimeout  time.Duration       `toml:"health_check_timeout"`
}

// DefaultManagerConfig 默认配置
// 设计依据：
//   - Priority 策略：RAG 场景通常有主备关系，优先使用高质量模型，故障时降级到备用
//   - 重试 3 次 + 1s 基础延迟 + 2x 退避：平衡恢复速度和后端压力（1s → 2s → 4s）
//   - 5 次连续失败触发熔断：容忍偶发错误（网络抖动），但快速识别持续故障
//   - 30s 熔断冷却期：给后端足够恢复时间，过短会频繁触发 HalfOpen 探测增加负载
//   - HalfOpen 最多 3 个探测请求：既能验证恢复，又不会对刚恢复的后端造成瞬时压力
//   - 1 分钟健康检查：被动等待 + 主动探测相结合，主动探测使 Open 态后端更快被发现已恢复
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		Strategy:            LoadBalancePriority,
		MaxRetries:          3,
		RetryDelay:          time.Second,
		RetryMaxDelay:       30 * time.Second,
		RetryMultiplier:     2.0,
		CircuitThreshold:    5,
		CircuitTimeout:      30 * time.Second,
		CircuitHalfOpenMax:  3,
		HealthCheckInterval: time.Minute,
		HealthCheckTimeout:  10 * time.Second,
	}
}

// Provider 封装一个 Embedding 后端，包含其熔断器状态和调用统计
// 每个 Provider 拥有独立的熔断器，互不影响
type Provider struct {
	config   ProviderConfig
	embedder embedding.Embedder

	// --- 熔断器状态（受 mu 保护） ---
	circuitState     CircuitState
	consecutiveFails int64     // 连续失败计数，成功一次即清零
	lastFailTime     time.Time // 最近一次失败时间，Open→HalfOpen 转换的计时起点
	halfOpenCount    int64     // HalfOpen 态已放行的探测请求数

	// --- 调用统计（受 mu 保护，用于 GetStats 快照） ---
	totalRequests   int64
	successRequests int64
	failedRequests  int64
	totalLatency    int64 // 纳秒累计，仅统计成功请求

	mu sync.RWMutex
}

// ProviderStats Provider 统计信息
type ProviderStats struct {
	Name            string         `json:"name"`
	Status          ProviderStatus `json:"status"`
	CircuitState    CircuitState   `json:"circuit_state"`
	TotalRequests   int64          `json:"total_requests"`
	SuccessRequests int64          `json:"success_requests"`
	FailedRequests  int64          `json:"failed_requests"`
	SuccessRate     float64        `json:"success_rate"`
	AvgLatency      time.Duration  `json:"avg_latency"`
	Priority        int            `json:"priority"`
	Weight          int            `json:"weight"`
}

// Manager 全局 Embedding 调度中心
// 职责：按策略选择 Provider、自动重试/故障转移、管理熔断器、周期健康检查
type Manager struct {
	config    ManagerConfig
	providers []*Provider

	// RoundRobin 策略的原子计数器，无锁递增
	// 选择 atomic 而非 mutex：EmbedStrings 是高频热路径，atomic 避免锁争用，保证 O(1) 选择
	rrIndex int64

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup // 等待健康检查协程退出，Stop() 中 wg.Wait() 确保无 goroutine 泄漏
	isRunning bool

	mu sync.RWMutex
}

// EmbedderFactory 根据 ProviderConfig 创建 eino Embedder 的工厂函数
// 通过 RegisterFactory 按 type 注册，实现开闭原则（新增 Provider 类型无需修改 Manager）
type EmbedderFactory func(ctx context.Context, config ProviderConfig) (embedding.Embedder, error)

var (
	// 全局单例 Manager，整个进程共享一个实例
	globalManager *Manager
	globalMu      sync.RWMutex

	// 工厂注册表，key 为 provider type（如 "ark"、"openai"、"local"）
	factories   = make(map[string]EmbedderFactory)
	factoriesMu sync.RWMutex
)

// RegisterFactory 注册 Embedder 工厂函数
func RegisterFactory(providerType string, factory EmbedderFactory) {
	factoriesMu.Lock()
	defer factoriesMu.Unlock()
	factories[providerType] = factory
	logrus.Infof("[EmbeddingManager] Registered factory for type: %s", providerType)
}

// GetFactory 获取工厂函数
func GetFactory(providerType string) (EmbedderFactory, bool) {
	factoriesMu.RLock()
	defer factoriesMu.RUnlock()
	f, ok := factories[providerType]
	return f, ok
}

// NewManager 创建管理器
func NewManager(config ManagerConfig) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		config:    config,
		providers: make([]*Provider, 0),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// AddProvider 通过工厂创建 Embedder 并注册为 Provider
// 新 Provider 以 CircuitStateClosed（乐观假设）初始化：
//   假定新加入的后端是健康的，由实际请求结果驱动熔断状态变化
//   若初始化为 Open 则需要等一个健康检查周期才能使用，影响首次请求延迟
func (m *Manager) AddProvider(ctx context.Context, config ProviderConfig) error {
	if !config.Enabled {
		logrus.Infof("[EmbeddingManager] Provider %s is disabled, skipping", config.Name)
		return nil
	}

	factory, ok := GetFactory(config.Type)
	if !ok {
		return fmt.Errorf("unknown provider type: %s", config.Type)
	}

	embedder, err := factory(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create embedder for %s: %w", config.Name, err)
	}

	provider := &Provider{
		config:       config,
		embedder:     embedder,
		circuitState: CircuitStateClosed,
	}

	m.mu.Lock()
	m.providers = append(m.providers, provider)
	m.mu.Unlock()

	logrus.WithContext(ctx).Infof("[EmbeddingManager] Added provider: %s (type=%s, priority=%d, weight=%d)",
		config.Name, config.Type, config.Priority, config.Weight)

	return nil
}

// Start 幂等启动管理器（重复调用安全，内部用 isRunning 标志防止重复启动健康检查协程）
func (m *Manager) Start() {
	m.mu.Lock()
	if m.isRunning {
		m.mu.Unlock()
		return
	}
	m.isRunning = true
	m.mu.Unlock()

	logrus.Info("[EmbeddingManager] Starting embedding manager...")

	if m.config.HealthCheckInterval > 0 {
		m.wg.Add(1)
		go m.healthCheckLoop()
	}
}

// Stop 优雅停止管理器：cancel 通知所有关联 goroutine → wg.Wait 等待退出 → 无泄漏
// 先设 isRunning=false 防止 Start 重入，再 cancel context 触发 healthCheckLoop 退出
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.isRunning {
		m.mu.Unlock()
		return
	}
	m.isRunning = false
	m.mu.Unlock()

	logrus.Info("[EmbeddingManager] Stopping embedding manager...")
	m.cancel()
	m.wg.Wait()
	logrus.Info("[EmbeddingManager] Embedding manager stopped")
}

// EmbedStrings 核心调用入口，流程：选择 Provider → 调用 → 失败则重试/故障转移
// Priority 策略下 attempt=0 选最高优先级，attempt=1 选次优先级，实现逐级降级
// 其他策略下每次 attempt 按策略重新选择（可能命中同一个或不同 Provider）
func (m *Manager) EmbedStrings(ctx context.Context, texts []string) ([][]float64, error) {
	providers := m.getAvailableProviders()
	if len(providers) == 0 {
		return nil, errors.New("no available embedding providers")
	}

	var lastErr error
	for attempt := 0; attempt <= m.config.MaxRetries; attempt++ {
		provider := m.selectProvider(providers, attempt)
		if provider == nil {
			continue
		}

		// 熔断器检查：Open 态直接跳过，避免向已知故障的后端发送请求
		if !m.canUseProvider(provider) {
			logrus.WithContext(ctx).Debugf("[EmbeddingManager] Provider %s is circuit-broken, skipping", provider.config.Name)
			continue
		}

		result, err := m.embedWithProvider(ctx, provider, texts)
		if err == nil {
			return result, nil
		}

		lastErr = err
		logrus.WithContext(ctx).Warnf("[EmbeddingManager] Provider %s failed (attempt %d/%d): %v",
			provider.config.Name, attempt+1, m.config.MaxRetries+1, err)

		// 指数退避等待，同时监听 ctx 取消以支持快速中断
		if attempt < m.config.MaxRetries {
			delay := m.calculateRetryDelay(attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	return nil, fmt.Errorf("all providers failed: %w", lastErr)
}

// embedWithProvider 调用单个 Provider 并更新熔断器状态
// 失败时递增 consecutiveFails，达到阈值触发 Open；成功时清零计数并恢复 Closed
func (m *Manager) embedWithProvider(ctx context.Context, provider *Provider, texts []string) ([][]float64, error) {
	startTime := time.Now()

	timeout := provider.config.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	vectors, err := provider.embedder.EmbedStrings(ctx, texts)

	latency := time.Since(startTime)

	provider.mu.Lock()
	provider.totalRequests++
	provider.totalLatency += latency.Nanoseconds()

	if err != nil {
		provider.failedRequests++
		provider.consecutiveFails++
		provider.lastFailTime = time.Now()

		// 连续失败达到阈值 → 触发熔断（Closed → Open）
		if provider.consecutiveFails >= int64(m.config.CircuitThreshold) {
			provider.circuitState = CircuitStateOpen
			logrus.Warnf("[EmbeddingManager] Provider %s circuit opened after %d consecutive failures",
				provider.config.Name, provider.consecutiveFails)
		}
		provider.mu.Unlock()
		return nil, err
	}

	provider.successRequests++
	provider.consecutiveFails = 0 // 成功一次即清零，防止偶发失败误触发熔断
	// HalfOpen 态探测成功 → 恢复正常（HalfOpen → Closed）
	if provider.circuitState == CircuitStateHalfOpen {
		provider.circuitState = CircuitStateClosed
		logrus.Infof("[EmbeddingManager] Provider %s circuit closed", provider.config.Name)
	}
	provider.mu.Unlock()

	return vectors, nil
}

// getAvailableProviders 返回所有启用的 Provider
// Priority 策略下按 Priority 升序排列（数值小 = 优先级高），使 selectProvider 的 attempt 索引对应优先级降级顺序
// 使用冒泡排序而非 sort.Slice：Provider 数量通常 ≤5 个，简单排序足够且避免引入 sort 包的闭包开销
func (m *Manager) getAvailableProviders() []*Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Provider, 0)
	for _, p := range m.providers {
		if p.config.Enabled {
			result = append(result, p)
		}
	}

	if m.config.Strategy == LoadBalancePriority {
		sorted := make([]*Provider, len(result))
		copy(sorted, result)
		for i := 0; i < len(sorted)-1; i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[j].config.Priority < sorted[i].config.Priority {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		return sorted
	}

	return result
}

// selectProvider 按策略选择一个 Provider
// Priority: attempt 即为已排序列表的索引，attempt=0 为最高优先级，逐次降级
// RoundRobin: 原子递增计数器取模，无锁实现均匀轮转
// Weighted: 按权重随机选择，权重越大被选中概率越高
func (m *Manager) selectProvider(providers []*Provider, attempt int) *Provider {
	if len(providers) == 0 {
		return nil
	}

	switch m.config.Strategy {
	case LoadBalanceRoundRobin:
		idx := atomic.AddInt64(&m.rrIndex, 1) - 1
		return providers[idx%int64(len(providers))]

	case LoadBalanceRandom:
		return providers[rand.Intn(len(providers))]

	case LoadBalanceWeighted:
		return m.selectWeighted(providers)

	case LoadBalancePriority:
		if attempt < len(providers) {
			return providers[attempt]
		}
		return providers[0] // 所有 Provider 都试过后回到最高优先级重试

	default:
		return providers[0]
	}
}

// selectWeighted 加权随机选择：累加权重，随机数落在哪个区间就选谁
// 跳过熔断中的 Provider，使流量自动转移到健康节点
func (m *Manager) selectWeighted(providers []*Provider) *Provider {
	totalWeight := 0
	for _, p := range providers {
		if m.canUseProvider(p) {
			totalWeight += p.config.Weight
		}
	}

	if totalWeight == 0 {
		return nil
	}

	r := rand.Intn(totalWeight)
	for _, p := range providers {
		if m.canUseProvider(p) {
			r -= p.config.Weight
			if r < 0 {
				return p
			}
		}
	}

	return providers[0]
}

// canUseProvider 熔断器状态机判定
//   Closed   → 放行
//   Open     → 检查超时：已过冷却期则转 HalfOpen 并放行，否则快速拒绝
//   HalfOpen → 限流放行：已放行探测数 < HalfOpenMax 则放行，否则拒绝
func (m *Manager) canUseProvider(provider *Provider) bool {
	provider.mu.Lock()
	defer provider.mu.Unlock()

	switch provider.circuitState {
	case CircuitStateClosed:
		return true

	case CircuitStateOpen:
		// 冷却期过后进入半开状态，允许少量探测请求测试后端是否恢复
		if time.Since(provider.lastFailTime) > m.config.CircuitTimeout {
			provider.circuitState = CircuitStateHalfOpen
			provider.halfOpenCount = 0
			logrus.Infof("[EmbeddingManager] Provider %s entering half-open state", provider.config.Name)
			return true
		}
		return false

	case CircuitStateHalfOpen:
		// 限制探测并发：避免大量请求涌入刚恢复的后端
		if provider.halfOpenCount < int64(m.config.CircuitHalfOpenMax) {
			provider.halfOpenCount++
			return true
		}
		return false

	default:
		return true
	}
}

// calculateRetryDelay 指数退避 + 随机抖动
// delay(n) = min(base * multiplier^n, max_delay) + rand(0, delay/4)
// 抖动防止多客户端同时重试导致「惊群效应」
func (m *Manager) calculateRetryDelay(attempt int) time.Duration {
	delay := m.config.RetryDelay
	for i := 0; i < attempt; i++ {
		delay = time.Duration(float64(delay) * m.config.RetryMultiplier)
	}
	if delay > m.config.RetryMaxDelay {
		delay = m.config.RetryMaxDelay
	}
	jitter := time.Duration(rand.Int63n(int64(delay / 4)))
	return delay + jitter
}

// healthCheckLoop 后台健康检查协程，按 HealthCheckInterval 周期执行
// 作用：主动探测而非被动等待请求失败，使 Open 态的 Provider 能更快被发现已恢复
func (m *Manager) healthCheckLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.performHealthCheck()
		}
	}
}

// performHealthCheck 对每个 Provider 并发发起探测（各 Provider 互不阻塞）
func (m *Manager) performHealthCheck() {
	m.mu.RLock()
	providers := make([]*Provider, len(m.providers))
	copy(providers, m.providers)
	m.mu.RUnlock()

	for _, p := range providers {
		go m.checkProviderHealth(p)
	}
}

// checkProviderHealth 发送轻量 probe 请求，根据结果更新熔断器
// 健康检查成功可直接将 Open → Closed，绕过 HalfOpen 阶段（因为是主动探测而非用户请求）
func (m *Manager) checkProviderHealth(provider *Provider) {
	ctx, cancel := context.WithTimeout(m.ctx, m.config.HealthCheckTimeout)
	defer cancel()

	_, err := provider.embedder.EmbedStrings(ctx, []string{"health check"})

	provider.mu.Lock()
	defer provider.mu.Unlock()

	if err != nil {
		logrus.Warnf("[EmbeddingManager] Health check failed for %s: %v", provider.config.Name, err)
		provider.consecutiveFails++
		if provider.consecutiveFails >= int64(m.config.CircuitThreshold) {
			provider.circuitState = CircuitStateOpen
			provider.lastFailTime = time.Now()
		}
	} else {
		// 健康检查成功 → 直接恢复 Closed，跳过 HalfOpen
		if provider.circuitState == CircuitStateOpen {
			provider.circuitState = CircuitStateClosed
			provider.consecutiveFails = 0
			logrus.Infof("[EmbeddingManager] Provider %s recovered from circuit break", provider.config.Name)
		}
	}
}

// GetStats 获取所有 Provider 的统计快照
// 返回的是值拷贝而非指针/引用，调用方可安全持有、序列化或跨 goroutine 传递，无需担心数据竞争
// 外层 RLock 仅保护 providers 列表遍历，每个 provider 的统计由其自身 RLock 保护
func (m *Manager) GetStats() []ProviderStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := make([]ProviderStats, 0, len(m.providers))
	for _, p := range m.providers {
		stats = append(stats, m.getProviderStats(p))
	}
	return stats
}

// getProviderStats 创建 Provider 统计的只读快照
// 健康判定规则：Closed + 成功率≥95% = Healthy; Closed + 成功率<95% 或 HalfOpen = Degraded; Open = Unhealthy
func (m *Manager) getProviderStats(p *Provider) ProviderStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := ProviderStats{
		Name:            p.config.Name,
		CircuitState:    p.circuitState,
		TotalRequests:   p.totalRequests,
		SuccessRequests: p.successRequests,
		FailedRequests:  p.failedRequests,
		Priority:        p.config.Priority,
		Weight:          p.config.Weight,
	}

	if stats.TotalRequests > 0 {
		stats.SuccessRate = float64(stats.SuccessRequests) / float64(stats.TotalRequests) * 100
	}

	if stats.SuccessRequests > 0 {
		stats.AvgLatency = time.Duration(p.totalLatency / stats.SuccessRequests)
	}

	switch p.circuitState {
	case CircuitStateClosed:
		if stats.SuccessRate >= 95 {
			stats.Status = ProviderStatusHealthy
		} else {
			stats.Status = ProviderStatusDegraded
		}
	case CircuitStateHalfOpen:
		stats.Status = ProviderStatusDegraded
	case CircuitStateOpen:
		stats.Status = ProviderStatusUnhealthy
	}

	return stats
}

// ResetStats 重置统计信息
// 外层用 RLock 而非 Lock：我们只读 providers 列表（不增删 provider），
// 每个 provider 的统计清零由其自身 Lock 保护，RLock 允许与 GetStats 并发执行
func (m *Manager) ResetStats() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, p := range m.providers {
		p.mu.Lock()
		p.totalRequests = 0
		p.successRequests = 0
		p.failedRequests = 0
		p.totalLatency = 0
		p.mu.Unlock()
	}
}

// InitGlobalManager 初始化全局管理器（支持热替换：先 Stop 旧实例释放资源，再创建新实例）
// 全局单例设计：整个进程共享一个 Manager，避免多处创建导致 Provider 连接重复和资源浪费
// 包级别函数 EmbedStrings()/GetStats() 委托给此单例，简化调用方代码
func InitGlobalManager(config ManagerConfig) *Manager {
	globalMu.Lock()
	defer globalMu.Unlock()

	if globalManager != nil {
		globalManager.Stop()
	}

	globalManager = NewManager(config)
	return globalManager
}

// GetGlobalManager 获取全局管理器
func GetGlobalManager() *Manager {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalManager
}

// EmbedStrings 包级别便捷函数，委托给全局 Manager
// 调用方无需持有 Manager 引用，直接 rag.EmbedStrings() 即可，降低跨模块耦合
func EmbedStrings(ctx context.Context, texts []string) ([][]float64, error) {
	m := GetGlobalManager()
	if m == nil {
		return nil, errors.New("embedding manager not initialized")
	}
	return m.EmbedStrings(ctx, texts)
}

// GetStats 获取全局管理器统计（包级别函数）
func GetStats() []ProviderStats {
	m := GetGlobalManager()
	if m == nil {
		return nil
	}
	return m.GetStats()
}
