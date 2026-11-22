package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Account 协议号本地记录。
type Account struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Phone       string  `json:"phone"`
	Username    string  `json:"username"`
	Status      string  `json:"status"`
	ProxyID     *string `json:"proxyId"`
	SessionPath string  `json:"sessionPath"`
	AppID       int     `json:"appId"`
	AppHash     string  `json:"appHash"`
	DeviceJSON  string  `json:"deviceJson"`
	CreatedAt   int64   `json:"createdAt"`
	UpdatedAt   int64   `json:"updatedAt"`
}

// Proxy 代理配置（按协议号绑定）。
type Proxy struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	PasswordEnc string `json:"-"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// Store 封装 SQLite 访问。
type Store struct {
	DB *sql.DB
}

/**
 * 打开数据目录下的 SQLite，并执行基础迁移。
 *
 * @param dataDir 数据根目录
 * @return Store 实例
 * @author Auto
 * @date 2026-10-08
 */
func Open(dataDir string) (*Store, error) {
	// 1. 确保数据目录存在
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	// 2. 打开数据库
	dbPath := filepath.Join(dataDir, "app.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// 允许多读；写仍串行。单连接时长事务会拖死全部 HTTP（表现为服务未连接）。
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0)

	// 3. 基础 PRAGMA：busy_timeout 避免写锁时其它查询永久阻塞
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pragma: %w", err)
	}

	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

/**
 * 关闭数据库连接。
 *
 * @return 错误
 * @author Auto
 * @date 2026-10-08
 */
func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

/**
 * 列出全部协议号，按更新时间倒序。
 *
 * @return 协议号列表
 * @return 错误
 * @author Auto
 * @date 2026-10-08
 */
func (s *Store) ListAccounts() ([]Account, error) {
	rows, err := s.DB.Query(`
SELECT id, label, phone, username, status, proxy_id, session_path,
  api_id, api_hash, device_json, created_at, updated_at
FROM accounts
ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	list := make([]Account, 0)
	for rows.Next() {
		var item Account
		var proxyID sql.NullString
		if err := rows.Scan(
			&item.ID, &item.Label, &item.Phone, &item.Username, &item.Status,
			&proxyID, &item.SessionPath, &item.AppID, &item.AppHash, &item.DeviceJSON,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		if proxyID.Valid {
			v := proxyID.String
			item.ProxyID = &v
		}
		list = append(list, item)
	}
	return list, rows.Err()
}

/**
 * 是否存在带 api_id/api_hash 的协议号（轻量 EXISTS，供 Configured 热路径）。
 *
 * @return 是否存在
 * @return 错误
 */
