package api

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gotd/td/session"

	"github.com/tg-matrix/sidecar/internal/sessionimp"
	"github.com/tg-matrix/sidecar/internal/store"
)

type importFilePart struct {
	Name   string `json:"name"`
	Text   string `json:"text"`
	Base64 string `json:"base64"`
}

type importAccountRequest struct {
	Label           string           `json:"label"`
	SessionJSON     string           `json:"sessionJson"`
	SessionFilePath string           `json:"sessionFilePath"`
	AccountJSON     string           `json:"accountJson"`
	SessionBase64   string           `json:"sessionBase64"`
	SessionFileName string           `json:"sessionFileName"`
	Files           []importFilePart `json:"files"`
	ID              string           `json:"id"`
}

type importAccountItemResult struct {
	OK      bool       `json:"ok"`
	Created bool       `json:"created"`
	Source  string     `json:"source,omitempty"`
	Account accountDTO `json:"account,omitempty"`
	Error   string     `json:"error,omitempty"`
	File    string     `json:"file,omitempty"`
}

type importAccountResponse struct {
	OK      bool                      `json:"ok"`
	Account accountDTO                `json:"account,omitempty"`
	Created bool                      `json:"created,omitempty"`
	Items   []importAccountItemResult `json:"items,omitempty"`
	Note    string                    `json:"note,omitempty"`
}

type bindProxyRequest struct {
	ProxyID string `json:"proxyId"`
}

