package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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
	db.SetMaxOpenConns(1)

	// 3. 基础 PRAGMA
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
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
SELECT id, label, phone, username, status, proxy_id, session_path, created_at, updated_at
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
			&proxyID, &item.SessionPath, &item.CreatedAt, &item.UpdatedAt,
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
SELECT id, label, phone, username, status, proxy_id, session_path, created_at, updated_at
FROM accounts WHERE id = ?`, id).Scan(
		&item.ID, &item.Label, &item.Phone, &item.Username, &item.Status,
		&proxyID, &item.SessionPath, &item.CreatedAt, &item.UpdatedAt,
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
INSERT INTO accounts (id, label, phone, username, status, proxy_id, session_path, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  label=excluded.label,
  phone=excluded.phone,
  username=excluded.username,
  status=excluded.status,
  proxy_id=excluded.proxy_id,
  session_path=excluded.session_path,
  updated_at=excluded.updated_at
`, account.ID, account.Label, account.Phone, account.Username, account.Status,
		proxy, account.SessionPath, account.CreatedAt, account.UpdatedAt)
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

func (s *Store) migrate() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS accounts (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL DEFAULT '',
  phone TEXT NOT NULL DEFAULT '',
  username TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'unknown',
  proxy_id TEXT,
  session_path TEXT NOT NULL DEFAULT '',
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
`
	if _, err := s.DB.Exec(ddl); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
