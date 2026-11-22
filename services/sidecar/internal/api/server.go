package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/tg-matrix/sidecar/internal/marketing"
	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

// Server 提供本地 HTTP JSON RPC，并可托管前端静态页。
type Server struct {
	Store     *store.Store
	TG        *tgclient.Manager
	Runner    *Runner
	SendSched *marketing.Scheduler
	DataDir   string
	WebDir    string
	Started   time.Time
}

/**
 * 注册路由并返回 HTTP Handler。
 *
 * @return http.Handler
 * @author Auto
 * @date 2026-10-08
 */
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)

	mux.HandleFunc("GET /v1/accounts", s.handleListAccounts)
	mux.HandleFunc("POST /v1/accounts/import", s.handleImportAccount)
	mux.HandleFunc("PATCH /v1/accounts/{id}", s.handleUpdateAccount)
	mux.HandleFunc("DELETE /v1/accounts/{id}", s.handleDeleteAccount)
	mux.HandleFunc("POST /v1/accounts/{id}/check", s.handleCheckAccount)
	mux.HandleFunc("POST /v1/accounts/{id}/spambot", s.handleSpamBotCheck)
	mux.HandleFunc("PUT /v1/accounts/{id}/proxy", s.handleBindAccountProxy)

	mux.HandleFunc("GET /v1/proxies", s.handleListProxies)
	mux.HandleFunc("POST /v1/proxies", s.handleUpsertProxy)
	mux.HandleFunc("DELETE /v1/proxies/{id}", s.handleDeleteProxy)

	mux.HandleFunc("POST /v1/messages/send", s.handleSendMessages)
	mux.HandleFunc("GET /v1/chat/dialogs", s.handleListChatDialogs)
	mux.HandleFunc("GET /v1/chat/history", s.handleGetChatHistory)
	mux.HandleFunc("POST /v1/chat/send", s.handleSendChatMessage)
	mux.HandleFunc("POST /v1/chat/send-media", s.handleSendChatMedia)
	mux.HandleFunc("GET /v1/chat/media", s.handleDownloadChatMedia)
	mux.HandleFunc("POST /v1/chat/read", s.handleMarkChatRead)
	mux.HandleFunc("POST /v1/groups/batch", s.handleGroupBatch)
	mux.HandleFunc("POST /v1/groups/invite", s.handleInviteBatch)
	mux.HandleFunc("GET /v1/groups/joined", s.handleListJoinedGroups)
	mux.HandleFunc("POST /v1/groups/joined/refresh", s.handleRefreshJoinedGroups)
	mux.HandleFunc("GET /v1/groups/monitor", s.handleListGroupMonitors)
	mux.HandleFunc("POST /v1/groups/monitor/start", s.handleStartGroupMonitor)
	mux.HandleFunc("POST /v1/groups/monitor/stop", s.handleStopGroupMonitor)
	mux.HandleFunc("POST /v1/groups/monitor/export", s.handleExportGroupMonitor)
	mux.HandleFunc("GET /v1/groups/monitor/export/file", s.handleDownloadGroupMonitorExport)
	mux.HandleFunc("POST /v1/groups/join/preview", s.handleJoinPreview)
	mux.HandleFunc("POST /v1/groups/join/confirm", s.handleJoinConfirm)
	mux.HandleFunc("POST /v1/groups/join/check", s.handleJoinCheck)
	mux.HandleFunc("POST /v1/groups/join/challenges", s.handleListJoinChallenges)
	mux.HandleFunc("POST /v1/groups/join/challenge/answer", s.handleAnswerJoinChallenge)
	mux.HandleFunc("POST /v1/groups/join/challenge/webview", s.handleOpenChallengeWebView)
	mux.HandleFunc("POST /v1/groups/join/challenge/bot-start", s.handleStartBotDeepLink)
	mux.HandleFunc("GET /v1/contacts", s.handleListContacts)
	mux.HandleFunc("POST /v1/contacts/refresh", s.handleRefreshContacts)
	mux.HandleFunc("POST /v1/contacts/batch", s.handleContactBatch)
	mux.HandleFunc("POST /v1/scrape/group", s.handleScrape)
	mux.HandleFunc("POST /v1/scrape/phones", s.handlePhoneCheck)

	mux.HandleFunc("GET /v1/marketing/products", s.handleListMarketingProducts)
	mux.HandleFunc("POST /v1/marketing/products", s.handleUpsertMarketingProduct)
	mux.HandleFunc("GET /v1/marketing/products/{id}", s.handleGetMarketingProduct)
	mux.HandleFunc("DELETE /v1/marketing/products/{id}", s.handleDeleteMarketingProduct)
	mux.HandleFunc("GET /v1/marketing/products/{id}/leads", s.handleListMarketingLeads)
	mux.HandleFunc("PATCH /v1/marketing/leads/{id}", s.handlePatchMarketingLead)
	mux.HandleFunc("POST /v1/marketing/leads/{id}", s.handlePatchMarketingLead)
	mux.HandleFunc("POST /v1/marketing/campaign/collect", s.handleMarketingCollect)

	mux.HandleFunc("GET /v1/marketing/send-policies", s.handleListMarketingSendPolicies)
	mux.HandleFunc("POST /v1/marketing/send-policies", s.handleUpsertMarketingSendPolicy)
	mux.HandleFunc("DELETE /v1/marketing/send-policies/{id}", s.handleDeleteMarketingSendPolicy)
	mux.HandleFunc("GET /v1/marketing/send-jobs", s.handleListMarketingSendJobs)
	mux.HandleFunc("POST /v1/marketing/send-jobs", s.handleCreateMarketingSendJob)
	mux.HandleFunc("GET /v1/marketing/send-jobs/{id}/items", s.handleListMarketingSendJobItems)
	mux.HandleFunc("POST /v1/marketing/send-jobs/{id}/pause", s.handlePauseMarketingSendJob)
	mux.HandleFunc("POST /v1/marketing/send-jobs/{id}/resume", s.handleResumeMarketingSendJob)
	mux.HandleFunc("POST /v1/marketing/send-jobs/{id}/cancel", s.handleCancelMarketingSendJob)
	mux.HandleFunc("GET /v1/send-queue", s.handleListSendQueue)
	mux.HandleFunc("GET /v1/send-queue/{id}/suggest", s.handleSuggestSendQueueReady)
	mux.HandleFunc("PATCH /v1/send-queue/{id}", s.handlePatchSendQueueItem)
	mux.HandleFunc("DELETE /v1/send-queue/{id}", s.handleDeleteSendQueueItem)

	// 消息群发：复用营销策略调度，product_id=__bulk__
	mux.HandleFunc("POST /v1/messages/bulk-send", s.handleCreateBulkSendJob)

	mux.HandleFunc("GET /v1/tasks", s.handleListTasks)
	mux.HandleFunc("GET /v1/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("GET /v1/tasks/{id}/items", s.handleListTaskItems)
	mux.HandleFunc("POST /v1/tasks/{id}/cancel", s.handleCancelTask)
	mux.HandleFunc("POST /v1/tasks/{id}/resume", s.handleResumeTask)
	mux.HandleFunc("POST /v1/tasks/{id}/abandon", s.handleAbandonTask)

	// 托管打包后的前端（SPA：未命中文件时回退 index.html）
	if s.WebDir != "" {
		mux.Handle("/", s.spaFileServer(s.WebDir))
	}

	return withCORS(mux)
}

