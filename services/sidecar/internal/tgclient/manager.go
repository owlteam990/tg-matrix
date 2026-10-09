package tgclient

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"golang.org/x/net/proxy"

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
 * 返回 gotd 是否已配置 API 凭证。
 *
 * @return 是否可用
 * @author Auto
 * @date 2026-10-08
 */
func (m *Manager) Configured() bool {
	return m.appID > 0 && m.appHash != ""
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
	if !m.Configured() {
		return fmt.Errorf("telegram api credentials not configured (set TG_APP_ID / TG_APP_HASH)")
	}

	account, ok, err := m.store.GetAccount(accountID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("account not found: %s", accountID)
	}

	opts, err := m.buildOptions(account)
	if err != nil {
		return err
	}

	client := telegram.NewClient(m.appID, m.appHash, opts)
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
