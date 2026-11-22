package api

import (
	"net/http"
	"time"

	"github.com/tg-matrix/sidecar/internal/store"
)

type taskDTO struct {
	ID               string `json:"id"`
	Module           string `json:"module"`
	Status           string `json:"status"`
	Title            string `json:"title"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`
	FinishedAt       int64  `json:"finishedAt"`
	ItemCount        int    `json:"itemCount"`
	TotalCount       int    `json:"totalCount"`
	CursorIndex      int    `json:"cursorIndex"`
	WaitUntil        int64  `json:"waitUntil"`
	WaitReason       string `json:"waitReason"`
	WaitRemainingSec int64  `json:"waitRemainingSec"`
	PendingCount     int    `json:"pendingCount"`
	Resumable        bool   `json:"resumable"`
	Running          bool   `json:"running"`
}

type taskItemDTO struct {
	ID             string `json:"id"`
	TaskID         string `json:"taskId"`
	AccountID      string `json:"accountId"`
	Target         string `json:"target"`
	Status         string `json:"status"`
	LocalResult    string `json:"localResult"`
	APIResult      string `json:"apiResult"`
	BusinessResult string `json:"businessResult"`
	ErrorCode      string `json:"errorCode"`
	ErrorMessage   string `json:"errorMessage"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

func toTaskDTO(t store.Task, running bool, pending int) taskDTO {
	remain := int64(0)
	if t.Status == store.TaskWaiting && t.WaitUntil > 0 {
		remain = t.WaitUntil - time.Now().Unix()
		if remain < 0 {
			remain = 0
		}
	}
	resumable := t.Resumable() && pending > 0 && !running
	return taskDTO{
		ID: t.ID, Module: t.Module, Status: t.Status, Title: t.Title,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, FinishedAt: t.FinishedAt,
		ItemCount: t.ItemCount, TotalCount: t.TotalCount, CursorIndex: t.CursorIndex,
		WaitUntil: t.WaitUntil, WaitReason: t.WaitReason, WaitRemainingSec: remain,
		PendingCount: pending, Resumable: resumable, Running: running,
	}
}

func (s *Server) taskDTOOf(t store.Task) taskDTO {
	running := s.Runner != nil && s.Runner.IsRunning(t.ID)
	pending := 0
	if t.PayloadJSON != "" {
		pending = s.Store.CountTaskItemsByStatus(t.ID, store.ItemPending)
	}
	return toTaskDTO(t, running, pending)
}

/**
 * 列出最近任务。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListTasks(100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_query_failed", err.Error())
		return
	}
	list := make([]taskDTO, 0, len(items))
	for _, t := range items {
		list = append(list, s.taskDTOOf(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

/**
 * 查询单个任务。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_id", "task id required")
		return
	}
	t, ok, err := s.Store.GetTask(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_query_failed", err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": s.taskDTOOf(t)})
}

/**
 * 列出任务明细。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleListTaskItems(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_id", "task id required")
		return
	}
	items, err := s.Store.ListTaskItems(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_query_failed", err.Error())
		return
	}
	list := make([]taskItemDTO, 0, len(items))
	for _, item := range items {
		list = append(list, taskItemDTO{
			ID: item.ID, TaskID: item.TaskID, AccountID: item.AccountID, Target: item.Target,
			Status: item.Status, LocalResult: item.LocalResult, APIResult: item.APIResult,
			BusinessResult: item.BusinessResult, ErrorCode: item.ErrorCode, ErrorMessage: item.ErrorMessage,
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

/**
 * 取消后台运行中 / 等待中的任务。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_id", "task id required")
		return
	}
	t, ok, _ := s.Store.GetTask(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
	cancelledLive := s.Runner != nil && s.Runner.Cancel(id)
	switch t.Status {
	case store.TaskRunning, store.TaskPending, store.TaskWaiting:
		_, _ = s.Store.MarkRemainingItemsCancelled(id)
		_ = s.Store.FinishTask(id, store.TaskCancelled)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "id": id, "status": store.TaskCancelled, "cancelledLive": cancelledLive,
		})
		return
	default:
		if cancelledLive {
			_, _ = s.Store.MarkRemainingItemsCancelled(id)
			_ = s.Store.FinishTask(id, store.TaskCancelled)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "status": store.TaskCancelled})
			return
		}
		writeErr(w, http.StatusConflict, "not_running", "task is not cancellable in current status")
		return
	}
}

/**
 * 续跑 interrupted / circuit_break 等可恢复任务。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleResumeTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_id", "task id required")
		return
	}
	if code := s.resumeJob(id); code != "" {
		status := http.StatusConflict
		switch code {
		case "not_found":
			status = http.StatusNotFound
		case "gotd_not_configured", "runner_unavailable":
			status = http.StatusServiceUnavailable
		}
		writeErr(w, status, code, code)
		return
	}
	t, _, _ := s.Store.GetTask(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "status": t.Status, "task": s.taskDTOOf(t)})
}

/**
 * 放弃可恢复任务（剩余 pending → cancelled）。
 *
 * @param w 响应写入器
 * @param r 请求
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) handleAbandonTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_id", "task id required")
		return
	}
	if code := s.abandonJob(id); code != "" {
		status := http.StatusConflict
		if code == "not_found" {
			status = http.StatusNotFound
		}
		writeErr(w, status, code, code)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "status": store.TaskCancelled})
}
