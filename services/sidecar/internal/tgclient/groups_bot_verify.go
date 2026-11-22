package tgclient

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

// BotDeepLinkResult 协议号与验证机器人 deep link 交互结果。
type BotDeepLinkResult struct {
	AccountID      string          `json:"accountId"`
	OK             bool            `json:"ok"`
	BusinessResult string          `json:"businessResult"`
	BotUsername    string          `json:"botUsername,omitempty"`
	BotID          int64           `json:"botId,omitempty"`
	BotAccessHash  string          `json:"botAccessHash,omitempty"`
	StartParam     string          `json:"startParam,omitempty"`
	ReplyText      string          `json:"replyText,omitempty"`
	Challenges     []JoinChallenge `json:"challenges,omitempty"`
	ErrorCode      string          `json:"errorCode,omitempty"`
	ErrorMessage   string          `json:"errorMessage,omitempty"`
	Note           string          `json:"note,omitempty"`
}

var (
	reTMeBotPath     = regexp.MustCompile(`(?i)^/?([A-Za-z0-9_]{3,64})/?$`)
	reTMeMiniAppPath = regexp.MustCompile(`(?i)^/?([A-Za-z0-9_]{3,64})/([A-Za-z0-9_]{1,64})/?$`)
)

/**
 * 解析 t.me / telegram.me / tg:// 机器人 deep link（含 start 参数）。
 *
 * @param raw 按钮 URL，如 https://telegram.me/nmnmfunbot?start=joinverify_-1001356915330
 * @return 机器人用户名、start 参数、是否为 bot deep link
 * @author Auto
 * @date 2026-10-09
 */
func ParseBotStartDeepLink(raw string) (username, startParam string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	// 兼容无 scheme
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "t.me/") || strings.HasPrefix(lower, "telegram.me/") || strings.HasPrefix(lower, "telegram.dog/") {
		raw = "https://" + raw
		lower = strings.ToLower(raw)
	}

	if strings.HasPrefix(lower, "tg://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", false
		}
		q := u.Query()
		username = strings.TrimSpace(q.Get("domain"))
		if username == "" {
			username = strings.TrimSpace(q.Get("username"))
		}
		startParam = strings.TrimSpace(firstNonEmpty(q.Get("start"), q.Get("startapp")))
		username = strings.TrimPrefix(username, "@")
		if username == "" || startParam == "" {
			return "", "", false
		}
		return username, startParam, true
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", "", false
	}
	host := strings.ToLower(u.Hostname())
	if host != "t.me" && host != "telegram.me" && host != "telegram.dog" {
		return "", "", false
	}
	q := u.Query()
	startParam = strings.TrimSpace(firstNonEmpty(q.Get("start"), q.Get("startapp")))
	if startParam == "" {
		return "", "", false
	}
	path := strings.TrimSpace(u.Path)
	m := reTMeBotPath.FindStringSubmatch(path)
	if len(m) < 2 {
		return "", "", false
	}
	username = strings.TrimPrefix(m[1], "@")
	// 排除邀请链接、代理等路径
	switch strings.ToLower(username) {
	case "joinchat", "addstickers", "share", "proxy", "socks", "setlanguage", "c", "s":
		return "", "", false
	}
	if strings.HasPrefix(username, "+") {
		return "", "", false
	}
	return username, startParam, true
}

/**
 * 解析「协议号应加入」的公开频道/群 / 邀请链接。
 * 例如 https://t.me/pionexcn、t.me/+hash；排除 bot?start= 与 Mini App。
 *
 * @param raw 按钮或粘贴的 URL
 * @return 可供 JoinGroup 的 target、是否匹配
 */
