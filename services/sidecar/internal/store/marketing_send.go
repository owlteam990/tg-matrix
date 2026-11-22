package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MarketingSendPolicy 发送策略（滑动窗口 + 双向/非双向规则）。
type MarketingSendPolicy struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	WindowMinutes          int    `json:"windowMinutes"`
	MaxPerWindow           int    `json:"maxPerWindow"`
	MutualMinIntervalMs    int    `json:"mutualMinIntervalMs"`
	NonMutualMinIntervalMs int    `json:"nonMutualMinIntervalMs"`
	NonMutualDailyCap      int    `json:"nonMutualDailyCap"`
	SameTargetCooldownMs  int    `json:"sameTargetCooldownMs"`
	Status                 string `json:"status"` // active|disabled
	CreatedAt              int64  `json:"createdAt"`
	UpdatedAt              int64  `json:"updatedAt"`
}

// MarketingSendJob 一批入队发送任务。
type MarketingSendJob struct {
	ID               string   `json:"id"`
	ProductID        string   `json:"productId"`
	PolicyID         string   `json:"policyId"`
	AccountIDs       []string `json:"accountIds"`
	AccountIDsJSON   string   `json:"-"`
	Status           string   `json:"status"` // running|paused|completed|cancelled
	FailStopAfter    int      `json:"failStopAfter"`
	ConsecutiveFails int      `json:"consecutiveFails"`
	CreatedAt        int64    `json:"createdAt"`
	UpdatedAt        int64    `json:"updatedAt"`
	// 统计（列表接口填充）
	QueuedCount  int `json:"queuedCount,omitempty"`
	WaitingCount int `json:"waitingCount,omitempty"`
	SendingCount int `json:"sendingCount,omitempty"`
	SentCount    int `json:"sentCount,omitempty"`
	FailedCount  int `json:"failedCount,omitempty"`
	TotalCount   int `json:"totalCount,omitempty"`
}

// MarketingSendQueueItem 队列中的一条私信。
type MarketingSendQueueItem struct {
	ID         string `json:"id"`
	JobID      string `json:"jobId"`
	LeadID     string `json:"leadId"`
	AccountID  string `json:"accountId"`
	TargetMode string `json:"targetMode"`
	Target     string `json:"target"`
	Text       string `json:"text"`
	Status     string `json:"status"` // queued|waiting|sending|sent|failed|skipped|cancelled
	NotBefore  int64  `json:"notBefore"`
	Attempts   int    `json:"attempts"`
	LastError  string `json:"lastError"`
	SentAt     int64  `json:"sentAt"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// SendQueueItemView 发送队列统一视图（营销 + 群发）。
type SendQueueItemView struct {
	MarketingSendQueueItem
	Source      string `json:"source"`      // bulk | marketing
	JobStatus   string `json:"jobStatus"`   // running|paused|completed|cancelled
	ProductID   string `json:"productId"`   // __bulk__ 或营销产品 ID
	AccountIDs  []string `json:"accountIds"` // 任务号池
}

// MarketingSendEvent 成功发送记录（配额统计）。
type MarketingSendEvent struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	LeadID    string `json:"leadId"`
	Target    string `json:"target"`
	Mutual    bool   `json:"mutual"`
	SentAt    int64  `json:"sentAt"`
}

func decodeAccountIDs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return []string{}
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func encodeAccountIDs(ids []string) string {
	if ids == nil {
		ids = []string{}
	}
	b, _ := json.Marshal(ids)
	return string(b)
}

func scanSendPolicy(row interface {
	Scan(dest ...any) error
}) (*MarketingSendPolicy, error) {
	var p MarketingSendPolicy
	err := row.Scan(
		&p.ID, &p.Name, &p.WindowMinutes, &p.MaxPerWindow,
		&p.MutualMinIntervalMs, &p.NonMutualMinIntervalMs, &p.NonMutualDailyCap,
		&p.SameTargetCooldownMs, &p.Status, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

/**
 * 列出发送策略。
 *
 * @return 策略列表
 */
func (s *Store) ListMarketingSendPolicies() ([]MarketingSendPolicy, error) {
	rows, err := s.DB.Query(`
SELECT id, name, window_minutes, max_per_window, mutual_min_interval_ms,
       non_mutual_min_interval_ms, non_mutual_daily_cap, same_target_cooldown_ms,
       status, created_at, updated_at
FROM marketing_send_policies ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MarketingSendPolicy, 0)
	for rows.Next() {
		p, err := scanSendPolicy(rows)
		if err != nil {
			return nil, err
		}
		if p != nil {
			out = append(out, *p)
		}
	}
	return out, rows.Err()
}