/**
 * 导入协议号：支持成对 .json + .session、单独 .json（含 session_string）、单独 .session、gotd 原生 JSON。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleImportAccount(w http.ResponseWriter, r *http.Request) {
	var req importAccountRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	// 批量 files：按文件名主干配对
	if len(req.Files) > 0 {
		items := s.importFileParts(req.Label, req.Files)
		allOK := true
		created := 0
		for _, it := range items {
			if !it.OK {
				allOK = false
			}
			if it.Created {
				created++
			}
		}
		writeJSON(w, http.StatusOK, importAccountResponse{
			OK: allOK, Items: items,
			Note: fmt.Sprintf("导入完成：成功新建 %d / 共 %d 组", created, len(items)),
		})
		return
	}

	parsed, err := s.parseImportRequest(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "session_invalid", err.Error())
		return
	}
	if id := strings.TrimSpace(req.ID); id != "" {
		parsed.PreferredID = id
	}
	if label := strings.TrimSpace(req.Label); label != "" {
		parsed.LabelHint = label
	}

	acc, created, err := s.persistParsedAccount(parsed)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "import_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, importAccountResponse{
		OK: true, Account: toAccountDTO(acc), Created: created,
		Note: "重复内容按协议号 ID（优先手机号）跳过",
	})
}

func (s *Server) importFileParts(defaultLabel string, files []importFilePart) []importAccountItemResult {
	type bundle struct {
		jsonName string
		jsonText []byte
		sessName string
		sessBin  []byte
	}
	groups := map[string]*bundle{}
	order := make([]string, 0)

	for _, f := range files {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			continue
		}
		base := sessionimp.FileBase(name)
		key := strings.ToLower(base)
		b := groups[key]
		if b == nil {
			b = &bundle{}
			groups[key] = b
			order = append(order, key)
		}
		ext := strings.ToLower(filepath.Ext(name))
		content, err := decodeFilePart(f)
		if err != nil {
			continue
		}
		switch ext {
		case ".json":
			b.jsonName = name
			b.jsonText = content
		case ".session":
			b.sessName = name
			b.sessBin = content
		default:
			// 无扩展名时按内容猜测
			if json.Valid(content) {
				b.jsonName = name
				b.jsonText = content
			} else if looksLikeSQLite(content) {
				b.sessName = name
				b.sessBin = content
			}
		}
	}

	results := make([]importAccountItemResult, 0, len(order))
	for _, key := range order {
		b := groups[key]
		fileTag := firstNonEmptyStr(b.jsonName, b.sessName, key)
		parsed, err := s.parseBundle(b.jsonText, b.sessBin, key)
		if err != nil {
			results = append(results, importAccountItemResult{OK: false, File: fileTag, Error: err.Error()})
			continue
		}
		if defaultLabel != "" && len(order) == 1 {
			parsed.LabelHint = defaultLabel
		}
		acc, created, err := s.persistParsedAccount(parsed)
		if err != nil {
			results = append(results, importAccountItemResult{OK: false, File: fileTag, Error: err.Error()})
			continue
		}
		results = append(results, importAccountItemResult{
			OK: true, Created: created, Source: parsed.Source,
			Account: toAccountDTO(acc), File: fileTag,
		})
	}
	return results
}

func (s *Server) parseBundle(jsonText, sessBin []byte, fileBase string) (sessionimp.ParsedAccount, error) {
	var sqliteData *session.Data
	if len(sessBin) > 0 {
		tmp, err := writeTempSession(s.DataDir, fileBase, sessBin)
		if err != nil {
			return sessionimp.ParsedAccount{}, err
		}
		defer os.Remove(tmp)
		data, err := sessionimp.ParseTelethonSQLiteFile(tmp)
		if err != nil {
			return sessionimp.ParsedAccount{}, fmt.Errorf(".session: %w", err)
		}
		sqliteData = data
	}

	if len(jsonText) > 0 {
		if sessionimp.IsGotdSessionJSON(jsonText) {
			data, err := loadGotdSessionBytes(jsonText)
			if err != nil {
				return sessionimp.ParsedAccount{}, err
			}
			return sessionimp.ParsedAccount{
				PreferredID: fileBase,
				LabelHint:   fileBase,
				Data:        data,
				Source:      "gotd_json",
			}, nil
		}
		meta, err := sessionimp.ParseAccountJSON(jsonText)
		if err != nil {
			return sessionimp.ParsedAccount{}, err
		}
		return sessionimp.BuildFromMetaAndSession(meta, sqliteData, fileBase)
	}

	if sqliteData != nil {
		return sessionimp.BuildFromSQLiteOnly(sqliteData, fileBase), nil
	}
	return sessionimp.ParsedAccount{}, fmt.Errorf("需要 .json 或 .session")
}

func (s *Server) parseImportRequest(req importAccountRequest) (sessionimp.ParsedAccount, error) {
	// 成对：accountJson + sessionBase64
	if strings.TrimSpace(req.AccountJSON) != "" || strings.TrimSpace(req.SessionBase64) != "" {
		var sessBin []byte
		if strings.TrimSpace(req.SessionBase64) != "" {
			b, err := base64.StdEncoding.DecodeString(req.SessionBase64)
			if err != nil {
				return sessionimp.ParsedAccount{}, fmt.Errorf("sessionBase64: %w", err)
			}
			sessBin = b
		}
		base := sessionimp.FileBase(req.SessionFileName)
		if base == "" {
			base = "account"
		}
		return s.parseBundle([]byte(req.AccountJSON), sessBin, base)
	}

	raw := strings.TrimSpace(req.SessionJSON)
	if raw == "" && req.SessionFilePath != "" {
		b, err := os.ReadFile(req.SessionFilePath)
		if err != nil {
			return sessionimp.ParsedAccount{}, fmt.Errorf("session file: %w", err)
		}
		raw = string(b)
		base := sessionimp.FileBase(req.SessionFilePath)
		ext := strings.ToLower(filepath.Ext(req.SessionFilePath))
		if ext == ".session" || looksLikeSQLite(b) {
			return s.parseBundle(nil, b, base)
		}
		return s.parseBundle(b, nil, base)
	}
	if raw == "" {
		return sessionimp.ParsedAccount{}, fmt.Errorf("请提供 .json / .session 文件内容")
	}

	rawBytes := []byte(raw)
	if sessionimp.IsGotdSessionJSON(rawBytes) {
		data, err := loadGotdSessionBytes(rawBytes)
		if err != nil {
			return sessionimp.ParsedAccount{}, err
		}
		sum := sha1.Sum(rawBytes)
		id := hex.EncodeToString(sum[:8])
		return sessionimp.ParsedAccount{
			PreferredID: id, LabelHint: id, Data: data, Source: "gotd_json",
		}, nil
	}
	if looksLikeSQLite(rawBytes) {
		return s.parseBundle(nil, rawBytes, "account")
	}
	return s.parseBundle(rawBytes, nil, "account")
}

func (s *Server) persistParsedAccount(parsed sessionimp.ParsedAccount) (store.Account, bool, error) {
	if parsed.Data == nil {
		return store.Account{}, false, fmt.Errorf("empty session data")
	}
	id := strings.TrimSpace(parsed.PreferredID)
	if id == "" {
		id = fmt.Sprintf("acc_%x", parsed.Data.AuthKeyID[:4])
	}

	if existing, ok, err := s.Store.GetAccount(id); err != nil {
		return store.Account{}, false, err
	} else if ok {
		return existing, false, nil
	}

	gotdBytes, err := sessionimp.EncodeGotdSession(parsed.Data)
	if err != nil {
		return store.Account{}, false, err
	}

	sessionDir := filepath.Join(s.DataDir, "sessions")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return store.Account{}, false, err
	}
	sessionPath := filepath.Join(sessionDir, id+".session.json")
	if err := os.WriteFile(sessionPath, gotdBytes, 0o600); err != nil {
		return store.Account{}, false, err
	}

	deviceJSON := ""
	if parsed.Device.DeviceModel != "" || parsed.Device.AppVersion != "" {
		b, _ := json.Marshal(parsed.Device)
		deviceJSON = string(b)
	}

	label := strings.TrimSpace(parsed.LabelHint)
	if label == "" {
		label = id
	}
	acc := store.Account{
		ID:          id,
		Label:       label,
		Phone:       parsed.Phone,
		Username:    parsed.Username,
		Status:      "unknown",
		SessionPath: sessionPath,
		AppID:       parsed.AppID,
		AppHash:     parsed.AppHash,
		DeviceJSON:  deviceJSON,
	}
	if err := s.Store.UpsertAccount(acc); err != nil {
		return store.Account{}, false, err
	}
	return acc, true, nil
}

func decodeFilePart(f importFilePart) ([]byte, error) {
	if strings.TrimSpace(f.Base64) != "" {
		return base64.StdEncoding.DecodeString(f.Base64)
	}
	return []byte(f.Text), nil
}

func writeTempSession(dataDir, base string, bin []byte) (string, error) {
	dir := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, base+".session")
	if err := os.WriteFile(path, bin, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func loadGotdSessionBytes(raw []byte) (*session.Data, error) {
	storage := &session.StorageMemory{}
	if err := storage.StoreSession(context.Background(), raw); err != nil {
		return nil, err
	}
	loader := session.Loader{Storage: storage}
	return loader.Load(context.Background())
}

func looksLikeSQLite(b []byte) bool {
	return len(b) >= 16 && string(b[:15]) == "SQLite format 3"
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

type updateAccountRequest struct {
	Label   *string `json:"label"`
	ProxyID *string `json:"proxyId"`
}

/**
 * 更新协议号：备注名、附加/更换已保存代理。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_id", "account id required")
		return
	}
	var req updateAccountRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Label == nil && req.ProxyID == nil {
		writeErr(w, http.StatusBadRequest, "nothing_to_update", "provide label and/or proxyId")
		return
	}
	if _, ok, err := s.Store.GetAccount(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "db_query_failed", err.Error())
		return
	} else if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "account not found")
		return
	}
	if req.Label != nil {
		if err := s.Store.UpdateAccountLabel(id, *req.Label); err != nil {
			writeErr(w, http.StatusBadRequest, "update_label_failed", err.Error())
			return
		}
	}
	if req.ProxyID != nil {
		if err := s.Store.BindAccountProxy(id, strings.TrimSpace(*req.ProxyID)); err != nil {
			writeErr(w, http.StatusBadRequest, "bind_failed", err.Error())
			return
		}
	}
	acc, ok, err := s.Store.GetAccount(id)
	if err != nil || !ok {
		writeErr(w, http.StatusInternalServerError, "db_query_failed", fmt.Sprintf("%v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": toAccountDTO(acc)})
}

/**
 * 删除协议号及其本地 session 文件。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_id", "account id required")
		return
	}
	acc, ok, err := s.Store.GetAccount(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_query_failed", err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "account not found")
		return
	}
	if acc.SessionPath != "" {
		_ = os.Remove(acc.SessionPath)
	}
	if err := s.Store.DeleteAccount(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "db_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

/**
 * 为协议号绑定 / 解绑代理。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleBindAccountProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_id", "account id required")
		return
	}
	var req bindProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := s.Store.BindAccountProxy(id, strings.TrimSpace(req.ProxyID)); err != nil {
		writeErr(w, http.StatusBadRequest, "bind_failed", err.Error())
		return
	}
	acc, ok, err := s.Store.GetAccount(id)
	if err != nil || !ok {
		writeErr(w, http.StatusInternalServerError, "db_query_failed", fmt.Sprintf("%v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": toAccountDTO(acc)})
}

func toAccountDTO(item store.Account) accountDTO {
	return accountDTO{
		ID:       item.ID,
		Label:    item.Label,
		Phone:    item.Phone,
		Username: item.Username,
		Status:   item.Status,
		ProxyID:  item.ProxyID,
	}
}