func ParsePublicJoinLink(raw string) (target string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if _, _, isBot := ParseBotStartDeepLink(raw); isBot {
		return "", false
	}
	if _, _, _, isMini := ParseMiniAppDeepLink(raw); isMini {
		return "", false
	}

	lower := strings.ToLower(raw)
	isTG := strings.HasPrefix(lower, "https://t.me/") ||
		strings.HasPrefix(lower, "http://t.me/") ||
		strings.HasPrefix(lower, "t.me/") ||
		strings.HasPrefix(lower, "https://telegram.me/") ||
		strings.HasPrefix(lower, "http://telegram.me/") ||
		strings.HasPrefix(lower, "telegram.me/") ||
		strings.HasPrefix(lower, "https://telegram.dog/") ||
		strings.HasPrefix(lower, "http://telegram.dog/") ||
		strings.HasPrefix(lower, "telegram.dog/") ||
		strings.HasPrefix(lower, "tg://") ||
		strings.HasPrefix(raw, "@")
	if !isTG {
		return "", false
	}

	// 邀请链接优先
	if hash, okInvite := extractInviteHash(raw); okInvite {
		return "https://t.me/+" + hash, true
	}

	if strings.HasPrefix(lower, "tg://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false
		}
		q := u.Query()
		domain := strings.TrimPrefix(strings.TrimSpace(firstNonEmpty(q.Get("domain"), q.Get("username"))), "@")
		if domain == "" {
			return "", false
		}
		switch strings.ToLower(domain) {
		case "joinchat", "addstickers", "share", "proxy", "socks", "setlanguage", "c", "s", "iv":
			return "", false
		}
		return "@" + domain, true
	}

	username := extractPublicUsername(raw)
	if username == "" {
		return "", false
	}
	switch strings.ToLower(username) {
	case "joinchat", "addstickers", "share", "proxy", "socks", "setlanguage", "c", "s", "iv":
		return "", false
	}
	return "@" + username, true
}

/**
 * 解析 Mini App 直链：t.me/{bot}/{short_name}?startapp=xxx。
 *
 * @param raw 如 https://telegram.me/nmnmfunbot/panel?startapp=L3dlYi12...
 * @return 机器人用户名、小程序 short_name、start_param、是否匹配
 * @author Auto
 * @date 2026-10-09
 */
func ParseMiniAppDeepLink(raw string) (username, shortName, startParam string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", false
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "t.me/") || strings.HasPrefix(lower, "telegram.me/") || strings.HasPrefix(lower, "telegram.dog/") {
		raw = "https://" + raw
		lower = strings.ToLower(raw)
	}

	if strings.HasPrefix(lower, "tg://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", "", false
		}
		q := u.Query()
		username = strings.TrimPrefix(strings.TrimSpace(firstNonEmpty(q.Get("domain"), q.Get("username"))), "@")
		shortName = strings.TrimSpace(firstNonEmpty(q.Get("appname"), q.Get("shortname"), q.Get("short_name")))
		startParam = strings.TrimSpace(q.Get("startapp"))
		if username == "" || shortName == "" {
			return "", "", "", false
		}
		return username, shortName, startParam, true
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", "", "", false
	}
	host := strings.ToLower(u.Hostname())
	if host != "t.me" && host != "telegram.me" && host != "telegram.dog" {
		return "", "", "", false
	}
	m := reTMeMiniAppPath.FindStringSubmatch(strings.TrimSpace(u.Path))
	if len(m) < 3 {
		return "", "", "", false
	}
	username = strings.TrimPrefix(m[1], "@")
	shortName = m[2]
	switch strings.ToLower(username) {
	case "joinchat", "addstickers", "share", "proxy", "socks", "setlanguage", "c", "s":
		return "", "", "", false
	}
	switch strings.ToLower(shortName) {
	case "joinchat", "addstickers", "share", "proxy", "socks":
		return "", "", "", false
	}
	startParam = strings.TrimSpace(u.Query().Get("startapp"))
	return username, shortName, startParam, true
}

