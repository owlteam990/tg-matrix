package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

type sendMessageRequest struct {
	AccountIDs    []string `json:"accountIds"`
	Mode          string   `json:"mode"`
	Targets       []string `json:"targets"`
	Text          string   `json:"text"`
	IntervalMs    int      `json:"intervalMs"`
	FailStopAfter int      `json:"failStopAfter"`
	Async         bool     `json:"async"`
}

type sendMessageResponse struct {
	OK       bool                      `json:"ok"`
	Async    bool                      `json:"async,omitempty"`
	TaskID   string                    `json:"taskId,omitempty"`
	Mode     string                    `json:"mode"`
	Total    int                       `json:"total"`
	Success  int                       `json:"success"`
	Failed   int                       `json:"failed"`
	Stopped  bool                      `json:"stopped,omitempty"`
	StopReason string                  `json:"stopReason,omitempty"`
	Items    []tgclient.SendItemResult `json:"items"`
	Note     string                    `json:"note"`
}

/**
 * 批量发送私信（F01/F02/F03）。支持同步/异步与连续失败熔断。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleSendMessages(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}

	var req sendMessageRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = tgclient.ModeUsername
	}
	switch mode {
	case tgclient.ModeUsername, tgclient.ModePhone, tgclient.ModeUserID:
	default:
		writeErr(w, http.StatusBadRequest, "invalid_mode", "mode must be username|phone|userId")
		return
	}

	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeErr(w, http.StatusBadRequest, "text_required", "message text required")
		return
	}

	accounts := uniqueNonEmpty(req.AccountIDs)
	if len(accounts) == 0 {
		writeErr(w, http.StatusBadRequest, "accounts_required", "select at least one account")
		return
	}
	targets := uniqueNonEmpty(req.Targets)
	if len(targets) == 0 {
		writeErr(w, http.StatusBadRequest, "targets_required", "targets required")
		return
	}

	// 每协议号发一条后至少等 5 分钟；intervalMs 仅可调大
	gap := accountSendGap(req.IntervalMs)
	failStop := failStopAfterOrDefault(req.FailStopAfter)

	if req.Async {
		if s.Runner == nil {
			writeErr(w, http.StatusServiceUnavailable, "runner_unavailable", "async runner not ready")
			return
		}
		taskID := newTaskID()
		title := fmt.Sprintf("私信 %s ×%d", mode, len(targets))
		payload := encodeMessagesJob(MessagesJobPayload{
			AccountIDs: accounts, Mode: mode, Targets: targets, Text: text,
			IntervalMs: int(gap / time.Millisecond), FailStopAfter: failStop,
		})
		if err := s.Store.CreateJobTask(taskID, "messages", title, payload, len(targets)); err != nil {
			writeErr(w, http.StatusInternalServerError, "task_create_failed", err.Error())
			return
		}
		seeds := make([]store.TaskItem, 0, len(targets))
		for i, target := range targets {
			seeds = append(seeds, store.TaskItem{
				TaskID:    taskID,
				AccountID: accounts[i%len(accounts)],
				Target:    target,
				Status:    store.ItemPending,
			})
		}
		if err := s.Store.SeedPendingItems(taskID, seeds); err != nil {
			_ = s.Store.FinishTask(taskID, store.TaskFailed)
			writeErr(w, http.StatusInternalServerError, "task_seed_failed", err.Error())
			return
		}
		s.startMessagesJob(taskID)
		writeJSON(w, http.StatusAccepted, sendMessageResponse{
			OK: true, Async: true, TaskID: taskID, Mode: mode, Total: len(targets),
			Note: "异步可恢复任务：FloodWait 进入 waiting；中断后可在任务中心「继续」；本地限速，非 Telegram 官方额度",
		})
		return
	}

	items, taskItems, success, stopped, reason := s.runSendMessages(r.Context(), "", accounts, targets, mode, text, gap, failStop, false)
	allOK := !stopped && success == len(targets)
	status := "done"
	if stopped {
		if reason == "cancelled" {
			status = "cancelled"
		} else {
			status = "circuit_break"
		}
		allOK = false
	} else if !allOK {
		status = "done_partial"
	}
	taskID := s.saveFinishedTask("messages", fmt.Sprintf("私信 %s ×%d", mode, len(targets)), allOK, taskItems)
	if status == "circuit_break" || status == "cancelled" {
		// saveFinishedTask 用 done/done_partial；覆盖终态
		if taskID != "" {
			_ = s.Store.FinishTask(taskID, status)
		}
	}
	writeJSON(w, http.StatusOK, sendMessageResponse{
		OK: allOK, TaskID: taskID, Mode: mode, Total: len(targets),
		Success: success, Failed: len(items) - success, Stopped: stopped, StopReason: reason,
		Items: items, Note: "每协议号发信间隔≥5分钟（本地限速，非 Telegram 官方额度）",
	})
}

func (s *Server) runSendMessages(
	ctx context.Context,
	taskID string,
	accounts, targets []string,
	mode, text string,
	gap time.Duration,
	failStop int,
	persistLive bool,
) (items []tgclient.SendItemResult, taskItems []store.TaskItem, success int, stopped bool, reason string) {
	items = make([]tgclient.SendItemResult, 0, len(targets))
	taskItems = make([]store.TaskItem, 0, len(targets))
	consecutive := 0

	for i, target := range targets {
		accountID := accounts[i%len(accounts)]
		// 1. 同一协议号未满间隔则等待
		if waitAccountSendGap(ctx, accountID, gap) {
			return items, taskItems, success, true, "cancelled"
		}

		result := s.TG.SendDirect(ctx, accountID, mode, target, text)
		retried := false
		if result.BusinessResult == tgclient.ResultFlood && result.WaitSeconds > 0 {
			if awaitFlood(ctx, result.WaitSeconds) {
				result = s.TG.SendDirect(ctx, accountID, mode, target, text)
				retried = true
			}
		}
		// 2. 记点，避免同号短时间连发
		markAccountSent(accountID)
		if result.OK {
			success++
		}
		items = append(items, result)
		ti := taskItemFromSend(result, retried)
		if persistLive && taskID != "" {
			ti.TaskID = taskID
			_ = s.Store.AppendTaskItem(ti)
		}
		taskItems = append(taskItems, ti)

		consecutive = nextConsecutiveFails(consecutive, result.OK)
		if shouldCircuitBreak(failStop, consecutive) {
			return items, taskItems, success, true, fmt.Sprintf("circuit_break_after_%d_fails", failStop)
		}
	}
	return items, taskItems, success, false, ""
}
