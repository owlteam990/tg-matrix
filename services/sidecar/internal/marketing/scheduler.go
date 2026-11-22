package marketing

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

// Scheduler 营销发送后台调度：按策略配额 / 双向规则消费队列。
type Scheduler struct {
	Store  *store.Store
	TG     *tgclient.Manager
	tick   time.Duration
	stopCh chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	busy   bool
}

/**
 * 创建调度器。
 *
 * @param st 存储
 * @param tg Telegram 管理器
 */
func NewScheduler(st *store.Store, tg *tgclient.Manager) *Scheduler {
	return &Scheduler{
		Store:  st,
		TG:     tg,
		tick:   2 * time.Second,
		stopCh: make(chan struct{}),
	}
}

/**
 * 启动后台 tick 循环。
 */
func (s *Scheduler) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(s.tick)
		defer t.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-t.C:
				s.safeTick()
			}
		}
	}()
	log.Printf("marketing send scheduler started (tick=%s)", s.tick)
}

/**
 * 停止调度器并等待退出。
 */
func (s *Scheduler) Stop() {
	close(s.stopCh)
	s.wg.Wait()
}

func (s *Scheduler) safeTick() {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return
	}
	s.busy = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()
	if err := s.tickOnce(); err != nil {
		log.Printf("marketing send scheduler: %v", err)
	}
}

/**
 * 单次调度：每个 running job 尝试发 1 条就绪项。
 */
func (s *Scheduler) tickOnce() error {
	if s.Store == nil || s.TG == nil || !s.TG.Configured() {
		return nil
	}
	jobs, err := s.Store.ListRunningMarketingSendJobs()
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := s.processJob(job); err != nil {
			log.Printf("marketing send job %s: %v", job.ID, err)
		}
	}
	return nil
}