/**
 * 协议号打开 Mini App 直链（getBotApp + requestAppWebView），返回带登录态的 URL。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param deepLink Mini App 链接
 * @return 可在浏览器打开的带登录态地址
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) OpenMiniAppDeepLink(ctx context.Context, accountID, deepLink string) ChallengeWebViewResult {
	out := ChallengeWebViewResult{AccountID: accountID, Mode: "mini_app"}
	username, shortName, startParam, ok := ParseMiniAppDeepLink(deepLink)
	if !ok {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "not_miniapp_link"
		out.ErrorMessage = "不是 Mini App 直链（需 t.me/bot/app?startapp=）"
		return out
	}
	out.BotUsername = username
	out.StartParam = startParam
	out.URL = strings.TrimSpace(deepLink)

	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
		if err != nil {
			biz, code, msg, _ := classifyContactErr(err)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			return nil
		}
		user, okUser := findUserFromResolved(resolved)
		if !okUser || user == nil {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "bot_not_found"
			out.ErrorMessage = "无法解析机器人 @" + username
			return nil
		}
		if !user.Bot {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "not_a_bot"
			out.ErrorMessage = "@" + username + " 不是机器人"
			return nil
		}
		ensureUserAccessHash(user)
		_ = m.store.UpsertEntity(accountID, user.ID, user.AccessHash, "user", user.Username)
		out.PeerID = user.ID

		botInput := &tg.InputUser{UserID: user.ID, AccessHash: user.AccessHash}
		peer := &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
		appInfo, err := api.MessagesGetBotApp(ctx, &tg.MessagesGetBotAppRequest{
			App: &tg.InputBotAppShortName{
				BotID:     botInput,
				ShortName: shortName,
			},
		})
		if err != nil {
			// getBotApp 失败时仍尝试 webview 回退（部分验证链不是正式 Bot App）
			if fallbackURL, note, okFB := m.openBotWebViewFallback(ctx, api, botInput, peer, deepLink, startParam); okFB {
				out.OK = true
				out.BusinessResult = ResultOK
				out.Mode = "webview"
				out.URL = fallbackURL
				out.Note = note
				return nil
			}
			biz, code, msg, _ := classifyGroupErr(err, true)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			return nil
		}
		botApp, okApp := appInfo.App.(*tg.BotApp)
		if !okApp || botApp == nil {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "bot_app_invalid"
			out.ErrorMessage = "未获取到 Mini App 信息"
			return nil
		}
		// 注意：gotd 的 BotApp.AsInputBotAppID 会误把 AccessHash 写成 Hash（缓存字段），导致 BOT_APP_INVALID
		appInput := &tg.InputBotAppID{
			ID:         botApp.ID,
			AccessHash: botApp.AccessHash,
		}
		if appInput.ID == 0 || appInput.AccessHash == 0 {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "bot_app_invalid"
			out.ErrorMessage = "Mini App id/access_hash 无效"
			return nil
		}
		req := &tg.MessagesRequestAppWebViewRequest{
			Peer:     peer,
			App:      appInput,
			Platform: "android",
		}
		if appInfo.RequestWriteAccess {
			req.SetWriteAllowed(true)
		}
		if startParam != "" {
			req.SetStartParam(startParam)
		}
		result, err := api.MessagesRequestAppWebView(ctx, req)
		if err != nil {
			// 回退：部分 bot 的 t.me/bot/name?startapp= 实际走 requestWebView / simpleWebView
			if fallbackURL, note, okFB := m.openBotWebViewFallback(ctx, api, botInput, peer, deepLink, startParam); okFB {
				out.OK = true
				out.BusinessResult = ResultOK
				out.Mode = "webview"
				out.URL = fallbackURL
				out.Note = note
				return nil
			}
			biz, code, msg, _ := classifyGroupErr(err, true)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			return nil
		}
		if result == nil || strings.TrimSpace(result.URL) == "" {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "empty_webview_url"
			out.ErrorMessage = "未返回 Mini App 地址"
			return nil
		}
		out.OK = true
		out.BusinessResult = ResultOK
		out.URL = result.URL
		if qid, okQ := result.GetQueryID(); okQ {
			out.QueryID = qid
		}
		out.Note = fmt.Sprintf("已用协议号打开 @%s/%s Mini App，请在浏览器完成验证", username, shortName)
		return nil
	})
	if err != nil {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "client_error"
		out.ErrorMessage = err.Error()
	}
	return out
}

/**
 * Mini App 失败时回退：requestWebView → requestSimpleWebView。
 *
 * @param ctx 上下文
 * @param api TG 客户端
 * @param bot 机器人 InputUser
 * @param peer 会话 peer（通常为机器人自己）
 * @param buttonURL 原始按钮 URL
 * @param startParam startapp 参数
 * @return 最终 URL、说明、是否成功
 */
