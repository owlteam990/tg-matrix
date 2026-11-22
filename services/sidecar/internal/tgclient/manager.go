package tgclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"golang.org/x/net/proxy"

	"github.com/tg-matrix/sidecar/internal/sessionimp"
	"github.com/tg-matrix/sidecar/internal/store"
)

// Manager 管理「一协议号一 gotd Client」。
type Manager struct {
	mu       sync.Mutex
	store    *store.Store
	dataDir  string
	appID    int
	appHash  string
	sessions map[string]*sessionRuntime

	// configured 缓存：避免调度器/健康检查每秒 ListAccounts 抢 SQLite 连接
	configuredOK   atomic.Bool
	configuredAt   atomic.Int64 // unix milli
}

type sessionRuntime struct {
	client *telegram.Client
	cancel context.CancelFunc
	ready  chan struct{}
	err    error
}

/**
 * 创建协议号客户端管理器。
 *
 * @param st 本地存储
 * @param dataDir 数据目录（session 文件落盘）
 * @param appID Telegram API ID（0 表示未配置，仅占位）
 * @param appHash Telegram API Hash
 * @return Manager
 * @author Auto
 * @date 2026-10-08
 */
func NewManager(st *store.Store, dataDir string, appID int, appHash string) *Manager {
	return &Manager{
		store:    st,
		dataDir:  dataDir,
		appID:    appID,
		appHash:  appHash,
		sessions: map[string]*sessionRuntime{},
	}
}

/**
 * 仅读缓存/环境变量，绝不查库（供 /health）。
 *
 * @return 是否可用
 */
func (m *Manager) ConfiguredCached() bool {
	if m.appID > 0 && m.appHash != "" {
		return true
	}
	return m.configuredOK.Load()
}

/**
 * 返回是否具备可用的 API 凭证（环境变量，或任一协议号自带 api_id/hash）。
 * 结果缓存约 30s，避免热路径反复查库。
 *
 * @return 是否可用
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) Configured() bool {
	if m.appID > 0 && m.appHash != "" {
		return true
	}
	now := time.Now().UnixMilli()
	if now-m.configuredAt.Load() < 30_000 {
		return m.configuredOK.Load()
	}
	ok := false
	if m.store != nil {
		if has, err := m.store.HasAccountAPICredentials(); err == nil {
			ok = has
		}
	}
	m.configuredOK.Store(ok)
	m.configuredAt.Store(now)
	return ok
}

/**
 * 关闭全部协议号 Client。
 *
 * @author Auto
 * @date 2026-10-08
 */
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rt := range m.sessions {
		if rt.cancel != nil {
			rt.cancel()
		}
		delete(m.sessions, id)
	}
}

/**
 * 使用指定协议号执行一次已授权 API 调用（一协议号一 Client）。
 *
 * @param ctx 上下文
 * @param accountID 协议号 ID
 * @param fn 回调，仅在 Client.Run 生命周期内有效
 * @return 错误
 * @author Auto
 * @date 2026-10-08
 */
func (m *Manager) WithAPI(ctx context.Context, accountID string, fn func(ctx context.Context, api *tg.Client) error) error {
	account, ok, err := m.store.GetAccount(accountID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("account not found: %s", accountID)
	}

	appID, appHash, err := m.credentialsFor(account)
	if err != nil {
		return err
	}

	opts, err := m.buildOptions(account)
	if err != nil {
		return err
	}

	client := telegram.NewClient(appID, appHash, opts)
	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("auth status: %w", err)
		}
		if !status.Authorized {
			return fmt.Errorf("account not authorized: %s", accountID)
		}
		return fn(ctx, client.API())
	})
}

/**
 * 解析协议号可用的 API 凭证：优先账号自身，其次环境变量。
 *
 * @param account 协议号
 * @return appID、appHash、错误
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) credentialsFor(account store.Account) (int, string, error) {
	if account.AppID > 0 && account.AppHash != "" {
		return account.AppID, account.AppHash, nil
	}
	if m.appID > 0 && m.appHash != "" {
		return m.appID, m.appHash, nil
	}
	return 0, "", fmt.Errorf("telegram api credentials missing（账号 JSON 无 api_id/api_hash，且未设置 TG_APP_ID / TG_APP_HASH）")
}

/**
 * 检查协议号会话是否已授权（F11 骨架）。
 *
 * @param ctx 上下文
 * @param accountID 协议号 ID
 * @return 自身用户信息（若可用）
 * @return 错误
 * @author Auto
 * @date 2026-10-08
 */
func (m *Manager) CheckAccount(ctx context.Context, accountID string) (*tg.User, error) {
	var self *tg.User
	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		u, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		if err != nil {
			return err
		}
		if len(u) == 0 {
			return fmt.Errorf("empty self user")
		}
		user, ok := u[0].(*tg.User)
		if !ok {
			return fmt.Errorf("unexpected self type")
		}
		self = user
		return nil
	})
	return self, err
}

func (m *Manager) buildOptions(account store.Account) (telegram.Options, error) {
	sessionDir := filepath.Join(m.dataDir, "sessions")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return telegram.Options{}, err
	}

	sessionFile := account.SessionPath
	if sessionFile == "" {
		sessionFile = filepath.Join(sessionDir, account.ID+".session.json")
	}

	opts := telegram.Options{
		SessionStorage: &telegram.FileSessionStorage{Path: sessionFile},
	}

	// 使用导入 JSON 中的设备指纹，降低与原端不一致风险
	if account.DeviceJSON != "" {
		var dev sessionimp.DeviceProfile
		if err := json.Unmarshal([]byte(account.DeviceJSON), &dev); err == nil {
			opts.Device = telegram.DeviceConfig{
				DeviceModel:    firstNonEmpty(dev.DeviceModel, "iPhone"),
				SystemVersion:  firstNonEmpty(dev.SystemVersion, "17.0"),
				AppVersion:     firstNonEmpty(dev.AppVersion, "10.0"),
				LangCode:       firstNonEmpty(dev.LangCode, "en"),
				LangPack:       dev.LangPack,
				SystemLangCode: firstNonEmpty(dev.SystemLangCode, "en"),
			}
		}
	}

	// 按协议号绑定代理
	if account.ProxyID != nil && *account.ProxyID != "" {
		p, ok, err := m.store.GetProxy(*account.ProxyID)
		if err != nil {
			return telegram.Options{}, err
		}
		if !ok {
			return telegram.Options{}, fmt.Errorf("proxy not found: %s", *account.ProxyID)
		}
		resolver, err := socksResolver(p)
		if err != nil {
			return telegram.Options{}, err
		}
		opts.Resolver = resolver
	}

	return opts, nil
}

func socksResolver(p store.Proxy) (dcs.Resolver, error) {
	proto := p.Protocol
	if proto == "" {
		proto = "socks5"
	}
	u := &url.URL{
		Scheme: proto,
		Host:   net.JoinHostPort(p.Host, fmt.Sprintf("%d", p.Port)),
	}
	if p.Username != "" {
		u.User = url.UserPassword(p.Username, p.PasswordEnc)
	}

	dialer, err := proxy.FromURL(u, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("proxy dialer: %w", err)
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("proxy does not support context dialer")
	}

	return dcs.Plain(dcs.PlainOptions{
		Dial: contextDialer.DialContext,
	}), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