// spaFileServer 提供静态资源，并对前端路由回退到 index.html。
func (s *Server) spaFileServer(root string) http.Handler {
	fileServer := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API 前缀不走静态
		if strings.HasPrefix(r.URL.Path, "/health") || strings.HasPrefix(r.URL.Path, "/v1/") {
			http.NotFound(w, r)
			return
		}

		clean := path.Clean("/" + r.URL.Path)
		full := path.Join(root, clean)
		if st, err := os.Stat(full); err == nil && !st.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		// 目录或缺失：回退 index.html（排除明显静态后缀的 404）
		if hasStaticExt(clean) {
			http.NotFound(w, r)
			return
		}
		index := path.Join(root, "index.html")
		if _, err := os.Stat(index); err != nil {
			if err == fs.ErrNotExist || os.IsNotExist(err) {
				http.Error(w, "web ui not found", http.StatusNotFound)
				return
			}
		}
		http.ServeFile(w, r, index)
	})
}

func hasStaticExt(p string) bool {
	lower := strings.ToLower(p)
	for _, ext := range []string{".js", ".css", ".map", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".woff", ".woff2", ".ttf"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

type healthResponse struct {
	OK             bool   `json:"ok"`
	Service        string `json:"service"`
	DataDir        string `json:"dataDir"`
	Uptime         string `json:"uptime"`
	Store          string `json:"store"`
	GotdConfigured bool   `json:"gotdConfigured"`
}

/**
 * 健康检查，供桌面端确认 sidecar 已就绪。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-08
 */
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// 健康检查不得触库：SQLite 死锁时仍须返回 200，否则 UI 一直「服务未连接」
	gotd := s.TG != nil && s.TG.ConfiguredCached()
	writeJSON(w, http.StatusOK, healthResponse{
		OK:             true,
		Service:        "tg-sidecar",
		DataDir:        s.DataDir,
		Uptime:         time.Since(s.Started).Round(time.Second).String(),
		Store:          "sqlite",
		GotdConfigured: gotd,
	})
}

type accountDTO struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Phone    string  `json:"phone"`
	Username string  `json:"username"`
	Status   string  `json:"status"`
	ProxyID  *string `json:"proxyId"`
}

