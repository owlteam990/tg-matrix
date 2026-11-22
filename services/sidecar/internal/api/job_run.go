package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

/**
 * FloodWait：落库 waiting + 倒计时，到期后清回 running；取消则返回 false。
 *
 * @param ctx 上下文
 * @param taskID 任务 ID（空则仅本地 sleep）
 * @param waitSec 等待秒数
 * @param reason 原因文案
 * @return 是否完整等到点
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) awaitFloodTracked(ctx context.Context, taskID string, waitSec int, reason string) bool {
	wait := floodWaitDuration(waitSec)
	if wait <= 0 {
		return false
	}
	if taskID != "" && s.Store != nil {
		until := time.Now().Add(wait).Unix()
		if reason == "" {
			reason = "flood_wait"
		}
		_ = s.Store.SetTaskWaiting(taskID, until, reason)
	}
	ok := false
	select {
	case <-ctx.Done():
		ok = false
	case <-time.After(wait):
		ok = true
	}
	if taskID != "" && s.Store != nil {
		// 被取消时由 cancel 路径写终态；此处仅在仍应继续时清 waiting
		if ok {
			_ = s.Store.ClearTaskWaiting(taskID)
		}
	}
	return ok
}

/**
 * 启动（或续跑）私信可恢复任务。
 *
 * @param taskID 任务 ID
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) startMessagesJob(taskID string) {
	if s.Runner == nil {
		return
	}
	s.Runner.Go(taskID, func(ctx context.Context) {
		status := s.executeMessagesJob(ctx, taskID)
		if status != "" {
			_ = s.Store.FinishTask(taskID, status)
		}
	})
}

/**
 * 执行私信任务：只处理 pending 明细；FloodWait 进入 waiting。
 *
 * @param ctx 上下文
 * @param taskID 任务 ID
 * @return 终态（空串表示已被外部写成 cancelled）
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) executeMessagesJob(ctx context.Context, taskID string) string {
	t, ok, err := s.Store.GetTask(taskID)
	if err != nil || !ok {
		return store.TaskFailed
	}
	var payload MessagesJobPayload
	if err := json.Unmarshal([]byte(t.PayloadJSON), &payload); err != nil || payload.Kind != jobKindMessages {
		return store.TaskFailed
	}
	gap := accountSendGap(payload.IntervalMs)
	failStop := failStopAfterOrDefault(payload.FailStopAfter)

	_ = s.Store.SetTaskProgress(taskID, store.TaskRunning, t.CursorIndex)

	pending, err := s.Store.ListPendingTaskItems(taskID)
	if err != nil {
		return store.TaskFailed
	}
	consecutive := 0
	doneBefore := t.TotalCount - len(pending)
	if doneBefore < 0 {
		doneBefore = 0
	}
	cursor := doneBefore

	for _, item := range pending {
		select {
		case <-ctx.Done():
			_, _ = s.Store.MarkRemainingItemsCancelled(taskID)
			return store.TaskCancelled
		default:
		}

		// 1. 同一协议号发信间隔 ≥5 分钟
		if waitAccountSendGap(ctx, item.AccountID, gap) {
			_, _ = s.Store.MarkRemainingItemsCancelled(taskID)
			return store.TaskCancelled
		}

		_ = s.Store.MarkTaskItemInflight(item.ID)
		_ = s.Store.SetTaskProgress(taskID, store.TaskRunning, cursor)

		result := s.TG.SendDirect(ctx, item.AccountID, payload.Mode, item.Target, payload.Text)
		retried := false
		if result.BusinessResult == tgclient.ResultFlood && result.WaitSeconds > 0 {
			if s.awaitFloodTracked(ctx, taskID, result.WaitSeconds, fmt.Sprintf("flood_wait_%ds", result.WaitSeconds)) {
				result = s.TG.SendDirect(ctx, item.AccountID, payload.Mode, item.Target, payload.Text)
				retried = true
			} else {
				_, _ = s.Store.MarkRemainingItemsCancelled(taskID)
				_ = s.Store.UpdateTaskItemResult(
					item.ID, store.ItemCancelled, "cancelled_during_wait", "", "",
					"cancelled", "cancelled during flood wait",
				)
				return store.TaskCancelled
			}
		}
		markAccountSent(item.AccountID)

		ti := taskItemFromSend(result, retried)
		status := store.ItemFailed
		if result.OK {
			status = store.ItemOK
			consecutive = 0
		} else {
			consecutive++
		}
		_ = s.Store.UpdateTaskItemResult(
			item.ID, status, ti.LocalResult, ti.APIResult, ti.BusinessResult, ti.ErrorCode, ti.ErrorMessage,
		)
		cursor++
		_ = s.Store.SetTaskProgress(taskID, store.TaskRunning, cursor)

		if shouldCircuitBreak(failStop, consecutive) {
			// 剩余保持 pending，便于任务中心「继续」续跑
			return store.TaskCircuitBreak
		}
	}

	// 根据明细汇总终态
	failN := s.Store.CountTaskItemsByStatus(taskID, store.ItemFailed)
	unkN := s.Store.CountTaskItemsByStatus(taskID, store.ItemUnknown)
	okN := s.Store.CountTaskItemsByStatus(taskID, store.ItemOK)
	if failN == 0 && unkN == 0 {
		return store.TaskSuccess
	}
	if okN == 0 {
		return store.TaskFailed
	}
	return store.TaskDonePartial
}

/**
 * 从 interrupted/waiting 等状态续跑可恢复任务。
 *
 * @param taskID 任务 ID
 * @return 错误信息（空表示已启动）
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) resumeJob(taskID string) string {
	if s.TG == nil || !s.TG.Configured() {
		return "gotd_not_configured"
	}
	if s.Runner == nil {
		return "runner_unavailable"
	}
	if s.Runner.IsRunning(taskID) {
		return "already_running"
	}
	t, ok, err := s.Store.GetTask(taskID)
	if err != nil || !ok {
		return "not_found"
	}
	if !t.Resumable() {
		return "not_resumable"
	}
	if s.Store.CountTaskItemsByStatus(taskID, store.ItemPending) == 0 {
		return "nothing_pending"
	}
	switch jobKindOf(t.PayloadJSON) {
	case jobKindMessages:
		_ = s.Store.SetTaskProgress(taskID, store.TaskRunning, t.CursorIndex)
		s.startMessagesJob(taskID)
		return ""
	default:
		return "unsupported_kind"
	}
}

/**
 * 放弃可恢复任务：剩余 pending 标 cancelled，任务标 cancelled。
 *
 * @param taskID 任务 ID
 * @return 错误码
 */
func (s *Server) abandonJob(taskID string) string {
	if s.Runner != nil && s.Runner.IsRunning(taskID) {
		s.Runner.Cancel(taskID)
	}
	t, ok, _ := s.Store.GetTask(taskID)
	if !ok {
		return "not_found"
	}
	switch t.Status {
	case store.TaskInterrupted, store.TaskWaiting, store.TaskRunning, store.TaskPending, store.TaskCircuitBreak:
	default:
		return "not_abandonable"
	}
	_, _ = s.Store.MarkRemainingItemsCancelled(taskID)
	_ = s.Store.FinishTask(taskID, store.TaskCancelled)
	return ""
}