func (m *Manager) openBotWebViewFallback(
	ctx context.Context,
	api *tg.Client,
	bot *tg.InputUser,
	peer tg.InputPeerClass,
	buttonURL, startParam string,
) (finalURL, note string, ok bool) {
	buttonURL = strings.TrimSpace(buttonURL)
	if bot == nil || buttonURL == "" {
		return "", "", false
	}
	// 1. requestWebView（带 peer，等同点击聊天气泡里的 webview 按钮）
	{
		req := &tg.MessagesRequestWebViewRequest{
			Peer:     peer,
			Bot:      bot,
			Platform: "android",
		}
		req.SetURL(buttonURL)
		if result, err := api.MessagesRequestWebView(ctx, req); err == nil && result != nil {
			if u := strings.TrimSpace(result.URL); u != "" {
				return u, "已用协议号 requestWebView 打开，请在浏览器完成验证", true
			}
		}
	}
	// 2. requestSimpleWebView（部分直链 Mini App 走此路径）
	{
		req := &tg.MessagesRequestSimpleWebViewRequest{
			Bot:      bot,
			Platform: "android",
		}
		req.SetURL(buttonURL)
		if startParam != "" {
			req.SetStartParam(startParam)
		}
		if result, err := api.MessagesRequestSimpleWebView(ctx, req); err == nil && result != nil {
			if u := strings.TrimSpace(result.URL); u != "" {
				return u, "已用协议号 requestSimpleWebView 打开，请在浏览器完成验证", true
			}
		}
	}
	return "", "", false
}

/**
 * 协议号打开机器人入群验证 deep link（messages.startBot + 拉取私信验证题）。
 * 对应官方客户端打开 https://t.me/bot?start=joinverify_xxx，而非裸发 /start。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param deepLink 机器人 start 链接
 * @param groupTarget 来源群（仅标记用）
 * @param wait 等待机器人回复时长
 * @return 交互结果（含私信中的验证按钮）
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) StartBotDeepLink(
	ctx context.Context,
	accountID, deepLink, groupTarget string,
	wait time.Duration,
) BotDeepLinkResult {
	out := BotDeepLinkResult{AccountID: accountID}
	username, startParam, ok := ParseBotStartDeepLink(deepLink)
	if !ok {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "not_bot_deeplink"
		out.ErrorMessage = "不是带 start 参数的机器人链接"
		return out
	}
	out.BotUsername = username
	out.StartParam = startParam
	if wait < 2*time.Second {
		wait = 3 * time.Second
	}
	if wait > 12*time.Second {
		wait = 12 * time.Second
	}

	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
		if err != nil {
			biz, code, msg, _ := classifyContactErr(err)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			return nil
		}
		user, okUser := findUserFromResolved(resolved)
		if !okUser || user == nil {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "bot_not_found"
			out.ErrorMessage = "无法解析验证机器人 @" + username
			return nil
		}
		if !user.Bot {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "not_a_bot"
			out.ErrorMessage = "@" + username + " 不是机器人"
			return nil
		}
		ensureUserAccessHash(user)
		_ = m.store.UpsertEntity(accountID, user.ID, user.AccessHash, "user", user.Username)
		out.BotID = user.ID
		out.BotAccessHash = formatAccessHash(user.AccessHash)

		botInput := &tg.InputUser{UserID: user.ID, AccessHash: user.AccessHash}
		peer := &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
		randomID, err := randomInt64()
		if err != nil {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "random_id"
			out.ErrorMessage = err.Error()
			return nil
		}

		// 1. 带 start_param 启动（等同官方客户端打开 deep link）
		_, err = api.MessagesStartBot(ctx, &tg.MessagesStartBotRequest{
			Bot:        botInput,
			Peer:       peer,
			RandomID:   randomID,
			StartParam: startParam,
		})
		if err != nil {
			biz, code, msg, _ := classifySendErr(err)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			return nil
		}

		// 2. 等待机器人私信回复
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			out.BusinessResult = ResultFailed
			out.ErrorCode = "cancelled"
			out.ErrorMessage = ctx.Err().Error()
			return nil
		case <-timer.C:
		}

		// 3. 拉取私信中的验证题
		challenges, reply, errList := m.listBotDMChallengesLocked(ctx, api, accountID, user, groupTarget, 12)
		if errList != nil {
			out.OK = true
			out.BusinessResult = ResultOK
			out.Note = "已向机器人发送 start 参数，但拉取私信失败：" + errList.Error()
			return nil
		}
		out.OK = true
		out.BusinessResult = ResultOK
		out.ReplyText = reply
		out.Challenges = challenges
		if len(challenges) == 0 {
			if reply != "" {
				out.Note = "已私信机器人（start=" + startParam + "），回复：" + truncateRunes(reply, 120) + "。若无按钮请稍后再拉取验证题"
			} else {
				out.Note = "已私信机器人（start=" + startParam + "），暂未收到带按钮的验证消息，请稍后重试"
			}
		} else {
			out.Note = fmt.Sprintf("已用协议号打开 @%s?start=%s，收到 %d 条私信验证题，请点选答案", username, startParam, len(challenges))
		}
		return nil
	})
	if err != nil {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "client_error"
		out.ErrorMessage = err.Error()
	}
	return out
}

/**
 * 拉取与验证机器人私信中的入群验证题。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param botUsername 机器人用户名
 * @param groupTarget 来源群标记
 * @param limit 历史条数
 * @return 验证题列表
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) ListBotDMChallenges(
	ctx context.Context,
	accountID, botUsername, groupTarget string,
	limit int,
) ([]JoinChallenge, error) {
	botUsername = strings.TrimPrefix(strings.TrimSpace(botUsername), "@")
	if botUsername == "" {
		return nil, fmt.Errorf("botUsername required")
	}
	if limit <= 0 {
		limit = 12
	}
	if limit > 40 {
		limit = 40
	}
	var out []JoinChallenge
	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: botUsername})
		if err != nil {
			return err
		}
		user, ok := findUserFromResolved(resolved)
		if !ok || user == nil {
			return fmt.Errorf("bot not found: @%s", botUsername)
		}
		ensureUserAccessHash(user)
		_ = m.store.UpsertEntity(accountID, user.ID, user.AccessHash, "user", user.Username)
		list, _, errList := m.listBotDMChallengesLocked(ctx, api, accountID, user, groupTarget, limit)
		out = list
		return errList
	})
	return out, err
}

/**
 * 在已持有 API 客户端时拉取机器人私信验证题。
 *
 * @param ctx 上下文
 * @param api TG 客户端
 * @param accountID 协议号
 * @param bot 机器人用户
 * @param groupTarget 来源群
 * @param limit 条数
 * @return 验证题、最新回复文案、错误
 */
