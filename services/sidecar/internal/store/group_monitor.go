package store

import (
	"database/sql"
	"time"
)

// 群监控状态
const (
	GroupMonitorBackfilling = "backfilling"
	GroupMonitorMonitoring  = "monitoring"
	GroupMonitorStopped     = "stopped"
	GroupMonitorError       = "error"
)

// GroupMonitor 单个群的监控配置与状态。
type GroupMonitor struct {
	AccountID     string `json:"accountId"`
	PeerID        int64  `json:"peerId"`
	AccessHash    int64  `json:"accessHash"`
	PeerType      string `json:"peerType"` // channel | chat
	Title         string `json:"title"`
	Status        string `json:"status"`
	HistoryLimit  int    `json:"historyLimit"`
	LastMsgID     int    `json:"lastMsgId"`
	MsgCount      int    `json:"msgCount"`
	BackfillDone  int    `json:"backfillDone"`
	BackfillTotal int    `json:"backfillTotal"`
	ErrorMessage  string `json:"errorMessage"`
	StartedAt     int64  `json:"startedAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// GroupMessage 入库的群消息。
type GroupMessage struct {
	AccountID string `json:"accountId"`
	PeerID    int64  `json:"peerId"`
	MsgID     int    `json:"msgId"`
	Date      int    `json:"date"`
	FromID    int64  `json:"fromId"`
	FromName  string `json:"fromName"`
	Out       bool   `json:"out"`
	Text      string `json:"text"`
	Media     string `json:"media"`
	CreatedAt int64  `json:"createdAt"`
}

/**
 * 插入或更新监控配置（开启/重开监控时调用）。
 *
 * @param m 监控记录
 */
func (s *Store) UpsertGroupMonitor(m GroupMonitor) error {
	now := time.Now().Unix()
	if m.StartedAt <= 0 {
		m.StartedAt = now
	}
	m.UpdatedAt = now
	if m.Status == "" {
		m.Status = GroupMonitorBackfilling
	}
	_, err := s.DB.Exec(`
INSERT INTO group_monitors (
  account_id, peer_id, access_hash, peer_type, title, status,
  history_limit, last_msg_id, msg_count, backfill_done, backfill_total,
  error_message, started_at, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(account_id, peer_id) DO UPDATE SET
  access_hash=excluded.access_hash,
  peer_type=excluded.peer_type,
  title=excluded.title,
  status=excluded.status,
  history_limit=excluded.history_limit,
  backfill_done=excluded.backfill_done,
  backfill_total=excluded.backfill_total,
  error_message=excluded.error_message,
  started_at=excluded.started_at,
  updated_at=excluded.updated_at`,
		m.AccountID, m.PeerID, m.AccessHash, m.PeerType, m.Title, m.Status,
		m.HistoryLimit, m.LastMsgID, m.MsgCount, m.BackfillDone, m.BackfillTotal,
		m.ErrorMessage, m.StartedAt, m.UpdatedAt,
	)
	return err
}

/**
 * 按账号列出监控（可空=全部账号）。
 *
 * @param accountID 协议号，空则全部
 */
func (s *Store) ListGroupMonitors(accountID string) ([]GroupMonitor, error) {
	q := `
SELECT account_id, peer_id, access_hash, peer_type, title, status,
       history_limit, last_msg_id, msg_count, backfill_done, backfill_total,
       error_message, started_at, updated_at
FROM group_monitors`
	args := []any{}
	if accountID != "" {
		q += ` WHERE account_id=?`
		args = append(args, accountID)
	}
	q += ` ORDER BY updated_at DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]GroupMonitor, 0)
	for rows.Next() {
		m, err := scanGroupMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

/**
 * 列出指定状态的监控（后台轮询用）。
 *
 * @param status 状态
 */
func (s *Store) ListGroupMonitorsByStatus(status string) ([]GroupMonitor, error) {
	rows, err := s.DB.Query(`
SELECT account_id, peer_id, access_hash, peer_type, title, status,
       history_limit, last_msg_id, msg_count, backfill_done, backfill_total,
       error_message, started_at, updated_at
FROM group_monitors WHERE status=?
ORDER BY updated_at ASC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]GroupMonitor, 0)
	for rows.Next() {
		m, err := scanGroupMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

/**
 * 取单条监控。
 *
 * @param accountID 协议号
 * @param peerID 群 peer
 */
func (s *Store) GetGroupMonitor(accountID string, peerID int64) (*GroupMonitor, error) {
	row := s.DB.QueryRow(`
SELECT account_id, peer_id, access_hash, peer_type, title, status,
       history_limit, last_msg_id, msg_count, backfill_done, backfill_total,
       error_message, started_at, updated_at
FROM group_monitors WHERE account_id=? AND peer_id=?`, accountID, peerID)
	m, err := scanGroupMonitor(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

/**
 * 更新监控状态与错误信息。
 *
 * @param accountID 协议号
 * @param peerID 群
 * @param status 状态
 * @param errMsg 错误（可空）
 */
func (s *Store) SetGroupMonitorStatus(accountID string, peerID int64, status, errMsg string) error {
	_, err := s.DB.Exec(`
UPDATE group_monitors SET status=?, error_message=?, updated_at=?
WHERE account_id=? AND peer_id=?`,
		status, errMsg, time.Now().Unix(), accountID, peerID,
	)
	return err
}

/**
 * 更新回填进度。
 *
 * @param accountID 协议号
 * @param peerID 群
 * @param done 已拉条数
 * @param total 目标条数
 */
func (s *Store) UpdateGroupMonitorBackfill(accountID string, peerID int64, done, total int) error {
	_, err := s.DB.Exec(`
UPDATE group_monitors SET backfill_done=?, backfill_total=?, updated_at=?
WHERE account_id=? AND peer_id=?`,
		done, total, time.Now().Unix(), accountID, peerID,
	)
	return err
}

/**
 * 更新 last_msg_id 与消息计数。
 *
 * @param accountID 协议号
 * @param peerID 群
 * @param lastMsgID 最新消息 id
 * @param msgCount 总条数
 */
func (s *Store) UpdateGroupMonitorProgress(accountID string, peerID int64, lastMsgID, msgCount int) error {
	_, err := s.DB.Exec(`
UPDATE group_monitors SET last_msg_id=?, msg_count=?, updated_at=?
WHERE account_id=? AND peer_id=?`,
		lastMsgID, msgCount, time.Now().Unix(), accountID, peerID,
	)
	return err
}

/**
 * 批量插入消息（忽略主键冲突）。
 *
 * @param items 消息列表
 * @return 新插入条数
 */
func (s *Store) InsertGroupMessagesIgnoreDup(items []GroupMessage) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
INSERT OR IGNORE INTO group_messages (
  account_id, peer_id, msg_id, date, from_id, from_name, out, text, media, created_at
) VALUES (?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	inserted := 0
	for _, it := range items {
		created := it.CreatedAt
		if created <= 0 {
			created = now
		}
		outInt := 0
		if it.Out {
			outInt = 1
		}
		res, err := stmt.Exec(
			it.AccountID, it.PeerID, it.MsgID, it.Date, it.FromID, it.FromName,
			outInt, it.Text, it.Media, created,
		)
		if err != nil {
			return inserted, err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			inserted++
		}
	}
	if err := tx.Commit(); err != nil {
		return inserted, err
	}
	return inserted, nil
}

/**
 * 统计某群已存消息数。
 *
 * @param accountID 协议号
 * @param peerID 群
 */
func (s *Store) CountGroupMessages(accountID string, peerID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`
SELECT COUNT(*) FROM group_messages WHERE account_id=? AND peer_id=?`,
		accountID, peerID,
	).Scan(&n)
	return n, err
}

/**
 * 取某群最大消息 id。
 *
 * @param accountID 协议号
 * @param peerID 群
 */
func (s *Store) MaxGroupMessageID(accountID string, peerID int64) (int, error) {
	var id sql.NullInt64
	err := s.DB.QueryRow(`
SELECT MAX(msg_id) FROM group_messages WHERE account_id=? AND peer_id=?`,
		accountID, peerID,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	if !id.Valid {
		return 0, nil
	}
	return int(id.Int64), nil
}

/**
 * 按 msg_id 升序分页读取消息（导出用）。先收集再关 rows，避免嵌套查询死锁。
 *
 * @param accountID 协议号
 * @param peerID 群
 * @param offset 偏移
 * @param limit 条数
 */
func (s *Store) ListGroupMessagesPage(accountID string, peerID int64, offset, limit int) ([]GroupMessage, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.DB.Query(`
SELECT account_id, peer_id, msg_id, date, from_id, from_name, out, text, media, created_at
FROM group_messages
WHERE account_id=? AND peer_id=?
ORDER BY msg_id ASC
LIMIT ? OFFSET ?`, accountID, peerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]GroupMessage, 0, limit)
	for rows.Next() {
		var m GroupMessage
		var outInt int
		if err := rows.Scan(
			&m.AccountID, &m.PeerID, &m.MsgID, &m.Date, &m.FromID, &m.FromName,
			&outInt, &m.Text, &m.Media, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		m.Out = outInt != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanGroupMonitor(row interface {
	Scan(dest ...any) error
}) (GroupMonitor, error) {
	var m GroupMonitor
	err := row.Scan(
		&m.AccountID, &m.PeerID, &m.AccessHash, &m.PeerType, &m.Title, &m.Status,
		&m.HistoryLimit, &m.LastMsgID, &m.MsgCount, &m.BackfillDone, &m.BackfillTotal,
		&m.ErrorMessage, &m.StartedAt, &m.UpdatedAt,
	)
	if err != nil {
		return m, err
	}
	return m, nil
}

/**
 * 刷新某群 msg_count / last_msg_id（插入后调用）。
 *
 * @param accountID 协议号
 * @param peerID 群
 */
func (s *Store) RefreshGroupMonitorCounts(accountID string, peerID int64) error {
	n, err := s.CountGroupMessages(accountID, peerID)
	if err != nil {
		return err
	}
	maxID, err := s.MaxGroupMessageID(accountID, peerID)
	if err != nil {
		return err
	}
	return s.UpdateGroupMonitorProgress(accountID, peerID, maxID, n)
}

/**
 * 确保导出目录相关表已存在（migrate 内调用）。
 */
func groupMonitorDDL() string {
	return `
CREATE TABLE IF NOT EXISTS group_monitors (
  account_id TEXT NOT NULL,
  peer_id INTEGER NOT NULL,
  access_hash INTEGER NOT NULL DEFAULT 0,
  peer_type TEXT NOT NULL DEFAULT 'channel',
  title TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'stopped',
  history_limit INTEGER NOT NULL DEFAULT 100,
  last_msg_id INTEGER NOT NULL DEFAULT 0,
  msg_count INTEGER NOT NULL DEFAULT 0,
  backfill_done INTEGER NOT NULL DEFAULT 0,
  backfill_total INTEGER NOT NULL DEFAULT 0,
  error_message TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (account_id, peer_id)
);
CREATE INDEX IF NOT EXISTS idx_group_monitors_status ON group_monitors(status);

CREATE TABLE IF NOT EXISTS group_messages (
  account_id TEXT NOT NULL,
  peer_id INTEGER NOT NULL,
  msg_id INTEGER NOT NULL,
  date INTEGER NOT NULL DEFAULT 0,
  from_id INTEGER NOT NULL DEFAULT 0,
  from_name TEXT NOT NULL DEFAULT '',
  out INTEGER NOT NULL DEFAULT 0,
  text TEXT NOT NULL DEFAULT '',
  media TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (account_id, peer_id, msg_id)
);
CREATE INDEX IF NOT EXISTS idx_group_messages_peer_date ON group_messages(account_id, peer_id, date);
`
}
