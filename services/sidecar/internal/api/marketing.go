package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

type marketingProductBody struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	SellingPoints string `json:"sellingPoints"`
	Audience      string `json:"audience"`
	AvoidAudience string `json:"avoidAudience"`
	Offer         string `json:"offer"`
	LandingURL    string `json:"landingUrl"`
	Tone          string `json:"tone"`
	BannedWords   string `json:"bannedWords"`
	CTA           string `json:"cta"`
	Status        string `json:"status"`
}

type marketingLeadPatch struct {
	Digest        *string `json:"digest"`
	FitScore      *int    `json:"fitScore"`
	FitLabel      *string `json:"fitLabel"`
	FitReasons    *string `json:"fitReasons"`
	RiskFlags     *string `json:"riskFlags"`
	ScriptA       *string `json:"scriptA"`
	ScriptB       *string `json:"scriptB"`
	ScriptChosen  *string `json:"scriptChosen"`
	Status        *string `json:"status"`
	SendAccountID *string `json:"sendAccountId"`
	CloudCallIDs  *string `json:"cloudCallIds"`
	ErrorMessage  *string `json:"errorMessage"`
}

type marketingCollectRequest struct {
	ProductID    string `json:"productId"`
	AccountID    string `json:"accountId"`
	Group        string `json:"group"`
	GroupTitle   string `json:"groupTitle"`
	HistoryLimit int    `json:"historyLimit"`
}

type marketingCollectResponse struct {
	OK            bool                   `json:"ok"`
	TaskID        string                 `json:"taskId,omitempty"`
	Imported      int                    `json:"imported"`
	SpeakersCount int                    `json:"speakersCount"`
	Rows          []tgclient.ScrapeRow   `json:"rows"`
	ErrorCode     string                 `json:"errorCode,omitempty"`
	ErrorMessage  string                 `json:"errorMessage,omitempty"`
	Note          string                 `json:"note"`
}

/**
 * 列出营销产品。
 *
 * @param w 响应
 * @param r 请求
 */
