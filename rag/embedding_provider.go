/*
┌───────────────────────────────────────────────────────────────────────────┐
│                   embedding_provider.go 结构总览                          │
├───────────────────────────────────────────────────────────────────────────┤
│                                                                           │
│  核心设计：OpenAI 兼容的统一接入层                                        │
│    所有工厂底层均使用 eino-ext/ark 组件，该组件实现了标准的                │
│    OpenAI /v1/embeddings 协议。因此任何兼容该协议的服务均可接入：         │
│      - 火山引擎 Ark（原生支持）                                          │
│      - DashScope / Azure OpenAI / vLLM / Ollama / TEI 等                 │
│    新增 Provider 类型只需修改 BaseURL 和 APIKey，无需编写新的 SDK 适配   │
│                                                                           │
│  init()                          — 自动注册三种内置工厂: ark/openai/local│
│                                                                           │
│  工厂函数 (均满足 EmbedderFactory 签名)                                  │
│    NewArkEmbedder()              — 火山引擎 Ark 平台（参考实现）         │
│    NewOpenAICompatibleEmbedder() — 兼容 OpenAI /v1/embeddings 协议       │
│    NewLocalEmbedder()            — 本地部署的 Embedding 服务              │
│                                                                           │
│  辅助函数                                                                │
│    ValidateProviderConfig()      — 校验配置合法性（验证工厂是否注册）     │
│    CreateProviderFromConfig()    — 批量创建 Provider 并组装 Manager       │
│                                                                           │
│  ProviderConfig.Dimension 的用途说明                                     │
│    该字段仅用于 Redis VectorStore 创建索引时声明向量维度（FT.CREATE       │
│    需要预声明 DIM），实际嵌入维度由模型 API 返回的 []float64 长度决定。   │
│    不同模型维度不同（text-embedding-3-small=1536, text-embedding-v3=1024）│
│    配置错误不影响 Embedding 调用，但会导致 Redis 索引创建失败             │
│                                                                           │
└───────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"context"
	"fmt"

	embeddingArk "github.com/cloudwego/eino-ext/components/embedding/ark"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/sirupsen/logrus"
)

// init 在包加载时自动注册所有内置 Provider 工厂
// 新增 Provider 类型只需添加一行 RegisterFactory 调用
func init() {
	RegisterFactory("ark", NewArkEmbedder)
	RegisterFactory("openai", NewOpenAICompatibleEmbedder)
	RegisterFactory("local", NewLocalEmbedder)
}

// NewArkEmbedder 创建火山引擎 Ark Embedder
func NewArkEmbedder(ctx context.Context, config ProviderConfig) (embedding.Embedder, error) {
	if config.BaseURL == "" {
		return nil, fmt.Errorf("ark provider requires base_url")
	}
	if config.APIKey == "" {
		return nil, fmt.Errorf("ark provider requires api_key")
	}
	if config.Model == "" {
		return nil, fmt.Errorf("ark provider requires model")
	}

	embedConfig := &embeddingArk.EmbeddingConfig{
		BaseURL: config.BaseURL,
		APIKey:  config.APIKey,
		Model:   config.Model,
	}

	embedder, err := embeddingArk.NewEmbedder(ctx, embedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create ark embedder: %w", err)
	}

	logrus.Infof("[EmbeddingProvider] Created Ark embedder: %s (model=%s)", config.Name, config.Model)
	return embedder, nil
}

// NewOpenAICompatibleEmbedder 创建 OpenAI 兼容 Embedder
// 底层复用 eino-ext/ark 组件，因为该组件实现的是标准 OpenAI /v1/embeddings 协议，
// 所以 DashScope、Azure OpenAI、vLLM 等兼容端点均可通过修改 BaseURL 接入
func NewOpenAICompatibleEmbedder(ctx context.Context, config ProviderConfig) (embedding.Embedder, error) {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}
	if config.APIKey == "" {
		return nil, fmt.Errorf("openai provider requires api_key")
	}
	if config.Model == "" {
		config.Model = "text-embedding-ada-002"
	}

	embedConfig := &embeddingArk.EmbeddingConfig{
		BaseURL: config.BaseURL,
		APIKey:  config.APIKey,
		Model:   config.Model,
	}

	embedder, err := embeddingArk.NewEmbedder(ctx, embedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create openai compatible embedder: %w", err)
	}

	logrus.Infof("[EmbeddingProvider] Created OpenAI compatible embedder: %s (model=%s, base_url=%s)",
		config.Name, config.Model, config.BaseURL)
	return embedder, nil
}

// NewLocalEmbedder 创建本地部署的 Embedding 服务
// 同样复用 eino-ext/ark（OpenAI 协议兼容），适用于 Ollama / vLLM / TEI 等本地服务
func NewLocalEmbedder(ctx context.Context, config ProviderConfig) (embedding.Embedder, error) {
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:8080/embeddings"
	}

	embedConfig := &embeddingArk.EmbeddingConfig{
		BaseURL: config.BaseURL,
		APIKey:  config.APIKey,
		Model:   config.Model,
	}

	embedder, err := embeddingArk.NewEmbedder(ctx, embedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create local embedder: %w", err)
	}

	logrus.Infof("[EmbeddingProvider] Created Local embedder: %s (model=%s, base_url=%s)",
		config.Name, config.Model, config.BaseURL)
	return embedder, nil
}

// ValidateProviderConfig 校验配置合法性，确保 type 已注册对应工厂
func ValidateProviderConfig(config ProviderConfig) error {
	if config.Name == "" {
		return fmt.Errorf("provider name is required")
	}
	if config.Type == "" {
		return fmt.Errorf("provider type is required")
	}

	if _, ok := GetFactory(config.Type); !ok {
		return fmt.Errorf("unknown provider type: %s", config.Type)
	}

	return nil
}

// CreateProviderFromConfig 批量创建 Provider 并组装为 Manager
// 跳过无效/创建失败的配置而非整体失败，保证尽可能多的 Provider 可用
func CreateProviderFromConfig(ctx context.Context, configs []ProviderConfig) (*Manager, error) {
	manager := NewManager(DefaultManagerConfig())

	for _, config := range configs {
		if err := ValidateProviderConfig(config); err != nil {
			logrus.Warnf("[EmbeddingProvider] Invalid config for %s: %v", config.Name, err)
			continue
		}

		if err := manager.AddProvider(ctx, config); err != nil {
			logrus.Errorf("[EmbeddingProvider] Failed to add provider %s: %v", config.Name, err)
		}
	}

	if len(manager.providers) == 0 {
		return nil, fmt.Errorf("no valid providers configured")
	}

	return manager, nil
}
