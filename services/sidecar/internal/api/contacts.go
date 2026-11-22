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

type contactBatchRequest struct {
	AccountIDs    []string `json:"accountIds"`
	Action        string   `json:"action"`
	Targets       []string `json:"targets"`
	IntervalMs    int      `json:"intervalMs"`
	FailStopAfter int      `json:"failStopAfter"`
}

type contactBatchResponse struct {
	OK         bool                         `json:"ok"`
	TaskID     string                       `json:"taskId,omitempty"`
	Action     string                       `json:"action"`
	Total      int                          `json:"total"`
	Success    int                          `json:"success"`
	Failed     int                          `json:"failed"`
	Stopped    bool                         `json:"stopped,omitempty"`
	StopReason string                       `json:"stopReason,omitempty"`
	Items      []tgclient.ContactItemResult `json:"items"`
	Note       string                       `json:"note"`
}

/**
 * 读取协议号本地 SQLite 通讯录（不请求 Telegram）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleListContacts(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
	if accountID == "" {
		writeErr(w, http.StatusBadRequest, "account_required", "accountId required")
		return
	}
	items, err := s.Store.ListContacts(accountID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "contacts_failed", err.Error())
		return
	}
	syncedAt, _ := s.Store.ContactsSyncedAt(accountID)
	writeJSON(w, http.StatusOK, map[string]any{
		"accountId": accountID,
		"items":     items,
		"total":     len(items),
		"syncedAt":  syncedAt,
		"source":    "sqlite",
		"note":      "本地缓存；点「刷新」才请求 Telegram 并覆盖该协议号通讯录",
	})
}

/**
 * 从 Telegram 拉取通讯录并写入该协议号本地缓存。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleRefreshContacts(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
	if accountID == "" {
		var body struct {
			AccountID string `json:"accountId"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		accountID = strings.TrimSpace(body.AccountID)
	}
	if accountID == "" {
		writeErr(w, http.StatusBadRequest, "account_required", "accountId required")
		return
	}
	items, err := s.TG.RefreshContacts(r.Context(), accountID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "refresh_failed", err.Error())
		return
	}
	syncedAt, _ := s.Store.ContactsSyncedAt(accountID)
	writeJSON(w, http.StatusOK, map[string]any{
		"accountId": accountID,
		"items":     items,
		"total":     len(items),
		"syncedAt":  syncedAt,
		"source":    "telegram",
		"note":      "已从 Telegram 刷新并写入本地 SQLite（按协议号隔离）",
	})
}

/**
 * 批量添加或删除联系人（F19/F20）。
 * 成功后仅局部更新本地 SQLite，不主动全量拉 TG。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleContactBatch(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}

	var req contactBatchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	action := strings.TrimSpace(req.Action)
	if action == "" {
		action = "add"
	}
	if action != "add" && action != "delete" {
		writeErr(w, http.StatusBadRequest, "invalid_action", "action must be add|delete")
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
		interval = 1500 * time.Millisecond
	}
	failStop := failStopAfterOrDefault(req.FailStopAfter)

	total := len(targets) * len(accounts)
	items := make([]tgclient.ContactItemResult, 0, total)
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

			var result tgclient.ContactItemResult
			if action == "add" {
				result = s.TG.AddContact(r.Context(), accountID, target)
			} else {
				result = s.TG.DeleteContact(r.Context(), accountID, target)
			}
			retried := false
			if result.BusinessResult == tgclient.ResultFlood && result.WaitSeconds > 0 {
				if awaitFlood(r.Context(), result.WaitSeconds) {
					if action == "add" {
						result = s.TG.AddContact(r.Context(), accountID, target)
					} else {
						result = s.TG.DeleteContact(r.Context(), accountID, target)
					}
					retried = true
				}
			}
			if result.OK {
				success++
				applyLocalContactCache(s.Store, action, result)
			}
			items = append(items, result)
			taskItems = append(taskItems, taskItemFromContact(result, retried))
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
	taskID := s.saveFinishedTask("contacts", fmt.Sprintf("联系人%s ×%d", action, len(items)), allOK, taskItems)
	if stopped && taskID != "" {
		if stopReason == "cancelled" {
			_ = s.Store.FinishTask(taskID, "cancelled")
		} else {
			_ = s.Store.FinishTask(taskID, "circuit_break")
		}
	}
	writeJSON(w, http.StatusOK, contactBatchResponse{
		OK: allOK, TaskID: taskID, Action: action, Total: total,
		Success: success, Failed: len(items) - success, Stopped: stopped, StopReason: stopReason,
		Items: items,
		Note:  "本地限速；成功项已写入/删除该协议号 SQLite 缓存，未全量拉 TG",
	})
}

/**
 * 根据添加/删除结果局部更新本地通讯录缓存。
 *
 * @param st 存储
 * @param action add|delete
 * @param result 单条结果
 */
func applyLocalContactCache(st *store.Store, action string, result tgclient.ContactItemResult) {
	if st == nil || result.PeerID == 0 || result.AccountID == "" {
		return
	}
	if action == "delete" {
		_ = st.DeleteContact(result.AccountID, result.PeerID)
		return
	}
	mutual := false
	if result.Mutual != nil {
		mutual = *result.Mutual
	}
	_ = st.UpsertContact(store.Contact{
		AccountID: result.AccountID,
		UserID:    result.PeerID,
		Username:  result.Username,
		FirstName: result.FirstName,
		LastName:  result.LastName,
		Phone:     result.Phone,
		Mutual:    mutual,
		SyncedAt:  time.Now().Unix(),
	})
}
