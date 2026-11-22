package api

import (
	"context"
	"sync"
	"time"
)

/**
 * 同一协议号两次主动发信的最小间隔。
 * 请求 intervalMs 仅可调大，不可低于此值。
 */
const MinAccountSendInterval = 5 * time.Minute

var (
	accountSendMu   sync.Mutex
	accountLastSend = map[string]time.Time{}
)

/**
 * 解析每协议号发信间隔（≥5 分钟）。
 *
 * @param intervalMs 请求间隔毫秒
 * @return 最小间隔
 * @author Auto
 * @date 2026-10-24
 */
func accountSendGap(intervalMs int) time.Duration {
	gap := MinAccountSendInterval
	if intervalMs > 0 {
		req := time.Duration(intervalMs) * time.Millisecond
		if req > gap {
			gap = req
		}
	}
	return gap
}

/**
 * 等待该协议号距上次发信已满 gap。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param gap 最小间隔
 * @return 是否因取消中断
 * @author Auto
 * @date 2026-10-24
 */
func waitAccountSendGap(ctx context.Context, accountID string, gap time.Duration) (cancelled bool) {
	accountSendMu.Lock()
	last := accountLastSend[accountID]
	accountSendMu.Unlock()
	if last.IsZero() {
		return false
	}
	wait := gap - time.Since(last)
	if wait <= 0 {
		return false
	}
	select {
	case <-ctx.Done():
		return true
	case <-time.After(wait):
		return false
	}
}

/**
 * 记录协议号发信时间点。
 *
 * @param accountID 协议号
 * @author Auto
 * @date 2026-10-24
 */
func markAccountSent(accountID string) {
	accountSendMu.Lock()
	defer accountSendMu.Unlock()
	accountLastSend[accountID] = time.Now()
}
