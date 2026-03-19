/*
┌──────────────────────────────────────────────────────────────────────────────┐
│ worker.go — 异步索引 Worker Pool                                              │
├──────────────────────────────────────────────────────────────────────────────┤
│ 目标: 多实例环境下并发消费 Redis Streams，提升索引吞吐并实现故障恢复。          │
│                                                                              │
│ 结构:                                                                        │
│  - IndexWorker: worker 池（queue/store/配置/ctx/wg）                          │
│  - NewIndexWorker(): 构造 + 默认参数                                           │
│  - Start(): 启动 N 个 worker，使用消费者组保证“至少一次”                     │
│  - Stop(): cancel + 等待退出，实现优雅关闭                                    │
│  - runWorker(): 消费循环（Consume → process → Ack）                          │
│  - processTask(): 调用 MultiFileRetriever.IndexDocument 执行实际索引          │
└──────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// IndexWorker 异步索引 Worker Pool
type IndexWorker struct {
	queue           *TaskQueue
	store           VectorStore
	retCfg          *RetrieverConfig
	chunkCfg        *ChunkingConfig
	consumer        string
	workers         int
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	graphStore      GraphStore      // 可选: 用于异步任务完成后自动提取实体
	entityExtractor EntityExtractor // 可选: 实体提取器
}

// NewIndexWorker 创建 Worker
func NewIndexWorker(queue *TaskQueue, store VectorStore, retCfg *RetrieverConfig, chunkCfg *ChunkingConfig, consumer string, workers int) *IndexWorker {
	if workers <= 0 {
		workers = 3
	}
	if consumer == "" {
		consumer = fmt.Sprintf("worker-%s", newUUID()[:8])
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &IndexWorker{
		queue:    queue,
		store:    store,
		retCfg:   retCfg,
		chunkCfg: chunkCfg,
		consumer: consumer,
		workers:  workers,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// SetGraphRAG 设置 Graph RAG 依赖，用于异步任务完成后自动提取实体
func (w *IndexWorker) SetGraphRAG(graphStore GraphStore, extractor EntityExtractor) {
	w.graphStore = graphStore
	w.entityExtractor = extractor
}

// Start 启动 worker pool
func (w *IndexWorker) Start() error {
	// 启动前确保消费者组存在，避免并发启动时报错
	if err := w.queue.EnsureGroup(w.ctx); err != nil {
		return fmt.Errorf("ensure consumer group: %w", err)
	}

	for i := 0; i < w.workers; i++ {
		workerName := fmt.Sprintf("%s-%d", w.consumer, i)
		w.wg.Add(1)
		go w.runWorker(workerName)
	}

	logrus.Infof("[IndexWorker] Started %d workers (consumer=%s)", w.workers, w.consumer)
	return nil
}

// Stop 优雅关闭
func (w *IndexWorker) Stop() {
	logrus.Info("[IndexWorker] Stopping workers...")
	// cancel 让所有 worker 结束阻塞读，再等待 goroutine 正常退出
	w.cancel()
	w.wg.Wait()
	logrus.Info("[IndexWorker] All workers stopped")
}

func (w *IndexWorker) runWorker(name string) {
	defer w.wg.Done()
	logrus.Infof("[IndexWorker] Worker %s started", name)

	for {
		select {
		case <-w.ctx.Done():
			logrus.Infof("[IndexWorker] Worker %s shutting down", name)
			return
		default:
		}

		// 阻塞读取任务，超时后循环以响应 ctx.Done()
		task, err := w.queue.Consume(w.ctx, name, 2*time.Second)
		if err != nil {
			if w.ctx.Err() != nil {
				return
			}
			logrus.Warnf("[IndexWorker] %s consume error: %v", name, err)
			time.Sleep(time.Second)
			continue
		}
		if task == nil {
			continue
		}

		w.processTask(name, task)
	}
}

func (w *IndexWorker) processTask(workerName string, task *IndexTask) {
	defer func() {
		if r := recover(); r != nil {
			errMsg := fmt.Sprintf("panic: %v", r)
			logrus.Errorf("[IndexWorker] %s panic processing task %s: %s", workerName, task.TaskID, errMsg)
			_ = w.queue.UpdateStatus(w.ctx, task.TaskID, TaskStatusFailed, nil, errMsg)
			if task.MessageID != "" {
				_ = w.queue.Ack(w.ctx, task.MessageID)
			}
		}
	}()

	logrus.Infof("[IndexWorker] %s processing task %s (file=%s, user=%d)",
		workerName, task.TaskID, task.FileID, task.UserID)

	_ = w.queue.UpdateStatus(w.ctx, task.TaskID, TaskStatusProcessing, nil, "")

	ctx, cancel := context.WithTimeout(w.ctx, 5*time.Minute)
	defer cancel()

	// 每个任务创建独立检索器，避免跨用户共享状态导致串扰
	retriever, err := NewMultiFileRetriever(ctx, w.store, nil, w.retCfg, w.chunkCfg, task.UserID)
	if err != nil {
		errMsg := fmt.Sprintf("create retriever: %v", err)
		logrus.Errorf("[IndexWorker] %s task %s failed: %s", workerName, task.TaskID, errMsg)
		_ = w.queue.UpdateStatus(w.ctx, task.TaskID, TaskStatusFailed, nil, errMsg)
		if task.MessageID != "" {
			_ = w.queue.Ack(w.ctx, task.MessageID)
		}
		return
	}

	content := task.Content
	if task.Format != "" {
		doc, parseErr := ParseDocument(content, DocumentFormat(task.Format))
		if parseErr == nil {
			content = doc.Content
		}
	}

	result, err := retriever.IndexDocument(ctx, task.FileID, task.FileName, content)
	if err != nil {
		errMsg := fmt.Sprintf("index document: %v", err)
		logrus.Errorf("[IndexWorker] %s task %s failed: %s", workerName, task.TaskID, errMsg)
		_ = w.queue.UpdateStatus(w.ctx, task.TaskID, TaskStatusFailed, nil, errMsg)
		// 失败时也发送 Webhook 通知
		w.queue.NotifyWebhook(&IndexTask{TaskID: task.TaskID, FileID: task.FileID, FileName: task.FileName, UserID: task.UserID, Status: TaskStatusFailed, Error: errMsg})
	} else {
		logrus.Infof("[IndexWorker] %s task %s completed: indexed=%d, failed=%d",
			workerName, task.TaskID, result.Indexed, result.Failed)
		_ = w.queue.UpdateStatus(w.ctx, task.TaskID, TaskStatusCompleted, result, "")
		// 索引成功后自动提取实体（非阻塞）
		w.extractEntitiesAsync(content, task.FileID)
		// 成功时发送 Webhook 通知
		w.queue.NotifyWebhook(&IndexTask{TaskID: task.TaskID, FileID: task.FileID, FileName: task.FileName, UserID: task.UserID, Status: TaskStatusCompleted, Result: result})
	}

	if task.MessageID != "" {
		if ackErr := w.queue.Ack(w.ctx, task.MessageID); ackErr != nil {
			logrus.Warnf("[IndexWorker] %s ack error for %s: %v", workerName, task.MessageID, ackErr)
		}
	}
}

// extractEntitiesAsync 异步任务完成后自动提取实体和关系写入图存储
// 将实体提取集成到 Worker 中，而非 tools 层的 fire-and-forget，提供更好的可视性
func (w *IndexWorker) extractEntitiesAsync(content, fileID string) {
	if w.graphStore == nil || w.entityExtractor == nil {
		return
	}

	go func() {
		extractCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		entities, relations, err := w.entityExtractor.Extract(extractCtx, content, fileID)
		if err != nil {
			logrus.Warnf("[IndexWorker] Entity extraction failed for %s: %v", fileID, err)
			return
		}

		if len(entities) > 0 {
			if err := w.graphStore.AddEntities(extractCtx, entities); err != nil {
				logrus.Warnf("[IndexWorker] Failed to store entities for %s: %v", fileID, err)
			}
		}
		if len(relations) > 0 {
			if err := w.graphStore.AddRelations(extractCtx, relations); err != nil {
				logrus.Warnf("[IndexWorker] Failed to store relations for %s: %v", fileID, err)
			}
		}

		if len(entities) > 0 || len(relations) > 0 {
			logrus.Infof("[IndexWorker] Graph RAG: extracted %d entities, %d relations for file %s",
				len(entities), len(relations), fileID)
		}
	}()
}
