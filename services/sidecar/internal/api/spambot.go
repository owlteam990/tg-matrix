package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

/**
 * 检查单个协议号 SpamBot 状态（F12）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleSpamBotCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_id", "account id required")
		return
	}
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}

	result := s.TG.CheckSpamBot(r.Context(), id, 4*time.Second)

	// 回写账号状态（有限推断，不宣称全能）
	if acc, ok, _ := s.Store.GetAccount(id); ok {
		switch result.Status {
		case tgclient.SpamStatusGood:
			if acc.Status == "unknown" || acc.Status == "available" {
				acc.Status = "available"
			}
		case tgclient.SpamStatusLimited:
			acc.Status = "limited"
		}
		_ = s.Store.UpsertAccount(acc)
	}

	taskID := s.saveFinishedTask("spambot", fmt.Sprintf("SpamBot 检查 %s", id), result.OK || result.Status == tgclient.SpamStatusUnknown, []store.TaskItem{{
		AccountID:      id,
		Target:         "SpamBot",
		Status:         result.Status,
		BusinessResult: result.BusinessResult,
		ErrorCode:      result.ErrorCode,
		ErrorMessage:   result.ErrorMessage,
		LocalResult:    result.Snippet,
	}})

	writeJSON(w, http.StatusOK, map[string]any{
		"accountId":      result.AccountID,
		"ok":             result.OK,
		"status":         result.Status,
		"businessResult": result.BusinessResult,
		"snippet":        result.Snippet,
		"errorCode":      result.ErrorCode,
		"errorMessage":   result.ErrorMessage,
		"waitSeconds":    result.WaitSeconds,
		"taskId":         taskID,
	})
}

func newTaskID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