func (s *Server) handleListMarketingProducts(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListMarketingProducts()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

/**
 * 创建或更新营销产品。
 *
 * @param w 响应
 * @param r 请求
 */
func (s *Server) handleUpsertMarketingProduct(w http.ResponseWriter, r *http.Request) {
	var body marketingProductBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name_required", "name required")
		return
	}
	saved, err := s.Store.UpsertMarketingProduct(store.MarketingProduct{
		ID: body.ID, Name: name, SellingPoints: body.SellingPoints, Audience: body.Audience,
		AvoidAudience: body.AvoidAudience, Offer: body.Offer, LandingURL: body.LandingURL,
		Tone: body.Tone, BannedWords: body.BannedWords, CTA: body.CTA, Status: body.Status,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "upsert_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

/**
 * 获取单个营销产品。
 *
 * @param w 响应
 * @param r 请求
 */
func (s *Server) handleGetMarketingProduct(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	p, err := s.Store.GetMarketingProduct(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	if p == nil {
		writeErr(w, http.StatusNotFound, "not_found", "product not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

/**
 * 删除营销产品及其线索。
 *
 * @param w 响应
 * @param r 请求
 */
func (s *Server) handleDeleteMarketingProduct(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id_required", "id required")
		return
	}
	if err := s.Store.DeleteMarketingProduct(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/**
 * 列出某产品下线索。
 *
 * @param w 响应
 * @param r 请求
 */
func (s *Server) handleListMarketingLeads(w http.ResponseWriter, r *http.Request) {
	productID := strings.TrimSpace(r.PathValue("id"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	items, err := s.Store.ListMarketingLeads(productID, status)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

/**
 * 更新线索（AI 结果 / 审核字段）。
 *
 * @param w 响应
 * @param r 请求
 */
func (s *Server) handlePatchMarketingLead(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	lead, err := s.Store.GetMarketingLead(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	if lead == nil {
		writeErr(w, http.StatusNotFound, "not_found", "lead not found")
		return
	}
	var body marketingLeadPatch
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	// 1. 仅覆盖传入字段
	if body.Digest != nil {
		lead.Digest = *body.Digest
	}
	if body.FitScore != nil {
		lead.FitScore = *body.FitScore
	}
	if body.FitLabel != nil {
		lead.FitLabel = *body.FitLabel
	}
	if body.FitReasons != nil {
		lead.FitReasons = *body.FitReasons
	}
	if body.RiskFlags != nil {
		lead.RiskFlags = *body.RiskFlags
	}
	if body.ScriptA != nil {
		lead.ScriptA = *body.ScriptA
	}
	if body.ScriptB != nil {
		lead.ScriptB = *body.ScriptB
	}
	if body.ScriptChosen != nil {
		lead.ScriptChosen = *body.ScriptChosen
	}
	if body.Status != nil {
		lead.Status = *body.Status
	}
	if body.SendAccountID != nil {
		lead.SendAccountID = *body.SendAccountID
	}
	if body.CloudCallIDs != nil {
		lead.CloudCallIDs = *body.CloudCallIDs
	}
	if body.ErrorMessage != nil {
		lead.ErrorMessage = *body.ErrorMessage
	}
	if err := s.Store.UpdateMarketingLead(*lead); err != nil {
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	updated, _ := s.Store.GetMarketingLead(id)
	writeJSON(w, http.StatusOK, updated)
}

/**
 * 采集群发言者（含 digest）并写入营销线索。
 *
 * @param w 响应
 * @param r 请求
 */
func (s *Server) handleMarketingCollect(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	var req marketingCollectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	productID := strings.TrimSpace(req.ProductID)
	accountID := strings.TrimSpace(req.AccountID)
	group := strings.TrimSpace(req.Group)
	if productID == "" || accountID == "" || group == "" {
		writeErr(w, http.StatusBadRequest, "params_required", "productId, accountId, group required")
		return
	}
	p, err := s.Store.GetMarketingProduct(productID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "product_failed", err.Error())
		return
	}
	if p == nil {
		writeErr(w, http.StatusNotFound, "product_not_found", "product not found")
		return
	}
	historyLimit := req.HistoryLimit
	if historyLimit <= 0 {
		historyLimit = 200
	}
	// 1. 发言者采集（附 digest）
	result := s.TG.ScrapeGroup(r.Context(), accountID, group, []string{tgclient.SourceSpeakers}, 0, historyLimit)
	if !result.OK {
		writeJSON(w, http.StatusOK, marketingCollectResponse{
			OK: false, SpeakersCount: result.SpeakersCount, Rows: result.Rows,
			ErrorCode: result.ErrorCode, ErrorMessage: result.ErrorMessage,
			Note: result.Note,
		})
		return
	}
	groupTitle := strings.TrimSpace(req.GroupTitle)
	leads := make([]store.MarketingLead, 0, len(result.Rows))
	for _, row := range result.Rows {
		if row.IsBot || row.UserID == 0 {
			continue
		}
		display := strings.TrimSpace(strings.TrimSpace(row.FirstName + " " + row.LastName))
		leads = append(leads, store.MarketingLead{
			ProductID:   productID,
			AccountID:   accountID,
			GroupTarget: group,
			GroupTitle:  groupTitle,
			UserID:      row.UserID,
			Username:    row.Username,
			DisplayName: display,
			Digest:      row.Digest,
			Status:      "pending",
		})
	}
	n, err := s.Store.UpsertMarketingLeads(leads)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "import_failed", err.Error())
		return
	}
	taskID := s.saveFinishedTask(
		"marketing_collect",
		fmt.Sprintf("营销采集 %s ×%d", group, n),
		true,
		taskItemsFromScrape(result),
	)
	writeJSON(w, http.StatusOK, marketingCollectResponse{
		OK: true, TaskID: taskID, Imported: n, SpeakersCount: result.SpeakersCount,
		Rows: result.Rows, Note: "发言摘要已写入线索；请在桌面端执行 AI 分析与话术",
	})
}
