package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

const (
	groupMonitorHistoryDefault = 100
	groupMonitorHistoryMax     = 1000
	groupMonitorCSVChunk       = 10000
	groupMonitorBackfillTO     = 3 * time.Minute
)

type groupMonitorStartReq struct {
	AccountID    string          `json:"accountId"`
	PeerID       int64           `json:"peerId"`
	AccessHash   json.RawMessage `json:"accessHash"`
	PeerType     string          `json:"peerType"`
	Kind         string          `json:"kind"`
	Title        string          `json:"title"`
	HistoryLimit int             `json:"historyLimit"`
}

type groupMonitorStopReq struct {
	AccountID string `json:"accountId"`
	PeerID    int64  `json:"peerId"`
}

type groupMonitorExportReq struct {
	AccountID string `json:"accountId"`
	PeerID    int64  `json:"peerId"`
}

/**
 * 列出协议号的群监控状态。
 */
func (s *Server) handleListGroupMonitors(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.URL.Query().Get("accountId"))
	items, err := s.Store.ListGroupMonitors(accountID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accountId": accountID,
		"items":     items,
	})
}

/**
 * 开启群监控：写库后异步回填历史，完成后进入 monitoring。
 */
func (s *Server) handleStartGroupMonitor(w http.ResponseWriter, r *http.Request) {
	var req groupMonitorStartReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", "invalid json")
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" || req.PeerID == 0 {
		writeErr(w, http.StatusBadRequest, "invalid", "accountId and peerId required")
		return
	}
	if s.TG == nil || !s.TG.Configured() {
		writeErr(w, http.StatusBadRequest, "gotd_required", "Telegram API 未配置")
		return
	}
	limit := req.HistoryLimit
	if limit <= 0 {
		limit = groupMonitorHistoryDefault
	}
	if limit > groupMonitorHistoryMax {
		limit = groupMonitorHistoryMax
	}
	peerType := strings.ToLower(strings.TrimSpace(req.PeerType))
	if peerType == "" {
		peerType = tgclient.PeerTypeFromJoinedKind(req.Kind)
	}
	if peerType != "channel" && peerType != "chat" {
		peerType = "channel"
	}
	accessHash := parseAccessHashJSON(req.AccessHash)
	title := strings.TrimSpace(req.Title)
	// 优先用本地 joined_groups / entities 的 access_hash，避免前端 JS 大整数精度丢失导致 CHANNEL_INVALID
	if g, err := s.Store.GetJoinedGroup(accountID, req.PeerID); err == nil && g != nil {
		if g.AccessHash != 0 {
			accessHash = g.AccessHash
		}
		if title == "" && g.Title != "" {
			title = g.Title
		}
		if req.Kind == "" && g.Kind != "" {
			peerType = tgclient.PeerTypeFromJoinedKind(g.Kind)
		}
	}
	if accessHash == 0 {
		if ent, ok, _ := s.Store.GetEntity(accountID, req.PeerID, peerType); ok && ent.AccessHash != 0 {
			accessHash = ent.AccessHash
		} else if ent, ok, _ := s.Store.GetEntity(accountID, req.PeerID, "channel"); ok && ent.AccessHash != 0 {
			accessHash = ent.AccessHash
			peerType = "channel"
		}
	}
	if title == "" {
		title = fmt.Sprintf("peer_%d", req.PeerID)
	}

	mon := store.GroupMonitor{
		AccountID:     accountID,
		PeerID:        req.PeerID,
		AccessHash:    accessHash,
		PeerType:      peerType,
		Title:         title,
		Status:        store.GroupMonitorBackfilling,
		HistoryLimit:  limit,
		BackfillDone:  0,
		BackfillTotal: limit,
		ErrorMessage:  "",
		StartedAt:     time.Now().Unix(),
	}
	if err := s.Store.UpsertGroupMonitor(mon); err != nil {
		writeErr(w, http.StatusInternalServerError, "upsert_failed", err.Error())
		return
	}

	taskID := groupMonitorTaskID(accountID, req.PeerID)
	s.Runner.Go(taskID, func(ctx context.Context) {
		s.runGroupMonitorBackfill(ctx, mon)
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"item":   mon,
		"taskId": taskID,
		"note":   fmt.Sprintf("已开始拉取最近 %d 条历史", limit),
	})
}

/**
 * 停止监控（取消回填任务）。
 */
func (s *Server) handleStopGroupMonitor(w http.ResponseWriter, r *http.Request) {
	var req groupMonitorStopReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", "invalid json")
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" || req.PeerID == 0 {
		writeErr(w, http.StatusBadRequest, "invalid", "accountId and peerId required")
		return
	}
	_ = s.Runner.Cancel(groupMonitorTaskID(accountID, req.PeerID))
	if err := s.Store.SetGroupMonitorStatus(accountID, req.PeerID, store.GroupMonitorStopped, ""); err != nil {
		writeErr(w, http.StatusInternalServerError, "stop_failed", err.Error())
		return
	}
	item, _ := s.Store.GetGroupMonitor(accountID, req.PeerID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": item})
}

/**
 * 导出已入库消息为拆分 CSV，写入 dataDir/exports。
 */
