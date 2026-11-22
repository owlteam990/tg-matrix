package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

type sendPolicyBody struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	WindowMinutes          int    `json:"windowMinutes"`
	MaxPerWindow           int    `json:"maxPerWindow"`
	MutualMinIntervalMs    int    `json:"mutualMinIntervalMs"`
	NonMutualMinIntervalMs int    `json:"nonMutualMinIntervalMs"`
	NonMutualDailyCap      int    `json:"nonMutualDailyCap"`
	SameTargetCooldownMs  int    `json:"sameTargetCooldownMs"`
	Status                 string `json:"status"`
}

type createSendJobBody struct {
	ProductID     string   `json:"productId"`
	PolicyID      string   `json:"policyId"`
	LeadIDs       []string `json:"leadIds"`
	AccountIDs    []string `json:"accountIds"`
	FailStopAfter int      `json:"failStopAfter"`
}

/**
 * 列出发送策略。
 */
func (s *Server) handleListMarketingSendPolicies(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListMarketingSendPolicies()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

/**
 * 创建或更新发送策略。
 */
func (s *Server) handleUpsertMarketingSendPolicy(w http.ResponseWriter, r *http.Request) {
	var body sendPolicyBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	saved, err := s.Store.UpsertMarketingSendPolicy(store.MarketingSendPolicy{
		ID: body.ID, Name: body.Name, WindowMinutes: body.WindowMinutes,
		MaxPerWindow: body.MaxPerWindow, MutualMinIntervalMs: body.MutualMinIntervalMs,
		NonMutualMinIntervalMs: body.NonMutualMinIntervalMs, NonMutualDailyCap: body.NonMutualDailyCap,
		SameTargetCooldownMs: body.SameTargetCooldownMs, Status: body.Status,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "upsert_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

/**
 * 删除发送策略。
 */
func (s *Server) handleDeleteMarketingSendPolicy(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id_required", "id required")
		return
	}
	if err := s.Store.DeleteMarketingSendPolicy(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/**
 * 列出发送任务。
 */
func (s *Server) handleListMarketingSendJobs(w http.ResponseWriter, r *http.Request) {
	productID := strings.TrimSpace(r.URL.Query().Get("productId"))
	items, err := s.Store.ListMarketingSendJobs(productID, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

/**
 * 创建发送任务并入队已审核线索。
 */
func (s *Server) handleCreateMarketingSendJob(w http.ResponseWriter, r *http.Request) {
	var body createSendJobBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	productID := strings.TrimSpace(body.ProductID)
	policyID := strings.TrimSpace(body.PolicyID)
	if productID == "" || policyID == "" {
		writeErr(w, http.StatusBadRequest, "params_required", "productId and policyId required")
		return
	}
	policy, err := s.Store.GetMarketingSendPolicy(policyID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "policy_get_failed", err.Error())
		return
	}
	if policy == nil || policy.Status != "active" {
		writeErr(w, http.StatusBadRequest, "policy_invalid", "策略不存在或已停用")
		return
	}
	accounts := uniqueNonEmpty(body.AccountIDs)
	if len(accounts) == 0 {
		writeErr(w, http.StatusBadRequest, "accounts_required", "请选择至少一个协议号")
		return
	}
	leadIDs := uniqueNonEmpty(body.LeadIDs)
	if len(leadIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "leads_required", "请选择已审核线索")
		return
	}

	items := make([]store.MarketingSendQueueItem, 0, len(leadIDs))
	for _, lid := range leadIDs {
		lead, err := s.Store.GetMarketingLead(lid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "lead_get_failed", err.Error())
			return
		}
		if lead == nil {
			writeErr(w, http.StatusBadRequest, "lead_not_found", "线索不存在: "+lid)
			return
		}
		if lead.ProductID != productID {
			writeErr(w, http.StatusBadRequest, "lead_product_mismatch", "线索不属于当前产品")
			return
		}
		if lead.Status != "approved" {
			writeErr(w, http.StatusBadRequest, "lead_not_approved", "仅已审核线索可入队: "+lid)
			return
		}
		text := strings.TrimSpace(lead.ScriptChosen)
		if text == "" {
			writeErr(w, http.StatusBadRequest, "script_required", "线索缺少发送文案: "+lid)
			return
		}
		mode := tgclient.ModeUserID
		target := ""
		if u := strings.TrimPrefix(strings.TrimSpace(lead.Username), "@"); u != "" {
			mode = tgclient.ModeUsername
			target = u
		} else if lead.UserID != 0 {
			mode = tgclient.ModeUserID
			target = strconv.FormatInt(lead.UserID, 10)
		} else {
			writeErr(w, http.StatusBadRequest, "target_missing", "线索无 username/userId: "+lid)
			return
		}
		pref := strings.TrimSpace(lead.SendAccountID)
		items = append(items, store.MarketingSendQueueItem{
			LeadID:     lead.ID,
			AccountID:  pref,
			TargetMode: mode,
			Target:     target,
			Text:       text,
			Status:     "queued",
		})
	}

	failStop := body.FailStopAfter
	if failStop == 0 {
		failStop = 5
	}
	job, err := s.Store.CreateMarketingSendJob(store.MarketingSendJob{
		ProductID:     productID,
		PolicyID:      policyID,
		AccountIDs:    accounts,
		Status:        "running",
		FailStopAfter: failStop,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "job_create_failed", err.Error())
		return
	}
	for i := range items {
		items[i].JobID = job.ID
	}
	if err := s.Store.InsertMarketingSendQueueItems(items); err != nil {
		_ = s.Store.UpdateMarketingSendJob(store.MarketingSendJob{
			ID: job.ID, ProductID: job.ProductID, PolicyID: job.PolicyID,
			AccountIDs: job.AccountIDs, Status: "cancelled", FailStopAfter: job.FailStopAfter,
		})
		writeErr(w, http.StatusInternalServerError, "queue_seed_failed", err.Error())
		return
	}
	for _, it := range items {
		if lead, err := s.Store.GetMarketingLead(it.LeadID); err == nil && lead != nil {
			lead.Status = "queued"
			lead.ErrorMessage = ""
			_ = s.Store.UpdateMarketingLead(*lead)
		}
	}
	full, _ := s.Store.GetMarketingSendJob(job.ID)
	if full == nil {
		full = &job
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": full, "enqueued": len(items)})
}

/**
 * 获取任务队列明细。
 */
func (s *Server) handleListMarketingSendJobItems(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	job, err := s.Store.GetMarketingSendJob(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	if job == nil {
		writeErr(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	items, err := s.Store.ListMarketingSendQueueItems(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job, "items": items})
}

/**
 * 暂停发送任务。
 */
func (s *Server) handlePauseMarketingSendJob(w http.ResponseWriter, r *http.Request) {
	s.setMarketingSendJobStatus(w, r, "paused")
}

/**
 * 继续发送任务。
 */
func (s *Server) handleResumeMarketingSendJob(w http.ResponseWriter, r *http.Request) {
	s.setMarketingSendJobStatus(w, r, "running")
}

/**
 * 取消发送任务。
 */
func (s *Server) handleCancelMarketingSendJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	job, err := s.Store.GetMarketingSendJob(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	if job == nil {
		writeErr(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	job.Status = "cancelled"
	if err := s.Store.UpdateMarketingSendJob(*job); err != nil {
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	_ = s.Store.CancelPendingMarketingSendItems(id)
	// 未发出的线索回到 approved，便于重新入队
	items, _ := s.Store.ListMarketingSendQueueItems(id)
	for _, it := range items {
		if it.Status == "cancelled" {
			if lead, err := s.Store.GetMarketingLead(it.LeadID); err == nil && lead != nil && lead.Status == "queued" {
				lead.Status = "approved"
				_ = s.Store.UpdateMarketingLead(*lead)
			}
		}
	}
	full, _ := s.Store.GetMarketingSendJob(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": full})
}

func (s *Server) setMarketingSendJobStatus(w http.ResponseWriter, r *http.Request, status string) {
	id := strings.TrimSpace(r.PathValue("id"))
	job, err := s.Store.GetMarketingSendJob(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	if job == nil {
		writeErr(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	if job.Status == "completed" || job.Status == "cancelled" {
		writeErr(w, http.StatusBadRequest, "job_finished", "任务已结束，无法变更状态")
		return
	}
	job.Status = status
	if status == "running" {
		job.ConsecutiveFails = 0
	}
	if err := s.Store.UpdateMarketingSendJob(*job); err != nil {
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	full, _ := s.Store.GetMarketingSendJob(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": full})
}