func (s *Scheduler) processJob(job store.MarketingSendJob) error {
	policy, err := s.Store.GetMarketingSendPolicy(job.PolicyID)
	if err != nil {
		return err
	}
	if policy == nil || policy.Status != "active" {
		// 策略缺失：暂缓，不丢队
		return nil
	}
	item, err := s.Store.NextReadyMarketingSendItem(job.ID)
	if err != nil || item == nil {
		if err == nil {
			_ = s.Store.MaybeCompleteMarketingSendJob(job.ID)
		}
		return err
	}

	// 历史项：已是 PEER_FLOOD 等硬失败却仍 waiting 的，直接标失败，避免重复再发
	if tgclient.IsNonRetriableSendError("", item.LastError) {
		item.Status = "failed"
		item.LastError = tgclient.HumanizeSendError("", item.LastError)
		_ = s.Store.UpdateMarketingSendQueueItem(*item)
		_ = s.Store.MaybeCompleteMarketingSendJob(job.ID)
		return nil
	}

	accountID, notBefore, reason := s.pickAccount(job, *policy, *item)
	if accountID == "" {
		item.Status = "waiting"
		item.NotBefore = notBefore
		if reason != "" {
			item.LastError = reason
		}
		return s.Store.UpdateMarketingSendQueueItem(*item)
	}

	item.AccountID = accountID
	item.Status = "sending"
	item.Attempts++
	if err := s.Store.UpdateMarketingSendQueueItem(*item); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result := s.TG.SendDirect(ctx, accountID, item.TargetMode, item.Target, item.Text)

	nowMs := time.Now().UnixMilli()
	if result.OK {
		mutual := s.isMutual(accountID, item)
		_ = s.Store.InsertMarketingSendEvent(store.MarketingSendEvent{
			AccountID: accountID,
			LeadID:    item.LeadID,
			Target:    item.Target,
			Mutual:    mutual,
			SentAt:    nowMs,
		})
		item.Status = "sent"
		item.SentAt = nowMs
		item.LastError = ""
		item.NotBefore = 0
		if err := s.Store.UpdateMarketingSendQueueItem(*item); err != nil {
			return err
		}
		if lead, err := s.Store.GetMarketingLead(item.LeadID); err == nil && lead != nil {
			lead.Status = "sent"
			lead.SendAccountID = accountID
			lead.ErrorMessage = ""
			_ = s.Store.UpdateMarketingLead(*lead)
		}
		job.ConsecutiveFails = 0
		_ = s.Store.UpdateMarketingSendJob(job)
		_ = s.Store.MaybeCompleteMarketingSendJob(job.ID)
		return nil
	}

	// 中文错误说明；PEER_FLOOD 等硬失败不重试，避免重复再发加重限制
	humanErr := tgclient.HumanizeSendError(result.ErrorCode, result.ErrorMessage)
	hardFail := tgclient.IsNonRetriableSendError(result.ErrorCode, result.ErrorMessage)

	waitMs := int64(result.WaitSeconds) * 1000
	if waitMs <= 0 {
		waitMs = int64(policy.NonMutualMinIntervalMs)
		if waitMs < 60_000 {
			waitMs = 60_000
		}
	}
	// 仅 FLOOD_WAIT（带等待秒数）或超时可退避重试；PEER_FLOOD 不重试
	retriable := !hardFail && (result.WaitSeconds > 0 ||
		strings.EqualFold(result.ErrorCode, "FLOOD_WAIT") ||
		strings.Contains(strings.ToLower(result.ErrorCode), "timeout") ||
		strings.Contains(strings.ToLower(result.ErrorMessage), "timeout"))

	job.ConsecutiveFails++
	if retriable && item.Attempts < 8 {
		item.Status = "waiting"
		item.NotBefore = nowMs + waitMs
		item.LastError = humanErr
		// 释放绑定，便于号池换号重试；用户在队列页改号后会重新绑定
		item.AccountID = ""
		_ = s.Store.UpdateMarketingSendQueueItem(*item)
	} else {
		item.Status = "failed"
		item.LastError = humanErr
		_ = s.Store.UpdateMarketingSendQueueItem(*item)
		if lead, err := s.Store.GetMarketingLead(item.LeadID); err == nil && lead != nil {
			lead.ErrorMessage = humanErr
			_ = s.Store.UpdateMarketingLead(*lead)
		}
	}

	failStop := job.FailStopAfter
	if failStop < 0 {
		failStop = 0
	}
	if failStop > 0 && job.ConsecutiveFails >= failStop {
		job.Status = "paused"
		item.LastError = "连续失败熔断，任务已暂停"
	}
	_ = s.Store.UpdateMarketingSendJob(job)
	_ = s.Store.MaybeCompleteMarketingSendJob(job.ID)
	return nil
}

func (s *Scheduler) isMutual(accountID string, item *store.MarketingSendQueueItem) bool {
	lead, err := s.Store.GetMarketingLead(item.LeadID)
	if err != nil || lead == nil {
		return false
	}
	mutual, found, err := s.Store.ContactMutual(accountID, lead.UserID)
	if err != nil || !found {
		return false
	}
	return mutual
}

// AccountReadyHint 某协议号对队列项的可发时间提示。
type AccountReadyHint struct {
	AccountID   string `json:"accountId"`
	NotBefore   int64  `json:"notBefore"`
	Reason      string `json:"reason"`
	ReadyNow    bool   `json:"readyNow"`
	WindowUsage int    `json:"windowUsage"`
}

// SuggestReadyResult 计算下次可发时间的结果。
type SuggestReadyResult struct {
	ItemID      string             `json:"itemId"`
	AccountID   string             `json:"accountId"`
	NotBefore   int64              `json:"notBefore"`
	Reason      string             `json:"reason"`
	ReadyNow    bool               `json:"readyNow"`
	Candidates  []AccountReadyHint `json:"candidates"`
	PolicyID    string             `json:"policyId"`
	PolicyName  string             `json:"policyName"`
}

