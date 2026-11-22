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

type scrapeRequest struct {
	AccountID    string   `json:"accountId"`
	Group        string   `json:"group"`
	Sources      []string `json:"sources"`
	MemberLimit  int      `json:"memberLimit"`
	HistoryLimit int      `json:"historyLimit"`
}

type scrapeResponse struct {
	tgclient.ScrapeResult
	TaskID string `json:"taskId,omitempty"`
}

type phoneCheckRequest struct {
	AccountIDs    []string `json:"accountIds"`
	Phones        []string `json:"phones"`
	IntervalMs    int      `json:"intervalMs"`
	FailStopAfter int      `json:"failStopAfter"`
}

type phoneCheckResponse struct {
	OK         bool                      `json:"ok"`
	TaskID     string                    `json:"taskId,omitempty"`
	Total      int                       `json:"total"`
	Success    int                       `json:"success"`
	Failed     int                       `json:"failed"`
	Stopped    bool                      `json:"stopped,omitempty"`
	StopReason string                    `json:"stopReason,omitempty"`
	Items      []tgclient.PhoneCheckItem `json:"items"`
	Note       string                    `json:"note"`
}

/**
 * 采集群成员 / 发言者（F23）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleScrape(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	var req scrapeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" {
		writeErr(w, http.StatusBadRequest, "account_required", "accountId required")
		return
	}
	sources := uniqueNonEmpty(req.Sources)
	result := s.TG.ScrapeGroup(r.Context(), accountID, req.Group, sources, req.MemberLimit, req.HistoryLimit)
	taskID := s.saveFinishedTask(
		"scrape",
		fmt.Sprintf("采集 %s ×%d", result.Group, len(result.Rows)),
		result.OK,
		taskItemsFromScrape(result),
	)
	writeJSON(w, http.StatusOK, scrapeResponse{ScrapeResult: result, TaskID: taskID})
}

/**
 * 批量手机号检查（F25）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handlePhoneCheck(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	var req phoneCheckRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	accounts := uniqueNonEmpty(req.AccountIDs)
	phones := uniqueNonEmpty(req.Phones)
	if len(accounts) == 0 {
		writeErr(w, http.StatusBadRequest, "accounts_required", "select at least one account")
		return
	}
	if len(phones) == 0 {
		writeErr(w, http.StatusBadRequest, "phones_required", "phones required")
		return
	}
	interval := time.Duration(req.IntervalMs) * time.Millisecond
	if interval < 500*time.Millisecond {
		interval = 1500 * time.Millisecond
	}
	failStop := failStopAfterOrDefault(req.FailStopAfter)

	total := len(phones)
	items := make([]tgclient.PhoneCheckItem, 0, total)
	taskItems := make([]store.TaskItem, 0, total)
	success := 0
	consecutive := 0
	stopped := false
	stopReason := ""
	for i, phone := range phones {
		if i > 0 {
			select {
			case <-r.Context().Done():
				stopped, stopReason = true, "cancelled"
			case <-time.After(interval):
			}
		}
		if stopped {
			break
		}
		accountID := accounts[i%len(accounts)]
		item := s.TG.CheckPhone(r.Context(), accountID, phone)
		retried := false
		if item.BusinessResult == tgclient.ResultFlood && item.WaitSeconds > 0 {
			if awaitFlood(r.Context(), item.WaitSeconds) {
				item = s.TG.CheckPhone(r.Context(), accountID, phone)
				retried = true
			}
		}
		if item.OK {
			success++
		}
		items = append(items, item)
		taskItems = append(taskItems, taskItemFromPhone(item, retried))
		consecutive = nextConsecutiveFails(consecutive, item.OK)
		if shouldCircuitBreak(failStop, consecutive) {
			stopped, stopReason = true, fmt.Sprintf("circuit_break_after_%d_fails", failStop)
			break
		}
	}
	allOK := !stopped && success == total
	taskID := s.saveFinishedTask("phones", fmt.Sprintf("手机号检查 ×%d", len(items)), allOK, taskItems)
	if stopped && taskID != "" {
		if stopReason == "cancelled" {
			_ = s.Store.FinishTask(taskID, "cancelled")
		} else {
			_ = s.Store.FinishTask(taskID, "circuit_break")
		}
	}
	writeJSON(w, http.StatusOK, phoneCheckResponse{
		OK: allOK, TaskID: taskID, Total: total, Success: success, Failed: len(items) - success,
		Stopped: stopped, StopReason: stopReason,
		Items: items, Note: "未匹配 ≠ 未注册；结果含匹配/未匹配/隐私/失败/待重试",
	})
}
