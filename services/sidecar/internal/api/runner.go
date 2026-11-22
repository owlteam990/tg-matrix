package api

import (
	"context"
	"sync"
)

// Runner 管理可取消的后台任务。
type Runner struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

/**
 * 创建任务运行器。
 *
 * @return Runner
 * @author Auto
 * @date 2026-10-09
 */
func NewRunner() *Runner {
	return &Runner{cancels: map[string]context.CancelFunc{}}
}

/**
 * 注册并启动后台任务；结束后自动注销 cancel。
 *
 * @param taskID 任务 ID
 * @param fn 执行函数（接收可取消 ctx）
 * @author Auto
 * @date 2026-10-09
 */
func (r *Runner) Go(taskID string, fn func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	if old, ok := r.cancels[taskID]; ok {
		old()
	}
	r.cancels[taskID] = cancel
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.cancels, taskID)
			r.mu.Unlock()
			cancel()
		}()
		fn(ctx)
	}()
}

/**
 * 取消后台任务。
 *
 * @param taskID 任务 ID
 * @return 是否找到并取消
 * @author Auto
 * @date 2026-10-09
 */
func (r *Runner) Cancel(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	cancel, ok := r.cancels[taskID]
	if !ok {
		return false
	}
	cancel()
	delete(r.cancels, taskID)
	return true
}

/**
 * 是否有进行中的后台任务。
 *
 * @param taskID 任务 ID
 * @return 是否运行中
 * @author Auto
 * @date 2026-10-09
 */
func (r *Runner) IsRunning(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.cancels[taskID]
	return ok
}
