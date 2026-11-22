package api

import (
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"strings"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

// BulkProductID 群发任务写入 marketing_send_jobs.product_id 的标记（与营销产品隔离）。
const BulkProductID = "__bulk__"

type createBulkSendBody struct {
	Mode          string   `json:"mode"` // username|phone|userId
	Targets       []string `json:"targets"`
	Variants      []string `json:"variants"` // 多条文案，入队时每目标随机选一条
	PolicyID      string   `json:"policyId"`
	AccountIDs    []string `json:"accountIds"`
	FailStopAfter int      `json:"failStopAfter"`
}

/**
 * 创建群发任务：复用营销发送策略与后台调度，文案从 variants 中随机选用。
 */
func (s *Server) handleCreateBulkSendJob(w http.ResponseWriter, r *http.Request) {
	var body createBulkSendBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	mode := strings.TrimSpace(body.Mode)
	switch mode {
	case tgclient.ModeUsername, tgclient.ModePhone, tgclient.ModeUserID:
	default:
		writeErr(w, http.StatusBadRequest, "invalid_mode", "mode 须为 username|phone|userId")
		return
	}
	policyID := strings.TrimSpace(body.PolicyID)
	if policyID == "" {
		writeErr(w, http.StatusBadRequest, "policy_required", "请选择发送策略")
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
	targets := uniqueNonEmpty(body.Targets)
	if len(targets) == 0 {
		writeErr(w, http.StatusBadRequest, "targets_required", "请填写目标名单")
		return
	}
	variants := make([]string, 0, len(body.Variants))
	for _, v := range body.Variants {
		v = strings.TrimSpace(v)
		if v != "" {
			variants = append(variants, v)
		}
	}
	if len(variants) == 0 {
		writeErr(w, http.StatusBadRequest, "variants_required", "请至少填写一条文案")
		return
	}

	failStop := body.FailStopAfter
	if failStop == 0 {
		failStop = 5
	}
	job, err := s.Store.CreateMarketingSendJob(store.MarketingSendJob{
		ProductID:     BulkProductID,
		PolicyID:      policyID,
		AccountIDs:    accounts,
		Status:        "running",
		FailStopAfter: failStop,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "job_create_failed", err.Error())
		return
	}

	items := make([]store.MarketingSendQueueItem, 0, len(targets))
	for _, t := range targets {
		text := variants[rand.Intn(len(variants))]
		items = append(items, store.MarketingSendQueueItem{
			JobID:      job.ID,
			LeadID:     "", // 群发无营销线索
			AccountID:  "",
			TargetMode: mode,
			Target:     t,
			Text:       text,
			Status:     "queued",
		})
	}
	if err := s.Store.InsertMarketingSendQueueItems(items); err != nil {
		_ = s.Store.UpdateMarketingSendJob(store.MarketingSendJob{
			ID: job.ID, ProductID: job.ProductID, PolicyID: job.PolicyID,
			AccountIDs: job.AccountIDs, Status: "cancelled", FailStopAfter: job.FailStopAfter,
		})
		writeErr(w, http.StatusInternalServerError, "enqueue_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"job":          job,
		"enqueued":     len(items),
		"variantCount": len(variants),
		"note":         "已入队，后台按策略限速发送；每条目标随机选用一条文案",
	})
}
