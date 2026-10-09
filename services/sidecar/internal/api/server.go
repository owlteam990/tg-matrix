package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

// Server 提供本地 HTTP JSON RPC。
type Server struct {
	Store   *store.Store
	TG      *tgclient.Manager
	DataDir string
	Started time.Time
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
	mux.HandleFunc("POST /v1/accounts/{id}/check", s.handleCheckAccount)
	return withCORS(mux)
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
	writeJSON(w, http.StatusOK, healthResponse{
		OK:             true,
		Service:        "tg-sidecar",
		DataDir:        s.DataDir,
		Uptime:         time.Since(s.Started).Round(time.Second).String(),
		Store:          "sqlite",
		GotdConfigured: s.TG != nil && s.TG.Configured(),
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
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