/**
 * 按策略计算队列项下次可发时间（可指定协议号，空则评估号池）。
 *
 * @param itemID 队列项 ID
 * @param accountID 指定协议号（可空）
 */
func (s *Scheduler) SuggestReady(itemID, accountID string) (*SuggestReadyResult, error) {
	item, err := s.Store.GetMarketingSendQueueItem(itemID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("队列项不存在")
	}
	job, err := s.Store.GetMarketingSendJob(item.JobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, fmt.Errorf("任务不存在")
	}
	policy, err := s.Store.GetMarketingSendPolicy(job.PolicyID)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, fmt.Errorf("发送策略不存在")
	}

	nowMs := time.Now().UnixMilli()
	pool := append([]string{}, job.AccountIDs...)
	accountID = strings.TrimSpace(accountID)
	if accountID != "" {
		found := false
		for _, id := range pool {
			if id == accountID {
				found = true
				break
			}
		}
		if !found {
			pool = append([]string{accountID}, pool...)
		}
	}

	// 同目标冷却
	var targetReady int64
	var targetReason string
	if policy.SameTargetCooldownMs > 0 {
		lastTarget, _ := s.Store.LastMarketingSendToTarget(item.Target)
		if lastTarget > 0 {
			readyAt := lastTarget + int64(policy.SameTargetCooldownMs)
			if readyAt > nowMs {
				targetReady = readyAt
				targetReason = "同目标冷却中"
			}
		}
	}

	cands := make([]AccountReadyHint, 0, len(pool))
	for _, id := range pool {
		wait, why, usage := s.accountReadyAt(id, *policy, *item, nowMs)
		if targetReady > wait {
			wait = targetReady
			why = targetReason
		}
		cands = append(cands, AccountReadyHint{
			AccountID:   id,
			NotBefore:   wait,
			Reason:      why,
			ReadyNow:    wait <= nowMs,
			WindowUsage: usage,
		})
	}

	out := &SuggestReadyResult{
		ItemID:     item.ID,
		Candidates: cands,
		PolicyID:   policy.ID,
		PolicyName: policy.Name,
	}

	pickID := accountID
	if pickID == "" {
		pickID = strings.TrimSpace(item.AccountID)
	}
	var chosen *AccountReadyHint
	if pickID != "" {
		for i := range cands {
			if cands[i].AccountID == pickID {
				chosen = &cands[i]
				break
			}
		}
	}
	if chosen == nil {
		// 选最早可发且窗口用量最少
		for i := range cands {
			c := &cands[i]
			if chosen == nil {
				chosen = c
				continue
			}
			if c.NotBefore < chosen.NotBefore ||
				(c.NotBefore == chosen.NotBefore && c.WindowUsage < chosen.WindowUsage) {
				chosen = c
			}
		}
	}
	if chosen == nil {
		out.NotBefore = nowMs + 30_000
		out.Reason = "号池为空"
		out.ReadyNow = false
		return out, nil
	}
	out.AccountID = chosen.AccountID
	out.NotBefore = chosen.NotBefore
	out.Reason = chosen.Reason
	out.ReadyNow = chosen.ReadyNow
	return out, nil
}

/**
 * 为队列项选择可用协议号；无可发时返回空 account 与建议 not_before。
 *
 * @param job 任务
 * @param policy 策略
 * @param item 队列项
 */