/**
 * 列出本地协议号。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-08
 */
func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.Store.ListAccounts()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_query_failed", err.Error())
		return
	}
	list := make([]accountDTO, 0, len(accounts))
	for _, item := range accounts {
		list = append(list, accountDTO{
			ID:       item.ID,
			Label:    item.Label,
			Phone:    item.Phone,
			Username: item.Username,
			Status:   item.Status,
			ProxyID:  item.ProxyID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

type checkResponse struct {
	AccountID string `json:"accountId"`
	OK        bool   `json:"ok"`
	UserID    int64  `json:"userId,omitempty"`
	Username  string `json:"username,omitempty"`
	Phone     string `json:"phone,omitempty"`
	Error     string `json:"error,omitempty"`
}

/**
 * 检查协议号授权状态（gotd UsersGetUsers self）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-08
 */
func (s *Server) handleCheckAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_id", "account id required")
		return
	}
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}

	user, err := s.TG.CheckAccount(r.Context(), id)
	if err != nil {
		if acc, ok, _ := s.Store.GetAccount(id); ok {
			acc.Status = "invalid"
			_ = s.Store.UpsertAccount(acc)
		}
		_ = s.Store.CreateFinishedTask(newTaskID(), "account_check", "账号检查 "+id, "done", []store.TaskItem{{
			AccountID: id, Target: "self", Status: "failed", BusinessResult: "invalid", ErrorMessage: err.Error(),
		}})
		writeJSON(w, http.StatusOK, checkResponse{
			AccountID: id,
			OK:        false,
			Error:     err.Error(),
		})
		return
	}

	acc, ok, _ := s.Store.GetAccount(id)
	if !ok {
		acc = store.Account{ID: id}
	}
	acc.Status = "available"
	acc.Username = user.Username
	acc.Phone = user.Phone
	if acc.Label == "" {
		acc.Label = user.Username
	}
	_ = s.Store.UpsertAccount(acc)

	_ = s.Store.CreateFinishedTask(newTaskID(), "account_check", "账号检查 "+id, "done", []store.TaskItem{{
		AccountID: id, Target: "self", Status: "ok", BusinessResult: "available",
	}})

	writeJSON(w, http.StatusOK, checkResponse{
		AccountID: id,
		OK:        true,
		UserID:    user.ID,
		Username:  user.Username,
		Phone:     user.Phone,
	})
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Code: code, Message: message})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