func (m *Manager) listBotDMChallengesLocked(
	ctx context.Context,
	api *tg.Client,
	accountID string,
	bot *tg.User,
	groupTarget string,
	limit int,
) ([]JoinChallenge, string, error) {
	if bot == nil {
		return nil, "", fmt.Errorf("bot required")
	}
	peer := &tg.InputPeerUser{UserID: bot.ID, AccessHash: bot.AccessHash}
	hist, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  peer,
		Limit: limit,
	})
	if err != nil {
		return nil, "", err
	}
	msgs, _ := flattenHistory(hist)
	cutoff := int(time.Now().Add(-2 * time.Hour).Unix())
	var out []JoinChallenge
	reply := ""
	botName := firstNonEmpty(bot.Username, strings.TrimSpace(bot.FirstName+" "+bot.LastName))
	targetLabel := strings.TrimSpace(groupTarget)
	if targetLabel == "" {
		targetLabel = "@" + botName
	}
	for _, msg := range msgs {
		if msg == nil || msg.ID == 0 {
			continue
		}
		if msg.Date > 0 && msg.Date < cutoff {
			continue
		}
		// 只要机器人发出的消息
		fromID := peerUserID(msg.FromID)
		if fromID != 0 && fromID != bot.ID {
			continue
		}
		text := strings.TrimSpace(msg.Message)
		if reply == "" && text != "" {
			reply = text
		}
		btns := extractChallengeButtons(msg)
		if len(btns) == 0 {
			continue
		}
		out = append(out, JoinChallenge{
			AccountID:     accountID,
			Target:        targetLabel,
			PeerID:        bot.ID,
			AccessHash:    formatAccessHash(bot.AccessHash),
			PeerType:      "user",
			MsgID:         msg.ID,
			Text:          text,
			BotID:         bot.ID,
			BotAccessHash: formatAccessHash(bot.AccessHash),
			BotName:       botName,
			Date:          msg.Date,
			Buttons:       btns,
			Kind:          "bot_dm",
		})
	}
	return out, reply, nil
}

/**
 * 确保 User 带有 AccessHash。
 *
 * @param u 用户实体
 */
func ensureUserAccessHash(u *tg.User) {
	if u == nil {
		return
	}
	if h, ok := u.GetAccessHash(); ok {
		u.AccessHash = h
	}
}

