package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

type joinPreviewRequest struct {
	AccountID string   `json:"accountId"`
	Targets   []string `json:"targets"`
}

type joinConfirmRequest struct {
	AccountID string   `json:"accountId"`
	Targets   []string `json:"targets"`
}

type joinCheckRequest struct {
	AccountID string   `json:"accountId"`
	Targets   []string `json:"targets"`
}

type joinChallengesRequest struct {
	AccountID string   `json:"accountId"`
	Targets   []string `json:"targets"`
	Limit     int      `json:"limit"`
}

type joinChallengeAnswerRequest struct {
	AccountID  string          `json:"accountId"`
	Target     string          `json:"target"`
	PeerType   string          `json:"peerType"` // channel | user
	PeerID     int64           `json:"peerId"`
	AccessHash json.RawMessage `json:"accessHash"`
	MsgID      int             `json:"msgId"`
	Data       string          `json:"data"` // base64(callback_data)
}

type botDeepLinkRequest struct {
	AccountID string `json:"accountId"`
	URL       string `json:"url"`
	Target    string `json:"target"`
	WaitMs    int    `json:"waitMs"`
}

type joinChallengeWebViewRequest struct {
	AccountID     string          `json:"accountId"`
	Target        string          `json:"target"`
	PeerType      string          `json:"peerType"` // channel | user
	PeerID        int64           `json:"peerId"`
	AccessHash    json.RawMessage `json:"accessHash"`
	MsgID         int             `json:"msgId"`
	BotID         int64           `json:"botId"`
	BotAccessHash json.RawMessage `json:"botAccessHash"`
	Kind          string          `json:"kind"` // webview | simple_webview | url
	URL           string          `json:"url"`
}

