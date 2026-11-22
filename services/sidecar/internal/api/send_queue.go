package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tg-matrix/sidecar/internal/tgclient"
)

type patchSendQueueBody struct {
	Text         *string `json:"text"`
	AccountID    *string `json:"accountId"`
	NotBefore    *int64  `json:"notBefore"`
	AutoSchedule *bool   `json:"autoSchedule"` // true：按策略自动写入建议可发时间
}

/**
 * 统一列出发送队列（营销 + 群发）。
 *
 * 查询参数：source=bulk|marketing、status、limit
 */
func (s *Server) handleListSendQueue(w http.ResponseWriter, r *http.Request) {
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	items, err := s.Store.ListSendQueueItems(source, status, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	// 列表展示统一中文错误说明
	for i := range items {
		if items[i].LastError != "" {
			items[i].LastError = tgclient.HumanizeSendError("", items[i].LastError)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

/**
 * 按策略计算下次可发时间。
 *
 * 查询参数：accountId（可选）
 */
func (s *Server) handleSuggestSendQueueReady(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少队列项 ID")
		return
	}
	if s.SendSched == nil {
		writeErr(w, http.StatusServiceUnavailable, "scheduler_unavailable", "发送调度器未就绪")
		return
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
	result, err := s.SendSched.SuggestReady(id, accountID)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "不存在") {
			writeErr(w, http.StatusNotFound, "not_found", msg)
			return
		}
		writeErr(w, http.StatusBadRequest, "suggest_failed", msg)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

/**
 * 修改待发送项（文案 / 协议号 / 计划时间）。
 */
func (s *Server) handlePatchSendQueueItem(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少队列项 ID")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var body patchSendQueueBody
	if err := json.Unmarshal(raw, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if body.Text == nil && body.AccountID == nil && body.NotBefore == nil &&
		(body.AutoSchedule == nil || !*body.AutoSchedule) {
		writeErr(w, http.StatusBadRequest, "bad_request", "请提供 text / accountId / notBefore / autoSchedule")
		return
	}

	notBefore := body.NotBefore
	suggestReason := ""
	if body.AutoSchedule != nil && *body.AutoSchedule {
		if s.SendSched == nil {
			writeErr(w, http.StatusServiceUnavailable, "scheduler_unavailable", "发送调度器未就绪")
			return
		}
		aid := ""
		if body.AccountID != nil {
			aid = strings.TrimSpace(*body.AccountID)
		}
		sug, err := s.SendSched.SuggestReady(id, aid)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "suggest_failed", err.Error())
			return
		}
		nb := sug.NotBefore
		if nb < time.Now().UnixMilli() {
			nb = time.Now().UnixMilli()
		}
		notBefore = &nb
		suggestReason = sug.Reason
		if body.AccountID == nil && sug.AccountID != "" {
			aid := sug.AccountID
			body.AccountID = &aid
		}
	}

	if notBefore != nil {
		// 允许秒级时间戳，统一成毫秒
		if *notBefore > 0 && *notBefore < 1e12 {
			v := *notBefore * 1000
			notBefore = &v
		}
	}

	it, err := s.Store.UpdatePendingSendQueueItem(id, body.Text, body.AccountID, notBefore)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "不存在") {
			writeErr(w, http.StatusNotFound, "not_found", msg)
			return
		}
		if strings.Contains(msg, "仅待发送") || strings.Contains(msg, "文案不能为空") {
			writeErr(w, http.StatusBadRequest, "invalid_state", msg)
			return
		}
		writeErr(w, http.StatusInternalServerError, "update_failed", msg)
		return
	}
	resp := map[string]any{"ok": true, "item": it}
	if suggestReason != "" {
		resp["suggestReason"] = suggestReason
	}
	writeJSON(w, http.StatusOK, resp)
}

/**
 * 删除待发送项。
 */
func (s *Server) handleDeleteSendQueueItem(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少队列项 ID")
		return
	}
	if err := s.Store.DeletePendingSendQueueItem(id); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "不存在") {
			writeErr(w, http.StatusNotFound, "not_found", msg)
			return
		}
		if strings.Contains(msg, "仅待发送") {
			writeErr(w, http.StatusBadRequest, "invalid_state", msg)
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete_failed", msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