/**
 * 按 ID 取发送策略。
 *
 * @param id 策略 ID
 */
func (s *Store) GetMarketingSendPolicy(id string) (*MarketingSendPolicy, error) {
	row := s.DB.QueryRow(`
SELECT id, name, window_minutes, max_per_window, mutual_min_interval_ms,
       non_mutual_min_interval_ms, non_mutual_daily_cap, same_target_cooldown_ms,
       status, created_at, updated_at
FROM marketing_send_policies WHERE id=?`, id)
	return scanSendPolicy(row)
}

/**
 * 规范化策略字段默认值。
 *
 * @param p 策略
 */
func normalizeSendPolicy(p *MarketingSendPolicy) {
	if p.Name == "" {
		p.Name = "默认策略"
	}
	if p.WindowMinutes <= 0 {
		p.WindowMinutes = 60
	}
	if p.MaxPerWindow <= 0 {
		p.MaxPerWindow = 5
	}
	if p.MutualMinIntervalMs <= 0 {
		// 默认：同一协议号两次主动发信至少间隔 5 分钟
		p.MutualMinIntervalMs = 300000
	}
	if p.NonMutualMinIntervalMs <= 0 {
		p.NonMutualMinIntervalMs = 600000
	}
	if p.NonMutualDailyCap <= 0 {
		p.NonMutualDailyCap = 20
	}
	if p.SameTargetCooldownMs < 0 {
		p.SameTargetCooldownMs = 0
	}
	if p.Status == "" {
		p.Status = "active"
	}
}

/**
 * 创建或更新发送策略。
 *
 * @param p 策略（空 ID 新建）
 */
