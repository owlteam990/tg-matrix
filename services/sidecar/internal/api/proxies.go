package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/tg-matrix/sidecar/internal/store"
)

type proxyDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	HasPass  bool   `json:"hasPassword"`
}

type upsertProxyRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

/**
 * 列出代理。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleListProxies(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListProxies()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_query_failed", err.Error())
		return
	}
	list := make([]proxyDTO, 0, len(items))
	for _, item := range items {
		list = append(list, toProxyDTO(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

/**
 * 创建或更新代理。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleUpsertProxy(w http.ResponseWriter, r *http.Request) {
	var req upsertProxyRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	host := strings.TrimSpace(req.Host)
	if host == "" || req.Port <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid_proxy", "host and port required")
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = newID()
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = host
	}
	proto := strings.TrimSpace(req.Protocol)
	if proto == "" {
		proto = "socks5"
	}

	existing, exists, err := s.Store.GetProxy(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_query_failed", err.Error())
		return
	}
	pass := req.Password
	if pass == "" && exists {
		pass = existing.PasswordEnc
	}

	item := store.Proxy{
		ID:          id,
		Name:        name,
		Protocol:    proto,
		Host:        host,
		Port:        req.Port,
		Username:    strings.TrimSpace(req.Username),
		PasswordEnc: pass,
	}
	if exists {
		item.CreatedAt = existing.CreatedAt
	}
	if err := s.Store.UpsertProxy(item); err != nil {
		writeErr(w, http.StatusInternalServerError, "db_write_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxy": toProxyDTO(item)})
}

/**
 * 删除代理。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleDeleteProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_id", "proxy id required")
		return
	}
	if err := s.Store.DeleteProxy(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "db_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func toProxyDTO(item store.Proxy) proxyDTO {
	return proxyDTO{
		ID:       item.ID,
		Name:     item.Name,
		Protocol: item.Protocol,
		Host:     item.Host,
		Port:     item.Port,
		Username: item.Username,
		HasPass:  item.PasswordEnc != "",
	}
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