func (s *Scheduler) pickAccount(
	job store.MarketingSendJob,
	policy store.MarketingSendPolicy,
	item store.MarketingSendQueueItem,
) (accountID string, notBefore int64, reason string) {
	pool := job.AccountIDs
	if len(pool) == 0 {
		return "", time.Now().UnixMilli() + 60_000, "号池为空"
	}

	// 项上已绑定协议号：严格使用该号（用户改号/定时后生效）；失败重试时会清空绑定
	preferred := strings.TrimSpace(item.AccountID)
	strict := preferred != ""
	if preferred == "" {
		if lead, err := s.Store.GetMarketingLead(item.LeadID); err == nil && lead != nil {
			preferred = strings.TrimSpace(lead.SendAccountID)
			if preferred == "" {
				preferred = strings.TrimSpace(lead.AccountID)
			}
		}
	}
	ordered := make([]string, 0, len(pool))
	if preferred != "" {
		inPool := false
		for _, id := range pool {
			if id == preferred {
				inPool = true
				break
			}
		}
		if inPool || strict {
			ordered = append(ordered, preferred)
		}
		if strict {
			// 严格绑定：不再尝试号池其他号
			pool = nil
		}
	}
	if !strict {
		for _, id := range pool {
			if id == preferred {
				continue
			}
			ordered = append(ordered, id)
		}
	}
	if len(ordered) == 0 {
		return "", time.Now().UnixMilli() + 30_000, "无可用协议号"
	}

	nowMs := time.Now().UnixMilli()
	bestWait := int64(0)
	bestReason := "暂无可用协议号"

	// 同目标冷却（全局）
	if policy.SameTargetCooldownMs > 0 {
		lastTarget, _ := s.Store.LastMarketingSendToTarget(item.Target)
		if lastTarget > 0 {
			readyAt := lastTarget + int64(policy.SameTargetCooldownMs)
			if readyAt > nowMs {
				return "", readyAt, "同目标冷却中"
			}
		}
	}

	type cand struct {
		id    string
		usage int
		wait  int64
		why   string
	}
	ready := make([]cand, 0)
	for _, id := range ordered {
		wait, why, usage := s.accountReadyAt(id, policy, item, nowMs)
		if wait <= nowMs {
			ready = append(ready, cand{id: id, usage: usage, wait: wait})
		} else if bestWait == 0 || wait < bestWait {
			bestWait = wait
			bestReason = why
		}
	}
	if len(ready) == 0 {
		if bestWait == 0 {
			bestWait = nowMs + 30_000
		}
		return "", bestWait, bestReason
	}
	// 选用窗口内用量最少的号
	best := ready[0]
	for _, c := range ready[1:] {
		if c.usage < best.usage {
			best = c
		}
	}
	return best.id, 0, ""
}

func (s *Scheduler) accountReadyAt(
	accountID string,
	policy store.MarketingSendPolicy,
	item store.MarketingSendQueueItem,
	nowMs int64,
) (readyAt int64, reason string, windowUsage int) {
	windowMs := int64(policy.WindowMinutes) * 60_000
	since := nowMs - windowMs
	if since < 0 {
		since = 0
	}
	usage, _ := s.Store.CountMarketingSendEventsSince(accountID, since)
	windowUsage = usage
	if usage >= policy.MaxPerWindow {
		// 粗略：等到窗口内最早一条过期（用 now+1min 重试即可）
		return nowMs + 60_000, "窗口配额已满", usage
	}

	mutual := s.isMutual(accountID, &item)
	lastAt, _ := s.Store.LastMarketingSendAt(accountID)
	minInterval := int64(policy.MutualMinIntervalMs)
	if !mutual {
		minInterval = int64(policy.NonMutualMinIntervalMs)
		dayStart := dayStartMs(nowMs)
		dayCount, _ := s.Store.CountNonMutualSendsSince(accountID, dayStart)
		if dayCount >= policy.NonMutualDailyCap {
			return dayStart + 24*60*60*1000, "非双向日上限", usage
		}
	}
	if lastAt > 0 {
		ready := lastAt + minInterval
		if ready > nowMs {
			why := "双向间隔未到"
			if !mutual {
				why = "非双向间隔未到"
			}
			return ready, why, usage
		}
	}
	return nowMs, "", usage
}

func dayStartMs(nowMs int64) int64 {
	t := time.UnixMilli(nowMs).In(time.Local)
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location()).UnixMilli()
}
