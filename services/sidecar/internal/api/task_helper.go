package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

/**
 * 将 FloodWait 秒数转为可等待时长（上限 120s）。
 *
 * @param waitSec 等待秒数
 * @return 时长
 * @author Auto
 * @date 2026-10-09
 */
func floodWaitDuration(waitSec int) time.Duration {
	if waitSec <= 0 {
		return 0
	}
	wait := time.Duration(waitSec) * time.Second
	if wait > 120*time.Second {
		wait = 120 * time.Second
	}
	return wait
}

/**
 * 等待 FloodWait；若上下文取消则返回 false。
 *
 * @param ctx 上下文
 * @param waitSec 等待秒
 * @return 是否完成等待
 * @author Auto
 * @date 2026-10-09
 */
func awaitFlood(ctx context.Context, waitSec int) bool {
	wait := floodWaitDuration(waitSec)
	if wait <= 0 {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

/**
 * 落库已完成批量任务，返回 taskId（失败时返回空串）。
 *
 * @param module 模块名
 * @param title 标题
 * @param allOK 是否全部成功
 * @param items 明细
 * @return taskId
 * @author Auto
 * @date 2026-10-09
 */
func (s *Server) saveFinishedTask(module, title string, allOK bool, items []store.TaskItem) string {
	if s.Store == nil || len(items) == 0 {
		return ""
	}
	status := "done"
	if !allOK {
		status = "done_partial"
	}
	id := newTaskID()
	if err := s.Store.CreateFinishedTask(id, module, title, status, items); err != nil {
		return ""
	}
	return id
}

func taskItemFromSend(r tgclient.SendItemResult, retried bool) store.TaskItem {
	status := "failed"
	if r.OK {
		status = "ok"
	}
	local := ""
	if retried {
		local = "flood_retried"
	}
	return store.TaskItem{
		AccountID:      r.AccountID,
		Target:         r.Target,
		Status:         status,
		BusinessResult: r.BusinessResult,
		ErrorCode:      r.ErrorCode,
		ErrorMessage:   r.ErrorMessage,
		APIResult:      r.Mode,
		LocalResult:    local,
	}
}

func taskItemFromGroup(r tgclient.GroupItemResult, retried bool) store.TaskItem {
	status := "failed"
	if r.OK {
		status = "ok"
	}
	local := ""
	if retried {
		local = "flood_retried"
	}
	return store.TaskItem{
		AccountID:      r.AccountID,
		Target:         r.Target,
		Status:         status,
		BusinessResult: r.BusinessResult,
		ErrorCode:      r.ErrorCode,
		ErrorMessage:   r.ErrorMessage,
		APIResult:      r.Action,
		LocalResult:    local,
	}
}

func taskItemFromInvite(r tgclient.InviteItemResult, retried bool) store.TaskItem {
	status := "failed"
	if r.OK {
		status = "ok"
	}
	local := r.Group
	if retried {
		local = local + "|flood_retried"
	}
	return store.TaskItem{
		AccountID:      r.AccountID,
		Target:         r.Member,
		Status:         status,
		BusinessResult: r.BusinessResult,
		ErrorCode:      r.ErrorCode,
		ErrorMessage:   r.ErrorMessage,
		APIResult:      r.Mode,
		LocalResult:    local,
	}
}

func taskItemFromContact(r tgclient.ContactItemResult, retried bool) store.TaskItem {
	status := "failed"
	if r.OK {
		status = "ok"
	}
	local := ""
	if retried {
		local = "flood_retried"
	}
	return store.TaskItem{
		AccountID:      r.AccountID,
		Target:         r.Target,
		Status:         status,
		BusinessResult: r.BusinessResult,
		ErrorCode:      r.ErrorCode,
		ErrorMessage:   r.ErrorMessage,
		APIResult:      r.Action,
		LocalResult:    local,
	}
}

func taskItemFromPhone(r tgclient.PhoneCheckItem, retried bool) store.TaskItem {
	status := "failed"
	if r.OK {
		status = "ok"
	}
	local := ""
	if retried {
		local = "flood_retried"
	}
	return store.TaskItem{
		AccountID:      r.AccountID,
		Target:         r.Phone,
		Status:         status,
		BusinessResult: r.BusinessResult,
		ErrorCode:      r.ErrorCode,
		ErrorMessage:   r.ErrorMessage,
		LocalResult:    local,
	}
}

func taskItemsFromScrape(result tgclient.ScrapeResult) []store.TaskItem {
	items := make([]store.TaskItem, 0, len(result.Rows)+1)
	if !result.OK {
		items = append(items, store.TaskItem{
			AccountID:      result.AccountID,
			Target:         result.Group,
			Status:         "failed",
			BusinessResult: result.BusinessResult,
			ErrorCode:      result.ErrorCode,
			ErrorMessage:   result.ErrorMessage,
		})
		return items
	}
	// 明细过多时只存摘要行 + 前 200 条，避免撑爆本地库
	summary := store.TaskItem{
		AccountID:      result.AccountID,
		Target:         result.Group,
		Status:         "ok",
		BusinessResult: resultOKOr(result.BusinessResult),
		LocalResult:    fmt.Sprintf("members=%d speakers=%d rows=%d", result.MembersCount, result.SpeakersCount, len(result.Rows)),
	}
	items = append(items, summary)
	limit := len(result.Rows)
	if limit > 200 {
		limit = 200
	}
	for i := 0; i < limit; i++ {
		row := result.Rows[i]
		items = append(items, store.TaskItem{
			AccountID:      row.AccountID,
			Target:         fmt.Sprintf("%d", row.UserID),
			Status:         "ok",
			BusinessResult: row.Source,
			APIResult:      row.Username,
			LocalResult:    strings.TrimSpace(row.FirstName + " " + row.LastName),
		})
	}
	return items
}

func resultOKOr(v string) string {
	if v == "" {
		return tgclient.ResultOK
	}
	return v
}

/**
 * 解析熔断阈值：连续失败达到该数则停止。
 * 请求省略或 0 → 默认 5；负数 → 关闭熔断。
 *
 * @param v 请求值
 * @return 阈值（≤0 表示关闭）
 * @author Auto
 * @date 2026-10-09
 */
func failStopAfterOrDefault(v int) int {
	if v < 0 {
		return 0
	}
	if v == 0 {
		return 5
	}
	return v
}

/**
 * 根据是否成功更新连续失败计数。
 *
 * @param consecutive 当前连续失败数
 * @param ok 本条是否成功
 * @return 新连续失败数
 * @author Auto
 * @date 2026-10-09
 */
func nextConsecutiveFails(consecutive int, ok bool) int {
	if ok {
		return 0
	}
	return consecutive + 1
}

/**
 * 是否应因熔断停止。
 *
 * @param limit 阈值（≤0 关闭）
 * @param consecutive 连续失败数
 * @return 是否熔断
 * @author Auto
 * @date 2026-10-09
 */
func shouldCircuitBreak(limit, consecutive int) bool {
	return limit > 0 && consecutive >= limit
}
