package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"agentbox/internal/platform"
)

const (
	logRecorderBuffer     = 1024
	logRecorderBatchSize  = 50
	logRecorderFlushEvery = 500 * time.Millisecond
	logMessageMaxLen      = 1024
)

// logRecorder 异步批量写入系统日志：Record 非阻塞，
// 写协程按时间或批量阈值攒批调用 store.InsertLogs。
type logRecorder struct {
	store interface {
		InsertLogs(context.Context, []platform.LogEntry) error
	}
	logger  *slog.Logger
	entries chan platform.LogEntry
	stop    chan struct{}
	done    chan struct{}
	dropped atomic.Int64
}

func newLogRecorder(store interface {
	InsertLogs(context.Context, []platform.LogEntry) error
}, logger *slog.Logger) *logRecorder {
	recorder := &logRecorder{
		store:   store,
		logger:  logger,
		entries: make(chan platform.LogEntry, logRecorderBuffer),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go recorder.run()
	return recorder
}

// Record 投递一条日志；缓冲区满时丢弃并计数告警，绝不阻塞调用方。
func (r *logRecorder) Record(entry platform.LogEntry) {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	if entry.Level == "" {
		entry.Level = platform.LogLevelInfo
	}
	if entry.Status == "" {
		entry.Status = platform.LogStatusSuccess
	}
	if len(entry.Message) > logMessageMaxLen {
		entry.Message = entry.Message[:logMessageMaxLen]
	}
	select {
	case r.entries <- entry:
	default:
		if dropped := r.dropped.Add(1); dropped == 1 || dropped%100 == 0 {
			r.logger.Warn("system log buffer full, entry dropped", "dropped", dropped)
		}
	}
}

func (r *logRecorder) run() {
	defer close(r.done)
	ticker := time.NewTicker(logRecorderFlushEvery)
	defer ticker.Stop()
	batch := make([]platform.LogEntry, 0, logRecorderBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.store.InsertLogs(ctx, batch); err != nil {
			r.logger.Warn("insert system logs failed", "count", len(batch), "error", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case entry := <-r.entries:
			batch = append(batch, entry)
			if len(batch) >= logRecorderBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-r.stop:
			// 关停：排空缓冲区后做最后一次 flush。
			for {
				select {
				case entry := <-r.entries:
					batch = append(batch, entry)
					if len(batch) >= logRecorderBatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// Close 停止写协程并 flush 剩余日志。
func (r *logRecorder) Close(ctx context.Context) error {
	close(r.stop)
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// recordLog 补充操作者（登录用户）与来源地址后投递日志。
// entry.ActorID 已设置时保留（如登录成功后写入被认证用户）。
// logRecorder 为 nil（部分单测直接构造 Server）时静默跳过。
func (s *Server) recordLog(request *http.Request, entry platform.LogEntry) {
	if s.logRecorder == nil {
		return
	}
	if request != nil {
		if entry.RemoteAddr == "" {
			entry.RemoteAddr = s.clientIP(request)
		}
		if entry.ActorID == "" {
			if actor := userFromContext(request.Context()); actor.ID != "" {
				entry.ActorID = actor.ID
				entry.ActorName = actor.Name
			}
		}
	}
	s.logRecorder.Record(entry)
}

// RecordSystem 写入一条无请求上下文的系统事件日志（启动/关停等）。
func (s *Server) RecordSystem(level, action, message string, detail map[string]any) {
	if s.logRecorder == nil {
		return
	}
	s.logRecorder.Record(platform.LogEntry{
		Level: level, Category: platform.LogCategorySystem,
		Action: action, Message: message, Detail: detail,
	})
}

// Close 停止日志写入协程并 flush 剩余日志。
func (s *Server) Close(ctx context.Context) error {
	if s.logRecorder == nil {
		return nil
	}
	return s.logRecorder.Close(ctx)
}
