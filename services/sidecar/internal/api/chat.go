package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tg-matrix/sidecar/internal/tgclient"
)

// 上传体大小上限（约 48MB，覆盖常见图片/视频）。
const chatMediaMaxUpload = 48 << 20

type chatSendRequest struct {
	AccountID  string          `json:"accountId"`
	PeerType   string          `json:"peerType"`
	PeerID     int64           `json:"peerId"`
	AccessHash json.RawMessage `json:"accessHash"`
	Text       string          `json:"text"`
}

type chatReadRequest struct {
	AccountID  string          `json:"accountId"`
	PeerType   string          `json:"peerType"`
	PeerID     int64           `json:"peerId"`
	AccessHash json.RawMessage `json:"accessHash"`
	MaxID      int             `json:"maxId"`
}

/**
 * 解析 accessHash：支持 JSON 字符串或数字，优先按字符串保留精度。
 *
 * @param raw JSON 原始值
 * @return int64
 */
func parseAccessHashJSON(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0
	}
	if strings.HasPrefix(s, "\"") {
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			return tgclient.ParseAccessHash(str)
		}
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		v, err := n.Int64()
		if err == nil {
			return v
		}
	}
	return tgclient.ParseAccessHash(strings.Trim(s, `"`))
}

/**
 * 列出会话列表。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleListChatDialogs(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
	if accountID == "" {
		writeErr(w, http.StatusBadRequest, "account_required", "accountId required")
		return
	}
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	peerKind := strings.TrimSpace(r.URL.Query().Get("peerKind"))
	if peerKind == "" {
		// 兼容旧参数 onlyUsers
		if strings.TrimSpace(r.URL.Query().Get("onlyUsers")) == "1" ||
			strings.EqualFold(r.URL.Query().Get("onlyUsers"), "true") {
			peerKind = "user"
		} else {
			peerKind = "all"
		}
	}
	items, err := s.TG.ListChatDialogs(r.Context(), accountID, limit, peerKind)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "dialogs_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accountId": accountID,
		"peerKind":  peerKind,
		"items":     items,
		"note":      "accessHash 为字符串；peerKind=user|group|all",
	})
}

/**
 * 拉取会话历史。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleGetChatHistory(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	q := r.URL.Query()
	accountID := strings.TrimSpace(q.Get("accountId"))
	peerType := strings.TrimSpace(q.Get("peerType"))
	peerID, _ := strconv.ParseInt(strings.TrimSpace(q.Get("peerId")), 10, 64)
	accessHash := tgclient.ParseAccessHash(q.Get("accessHash"))
	limit, _ := strconv.Atoi(strings.TrimSpace(q.Get("limit")))
	offsetID, _ := strconv.Atoi(strings.TrimSpace(q.Get("offsetId")))
	minID, _ := strconv.Atoi(strings.TrimSpace(q.Get("minId")))
	if accountID == "" || peerType == "" || peerID == 0 {
		writeErr(w, http.StatusBadRequest, "params_required", "accountId, peerType, peerId required")
		return
	}
	items, err := s.TG.GetChatHistory(r.Context(), accountID, peerType, peerID, accessHash, limit, offsetID, minID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "history_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accountId": accountID,
		"peerId":    peerID,
		"peerType":  peerType,
		"items":     items,
		"note":      "items 按时间升序；minId 用于轮询新消息",
	})
}

/**
 * 会话内发送文本。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleSendChatMessage(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	var req chatSendRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" || req.PeerID == 0 || strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, "params_required", "accountId, peerId, text required")
		return
	}
	result := s.TG.SendChatMessage(
		r.Context(),
		accountID,
		req.PeerType,
		req.PeerID,
		parseAccessHashJSON(req.AccessHash),
		req.Text,
	)
	writeJSON(w, http.StatusOK, result)
}

/**
 * 下载会话消息中的媒体（图片/视频/文档）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleDownloadChatMedia(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	q := r.URL.Query()
	accountID := strings.TrimSpace(q.Get("accountId"))
	peerType := strings.TrimSpace(q.Get("peerType"))
	peerID, _ := strconv.ParseInt(strings.TrimSpace(q.Get("peerId")), 10, 64)
	accessHash := tgclient.ParseAccessHash(q.Get("accessHash"))
	messageID, _ := strconv.Atoi(strings.TrimSpace(q.Get("messageId")))
	if accountID == "" || peerType == "" || peerID == 0 || messageID <= 0 {
		writeErr(w, http.StatusBadRequest, "params_required", "accountId, peerType, peerId, messageId required")
		return
	}
	file, err := s.TG.DownloadChatMedia(r.Context(), accountID, peerType, peerID, accessHash, messageID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "media_download_failed", err.Error())
		return
	}
	mimeType := strings.TrimSpace(file.MimeType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	if name := strings.TrimSpace(file.FileName); name != "" {
		w.Header().Set("Content-Disposition", `inline; filename="`+filepath.Base(name)+`"`)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Data)
}

/**
 * 会话内发送媒体（multipart：图片/视频/文档；或 JSON：位置）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleSendChatMedia(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	// 位置可用 JSON；文件走 multipart
	if strings.HasPrefix(ct, "application/json") {
		var req struct {
			AccountID  string          `json:"accountId"`
			PeerType   string          `json:"peerType"`
			PeerID     int64           `json:"peerId"`
			AccessHash json.RawMessage `json:"accessHash"`
			Kind       string          `json:"kind"`
			Caption    string          `json:"caption"`
			Lat        float64         `json:"lat"`
			Long       float64         `json:"long"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		kind := strings.ToLower(strings.TrimSpace(req.Kind))
		if kind == "" {
			kind = tgclient.MediaLocation
		}
		if strings.TrimSpace(req.AccountID) == "" || req.PeerID == 0 || kind != tgclient.MediaLocation {
			writeErr(w, http.StatusBadRequest, "params_required", "accountId, peerId, kind=location, lat, long required")
			return
		}
		result := s.TG.SendChatMedia(r.Context(), tgclient.ChatSendMediaInput{
			AccountID:  strings.TrimSpace(req.AccountID),
			PeerType:   req.PeerType,
			PeerID:     req.PeerID,
			AccessHash: parseAccessHashJSON(req.AccessHash),
			Kind:       kind,
			Caption:    req.Caption,
			Lat:        req.Lat,
			Long:       req.Long,
		})
		writeJSON(w, http.StatusOK, result)
		return
	}

	if err := r.ParseMultipartForm(chatMediaMaxUpload); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_multipart", err.Error())
		return
	}
	accountID := strings.TrimSpace(r.FormValue("accountId"))
	peerType := strings.TrimSpace(r.FormValue("peerType"))
	peerID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("peerId")), 10, 64)
	accessHash := tgclient.ParseAccessHash(r.FormValue("accessHash"))
	kind := strings.ToLower(strings.TrimSpace(r.FormValue("kind")))
	caption := strings.TrimSpace(r.FormValue("caption"))
	if accountID == "" || peerID == 0 {
		writeErr(w, http.StatusBadRequest, "params_required", "accountId, peerId required")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "file_required", "multipart field file required")
		return
	}
	defer file.Close()
	fileName := header.Filename
	mimeType := header.Header.Get("Content-Type")
	if kind == "" {
		lower := strings.ToLower(mimeType)
		switch {
		case strings.HasPrefix(lower, "image/"):
			kind = tgclient.MediaPhoto
		case strings.HasPrefix(lower, "video/"):
			kind = tgclient.MediaVideo
		default:
			kind = tgclient.MediaDocument
		}
	}
	result := s.TG.SendChatMedia(r.Context(), tgclient.ChatSendMediaInput{
		AccountID:  accountID,
		PeerType:   peerType,
		PeerID:     peerID,
		AccessHash: accessHash,
		Kind:       kind,
		Caption:    caption,
		FileName:   fileName,
		MimeType:   mimeType,
		File:       file,
		FileSize:   header.Size,
	})
	writeJSON(w, http.StatusOK, result)
}

/**
 * 将会话标记为已读。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleMarkChatRead(w http.ResponseWriter, r *http.Request) {
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "gotd_not_configured", "set TG_APP_ID and TG_APP_HASH")
		return
	}
	var req chatReadRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" || req.PeerID == 0 || strings.TrimSpace(req.PeerType) == "" {
		writeErr(w, http.StatusBadRequest, "params_required", "accountId, peerType, peerId required")
		return
	}
	err := s.TG.MarkChatRead(
		r.Context(),
		accountID,
		req.PeerType,
		req.PeerID,
		parseAccessHashJSON(req.AccessHash),
		req.MaxID,
	)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"accountId": accountID,
		"peerId":    req.PeerID,
		"peerType":  req.PeerType,
		"maxId":     req.MaxID,
		"note":      "已标记已读",
	})
}