func (s *Store) UpsertMarketingSendPolicy(p MarketingSendPolicy) (MarketingSendPolicy, error) {
	now := time.Now().Unix()
	normalizeSendPolicy(&p)
	if p.ID == "" {
		p.ID = newMarketingID()
		p.CreatedAt = now
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	_, err := s.DB.Exec(`
INSERT INTO marketing_send_policies (
  id, name, window_minutes, max_per_window, mutual_min_interval_ms,
  non_mutual_min_interval_ms, non_mutual_daily_cap, same_target_cooldown_ms,
  status, created_at, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name,
  window_minutes=excluded.window_minutes,
  max_per_window=excluded.max_per_window,
  mutual_min_interval_ms=excluded.mutual_min_interval_ms,
  non_mutual_min_interval_ms=excluded.non_mutual_min_interval_ms,
  non_mutual_daily_cap=excluded.non_mutual_daily_cap,
  same_target_cooldown_ms=excluded.same_target_cooldown_ms,
  status=excluded.status,
  updated_at=excluded.updated_at
`, p.ID, p.Name, p.WindowMinutes, p.MaxPerWindow, p.MutualMinIntervalMs,
		p.NonMutualMinIntervalMs, p.NonMutualDailyCap, p.SameTargetCooldownMs,
		p.Status, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return p, fmt.Errorf("upsert send policy: %w", err)
	}
	return p, nil
}

/**
 * 删除发送策略。
 *
 * @param id 策略 ID
 */
func (s *Store) DeleteMarketingSendPolicy(id string) error {
	_, err := s.DB.Exec(`DELETE FROM marketing_send_policies WHERE id=?`, id)
	return err
}

func scanSendJob(row interface {
	Scan(dest ...any) error
}) (*MarketingSendJob, error) {
	var j MarketingSendJob
	err := row.Scan(
		&j.ID, &j.ProductID, &j.PolicyID, &j.AccountIDsJSON, &j.Status,
		&j.FailStopAfter, &j.ConsecutiveFails, &j.CreatedAt, &j.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.AccountIDs = decodeAccountIDs(j.AccountIDsJSON)
	return &j, nil
}

/**
 * 填充任务队列统计。
 *
 * @param job 任务
 */
func (s *Store) fillSendJobCounts(job *MarketingSendJob) error {
	rows, err := s.DB.Query(`
SELECT status, COUNT(*) FROM marketing_send_queue WHERE job_id=? GROUP BY status`, job.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return err
		}
		total += n
		switch st {
		case "queued":
			job.QueuedCount = n
		case "waiting":
			job.WaitingCount = n
		case "sending":
			job.SendingCount = n
		case "sent":
			job.SentCount = n
		case "failed", "skipped", "cancelled":
			job.FailedCount += n
		}
	}
	job.TotalCount = total
	return rows.Err()
}

/**
 * 列出发送任务（新→旧）。
 *
 * @param productID 可选按产品过滤
 * @param limit 条数上限
 */
func (s *Store) ListMarketingSendJobs(productID string, limit int) ([]MarketingSendJob, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `
SELECT id, product_id, policy_id, account_ids_json, status,
       fail_stop_after, consecutive_fails, created_at, updated_at
FROM marketing_send_jobs`
	args := []any{}
	if productID != "" {
		q += ` WHERE product_id=?`
		args = append(args, productID)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	out := make([]MarketingSendJob, 0)
	for rows.Next() {
		j, err := scanSendJob(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		if j == nil {
			continue
		}
		out = append(out, *j)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	// 必须先关闭 rows 再查统计：MaxOpenConns=1 时在 Next 内嵌套 Query 会死锁，拖死 /health
	_ = rows.Close()
	for i := range out {
		_ = s.fillSendJobCounts(&out[i])
	}
	return out, nil
}

/**
 * 按 ID 取发送任务。
 *
 * @param id 任务 ID
 */
func (s *Store) GetMarketingSendJob(id string) (*MarketingSendJob, error) {
	row := s.DB.QueryRow(`
SELECT id, product_id, policy_id, account_ids_json, status,
       fail_stop_after, consecutive_fails, created_at, updated_at
FROM marketing_send_jobs WHERE id=?`, id)
	j, err := scanSendJob(row)
	if err != nil || j == nil {
		return j, err
	}
	_ = s.fillSendJobCounts(j)
	return j, nil
}

/**
 * 列出仍在运行中的发送任务。
 */
func (s *Store) ListRunningMarketingSendJobs() ([]MarketingSendJob, error) {
	rows, err := s.DB.Query(`
SELECT id, product_id, policy_id, account_ids_json, status,
       fail_stop_after, consecutive_fails, created_at, updated_at
FROM marketing_send_jobs WHERE status='running' ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MarketingSendJob, 0)
	for rows.Next() {
		j, err := scanSendJob(rows)
		if err != nil {
			return nil, err
		}
		if j != nil {
			out = append(out, *j)
		}
	}
	return out, rows.Err()
}

/**
 * 创建发送任务。
 *
 * @param job 任务
 */
func (s *Store) CreateMarketingSendJob(job MarketingSendJob) (MarketingSendJob, error) {
	now := time.Now().Unix()
	if job.ID == "" {
		job.ID = newMarketingID()
	}
	if job.Status == "" {
		job.Status = "running"
	}
	if job.FailStopAfter == 0 {
		job.FailStopAfter = 5
	}
	job.AccountIDsJSON = encodeAccountIDs(job.AccountIDs)
	job.CreatedAt = now
	job.UpdatedAt = now
	_, err := s.DB.Exec(`
INSERT INTO marketing_send_jobs (
  id, product_id, policy_id, account_ids_json, status,
  fail_stop_after, consecutive_fails, created_at, updated_at
) VALUES (?,?,?,?,?,?,?,?,?)`,
		job.ID, job.ProductID, job.PolicyID, job.AccountIDsJSON, job.Status,
		job.FailStopAfter, job.ConsecutiveFails, job.CreatedAt, job.UpdatedAt,
	)
	if err != nil {
		return job, fmt.Errorf("create send job: %w", err)
	}
	return job, nil
}

/**
 * 更新发送任务状态与熔断计数。
 *
 * @param job 任务
 */
func (s *Store) UpdateMarketingSendJob(job MarketingSendJob) error {
	job.UpdatedAt = time.Now().Unix()
	job.AccountIDsJSON = encodeAccountIDs(job.AccountIDs)
	_, err := s.DB.Exec(`
UPDATE marketing_send_jobs SET
  status=?, account_ids_json=?, fail_stop_after=?, consecutive_fails=?, updated_at=?
WHERE id=?`,
		job.Status, job.AccountIDsJSON, job.FailStopAfter, job.ConsecutiveFails, job.UpdatedAt, job.ID,
	)
	return err
}

/**
 * 批量插入队列项。
 *
 * @param items 队列项
 */
func (s *Store) InsertMarketingSendQueueItems(items []MarketingSendQueueItem) error {
	if len(items) == 0 {
		return nil
	}
	now := time.Now().Unix()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`
INSERT INTO marketing_send_queue (
  id, job_id, lead_id, account_id, target_mode, target, text,
  status, not_before, attempts, last_error, sent_at, created_at, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, it := range items {
		if it.ID == "" {
			it.ID = newMarketingID()
		}
		if it.Status == "" {
			it.Status = "queued"
		}
		if it.CreatedAt == 0 {
			it.CreatedAt = now
		}
		it.UpdatedAt = now
		if _, err := stmt.Exec(
			it.ID, it.JobID, it.LeadID, it.AccountID, it.TargetMode, it.Target, it.Text,
			it.Status, it.NotBefore, it.Attempts, it.LastError, it.SentAt, it.CreatedAt, it.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanQueueItem(row interface {
	Scan(dest ...any) error
}) (*MarketingSendQueueItem, error) {
	var it MarketingSendQueueItem
	err := row.Scan(
		&it.ID, &it.JobID, &it.LeadID, &it.AccountID, &it.TargetMode, &it.Target, &it.Text,
		&it.Status, &it.NotBefore, &it.Attempts, &it.LastError, &it.SentAt, &it.CreatedAt, &it.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &it, nil
}

/**
 * 列出某任务队列项。
 *
 * @param jobID 任务 ID
 */
func (s *Store) ListMarketingSendQueueItems(jobID string) ([]MarketingSendQueueItem, error) {
	rows, err := s.DB.Query(`
SELECT id, job_id, lead_id, account_id, target_mode, target, text,
       status, not_before, attempts, last_error, sent_at, created_at, updated_at
FROM marketing_send_queue WHERE job_id=? ORDER BY created_at ASC`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MarketingSendQueueItem, 0)
	for rows.Next() {
		it, err := scanQueueItem(rows)
		if err != nil {
			return nil, err
		}
		if it != nil {
			out = append(out, *it)
		}
	}
	return out, rows.Err()
}

/**
 * 取一条可尝试发送的队列项（queued/waiting 且 not_before 已到）。
 *
 * @param jobID 任务 ID
 */
func (s *Store) NextReadyMarketingSendItem(jobID string) (*MarketingSendQueueItem, error) {
	nowMs := time.Now().UnixMilli()
	row := s.DB.QueryRow(`
SELECT id, job_id, lead_id, account_id, target_mode, target, text,
       status, not_before, attempts, last_error, sent_at, created_at, updated_at
FROM marketing_send_queue
WHERE job_id=? AND status IN ('queued','waiting') AND not_before<=?
ORDER BY not_before ASC, created_at ASC
LIMIT 1`, jobID, nowMs)
	return scanQueueItem(row)
}

/**
 * 更新队列项。
 *
 * @param it 队列项
 */
func (s *Store) UpdateMarketingSendQueueItem(it MarketingSendQueueItem) error {
	it.UpdatedAt = time.Now().Unix()
	_, err := s.DB.Exec(`
UPDATE marketing_send_queue SET
  account_id=?, text=?, status=?, not_before=?, attempts=?, last_error=?, sent_at=?, updated_at=?
WHERE id=?`,
		it.AccountID, it.Text, it.Status, it.NotBefore, it.Attempts, it.LastError, it.SentAt, it.UpdatedAt, it.ID,
	)
	return err
}

/**
 * 按 ID 取队列项。
 *
 * @param id 队列项 ID
 */
func (s *Store) GetMarketingSendQueueItem(id string) (*MarketingSendQueueItem, error) {
	row := s.DB.QueryRow(`
SELECT id, job_id, lead_id, account_id, target_mode, target, text,
       status, not_before, attempts, last_error, sent_at, created_at, updated_at
FROM marketing_send_queue WHERE id=?`, id)
	return scanQueueItem(row)
}

/**
 * 列出发送队列统一视图（营销 + 群发）。
 *
 * @param source bulk|marketing|空=全部
 * @param status 状态过滤，空=全部
 * @param limit 上限
 */
func (s *Store) ListSendQueueItems(source, status string, limit int) ([]SendQueueItemView, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	q := `
SELECT q.id, q.job_id, q.lead_id, q.account_id, q.target_mode, q.target, q.text,
       q.status, q.not_before, q.attempts, q.last_error, q.sent_at, q.created_at, q.updated_at,
       j.status, j.product_id, j.account_ids_json
FROM marketing_send_queue q
JOIN marketing_send_jobs j ON j.id = q.job_id
WHERE 1=1`
	args := []any{}
	source = strings.TrimSpace(strings.ToLower(source))
	switch source {
	case "bulk":
		q += ` AND j.product_id=?`
		args = append(args, "__bulk__")
	case "marketing":
		q += ` AND j.product_id<>?`
		args = append(args, "__bulk__")
	}
	status = strings.TrimSpace(status)
	if status != "" {
		q += ` AND q.status=?`
		args = append(args, status)
	}
	q += ` ORDER BY q.created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SendQueueItemView, 0)
	for rows.Next() {
		var it MarketingSendQueueItem
		var jobStatus, productID, accountsJSON string
		if err := rows.Scan(
			&it.ID, &it.JobID, &it.LeadID, &it.AccountID, &it.TargetMode, &it.Target, &it.Text,
			&it.Status, &it.NotBefore, &it.Attempts, &it.LastError, &it.SentAt, &it.CreatedAt, &it.UpdatedAt,
			&jobStatus, &productID, &accountsJSON,
		); err != nil {
			return nil, err
		}
		src := "marketing"
		if productID == "__bulk__" {
			src = "bulk"
		}
		out = append(out, SendQueueItemView{
			MarketingSendQueueItem: it,
			Source:                 src,
			JobStatus:              jobStatus,
			ProductID:              productID,
			AccountIDs:             decodeAccountIDs(accountsJSON),
		})
	}
	return out, rows.Err()
}

/**
 * 修改待发送项文案（仅 queued/waiting）。
 *
 * @param id 队列项 ID
 * @param text 新文案
 */
func (s *Store) UpdatePendingSendQueueText(id, text string) (*MarketingSendQueueItem, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("文案不能为空")
	}
	return s.UpdatePendingSendQueueItem(id, &text, nil, nil)
}

/**
 * 修改待发送项（文案 / 协议号 / 计划时间，仅 queued/waiting）。
 *
 * @param id 队列项 ID
 * @param text 新文案（nil=不改）
 * @param accountID 协议号（nil=不改；非 nil 可设空串清空绑定）
 * @param notBefore 计划发送时间毫秒（nil=不改）
 */
func (s *Store) UpdatePendingSendQueueItem(
	id string,
	text *string,
	accountID *string,
	notBefore *int64,
) (*MarketingSendQueueItem, error) {
	it, err := s.GetMarketingSendQueueItem(id)
	if err != nil {
		return nil, err
	}
	if it == nil {
		return nil, fmt.Errorf("队列项不存在")
	}
	if it.Status != "queued" && it.Status != "waiting" {
		return nil, fmt.Errorf("仅待发送（queued/waiting）可修改，当前=%s", it.Status)
	}
	if text != nil {
		t := strings.TrimSpace(*text)
		if t == "" {
			return nil, fmt.Errorf("文案不能为空")
		}
		it.Text = t
	}
	if accountID != nil {
		aid := strings.TrimSpace(*accountID)
		it.AccountID = aid
		if aid != "" {
			if err := s.EnsureAccountInSendJobPool(it.JobID, aid); err != nil {
				return nil, err
			}
		}
	}
	if notBefore != nil {
		nb := *notBefore
		if nb < 0 {
			nb = 0
		}
		it.NotBefore = nb
		// 定时后保持 waiting，避免与 queued 混淆
		if nb > time.Now().UnixMilli() {
			it.Status = "waiting"
		} else if it.Status == "waiting" && nb == 0 {
			it.Status = "queued"
		}
	}
	if err := s.UpdateMarketingSendQueueItem(*it); err != nil {
		return nil, err
	}
	return s.GetMarketingSendQueueItem(id)
}

/**
 * 确保协议号在发送任务号池中。
 *
 * @param jobID 任务 ID
 * @param accountID 协议号
 */
func (s *Store) EnsureAccountInSendJobPool(jobID, accountID string) error {
	job, err := s.GetMarketingSendJob(jobID)
	if err != nil {
		return err
	}
	if job == nil {
		return fmt.Errorf("任务不存在")
	}
	for _, id := range job.AccountIDs {
		if id == accountID {
			return nil
		}
	}
	job.AccountIDs = append(job.AccountIDs, accountID)
	return s.UpdateMarketingSendJob(*job)
}

/**
 * 删除待发送项（仅 queued/waiting）。
 *
 * @param id 队列项 ID
 */
func (s *Store) DeletePendingSendQueueItem(id string) error {
	it, err := s.GetMarketingSendQueueItem(id)
	if err != nil {
		return err
	}
	if it == nil {
		return fmt.Errorf("队列项不存在")
	}
	if it.Status != "queued" && it.Status != "waiting" {
		return fmt.Errorf("仅待发送（queued/waiting）可删除，当前=%s", it.Status)
	}
	_, err = s.DB.Exec(`DELETE FROM marketing_send_queue WHERE id=?`, id)
	if err != nil {
		return err
	}
	_ = s.MaybeCompleteMarketingSendJob(it.JobID)
	return nil
}

/**
 * 取消任务下未完成队列项。
 *
 * @param jobID 任务 ID
 */
func (s *Store) CancelPendingMarketingSendItems(jobID string) error {
	now := time.Now().Unix()
	_, err := s.DB.Exec(`
UPDATE marketing_send_queue SET status='cancelled', updated_at=?
WHERE job_id=? AND status IN ('queued','waiting','sending')`, now, jobID)
	return err
}

/**
 * 若任务无剩余可发项则标 completed。
 *
 * @param jobID 任务 ID
 */
func (s *Store) MaybeCompleteMarketingSendJob(jobID string) error {
	var n int
	err := s.DB.QueryRow(`
SELECT COUNT(*) FROM marketing_send_queue
WHERE job_id=? AND status IN ('queued','waiting','sending')`, jobID).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	job, err := s.GetMarketingSendJob(jobID)
	if err != nil || job == nil {
		return err
	}
	if job.Status == "cancelled" || job.Status == "paused" {
		return nil
	}
	job.Status = "completed"
	return s.UpdateMarketingSendJob(*job)
}

/**
 * 写入成功发送事件。
 *
 * @param ev 事件
 */
func (s *Store) InsertMarketingSendEvent(ev MarketingSendEvent) error {
	if ev.ID == "" {
		ev.ID = newMarketingID()
	}
	if ev.SentAt == 0 {
		ev.SentAt = time.Now().UnixMilli()
	}
	mutual := 0
	if ev.Mutual {
		mutual = 1
	}
	_, err := s.DB.Exec(`
INSERT INTO marketing_send_events (id, account_id, lead_id, target, mutual, sent_at)
VALUES (?,?,?,?,?,?)`, ev.ID, ev.AccountID, ev.LeadID, ev.Target, mutual, ev.SentAt)
	return err
}

/**
 * 统计某号在窗口内发送条数。
 *
 * @param accountID 协议号
 * @param sinceMs 窗口起点（毫秒）
 */
func (s *Store) CountMarketingSendEventsSince(accountID string, sinceMs int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`
SELECT COUNT(*) FROM marketing_send_events WHERE account_id=? AND sent_at>=?`, accountID, sinceMs).Scan(&n)
	return n, err
}

/**
 * 某号最近一次发送时间（毫秒）。
 *
 * @param accountID 协议号
 */
func (s *Store) LastMarketingSendAt(accountID string) (int64, error) {
	var ts sql.NullInt64
	err := s.DB.QueryRow(`
SELECT MAX(sent_at) FROM marketing_send_events WHERE account_id=?`, accountID).Scan(&ts)
	if err != nil {
		return 0, err
	}
	if !ts.Valid {
		return 0, nil
	}
	return ts.Int64, nil
}

/**
 * 某号当日非双向发送条数。
 *
 * @param accountID 协议号
 * @param dayStartMs 当日 0 点毫秒
 */
func (s *Store) CountNonMutualSendsSince(accountID string, dayStartMs int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`
SELECT COUNT(*) FROM marketing_send_events
WHERE account_id=? AND mutual=0 AND sent_at>=?`, accountID, dayStartMs).Scan(&n)
	return n, err
}

/**
 * 某目标最近一次被发送时间（毫秒）。
 *
 * @param target 目标 username/userId
 */
func (s *Store) LastMarketingSendToTarget(target string) (int64, error) {
	var ts sql.NullInt64
	err := s.DB.QueryRow(`
SELECT MAX(sent_at) FROM marketing_send_events WHERE target=?`, target).Scan(&ts)
	if err != nil {
		return 0, err
	}
	if !ts.Valid {
		return 0, nil
	}
	return ts.Int64, nil
}

/**
 * 查询协议号通讯录中某用户是否双向（未找到视为非双向）。
 *
 * @param accountID 协议号
 * @param userID Telegram user id
 * @return mutual, found
 */
func (s *Store) ContactMutual(accountID string, userID int64) (bool, bool, error) {
	var mutual int
	err := s.DB.QueryRow(`
SELECT mutual FROM contacts WHERE account_id=? AND user_id=?`, accountID, userID).Scan(&mutual)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return mutual == 1, true, nil
}
