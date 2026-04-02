package rag

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
//  Webhook 通知 — P3 功能增强
//
//  索引完成/删除/错误等事件通过 Webhook 通知外部系统。
//  支持 HMAC-SHA256 签名验证、异步发送、自动重试。
//
//  使用方式：
//    notifier := NewWebhookNotifier(WebhookConfig{URL: "https://...", Secret: "xxx"})
//    notifier.Start()
//    defer notifier.Stop()
//    notifier.Notify(WebhookEvent{Type: "index.complete", ...})
// ──────────────────────────────────────────────────────────────────────────────

// WebhookEventType 事件类型
type WebhookEventType string

const (
	EventIndexComplete WebhookEventType = "index.complete"
	EventIndexFailed   WebhookEventType = "index.failed"
	EventDocDeleted    WebhookEventType = "doc.deleted"
	EventSearchError   WebhookEventType = "search.error"
	EventHealthChanged WebhookEventType = "health.changed"
)

// WebhookEvent Webhook 事件
type WebhookEvent struct {
	ID        string                 `json:"id"`
	Type      WebhookEventType       `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	UserID    int64                  `json:"user_id,omitempty"`
	FileID    string                 `json:"file_id,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

// WebhookConfig Webhook 配置
type WebhookConfig struct {
	Enabled    bool          `toml:"enabled"`
	URL        string        `toml:"url"`
	Secret     string        `toml:"secret"`      // HMAC 签名密钥
	Timeout    time.Duration `toml:"timeout"`     // HTTP 请求超时
	MaxRetries int           `toml:"max_retries"` // 最大重试次数
	BufferSize int           `toml:"buffer_size"` // 异步缓冲区大小
}

// DefaultWebhookConfig 默认 Webhook 配置
func DefaultWebhookConfig() WebhookConfig {
	return WebhookConfig{
		Enabled:    false,
		Timeout:    10 * time.Second,
		MaxRetries: 3,
		BufferSize: 100,
	}
}

// WebhookNotifier 异步 Webhook 通知器
type WebhookNotifier struct {
	config WebhookConfig
	client *http.Client
	events chan WebhookEvent
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

// NewWebhookNotifier 创建 Webhook 通知器
func NewWebhookNotifier(cfg WebhookConfig) *WebhookNotifier {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.BufferSize == 0 {
		cfg.BufferSize = 100
	}

	return &WebhookNotifier{
		config: cfg,
		client: &http.Client{Timeout: cfg.Timeout},
		events: make(chan WebhookEvent, cfg.BufferSize),
	}
}

// Start 启动异步发送 goroutine
func (n *WebhookNotifier) Start() {
	if !n.config.Enabled || n.config.URL == "" {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	n.cancel = cancel

	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		for {
			select {
			case <-ctx.Done():
				// 清空缓冲区
				for {
					select {
					case evt := <-n.events:
						n.send(evt)
					default:
						return
					}
				}
			case evt := <-n.events:
				n.send(evt)
			}
		}
	}()

	log.Printf("[Webhook] Notifier started: url=%s, buffer=%d", n.config.URL, n.config.BufferSize)
}

// Stop 停止通知器，等待缓冲区清空
func (n *WebhookNotifier) Stop() {
	if n.cancel != nil {
		n.cancel()
	}
	n.wg.Wait()
	log.Println("[Webhook] Notifier stopped")
}

// Notify 发送 Webhook 事件（异步，非阻塞）
func (n *WebhookNotifier) Notify(evt WebhookEvent) {
	if !n.config.Enabled {
		return
	}

	if evt.ID == "" {
		evt.ID = fmt.Sprintf("evt_%d", time.Now().UnixNano())
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}

	select {
	case n.events <- evt:
	default:
		log.Printf("[Webhook] Event buffer full, dropping event: %s/%s", evt.Type, evt.ID)
	}
}

// NotifyIndexComplete 索引完成通知（便捷方法）
func (n *WebhookNotifier) NotifyIndexComplete(userID int64, fileID string, chunks int, duration time.Duration) {
	n.Notify(WebhookEvent{
		Type:   EventIndexComplete,
		UserID: userID,
		FileID: fileID,
		Data: map[string]interface{}{
			"chunks":      chunks,
			"duration_ms": duration.Milliseconds(),
		},
	})
}

// NotifyIndexFailed 索引失败通知（便捷方法）
func (n *WebhookNotifier) NotifyIndexFailed(userID int64, fileID string, err error) {
	n.Notify(WebhookEvent{
		Type:   EventIndexFailed,
		UserID: userID,
		FileID: fileID,
		Data: map[string]interface{}{
			"error": err.Error(),
		},
	})
}

// send 实际发送 HTTP POST 请求（带重试）
func (n *WebhookNotifier) send(evt WebhookEvent) {
	payload, err := json.Marshal(evt)
	if err != nil {
		log.Printf("[Webhook] Marshal error: %v", err)
		return
	}

	for attempt := 0; attempt <= n.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// 指数退避: 1s, 2s, 4s
			time.Sleep(time.Duration(1<<uint(attempt-1)) * time.Second)
		}

		req, err := http.NewRequest("POST", n.config.URL, bytes.NewReader(payload))
		if err != nil {
			log.Printf("[Webhook] Request create error: %v", err)
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Webhook-Event", string(evt.Type))
		req.Header.Set("X-Webhook-ID", evt.ID)

		// HMAC-SHA256 签名
		if n.config.Secret != "" {
			sig := computeHMAC(n.config.Secret, payload)
			req.Header.Set("X-Webhook-Signature", "sha256="+sig)
		}

		resp, err := n.client.Do(req)
		if err != nil {
			log.Printf("[Webhook] Send error (attempt %d/%d): %v", attempt+1, n.config.MaxRetries+1, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return // 成功
		}

		log.Printf("[Webhook] Non-2xx response (attempt %d/%d): %d", attempt+1, n.config.MaxRetries+1, resp.StatusCode)
	}

	log.Printf("[Webhook] All retries exhausted for event %s/%s", evt.Type, evt.ID)
}

// computeHMAC 计算 HMAC-SHA256 签名
func computeHMAC(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
