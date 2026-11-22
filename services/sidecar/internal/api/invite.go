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

type inviteBatchRequest struct {
	AccountIDs    []string `json:"accountIds"`
	Group         string   `json:"group"`
	Members       []string `json:"members"`
	Mode          string   `json:"mode"`
	IntervalMs    int      `json:"intervalMs"`
	FailStopAfter int      `json:"failStopAfter"`
}

type inviteBatchResponse struct {
	OK         bool                        `json:"ok"`
	TaskID     string                      `json:"taskId,omitempty"`
	Mode       string                      `json:"mode"`
	Group      string                      `json:"group"`
	Total      int                         `json:"total"`
	Success    int                         `json:"success"`
	Failed     int                         `json:"failed"`
	Stopped    bool                        `json:"stopped,omitempty"`
	StopReason string                      `json:"stopReason,omitempty"`
	Items      []tgclient.InviteItemResult `json:"items"`
	Note       string                      `json:"note"`
}

/**
 * 批量邀请成员入群（F08 联系人 / F09 用户名）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleInviteBatch(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}

	var req inviteBatchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = tgclient.InviteModeUsername
	}
	if mode != tgclient.InviteModeContact && mode != tgclient.InviteModeUsername {
		writeErr(w, http.StatusBadRequest, "invalid_mode", "mode must be contact|username")
		return
	}

	group := strings.TrimSpace(req.Group)
	if group == "" {
		writeErr(w, http.StatusBadRequest, "group_required", "group username required")
		return
	}

	accounts := uniqueNonEmpty(req.AccountIDs)
	if len(accounts) == 0 {
		writeErr(w, http.StatusBadRequest, "accounts_required", "select at least one account")
		return
	}
	members := uniqueNonEmpty(req.Members)
	if len(members) == 0 {
		writeErr(w, http.StatusBadRequest, "members_required", "members required")
		return
	}

	interval := time.Duration(req.IntervalMs) * time.Millisecond
	if interval < 500*time.Millisecond {
		interval = 2000 * time.Millisecond
	}
	failStop := failStopAfterOrDefault(req.FailStopAfter)

	total := len(members) * len(accounts)
	items := make([]tgclient.InviteItemResult, 0, total)
	taskItems := make([]store.TaskItem, 0, total)
	success := 0
	consecutive := 0
	stopped := false
	stopReason := ""
	idx := 0

	for _, member := range members {
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

			result := s.TG.InviteToGroup(r.Context(), accountID, group, member, mode)
			retried := false
			if result.BusinessResult == tgclient.ResultFlood && result.WaitSeconds > 0 {
				if awaitFlood(r.Context(), result.WaitSeconds) {
					result = s.TG.InviteToGroup(r.Context(), accountID, group, member, mode)
					retried = true
				}
			}
			if result.OK {
				success++
			}
			items = append(items, result)
			taskItems = append(taskItems, taskItemFromInvite(result, retried))
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
	taskID := s.saveFinishedTask("invite", fmt.Sprintf("邀请 %s ×%d", group, len(items)), allOK, taskItems)
	if stopped && taskID != "" {
		if stopReason == "cancelled" {
			_ = s.Store.FinishTask(taskID, "cancelled")
		} else {
			_ = s.Store.FinishTask(taskID, "circuit_break")
		}
	}
	writeJSON(w, http.StatusOK, inviteBatchResponse{
		OK: allOK, TaskID: taskID, Mode: mode, Group: group, Total: total,
		Success: success, Failed: len(items) - success, Stopped: stopped, StopReason: stopReason,
		Items: items,
		Note:  "本地限速，非 Telegram 官方额度；无邀请权 / 对方隐私 / 非双向联系人会单独成错",
	})
}
