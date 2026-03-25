/*
┌──────────────────────────────────────────────────────────────────────────────┐
│ task_queue.go — Redis Streams 异步索引任务队列                               │
├──────────────────────────────────────────────────────────────────────────────┤
│ 设计目标: 通过 Redis Streams 实现分布式任务队列，支持多实例竞争消费与容错。     │
│                                                                              │
│ 结构:                                                                        │
│  - 常量: TaskStatusPending/Processing/Completed/Failed                       │
│  - 数据结构:                                                                 │
│      IndexTask        — 任务载荷 + 状态快照 (用于状态查询)                    │
│      TaskQueueConfig  — 队列配置 (stream/group/status TTL 等)               │
│      TaskQueue        — 队列操作封装 (提交/消费/ACK/状态)                     │
│  - 构造: DefaultTaskQueueConfig / NewTaskQueue                               │
│  - 关键方法:                                                                 │
│      EnsureGroup    — 幂等创建消费者组 (XGROUP MKSTREAM)                      │
│      Submit         — 写入状态 + XADD 入队                                    │
│      Consume        — 先 claim PEL 过期消息，再阻塞拉取新消息                 │
│      Ack            — XACK 确认消费                                           │
│      UpdateStatus   — 状态机更新 (pending/processing/...)                    │
│      GetStatus      — 读取任务状态                                            │
│      claimStale     — XAUTOCLAIM/XPENDING 处理超时任务                        │
└──────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	redisCli "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

const (
	TaskStatusPending    = "pending"
	TaskStatusProcessing = "processing"
	TaskStatusCompleted  = "completed"
	TaskStatusFailed     = "failed"
)

// IndexTask 异步索引任务
type IndexTask struct {
	TaskID    string       `json:"task_id"`
	UserID    uint         `json:"user_id"`
	FileID    string       `json:"file_id"`
	FileName  string       `json:"file_name"`
	Content   string       `json:"content"`
	Format    string       `json:"format,omitempty"`
	Status    string       `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at,omitempty"`
	Error     string       `json:"error,omitempty"`
	Result    *IndexResult `json:"result,omitempty"`
	MessageID string       `json:"-"` // Redis Stream message ID, not serialized
}

// TaskQueueConfig 任务队列配置
type TaskQueueConfig struct {
	Enabled         bool          `toml:"enabled"`
	StreamKey       string        `toml:"stream_key"`
	GroupName       string        `toml:"group_name"`
	StatusPrefix    string        `toml:"status_prefix"`
	WorkerCount     int           `toml:"worker_count"`
	TaskTTL         time.Duration `toml:"task_ttl"`
	ClaimTimeout    time.Duration `toml:"claim_timeout"`
	WebhookURL      string        `toml:"webhook_url"`       // 任务完成/失败后的回调 URL（可选）
	WebhookSecret   string        `toml:"webhook_secret"`    // HMAC-SHA256 签名密钥，接收方可验证请求合法性
	WebhookMaxRetry int           `toml:"webhook_max_retry"` // 最大重试次数，默认 3
}

// DefaultTaskQueueConfig 默认配置
func DefaultTaskQueueConfig() TaskQueueConfig {
	return TaskQueueConfig{
		Enabled:      false,
		StreamKey:    "rag:index:tasks",
		GroupName:    "rag-workers",
		StatusPrefix: "rag:task:",
		WorkerCount:  3,
		TaskTTL:      24 * time.Hour,
		ClaimTimeout: 5 * time.Minute,
	}
}

// TaskQueue Redis Streams 任务队列
type TaskQueue struct {
	redis           redisCli.UniversalClient
	streamKey       string
	groupName       string
	statusPrefix    string
	taskTTL         time.Duration
	claimTimeout    time.Duration
	webhookURL      string // Webhook 回调 URL
	webhookSecret   string // HMAC 签名密钥
	webhookMaxRetry int    // 最大重试次数
}

// NewTaskQueue 创建任务队列
func NewTaskQueue(redis redisCli.UniversalClient, cfg TaskQueueConfig) *TaskQueue {
	streamKey := cfg.StreamKey
	if streamKey == "" {
		streamKey = "rag:index:tasks"
	}
	groupName := cfg.GroupName
	if groupName == "" {
		groupName = "rag-workers"
	}
	statusPrefix := cfg.StatusPrefix
	if statusPrefix == "" {
		statusPrefix = "rag:task:"
	}
	taskTTL := cfg.TaskTTL
	if taskTTL == 0 {
		taskTTL = 24 * time.Hour
	}
	claimTimeout := cfg.ClaimTimeout
	if claimTimeout == 0 {
		claimTimeout = 5 * time.Minute
	}
	webhookMaxRetry := cfg.WebhookMaxRetry
	if webhookMaxRetry <= 0 {
		webhookMaxRetry = 3
	}
	return &TaskQueue{
		redis:           redis,
		streamKey:       streamKey,
		groupName:       groupName,
		statusPrefix:    statusPrefix,
		taskTTL:         taskTTL,
		claimTimeout:    claimTimeout,
		webhookURL:      cfg.WebhookURL,
		webhookSecret:   cfg.WebhookSecret,
		webhookMaxRetry: webhookMaxRetry,
	}
}

// EnsureGroup 创建消费者组（幂等）
func (q *TaskQueue) EnsureGroup(ctx context.Context) error {
	// 使用 MKSTREAM 可在 stream 不存在时自动创建，保证首次启动即可工作
	// 幂等性由 Redis 返回 BUSYGROUP 错误控制
	err := q.redis.XGroupCreateMkStream(ctx, q.streamKey, q.groupName, "0").Err()
	if err != nil && !isGroupExistsError(err) {
		return fmt.Errorf("create consumer group: %w", err)
	}
	return nil
}

// Submit 提交异步索引任务，返回 taskID
func (q *TaskQueue) Submit(ctx context.Context, userID uint, fileID, fileName, content, format string) (string, error) {
	taskID := uuid.New().String()
	now := time.Now()

	task := IndexTask{
		TaskID:    taskID,
		UserID:    userID,
		FileID:    fileID,
		FileName:  fileName,
		Format:    format,
		Status:    TaskStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// 先写入任务状态（读路径更快），再写入 Stream；失败时可回滚状态 Key
	statusData, err := json.Marshal(task)
	if err != nil {
		return "", fmt.Errorf("marshal task status: %w", err)
	}
	statusKey := q.statusPrefix + taskID
	if err := q.redis.Set(ctx, statusKey, statusData, q.taskTTL).Err(); err != nil {
		return "", fmt.Errorf("set task status: %w", err)
	}

	_, err = q.redis.XAdd(ctx, &redisCli.XAddArgs{
		Stream: q.streamKey,
		Values: map[string]interface{}{
			"task_id":   taskID,
			"user_id":   userID,
			"file_id":   fileID,
			"file_name": fileName,
			"content":   content,
			"format":    format,
		},
	}).Result()
	if err != nil {
		// XADD 失败时删除状态，避免出现“状态存在但队列无消息”的不一致
		q.redis.Del(ctx, statusKey)
		return "", fmt.Errorf("xadd task: %w", err)
	}

	logrus.Infof("[TaskQueue] Submitted task %s (file=%s, user=%d)", taskID, fileID, userID)
	return taskID, nil
}

// Consume 消费一条任务（阻塞最多 blockDuration）
func (q *TaskQueue) Consume(ctx context.Context, consumer string, blockDuration time.Duration) (*IndexTask, error) {
	// 先尝试认领超时的 PEL 消息
	task, err := q.claimStale(ctx, consumer)
	if err != nil {
		logrus.Warnf("[TaskQueue] Claim stale error: %v", err)
	}
	if task != nil {
		return task, nil
	}

	streams, err := q.redis.XReadGroup(ctx, &redisCli.XReadGroupArgs{
		Group:    q.groupName,
		Consumer: consumer,
		Streams:  []string{q.streamKey, ">"},
		Count:    1,
		Block:    blockDuration, // 阻塞拉取新消息，避免 busy loop
	}).Result()
	if err != nil {
		if err == redisCli.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("xreadgroup: %w", err)
	}

	for _, stream := range streams {
		for _, msg := range stream.Messages {
			return parseStreamMessage(msg), nil
		}
	}
	return nil, nil
}

// Ack 确认消费
func (q *TaskQueue) Ack(ctx context.Context, messageID string) error {
	return q.redis.XAck(ctx, q.streamKey, q.groupName, messageID).Err()
}

// UpdateStatus 更新任务状态
func (q *TaskQueue) UpdateStatus(ctx context.Context, taskID, status string, result *IndexResult, errMsg string) error {
	statusKey := q.statusPrefix + taskID
	data, err := q.redis.Get(ctx, statusKey).Bytes()
	if err != nil {
		return fmt.Errorf("get task status: %w", err)
	}

	var task IndexTask
	if err := json.Unmarshal(data, &task); err != nil {
		return fmt.Errorf("unmarshal task: %w", err)
	}

	task.Status = status
	task.UpdatedAt = time.Now()
	task.Result = result
	task.Error = errMsg

	updated, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal updated task: %w", err)
	}

	ttl := q.redis.TTL(ctx, statusKey).Val()
	if ttl <= 0 {
		ttl = q.taskTTL
	}
	return q.redis.Set(ctx, statusKey, updated, ttl).Err()
}

// GetStatus 查询任务状态
func (q *TaskQueue) GetStatus(ctx context.Context, taskID string) (*IndexTask, error) {
	statusKey := q.statusPrefix + taskID
	data, err := q.redis.Get(ctx, statusKey).Bytes()
	if err != nil {
		if err == redisCli.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("get task status: %w", err)
	}
	var task IndexTask
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("unmarshal task: %w", err)
	}
	return &task, nil
}

// claimStale 认领超时未 ACK 的消息（PEL 中停留超过 claimTimeout 的）
func (q *TaskQueue) claimStale(ctx context.Context, consumer string) (*IndexTask, error) {
	msgs, _, err := q.redis.XAutoClaim(ctx, &redisCli.XAutoClaimArgs{
		Stream:   q.streamKey,
		Group:    q.groupName,
		Consumer: consumer,
		MinIdle:  q.claimTimeout,
		Start:    "0-0",
		Count:    1,
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	return parseStreamMessage(msgs[0]), nil
}

func parseStreamMessage(msg redisCli.XMessage) *IndexTask {
	task := &IndexTask{
		MessageID: msg.ID,
		CreatedAt: time.Now(),
	}
	if v, ok := msg.Values["task_id"].(string); ok {
		task.TaskID = v
	}
	if v, ok := msg.Values["user_id"]; ok {
		switch uid := v.(type) {
		case string:
			var n uint64
			fmt.Sscanf(uid, "%d", &n)
			task.UserID = uint(n)
		}
	}
	if v, ok := msg.Values["file_id"].(string); ok {
		task.FileID = v
	}
	if v, ok := msg.Values["file_name"].(string); ok {
		task.FileName = v
	}
	if v, ok := msg.Values["content"].(string); ok {
		task.Content = v
	}
	if v, ok := msg.Values["format"].(string); ok {
		task.Format = v
	}
	return task
}

func isGroupExistsError(err error) bool {
	return err != nil && (err.Error() == "BUSYGROUP Consumer Group name already exists" ||
		containsString(err.Error(), "BUSYGROUP"))
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- Webhook 回调通知 ---

// NotifyWebhook 在任务完成或失败时发送 Webhook 回调通知。
// 支持 HMAC-SHA256 签名验证和指数退避重试（问题 12）。
// 4xx 客户端错误不重试，5xx 服务端错误重试。
func (q *TaskQueue) NotifyWebhook(task *IndexTask) {
	if q.webhookURL == "" {
		return
	}

	go func() {
		payload, err := json.Marshal(map[string]interface{}{
			"event":     "task_" + task.Status,
			"task_id":   task.TaskID,
			"file_id":   task.FileID,
			"file_name": task.FileName,
			"user_id":   task.UserID,
			"status":    task.Status,
			"error":     task.Error,
			"result":    task.Result,
			"timestamp": time.Now().UTC(),
		})
		if err != nil {
			logrus.Warnf("[TaskQueue] Webhook payload marshal failed: %v", err)
			return
		}

		client := &http.Client{Timeout: 10 * time.Second}

		for attempt := 0; attempt <= q.webhookMaxRetry; attempt++ {
			if attempt > 0 {
				// 指数退避: 1s, 2s, 4s
				delay := time.Duration(1<<uint(attempt-1)) * time.Second
				time.Sleep(delay)
				logrus.Infof("[TaskQueue] Webhook retry %d/%d for task %s",
					attempt, q.webhookMaxRetry, task.TaskID)
			}

			req, reqErr := http.NewRequest(http.MethodPost, q.webhookURL, bytes.NewReader(payload))
			if reqErr != nil {
				logrus.Warnf("[TaskQueue] Webhook request creation failed: %v", reqErr)
				return
			}

			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Webhook-Event", "task_"+task.Status)
			req.Header.Set("X-Webhook-Delivery", task.TaskID)

			// HMAC-SHA256 签名：接收方可用同一密钥验证请求合法性
			if q.webhookSecret != "" {
				mac := hmac.New(sha256.New, []byte(q.webhookSecret))
				mac.Write(payload)
				signature := hex.EncodeToString(mac.Sum(nil))
				req.Header.Set("X-Webhook-Signature", "sha256="+signature)
			}

			resp, doErr := client.Do(req)
			if doErr != nil {
				logrus.Warnf("[TaskQueue] Webhook call failed (attempt %d): %v",
					attempt+1, doErr)
				continue // 重试
			}
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				logrus.Infof("[TaskQueue] Webhook notified for task %s (status=%s)",
					task.TaskID, task.Status)
				return // 成功，退出
			}

			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				// 4xx 客户端错误不重试（如 401/404/422 等）
				logrus.Warnf("[TaskQueue] Webhook returned %d (client error, no retry) for task %s",
					resp.StatusCode, task.TaskID)
				return
			}

			// 5xx 服务端错误，继续重试
			logrus.Warnf("[TaskQueue] Webhook returned %d (attempt %d), will retry",
				resp.StatusCode, attempt+1)
		}

		logrus.Errorf("[TaskQueue] Webhook exhausted all %d retries for task %s",
			q.webhookMaxRetry+1, task.TaskID)
	}()
}