/**
 * 列出协议号已加入的群组/频道。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleListJoinedGroups(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
	if accountID == "" {
		writeErr(w, http.StatusBadRequest, "account_required", "accountId required")
		return
	}
	items, err := s.Store.ListJoinedGroups(accountID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	syncedAt, _ := s.Store.JoinedGroupsSyncedAt(accountID)
	writeJSON(w, http.StatusOK, map[string]any{
		"accountId": accountID,
		"items":     items,
		"total":     len(items),
		"syncedAt":  syncedAt,
		"source":    "sqlite",
		"note":      "本地缓存；点「刷新」才请求 Telegram 并覆盖该协议号已加入群",
	})
}

/**
 * 从 Telegram 拉取已加入群组并写入该协议号本地缓存。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleRefreshJoinedGroups(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
	limit := 200
	if accountID == "" {
		var body struct {
			AccountID string `json:"accountId"`
			Limit     int    `json:"limit"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		accountID = strings.TrimSpace(body.AccountID)
		if body.Limit > 0 {
			limit = body.Limit
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	if accountID == "" {
		writeErr(w, http.StatusBadRequest, "account_required", "accountId required")
		return
	}
	items, err := s.TG.RefreshJoinedGroups(r.Context(), accountID, limit)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "refresh_failed", err.Error())
		return
	}
	syncedAt, _ := s.Store.JoinedGroupsSyncedAt(accountID)
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
 * 加群预检（手动验证）：是否需审批、是否已在群、邀请是否有效。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleJoinPreview(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	var req joinPreviewRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	targets := uniqueNonEmpty(req.Targets)
	if accountID == "" || len(targets) == 0 {
		writeErr(w, http.StatusBadRequest, "params_required", "accountId and targets required")
		return
	}
	items := make([]tgclient.JoinPreview, 0, len(targets))
	for _, t := range targets {
		items = append(items, s.TG.PreviewJoin(r.Context(), accountID, t))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accountId": accountID,
		"items":     items,
		"note":      "status=need_approval 时确认仅提交申请；请再用检查入群确认是否真正加入",
	})
}

/**
 * 手动确认后执行加入（或提交审批申请）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleJoinConfirm(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	var req joinConfirmRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	targets := uniqueNonEmpty(req.Targets)
	if accountID == "" || len(targets) == 0 {
		writeErr(w, http.StatusBadRequest, "params_required", "accountId and targets required")
		return
	}
	items := make([]tgclient.GroupItemResult, 0, len(targets))
	success := 0
	pending := 0
	for _, t := range targets {
		result := s.TG.JoinGroup(r.Context(), accountID, t)
		items = append(items, result)
		if result.OK {
			success++
		} else if result.BusinessResult == tgclient.ResultNeedApproval {
			pending++
		}
	}
	taskItems := make([]store.TaskItem, 0, len(items))
	for _, r := range items {
		taskItems = append(taskItems, taskItemFromGroup(r, false))
	}
	taskID := s.saveFinishedTask(
		"groups",
		fmt.Sprintf("手动确认加群 ×%d", len(items)),
		success == len(items),
		taskItems,
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      success == len(items),
		"taskId":  taskID,
		"total":   len(items),
		"success": success,
		"pending": pending,
		"failed":  len(items) - success - pending,
		"items":   items,
		"note":    "pending=已提交审批待管理员通过；请用「检查入群」确认",
	})
}

/**
 * 手动检查是否已真正入群（审批通过后）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleJoinCheck(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	var req joinCheckRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	targets := uniqueNonEmpty(req.Targets)
	if accountID == "" || len(targets) == 0 {
		writeErr(w, http.StatusBadRequest, "params_required", "accountId and targets required")
		return
	}
	items := make([]tgclient.JoinPreview, 0, len(targets))
	joined := 0
	for _, t := range targets {
		p := s.TG.CheckJoinStatus(r.Context(), accountID, t)
		items = append(items, p)
		if p.AlreadyIn || p.Status == tgclient.ResultAlreadyIn {
			joined++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accountId": accountID,
		"joined":    joined,
		"total":     len(items),
		"items":     items,
		"note":      "already_in 表示协议号已在群内；need_approval 表示仍待管理员审批",
	})
}

/**
 * 拉取入群验证题（机器人 inline 键盘，如算术题）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleListJoinChallenges(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	var req joinChallengesRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	targets := uniqueNonEmpty(req.Targets)
	if accountID == "" || len(targets) == 0 {
		writeErr(w, http.StatusBadRequest, "params_required", "accountId and targets required")
		return
	}
	items := make([]tgclient.JoinChallenge, 0)
	errors := make([]string, 0)
	for _, t := range targets {
		list, err := s.TG.ListJoinChallenges(r.Context(), accountID, t, req.Limit)
		if err != nil {
			errors = append(errors, t+": "+err.Error())
			continue
		}
		items = append(items, list...)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accountId": accountID,
		"total":     len(items),
		"items":     items,
		"errors":    errors,
		"note":      "在 App 内点击答案按钮完成验证；adminLikely=true 多为管理按钮请勿误点",
	})
}

/**
 * 点击入群验证 inline 按钮。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleAnswerJoinChallenge(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	var req joinChallengeAnswerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" || req.PeerID == 0 || req.MsgID == 0 || strings.TrimSpace(req.Data) == "" {
		writeErr(w, http.StatusBadRequest, "params_required", "accountId, peerId, msgId, data required")
		return
	}
	result := s.TG.AnswerJoinChallenge(
		r.Context(),
		accountID,
		req.Target,
		req.PeerType,
		req.PeerID,
		parseAccessHashJSON(req.AccessHash),
		req.MsgID,
		req.Data,
	)
	writeJSON(w, http.StatusOK, result)
}

/**
 * 协议号打开机器人入群验证 deep link（messages.startBot）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleStartBotDeepLink(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	var req botDeepLinkRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	deepLink := strings.TrimSpace(req.URL)
	if accountID == "" || deepLink == "" {
		writeErr(w, http.StatusBadRequest, "params_required", "accountId and url required")
		return
	}
	wait := time.Duration(req.WaitMs) * time.Millisecond
	if wait <= 0 {
		wait = 3 * time.Second
	}
	result := s.TG.StartBotDeepLink(r.Context(), accountID, deepLink, req.Target, wait)
	writeJSON(w, http.StatusOK, result)
}

/**
 * 打开入群验证小程序 / 浏览器链接。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleOpenChallengeWebView(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	var req joinChallengeWebViewRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" || strings.TrimSpace(req.URL) == "" {
		writeErr(w, http.StatusBadRequest, "params_required", "accountId and url required")
		return
	}
	result := s.TG.OpenChallengeWebView(
		r.Context(),
		accountID,
		req.Target,
		req.PeerType,
		req.PeerID,
		parseAccessHashJSON(req.AccessHash),
		req.MsgID,
		req.BotID,
		parseAccessHashJSON(req.BotAccessHash),
		req.Kind,
		req.URL,
	)
	writeJSON(w, http.StatusOK, result)
}