func (s *Server) handleExportGroupMonitor(w http.ResponseWriter, r *http.Request) {
	var req groupMonitorExportReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", "invalid json")
		return
	}
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" || req.PeerID == 0 {
		writeErr(w, http.StatusBadRequest, "invalid", "accountId and peerId required")
		return
	}
	mon, err := s.Store.GetGroupMonitor(accountID, req.PeerID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	title := fmt.Sprintf("peer_%d", req.PeerID)
	if mon != nil && mon.Title != "" {
		title = mon.Title
	}
	total, err := s.Store.CountGroupMessages(accountID, req.PeerID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "count_failed", err.Error())
		return
	}
	if total == 0 {
		writeErr(w, http.StatusBadRequest, "empty", "暂无已采集消息可导出")
		return
	}

	stamp := time.Now().Format("20060102_150405")
	safeTitle := sanitizeFileName(title)
	exportRoot := filepath.Join(s.DataDir, "exports", "group_monitor")
	dirName := fmt.Sprintf("%s_%d_%s", safeTitle, req.PeerID, stamp)
	dir := filepath.Join(exportRoot, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "mkdir_failed", err.Error())
		return
	}

	type fileInfo struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		Count int    `json:"count"`
		Part  int    `json:"part"`
	}
	files := make([]fileInfo, 0)
	offset := 0
	part := 1
	header := []string{"msg_id", "date", "from_id", "from_name", "out", "text", "media"}

	for offset < total {
		page, err := s.Store.ListGroupMessagesPage(accountID, req.PeerID, offset, groupMonitorCSVChunk)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "read_failed", err.Error())
			return
		}
		if len(page) == 0 {
			break
		}
		name := fmt.Sprintf("%s_%s_part%03d.csv", safeTitle, stamp, part)
		path := filepath.Join(dir, name)
		if err := writeGroupMessagesCSV(path, header, page); err != nil {
			writeErr(w, http.StatusInternalServerError, "write_failed", err.Error())
			return
		}
		files = append(files, fileInfo{Name: name, Path: path, Count: len(page), Part: part})
		offset += len(page)
		part++
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"dir":       dir,
		"total":     total,
		"chunkSize": groupMonitorCSVChunk,
		"files":     files,
		"note":      fmt.Sprintf("已导出 %d 条，共 %d 个文件", total, len(files)),
	})
}

/**
 * 下载导出目录内的单个 CSV（仅允许 dataDir/exports 下路径）。
 */
func (s *Server) handleDownloadGroupMonitorExport(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		writeErr(w, http.StatusBadRequest, "path_required", "path required")
		return
	}
	clean, err := filepath.Abs(path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_path", "invalid path")
		return
	}
	root, err := filepath.Abs(filepath.Join(s.DataDir, "exports"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "root_failed", err.Error())
		return
	}
	if !strings.HasPrefix(clean, root+string(os.PathSeparator)) && clean != root {
		writeErr(w, http.StatusForbidden, "forbidden", "path outside exports")
		return
	}
	f, err := os.Open(clean)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(clean)))
	_, _ = io.Copy(w, f)
}

func (s *Server) runGroupMonitorBackfill(ctx context.Context, mon store.GroupMonitor) {
	bfCtx, cancel := context.WithTimeout(ctx, groupMonitorBackfillTO)
	defer cancel()
	msgs, err := s.TG.FetchGroupHistoryPaged(
		bfCtx, mon.AccountID, mon.PeerType, mon.PeerID, mon.AccessHash, mon.HistoryLimit,
	)
	if err != nil && len(msgs) == 0 {
		_ = s.Store.SetGroupMonitorStatus(mon.AccountID, mon.PeerID, store.GroupMonitorError, err.Error())
		return
	}
	rows := make([]store.GroupMessage, 0, len(msgs))
	for i, msg := range msgs {
		rows = append(rows, store.GroupMessage{
			AccountID: mon.AccountID,
			PeerID:    mon.PeerID,
			MsgID:     msg.ID,
			Date:      msg.Date,
			FromID:    msg.FromID,
			FromName:  msg.FromName,
			Out:       msg.Out,
			Text:      msg.Text,
			Media:     tgclient.FormatChatMediaLabel(msg.Media),
		})
		if (i+1)%50 == 0 {
			_ = s.Store.UpdateGroupMonitorBackfill(mon.AccountID, mon.PeerID, i+1, mon.HistoryLimit)
		}
	}
	if _, err := s.Store.InsertGroupMessagesIgnoreDup(rows); err != nil {
		_ = s.Store.SetGroupMonitorStatus(mon.AccountID, mon.PeerID, store.GroupMonitorError, err.Error())
		return
	}
	_ = s.Store.UpdateGroupMonitorBackfill(mon.AccountID, mon.PeerID, len(msgs), mon.HistoryLimit)
	_ = s.Store.RefreshGroupMonitorCounts(mon.AccountID, mon.PeerID)
	note := ""
	if err != nil {
		note = "历史部分失败: " + err.Error()
	}
	_ = s.Store.SetGroupMonitorStatus(mon.AccountID, mon.PeerID, store.GroupMonitorMonitoring, note)
}

func groupMonitorTaskID(accountID string, peerID int64) string {
	return fmt.Sprintf("group_monitor_%s_%d", accountID, peerID)
}

var fileNameSafe = regexp.MustCompile(`[^a-zA-Z0-9_\-\p{Han}]+`)

func sanitizeFileName(name string) string {
	s := strings.TrimSpace(name)
	s = fileNameSafe.ReplaceAllString(s, "_")
	if len([]rune(s)) > 40 {
		s = string([]rune(s)[:40])
	}
	if s == "" {
		return "group"
	}
	return s
}

func writeGroupMessagesCSV(path string, header []string, rows []store.GroupMessage) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// UTF-8 BOM
	if _, err := f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	w := csv.NewWriter(f)
	if err := w.Write(header); err != nil {
		return err
	}
	for _, m := range rows {
		out := "0"
		if m.Out {
			out = "1"
		}
		dateStr := ""
		if m.Date > 0 {
			dateStr = time.Unix(int64(m.Date), 0).Format(time.RFC3339)
		}
		if err := w.Write([]string{
			strconv.Itoa(m.MsgID),
			dateStr,
			strconv.FormatInt(m.FromID, 10),
			m.FromName,
			out,
			m.Text,
			m.Media,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
