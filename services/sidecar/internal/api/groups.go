package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

type groupBatchRequest struct {
	AccountIDs    []string `json:"accountIds"`
	Action        string   `json:"action"`
	Targets       []string `json:"targets"`
	IntervalMs    int      `json:"intervalMs"`
	FailStopAfter int      `json:"failStopAfter"`
}

type groupBatchResponse struct {
	OK         bool                       `json:"ok"`
	TaskID     string                     `json:"taskId,omitempty"`
	Action     string                     `json:"action"`
	Total      int                        `json:"total"`
	Success    int                        `json:"success"`
	Failed     int                        `json:"failed"`
	Stopped    bool                       `json:"stopped,omitempty"`
	StopReason string                     `json:"stopReason,omitempty"`
	Items      []tgclient.GroupItemResult `json:"items"`
	Note       string                     `json:"note"`
}

/**
 * 批量加入或退出群组/频道（F06/F07）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleGroupBatch(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}

	var req groupBatchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	action := strings.TrimSpace(req.Action)
	if action == "" {
		action = "join"
	}
	if action != "join" && action != "leave" {
		writeErr(w, http.StatusBadRequest, "invalid_action", "action must be join|leave")
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

	interval := time.Duration(req.IntervalMs) * time.Millisecond
	if interval < 500*time.Millisecond {
		interval = 2000 * time.Millisecond
	}
	failStop := failStopAfterOrDefault(req.FailStopAfter)

	total := len(targets) * len(accounts)
	items := make([]tgclient.GroupItemResult, 0, total)
	taskItems := make([]store.TaskItem, 0, total)
	success := 0
	consecutive := 0
	stopped := false
	stopReason := ""
	idx := 0
	for _, target := range targets {
		for _, accountID := range accounts {
			if idx > 0 {
				select {
				case <-r.Context().Done():
					stopped, stopReason = true, "cancelled"
				case <-time.After(interval):
				}
			}
			if stopped {
				break
			}
			idx++

			var result tgclient.GroupItemResult
			if action == "join" {
				result = s.TG.JoinGroup(r.Context(), accountID, target)
			} else {
				result = s.TG.LeaveGroup(r.Context(), accountID, target)
			}
			retried := false
			if result.BusinessResult == tgclient.ResultFlood && result.WaitSeconds > 0 {
				if awaitFlood(r.Context(), result.WaitSeconds) {
					if action == "join" {
						result = s.TG.JoinGroup(r.Context(), accountID, target)
					} else {
						result = s.TG.LeaveGroup(r.Context(), accountID, target)
					}
					retried = true
				}
			}
			if result.OK {
				success++
			}
			items = append(items, result)
			taskItems = append(taskItems, taskItemFromGroup(result, retried))
			consecutive = nextConsecutiveFails(consecutive, result.OK)
			if shouldCircuitBreak(failStop, consecutive) {
				stopped, stopReason = true, fmt.Sprintf("circuit_break_after_%d_fails", failStop)
				break
			}
		}
		if stopped {
			break
		}
	}

	allOK := !stopped && success == total
	taskID := s.saveFinishedTask("groups", fmt.Sprintf("群组%s ×%d", action, len(items)), allOK, taskItems)
	if stopped && taskID != "" {
		if stopReason == "cancelled" {
			_ = s.Store.FinishTask(taskID, "cancelled")
		} else {
			_ = s.Store.FinishTask(taskID, "circuit_break")
		}
	}
	writeJSON(w, http.StatusOK, groupBatchResponse{
		OK: allOK, TaskID: taskID, Action: action, Total: total,
		Success: success, Failed: len(items) - success, Stopped: stopped, StopReason: stopReason,
		Items: items, Note: "本地限速，非 Telegram 官方额度；连续失败达阈值将熔断",
	})
}