func (s *Store) HasAccountAPICredentials() (bool, error) {
	var n int
	err := s.DB.QueryRow(`
SELECT 1 FROM accounts
WHERE api_id > 0 AND IFNULL(api_hash,'') != ''
LIMIT 1`).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

/**
 * 按 ID 读取协议号。
 *
 * @param id 协议号 ID
 * @return 协议号
 * @return 是否存在
 * @return 错误
 * @author Auto
 * @date 2026-10-08
 */
func (s *Store) GetAccount(id string) (Account, bool, error) {
	var item Account
	var proxyID sql.NullString
	err := s.DB.QueryRow(`
SELECT id, label, phone, username, status, proxy_id, session_path,
  api_id, api_hash, device_json, created_at, updated_at
FROM accounts WHERE id = ?`, id).Scan(
		&item.ID, &item.Label, &item.Phone, &item.Username, &item.Status,
		&proxyID, &item.SessionPath, &item.AppID, &item.AppHash, &item.DeviceJSON,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return Account{}, false, nil
	}
	if err != nil {
		return Account{}, false, err
	}
	if proxyID.Valid {
		v := proxyID.String
		item.ProxyID = &v
	}
	return item, true, nil
}

/**
 * 写入或更新协议号。
 *
 * @param account 协议号
 * @return 错误
 * @author Auto
 * @date 2026-10-08
 */
func (s *Store) UpsertAccount(account Account) error {
	now := time.Now().Unix()
	if account.CreatedAt == 0 {
		account.CreatedAt = now
	}
	account.UpdatedAt = now

	var proxy any
	if account.ProxyID != nil {
		proxy = *account.ProxyID
	}

	_, err := s.DB.Exec(`
INSERT INTO accounts (id, label, phone, username, status, proxy_id, session_path,
  api_id, api_hash, device_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  label=excluded.label,
  phone=excluded.phone,
  username=excluded.username,
  status=excluded.status,
  proxy_id=excluded.proxy_id,
  session_path=excluded.session_path,
  api_id=excluded.api_id,
  api_hash=excluded.api_hash,
  device_json=excluded.device_json,
  updated_at=excluded.updated_at
`, account.ID, account.Label, account.Phone, account.Username, account.Status,
		proxy, account.SessionPath, account.AppID, account.AppHash, account.DeviceJSON,
		account.CreatedAt, account.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert account: %w", err)
	}
	return nil
}

/**
 * 按 ID 读取代理。
 *
 * @param id 代理 ID
 * @return 代理
 * @return 是否存在
 * @return 错误
 * @author Auto
 * @date 2026-10-08
 */
func (s *Store) GetProxy(id string) (Proxy, bool, error) {
	var item Proxy
	err := s.DB.QueryRow(`
SELECT id, name, protocol, host, port, username, password_enc, created_at, updated_at
FROM proxies WHERE id = ?`, id).Scan(
		&item.ID, &item.Name, &item.Protocol, &item.Host, &item.Port,
		&item.Username, &item.PasswordEnc, &item.CreatedAt, &item.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return Proxy{}, false, nil
	}
	if err != nil {
		return Proxy{}, false, err
	}
	return item, true, nil
}

/**
 * 删除协议号记录。
 *
 * @param id 协议号 ID
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) DeleteAccount(id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("delete account begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM contacts WHERE account_id = ?`, id); err != nil {
		return fmt.Errorf("delete account contacts: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM joined_groups WHERE account_id = ?`, id); err != nil {
		return fmt.Errorf("delete account joined_groups: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM entities WHERE account_id = ?`, id); err != nil {
		return fmt.Errorf("delete account entities: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM accounts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete account commit: %w", err)
	}
	return nil
}

// Contact 按协议号缓存的通讯录条目。
type Contact struct {
	AccountID string `json:"accountId"`
	UserID    int64  `json:"userId"`
	Username  string `json:"username"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Phone     string `json:"phone"`
	Mutual    bool   `json:"mutual"`
	IsBot     bool   `json:"isBot"`
	IsSelf    bool   `json:"isSelf"`
	Status    string `json:"status"`
	SyncedAt  int64  `json:"syncedAt"`
}

/**
 * 读取协议号本地通讯录缓存（不请求 Telegram）。
 *
 * @param accountID 协议号
 * @return 联系人列表
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) ListContacts(accountID string) ([]Contact, error) {
	rows, err := s.DB.Query(`
SELECT account_id, user_id, username, first_name, last_name, phone,
  mutual, is_bot, is_self, status, synced_at
FROM contacts
WHERE account_id = ?
ORDER BY lower(first_name), lower(last_name), user_id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list contacts: %w", err)
	}
	defer rows.Close()

	list := make([]Contact, 0)
	for rows.Next() {
		var item Contact
		var mutual, isBot, isSelf int
		if err := rows.Scan(
			&item.AccountID, &item.UserID, &item.Username, &item.FirstName, &item.LastName, &item.Phone,
			&mutual, &isBot, &isSelf, &item.Status, &item.SyncedAt,
		); err != nil {
			return nil, fmt.Errorf("scan contact: %w", err)
		}
		item.Mutual = mutual == 1
		item.IsBot = isBot == 1
		item.IsSelf = isSelf == 1
		list = append(list, item)
	}
	return list, rows.Err()
}

/**
 * 协议号通讯录最近一次同步时间（无缓存则为 0）。
 *
 * @param accountID 协议号
 * @return unix 秒
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) ContactsSyncedAt(accountID string) (int64, error) {
	var ts sql.NullInt64
	err := s.DB.QueryRow(`SELECT MAX(synced_at) FROM contacts WHERE account_id = ?`, accountID).Scan(&ts)
	if err != nil {
		return 0, fmt.Errorf("contacts synced_at: %w", err)
	}
	if !ts.Valid {
		return 0, nil
	}
	return ts.Int64, nil
}

/**
 * 全量替换某协议号的本地通讯录（刷新后写入）。
 *
 * @param accountID 协议号
 * @param items 联系人
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) ReplaceContacts(accountID string, items []Contact) error {
	now := time.Now().Unix()
	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("replace contacts begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM contacts WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("clear contacts: %w", err)
	}
	stmt, err := tx.Prepare(`
INSERT INTO contacts (
  account_id, user_id, username, first_name, last_name, phone,
  mutual, is_bot, is_self, status, synced_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare contact insert: %w", err)
	}
	defer stmt.Close()

	for _, item := range items {
		synced := item.SyncedAt
		if synced <= 0 {
			synced = now
		}
		mutual, isBot, isSelf := 0, 0, 0
		if item.Mutual {
			mutual = 1
		}
		if item.IsBot {
			isBot = 1
		}
		if item.IsSelf {
			isSelf = 1
		}
		if _, err := stmt.Exec(
			accountID, item.UserID, item.Username, item.FirstName, item.LastName, item.Phone,
			mutual, isBot, isSelf, item.Status, synced,
		); err != nil {
			return fmt.Errorf("insert contact: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace contacts commit: %w", err)
	}
	return nil
}

/**
 * 写入或更新单条本地通讯录（添加成功后局部更新，不打 TG）。
 *
 * @param item 联系人
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) UpsertContact(item Contact) error {
	if item.AccountID == "" || item.UserID == 0 {
		return fmt.Errorf("accountId and userId required")
	}
	if item.SyncedAt <= 0 {
		item.SyncedAt = time.Now().Unix()
	}
	mutual, isBot, isSelf := 0, 0, 0
	if item.Mutual {
		mutual = 1
	}
	if item.IsBot {
		isBot = 1
	}
	if item.IsSelf {
		isSelf = 1
	}
	_, err := s.DB.Exec(`
INSERT INTO contacts (
  account_id, user_id, username, first_name, last_name, phone,
  mutual, is_bot, is_self, status, synced_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id, user_id) DO UPDATE SET
  username=excluded.username,
  first_name=excluded.first_name,
  last_name=excluded.last_name,
  phone=excluded.phone,
  mutual=excluded.mutual,
  is_bot=excluded.is_bot,
  is_self=excluded.is_self,
  status=excluded.status,
  synced_at=excluded.synced_at`,
		item.AccountID, item.UserID, item.Username, item.FirstName, item.LastName, item.Phone,
		mutual, isBot, isSelf, item.Status, item.SyncedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert contact: %w", err)
	}
	return nil
}

/**
 * 删除某协议号下的单条本地通讯录。
 *
 * @param accountID 协议号
 * @param userID 用户 ID
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) DeleteContact(accountID string, userID int64) error {
	_, err := s.DB.Exec(`DELETE FROM contacts WHERE account_id = ? AND user_id = ?`, accountID, userID)
	if err != nil {
		return fmt.Errorf("delete contact: %w", err)
	}
	return nil
}

// JoinedGroup 按协议号缓存的已加入群/频道。
type JoinedGroup struct {
	AccountID      string `json:"accountId"`
	PeerID         int64  `json:"peerId"`
	AccessHash     int64  `json:"accessHash"`
	Title          string `json:"title"`
	Username       string `json:"username"`
	Kind           string `json:"kind"`
	Participants   int    `json:"participants"`
	IsCreator      bool   `json:"isCreator"`
	IsAdmin        bool   `json:"isAdmin"`
	JoinRequestOut bool   `json:"joinRequestOut"`
	SyncedAt       int64  `json:"syncedAt"`
}

/**
 * 读取协议号本地已加入群组缓存（不请求 Telegram）。
 *
 * @param accountID 协议号
 * @return 群组列表
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) ListJoinedGroups(accountID string) ([]JoinedGroup, error) {
	rows, err := s.DB.Query(`
SELECT account_id, peer_id, access_hash, title, username, kind, participants,
  is_creator, is_admin, join_request_out, synced_at
FROM joined_groups
WHERE account_id = ?
ORDER BY lower(title), peer_id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list joined_groups: %w", err)
	}
	defer rows.Close()

	list := make([]JoinedGroup, 0)
	for rows.Next() {
		var item JoinedGroup
		var isCreator, isAdmin, joinReq int
		if err := rows.Scan(
			&item.AccountID, &item.PeerID, &item.AccessHash, &item.Title, &item.Username, &item.Kind, &item.Participants,
			&isCreator, &isAdmin, &joinReq, &item.SyncedAt,
		); err != nil {
			return nil, fmt.Errorf("scan joined_group: %w", err)
		}
		item.IsCreator = isCreator == 1
		item.IsAdmin = isAdmin == 1
		item.JoinRequestOut = joinReq == 1
		list = append(list, item)
	}
	return list, rows.Err()
}

/**
 * 读取单条已加入群缓存。
 *
 * @param accountID 协议号
 * @param peerID 群 peer
 * @return 群；不存在时 nil
 */
func (s *Store) GetJoinedGroup(accountID string, peerID int64) (*JoinedGroup, error) {
	row := s.DB.QueryRow(`
SELECT account_id, peer_id, access_hash, title, username, kind, participants,
  is_creator, is_admin, join_request_out, synced_at
FROM joined_groups
WHERE account_id=? AND peer_id=?`, accountID, peerID)
	var item JoinedGroup
	var isCreator, isAdmin, joinReq int
	err := row.Scan(
		&item.AccountID, &item.PeerID, &item.AccessHash, &item.Title, &item.Username, &item.Kind, &item.Participants,
		&isCreator, &isAdmin, &joinReq, &item.SyncedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get joined_group: %w", err)
	}
	item.IsCreator = isCreator == 1
	item.IsAdmin = isAdmin == 1
	item.JoinRequestOut = joinReq == 1
	return &item, nil
}

/**
 * 协议号已加入群组最近一次同步时间。
 *
 * @param accountID 协议号
 * @return unix 秒
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) JoinedGroupsSyncedAt(accountID string) (int64, error) {
	var ts sql.NullInt64
	err := s.DB.QueryRow(`SELECT MAX(synced_at) FROM joined_groups WHERE account_id = ?`, accountID).Scan(&ts)
	if err != nil {
		return 0, fmt.Errorf("joined_groups synced_at: %w", err)
	}
	if !ts.Valid {
		return 0, nil
	}
	return ts.Int64, nil
}

/**
 * 全量替换某协议号的已加入群组缓存。
 *
 * @param accountID 协议号
 * @param items 群组
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) ReplaceJoinedGroups(accountID string, items []JoinedGroup) error {
	now := time.Now().Unix()
	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("replace joined_groups begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM joined_groups WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("clear joined_groups: %w", err)
	}
	stmt, err := tx.Prepare(`
INSERT INTO joined_groups (
  account_id, peer_id, access_hash, title, username, kind, participants,
  is_creator, is_admin, join_request_out, synced_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare joined_group insert: %w", err)
	}
	defer stmt.Close()

	for _, item := range items {
		synced := item.SyncedAt
		if synced <= 0 {
			synced = now
		}
		isCreator, isAdmin, joinReq := 0, 0, 0
		if item.IsCreator {
			isCreator = 1
		}
		if item.IsAdmin {
			isAdmin = 1
		}
		if item.JoinRequestOut {
			joinReq = 1
		}
		if _, err := stmt.Exec(
			accountID, item.PeerID, item.AccessHash, item.Title, item.Username, item.Kind, item.Participants,
			isCreator, isAdmin, joinReq, synced,
		); err != nil {
			return fmt.Errorf("insert joined_group: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace joined_groups commit: %w", err)
	}
	return nil
}

/**
 * 删除某协议号下单条已加入群缓存。
 *
 * @param accountID 协议号
 * @param peerID 群/频道 ID
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) DeleteJoinedGroup(accountID string, peerID int64) error {
	_, err := s.DB.Exec(`DELETE FROM joined_groups WHERE account_id = ? AND peer_id = ?`, accountID, peerID)
	if err != nil {
		return fmt.Errorf("delete joined_group: %w", err)
	}
	return nil
}

/**
 * 列出全部代理。
 *
 * @return 代理列表
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) ListProxies() ([]Proxy, error) {
	rows, err := s.DB.Query(`
SELECT id, name, protocol, host, port, username, password_enc, created_at, updated_at
FROM proxies
ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list proxies: %w", err)
	}
	defer rows.Close()

	list := make([]Proxy, 0)
	for rows.Next() {
		var item Proxy
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Protocol, &item.Host, &item.Port,
			&item.Username, &item.PasswordEnc, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan proxy: %w", err)
		}
		list = append(list, item)
	}
	return list, rows.Err()
}

/**
 * 写入或更新代理。
 *
 * @param proxy 代理
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) UpsertProxy(proxy Proxy) error {
	now := time.Now().Unix()
	if proxy.CreatedAt == 0 {
		proxy.CreatedAt = now
	}
	proxy.UpdatedAt = now
	if proxy.Protocol == "" {
		proxy.Protocol = "socks5"
	}

	_, err := s.DB.Exec(`
INSERT INTO proxies (id, name, protocol, host, port, username, password_enc, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name,
  protocol=excluded.protocol,
  host=excluded.host,
  port=excluded.port,
  username=excluded.username,
  password_enc=excluded.password_enc,
  updated_at=excluded.updated_at
`, proxy.ID, proxy.Name, proxy.Protocol, proxy.Host, proxy.Port,
		proxy.Username, proxy.PasswordEnc, proxy.CreatedAt, proxy.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert proxy: %w", err)
	}
	return nil
}

/**
 * 删除代理；已绑定该代理的协议号会清空 proxy_id。
 *
 * @param id 代理 ID
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) DeleteProxy(id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`UPDATE accounts SET proxy_id = NULL, updated_at = ? WHERE proxy_id = ?`, time.Now().Unix(), id); err != nil {
		return fmt.Errorf("clear account proxy: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM proxies WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete proxy: %w", err)
	}
	return tx.Commit()
}

/**
 * 更新协议号备注名。
 *
 * @param accountID 协议号 ID
 * @param label 新备注
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) UpdateAccountLabel(accountID, label string) error {
	acc, ok, err := s.GetAccount(accountID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("account not found")
	}
	acc.Label = strings.TrimSpace(label)
	if acc.Label == "" {
		acc.Label = accountID
	}
	return s.UpsertAccount(acc)
}

/**
 * 给协议号绑定或解绑代理。
 *
 * @param accountID 协议号 ID
 * @param proxyID 代理 ID（空字符串表示解绑）
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) BindAccountProxy(accountID, proxyID string) error {
	acc, ok, err := s.GetAccount(accountID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("account not found")
	}
	if proxyID == "" {
		acc.ProxyID = nil
	} else {
		if _, ok, err := s.GetProxy(proxyID); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("proxy not found")
		}
		acc.ProxyID = &proxyID
	}
	return s.UpsertAccount(acc)
}

// Entity 按协议号隔离的对端实体缓存。
type Entity struct {
	AccountID  string
	PeerID     int64
	AccessHash int64
	PeerType   string
	Username   string
	UpdatedAt  int64
}

/**
 * 写入实体缓存（按协议号隔离）。
 *
 * @param accountID 协议号 ID
 * @param peerID 对端 ID
 * @param accessHash access_hash
 * @param peerType 类型（user/channel/...）
 * @param username 用户名（可空）
 * @return 错误
 * @author Auto
 * @date 2026-10-08
 */
func (s *Store) UpsertEntity(accountID string, peerID, accessHash int64, peerType, username string) error {
	now := time.Now().Unix()
	_, err := s.DB.Exec(`
INSERT INTO entities (account_id, peer_id, access_hash, peer_type, username, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id, peer_id, peer_type) DO UPDATE SET
  access_hash=excluded.access_hash,
  username=excluded.username,
  updated_at=excluded.updated_at
`, accountID, peerID, accessHash, peerType, username, now)
	if err != nil {
		return fmt.Errorf("upsert entity: %w", err)
	}
	return nil
}

/**
 * 按协议号 + peer 查询实体缓存。
 *
 * @param accountID 协议号 ID
 * @param peerID 对端 ID
 * @param peerType 类型
 * @return 实体、是否存在、错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) GetEntity(accountID string, peerID int64, peerType string) (Entity, bool, error) {
	row := s.DB.QueryRow(`
SELECT account_id, peer_id, access_hash, peer_type, username, updated_at
FROM entities WHERE account_id=? AND peer_id=? AND peer_type=?
`, accountID, peerID, peerType)
	var e Entity
	err := row.Scan(&e.AccountID, &e.PeerID, &e.AccessHash, &e.PeerType, &e.Username, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return Entity{}, false, nil
	}
	if err != nil {
		return Entity{}, false, fmt.Errorf("get entity: %w", err)
	}
	return e, true, nil
}

/**
 * 按协议号 + 用户名查询实体缓存。
 *
 * @param accountID 协议号 ID
 * @param username 用户名（不含 @）
 * @return 实体、是否存在、错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) GetEntityByUsername(accountID, username string) (Entity, bool, error) {
	row := s.DB.QueryRow(`
SELECT account_id, peer_id, access_hash, peer_type, username, updated_at
FROM entities WHERE account_id=? AND lower(username)=lower(?) AND peer_type='user'
LIMIT 1
`, accountID, username)
	var e Entity
	err := row.Scan(&e.AccountID, &e.PeerID, &e.AccessHash, &e.PeerType, &e.Username, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return Entity{}, false, nil
	}
	if err != nil {
		return Entity{}, false, fmt.Errorf("get entity by username: %w", err)
	}
	return e, true, nil
}

// 任务状态常量（可恢复状态机）。
const (
	TaskPending     = "pending"
	TaskRunning     = "running"
	TaskWaiting     = "waiting"
	TaskSuccess     = "success"
	TaskFailed      = "failed"
	TaskCancelled   = "cancelled"
	TaskInterrupted = "interrupted"
	TaskDonePartial = "done_partial"
	TaskCircuitBreak = "circuit_break"
	// 兼容旧终态
	TaskDone = "done"
)

// 明细行状态。
const (
	ItemPending  = "pending"
	ItemInflight = "inflight"
	ItemOK       = "ok"
	ItemFailed   = "failed"
	ItemCancelled = "cancelled"
	ItemUnknown  = "unknown"
)

// Task 任务摘要。
type Task struct {
	ID          string
	Module      string
	Status      string
	Title       string
	PayloadJSON string
	CursorIndex int
	TotalCount  int
	WaitUntil   int64
	WaitReason  string
	CreatedAt   int64
	UpdatedAt   int64
	FinishedAt  int64
	ItemCount   int
}

// TaskItem 任务明细行。
type TaskItem struct {
	ID             string
	TaskID         string
	AccountID      string
	Target         string
	Status         string
	LocalResult    string
	APIResult      string
	BusinessResult string
	ErrorCode      string
	ErrorMessage   string
	CreatedAt      int64
	UpdatedAt      int64
}

/**
 * 将仍为 running/pending/waiting 的任务标记为 interrupted（保留 payload，可供续跑）。
 *
 * @return 受影响行数
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) MarkInterruptedRunning() (int64, error) {
	now := time.Now().Unix()
	// 崩溃时正在执行的明细标为 unknown，避免静默重发
	_, _ = s.DB.Exec(`
UPDATE task_items SET status=?, local_result='interrupted_inflight', updated_at=?
WHERE status=? AND task_id IN (
  SELECT id FROM tasks WHERE status IN ('running','pending','waiting')
)
`, ItemUnknown, now, ItemInflight)

	res, err := s.DB.Exec(`
UPDATE tasks SET status=?, updated_at=?, wait_until=0, wait_reason=''
WHERE status IN ('running','pending','waiting')
`, TaskInterrupted, now)
	if err != nil {
		return 0, fmt.Errorf("mark interrupted: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

/**
 * 创建可恢复任务（含 payload 与总数）。
 *
 * @param id 任务 ID
 * @param module 模块
 * @param title 标题
 * @param payloadJSON 续跑所需参数 JSON
 * @param total 计划条目数
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) CreateJobTask(id, module, title, payloadJSON string, total int) error {
	now := time.Now().Unix()
	_, err := s.DB.Exec(`
INSERT INTO tasks (
  id, module, status, title, payload_json, cursor_index, total_count,
  wait_until, wait_reason, created_at, updated_at, finished_at
) VALUES (?, ?, ?, ?, ?, 0, ?, 0, '', ?, ?, NULL)
`, id, module, TaskRunning, title, payloadJSON, total, now, now)
	if err != nil {
		return fmt.Errorf("create job task: %w", err)
	}
	return nil
}

/**
 * 创建运行中任务壳（异步队列用；无 payload，不可续跑）。
 *
 * @param id 任务 ID
 * @param module 模块
 * @param title 标题
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) CreateRunningTask(id, module, title string) error {
	return s.CreateJobTask(id, module, title, "", 0)
}

/**
 * 追加一条任务明细。
 *
 * @param item 明细
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) AppendTaskItem(item TaskItem) error {
	now := time.Now().Unix()
	id := item.ID
	if id == "" {
		id = fmt.Sprintf("%s-%d-%s", item.TaskID, now, item.Target)
	}
	status := item.Status
	if status == "" {
		status = ItemPending
	}
	_, err := s.DB.Exec(`
INSERT INTO task_items (
  id, task_id, account_id, target, status, local_result, api_result,
  business_result, error_code, error_message, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, id, item.TaskID, item.AccountID, item.Target, status, item.LocalResult, item.APIResult,
		item.BusinessResult, item.ErrorCode, item.ErrorMessage, now, now)
	if err != nil {
		return fmt.Errorf("append task item: %w", err)
	}
	_, _ = s.DB.Exec(`UPDATE tasks SET updated_at=? WHERE id=?`, now, item.TaskID)
	return nil
}

/**
 * 批量写入 pending 明细（任务创建时预置，供断点续跑）。
 *
 * @param taskID 任务 ID
 * @param items 明细（status 强制 pending）
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) SeedPendingItems(taskID string, items []TaskItem) error {
	if len(items) == 0 {
		return nil
	}
	now := time.Now().Unix()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, item := range items {
		id := item.ID
		if id == "" {
			id = fmt.Sprintf("%s-p-%d", taskID, i)
		}
		_, err = tx.Exec(`
INSERT INTO task_items (
  id, task_id, account_id, target, status, local_result, api_result,
  business_result, error_code, error_message, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, '', '', '', '', '', ?, ?)
`, id, taskID, item.AccountID, item.Target, ItemPending, now, now)
		if err != nil {
			return fmt.Errorf("seed pending item: %w", err)
		}
	}
	return tx.Commit()
}

/**
 * 更新明细行结果。
 *
 * @param id 明细 ID
 * @param status 状态
 * @param localResult 本地标记
 * @param apiResult API 结果
 * @param businessResult 业务结果
 * @param errorCode 错误码
 * @param errorMessage 错误信息
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) UpdateTaskItemResult(
	id, status, localResult, apiResult, businessResult, errorCode, errorMessage string,
) error {
	now := time.Now().Unix()
	_, err := s.DB.Exec(`
UPDATE task_items SET status=?, local_result=?, api_result=?, business_result=?,
  error_code=?, error_message=?, updated_at=? WHERE id=?
`, status, localResult, apiResult, businessResult, errorCode, errorMessage, now, id)
	if err != nil {
		return fmt.Errorf("update task item: %w", err)
	}
	return nil
}

/**
 * 将明细标为执行中（发出请求前写入，崩溃后续跑时变 unknown）。
 *
 * @param id 明细 ID
 * @return 错误
 */
func (s *Store) MarkTaskItemInflight(id string) error {
	now := time.Now().Unix()
	_, err := s.DB.Exec(`
UPDATE task_items SET status=?, local_result='started', updated_at=? WHERE id=?
`, ItemInflight, now, id)
	return err
}

/**
 * 将剩余 pending 明细标为 cancelled。
 *
 * @param taskID 任务 ID
 * @return 影响行数、错误
 */
func (s *Store) MarkRemainingItemsCancelled(taskID string) (int64, error) {
	now := time.Now().Unix()
	res, err := s.DB.Exec(`
UPDATE task_items SET status=?, local_result='cancelled', updated_at=?
WHERE task_id=? AND status=?
`, ItemCancelled, now, taskID, ItemPending)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

/**
 * 设置任务为 waiting（FloodWait）。
 *
 * @param id 任务 ID
 * @param waitUntil 等待结束 unix 秒
 * @param reason 原因
 * @return 错误
 */
func (s *Store) SetTaskWaiting(id string, waitUntil int64, reason string) error {
	now := time.Now().Unix()
	_, err := s.DB.Exec(`
UPDATE tasks SET status=?, wait_until=?, wait_reason=?, updated_at=?, finished_at=NULL WHERE id=?
`, TaskWaiting, waitUntil, reason, now, id)
	return err
}

/**
 * 从 waiting 恢复为 running。
 *
 * @param id 任务 ID
 * @return 错误
 */
func (s *Store) ClearTaskWaiting(id string) error {
	now := time.Now().Unix()
	_, err := s.DB.Exec(`
UPDATE tasks SET status=?, wait_until=0, wait_reason='', updated_at=? WHERE id=?
`, TaskRunning, now, id)
	return err
}

/**
 * 更新任务运行态（非终态）。
 *
 * @param id 任务 ID
 * @param status 状态
 * @param cursor 游标
 * @return 错误
 */
func (s *Store) SetTaskProgress(id, status string, cursor int) error {
	now := time.Now().Unix()
	_, err := s.DB.Exec(`
UPDATE tasks SET status=?, cursor_index=?, updated_at=?, finished_at=NULL WHERE id=?
`, status, cursor, now, id)
	return err
}

/**
 * 更新任务终态。
 *
 * @param id 任务 ID
 * @param status 状态
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) FinishTask(id, status string) error {
	now := time.Now().Unix()
	_, err := s.DB.Exec(`
UPDATE tasks SET status=?, updated_at=?, finished_at=?, wait_until=0, wait_reason='' WHERE id=?
`, status, now, now, id)
	if err != nil {
		return fmt.Errorf("finish task: %w", err)
	}
	return nil
}

/**
 * 任务是否可续跑（有 payload 且仍有 pending 明细）。
 *
 * @param t 任务
 * @return 是否可续跑
 */
func (t Task) Resumable() bool {
	if strings.TrimSpace(t.PayloadJSON) == "" {
		return false
	}
	switch t.Status {
	case TaskInterrupted, TaskWaiting, TaskRunning, TaskPending, TaskCircuitBreak:
		return true
	default:
		return false
	}
}

func scanTask(scanner interface {
	Scan(dest ...any) error
}) (Task, error) {
	var t Task
	err := scanner.Scan(
		&t.ID, &t.Module, &t.Status, &t.Title, &t.PayloadJSON, &t.CursorIndex, &t.TotalCount,
		&t.WaitUntil, &t.WaitReason, &t.CreatedAt, &t.UpdatedAt, &t.FinishedAt, &t.ItemCount,
	)
	return t, err
}

const taskSelectSQL = `
SELECT t.id, t.module, t.status, t.title, IFNULL(t.payload_json,''), IFNULL(t.cursor_index,0),
  IFNULL(t.total_count,0), IFNULL(t.wait_until,0), IFNULL(t.wait_reason,''),
  t.created_at, t.updated_at, IFNULL(t.finished_at,0),
  (SELECT COUNT(1) FROM task_items i WHERE i.task_id=t.id)
FROM tasks t`

/**
 * 按 ID 读取任务。
 *
 * @param id 任务 ID
 * @return 任务、是否存在、错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) GetTask(id string) (Task, bool, error) {
	row := s.DB.QueryRow(taskSelectSQL+` WHERE t.id=?`, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, err
	}
	return t, true, nil
}

/**
 * 创建已完成任务及其明细（最小任务中心落库）。
 *
 * @param id 任务 ID
 * @param module 模块名
 * @param title 标题
 * @param status 状态
 * @param items 明细
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) CreateFinishedTask(id, module, title, status string, items []TaskItem) error {
	now := time.Now().Unix()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(`
INSERT INTO tasks (id, module, status, title, created_at, updated_at, finished_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
`, id, module, status, title, now, now, now)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	for i, item := range items {
		itemID := item.ID
		if itemID == "" {
			itemID = fmt.Sprintf("%s-%d", id, i)
		}
		_, err = tx.Exec(`
INSERT INTO task_items (
  id, task_id, account_id, target, status, local_result, api_result,
  business_result, error_code, error_message, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, itemID, id, item.AccountID, item.Target, item.Status, item.LocalResult, item.APIResult,
			item.BusinessResult, item.ErrorCode, item.ErrorMessage, now, now)
		if err != nil {
			return fmt.Errorf("insert task item: %w", err)
		}
	}
	return tx.Commit()
}

/**
 * 列出最近任务。
 *
 * @param limit 条数上限
 * @return 任务列表
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) ListTasks(limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(taskSelectSQL+`
ORDER BY t.created_at DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	list := make([]Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

/**
 * 统计任务下某状态明细数量。
 *
 * @param taskID 任务 ID
 * @param status 明细状态
 * @return 数量
 */
func (s *Store) CountTaskItemsByStatus(taskID, status string) int {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(1) FROM task_items WHERE task_id=? AND status=?`, taskID, status).Scan(&n)
	return n
}

/**
 * 列出仍可执行的 pending 明细（按创建序）。
 *
 * @param taskID 任务 ID
 * @return 明细列表
 */
func (s *Store) ListPendingTaskItems(taskID string) ([]TaskItem, error) {
	rows, err := s.DB.Query(`
SELECT id, task_id, account_id, target, status, local_result, api_result,
  business_result, error_code, error_message, created_at, updated_at
FROM task_items WHERE task_id=? AND status=? ORDER BY created_at ASC
`, taskID, ItemPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]TaskItem, 0)
	for rows.Next() {
		var item TaskItem
		if err := rows.Scan(
			&item.ID, &item.TaskID, &item.AccountID, &item.Target, &item.Status,
			&item.LocalResult, &item.APIResult, &item.BusinessResult,
			&item.ErrorCode, &item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, rows.Err()
}

/**
 * 列出任务明细。
 *
 * @param taskID 任务 ID
 * @return 明细列表
 * @author Auto
 * @date 2026-10-09
 */
func (s *Store) ListTaskItems(taskID string) ([]TaskItem, error) {
	rows, err := s.DB.Query(`
SELECT id, task_id, account_id, target, status, local_result, api_result,
  business_result, error_code, error_message, created_at, updated_at
FROM task_items WHERE task_id=? ORDER BY created_at ASC
`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task items: %w", err)
	}
	defer rows.Close()
	list := make([]TaskItem, 0)
	for rows.Next() {
		var item TaskItem
		if err := rows.Scan(
			&item.ID, &item.TaskID, &item.AccountID, &item.Target, &item.Status,
			&item.LocalResult, &item.APIResult, &item.BusinessResult,
			&item.ErrorCode, &item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, rows.Err()
}

func (s *Store) migrate() error {
	ddl := `
CREATE TABLE IF NOT EXISTS accounts (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL DEFAULT '',
  phone TEXT NOT NULL DEFAULT '',
  username TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'unknown',
  proxy_id TEXT,
  session_path TEXT NOT NULL DEFAULT '',
  api_id INTEGER NOT NULL DEFAULT 0,
  api_hash TEXT NOT NULL DEFAULT '',
  device_json TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS proxies (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  protocol TEXT NOT NULL DEFAULT 'socks5',
  host TEXT NOT NULL,
  port INTEGER NOT NULL,
  username TEXT NOT NULL DEFAULT '',
  password_enc TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  module TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  title TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '',
  cursor_index INTEGER NOT NULL DEFAULT 0,
  total_count INTEGER NOT NULL DEFAULT 0,
  wait_until INTEGER NOT NULL DEFAULT 0,
  wait_reason TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  finished_at INTEGER
);

CREATE TABLE IF NOT EXISTS task_items (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  account_id TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  local_result TEXT NOT NULL DEFAULT '',
  api_result TEXT NOT NULL DEFAULT '',
  business_result TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT,
  account_id TEXT,
  level TEXT NOT NULL DEFAULT 'info',
  message TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS entities (
  account_id TEXT NOT NULL,
  peer_id INTEGER NOT NULL,
  access_hash INTEGER NOT NULL,
  peer_type TEXT NOT NULL DEFAULT 'user',
  username TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (account_id, peer_id, peer_type)
);

CREATE TABLE IF NOT EXISTS contacts (
  account_id TEXT NOT NULL,
  user_id INTEGER NOT NULL,
  username TEXT NOT NULL DEFAULT '',
  first_name TEXT NOT NULL DEFAULT '',
  last_name TEXT NOT NULL DEFAULT '',
  phone TEXT NOT NULL DEFAULT '',
  mutual INTEGER NOT NULL DEFAULT 0,
  is_bot INTEGER NOT NULL DEFAULT 0,
  is_self INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT '',
  synced_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (account_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_contacts_account ON contacts(account_id);

CREATE TABLE IF NOT EXISTS joined_groups (
  account_id TEXT NOT NULL,
  peer_id INTEGER NOT NULL,
  access_hash INTEGER NOT NULL DEFAULT 0,
  title TEXT NOT NULL DEFAULT '',
  username TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT '',
  participants INTEGER NOT NULL DEFAULT 0,
  is_creator INTEGER NOT NULL DEFAULT 0,
  is_admin INTEGER NOT NULL DEFAULT 0,
  join_request_out INTEGER NOT NULL DEFAULT 0,
  synced_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (account_id, peer_id)
);
CREATE INDEX IF NOT EXISTS idx_joined_groups_account ON joined_groups(account_id);

CREATE TABLE IF NOT EXISTS marketing_products (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  selling_points TEXT NOT NULL DEFAULT '',
  audience TEXT NOT NULL DEFAULT '',
  avoid_audience TEXT NOT NULL DEFAULT '',
  offer TEXT NOT NULL DEFAULT '',
  landing_url TEXT NOT NULL DEFAULT '',
  tone TEXT NOT NULL DEFAULT '',
  banned_words TEXT NOT NULL DEFAULT '',
  cta TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'draft',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS marketing_leads (
  id TEXT PRIMARY KEY,
  product_id TEXT NOT NULL,
  account_id TEXT NOT NULL,
  group_target TEXT NOT NULL DEFAULT '',
  group_title TEXT NOT NULL DEFAULT '',
  user_id INTEGER NOT NULL,
  username TEXT NOT NULL DEFAULT '',
  display_name TEXT NOT NULL DEFAULT '',
  digest TEXT NOT NULL DEFAULT '',
  fit_score INTEGER NOT NULL DEFAULT 0,
  fit_label TEXT NOT NULL DEFAULT '',
  fit_reasons TEXT NOT NULL DEFAULT '',
  risk_flags TEXT NOT NULL DEFAULT '',
  script_a TEXT NOT NULL DEFAULT '',
  script_b TEXT NOT NULL DEFAULT '',
  script_chosen TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  send_account_id TEXT NOT NULL DEFAULT '',
  cloud_call_ids TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE(product_id, account_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_marketing_leads_product ON marketing_leads(product_id);

CREATE TABLE IF NOT EXISTS marketing_send_policies (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  window_minutes INTEGER NOT NULL DEFAULT 60,
  max_per_window INTEGER NOT NULL DEFAULT 5,
  mutual_min_interval_ms INTEGER NOT NULL DEFAULT 3000,
  non_mutual_min_interval_ms INTEGER NOT NULL DEFAULT 600000,
  non_mutual_daily_cap INTEGER NOT NULL DEFAULT 20,
  same_target_cooldown_ms INTEGER NOT NULL DEFAULT 86400000,
  status TEXT NOT NULL DEFAULT 'active',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS marketing_send_jobs (
  id TEXT PRIMARY KEY,
  product_id TEXT NOT NULL DEFAULT '',
  policy_id TEXT NOT NULL DEFAULT '',
  account_ids_json TEXT NOT NULL DEFAULT '[]',
  status TEXT NOT NULL DEFAULT 'running',
  fail_stop_after INTEGER NOT NULL DEFAULT 5,
  consecutive_fails INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_marketing_send_jobs_status ON marketing_send_jobs(status);

CREATE TABLE IF NOT EXISTS marketing_send_queue (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  lead_id TEXT NOT NULL DEFAULT '',
  account_id TEXT NOT NULL DEFAULT '',
  target_mode TEXT NOT NULL DEFAULT 'username',
  target TEXT NOT NULL DEFAULT '',
  text TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'queued',
  not_before INTEGER NOT NULL DEFAULT 0,
  attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  sent_at INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_marketing_send_queue_job ON marketing_send_queue(job_id, status);
CREATE INDEX IF NOT EXISTS idx_marketing_send_queue_ready ON marketing_send_queue(status, not_before);

CREATE TABLE IF NOT EXISTS marketing_send_events (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  lead_id TEXT NOT NULL DEFAULT '',
  target TEXT NOT NULL DEFAULT '',
  mutual INTEGER NOT NULL DEFAULT 0,
  sent_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_marketing_send_events_account ON marketing_send_events(account_id, sent_at);
CREATE INDEX IF NOT EXISTS idx_marketing_send_events_target ON marketing_send_events(target, sent_at);
`
	ddl += groupMonitorDDL()
	if _, err := s.DB.Exec(ddl); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// 增量列：已有库忽略重复
	for _, q := range []string{
		`ALTER TABLE accounts ADD COLUMN api_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE accounts ADD COLUMN api_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE accounts ADD COLUMN device_json TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN payload_json TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN cursor_index INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN total_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN wait_until INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN wait_reason TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = s.DB.Exec(q)
	}
	return nil
}
