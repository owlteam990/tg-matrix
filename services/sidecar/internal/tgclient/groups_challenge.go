package tgclient

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

// ChallengeButton 入群验证 inline 按钮。
type ChallengeButton struct {
	Text        string `json:"text"`
	Kind        string `json:"kind"`                  // callback | url | webview | simple_webview
	Data        string `json:"data,omitempty"`        // callback：base64(callback_data)
	URL         string `json:"url,omitempty"`         // url/webview 按钮原始 URL
	Row         int    `json:"row"`
	Col         int    `json:"col"`
	AdminLikely bool   `json:"adminLikely,omitempty"` // 疑似管理员按钮（通过/拒绝）
}

// JoinChallenge 群内/机器人私信验证题（含 inline 键盘）。
// AccessHash / BotAccessHash 必须用字符串，避免前端 Number 丢精度导致 CHANNEL_INVALID。
type JoinChallenge struct {
	AccountID     string            `json:"accountId"`
	Target        string            `json:"target"`
	PeerID        int64             `json:"peerId"`
	AccessHash    string            `json:"accessHash,omitempty"`
	PeerType      string            `json:"peerType,omitempty"` // channel | user（私信机器人为 user）
	MsgID         int               `json:"msgId"`
	Text          string            `json:"text"`
	BotID         int64             `json:"botId,omitempty"`
	BotAccessHash string            `json:"botAccessHash,omitempty"`
	BotName       string            `json:"botName,omitempty"`
	Date          int               `json:"date,omitempty"`
	Buttons       []ChallengeButton `json:"buttons"`
	Kind          string            `json:"kind"` // captcha | inline | bot_dm
}

// ChallengeAnswerResult 点击验证按钮结果。
type ChallengeAnswerResult struct {
	AccountID      string `json:"accountId"`
	PeerID         int64  `json:"peerId"`
	MsgID          int    `json:"msgId"`
	OK             bool   `json:"ok"`
	BusinessResult string `json:"businessResult"`
	Alert          bool   `json:"alert,omitempty"`
	Message        string `json:"message,omitempty"` // bot 弹窗/提示文案
	ErrorCode      string `json:"errorCode,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
}

// ChallengeWebViewResult 打开小程序/链接验证的结果。
type ChallengeWebViewResult struct {
	AccountID      string          `json:"accountId"`
	PeerID         int64           `json:"peerId"`
	MsgID          int             `json:"msgId"`
	OK             bool            `json:"ok"`
	BusinessResult string          `json:"businessResult"`
	Mode           string          `json:"mode,omitempty"` // browser | bot_dm | join_channel
	URL            string          `json:"url,omitempty"`  // 可在浏览器打开的最终地址
	QueryID        int64           `json:"queryId,omitempty"`
	BotUsername    string          `json:"botUsername,omitempty"`
	StartParam     string          `json:"startParam,omitempty"`
	JoinedTarget   string          `json:"joinedTarget,omitempty"` // mode=join_channel 时实际加入目标
	ReplyText      string          `json:"replyText,omitempty"`
	Challenges     []JoinChallenge `json:"challenges,omitempty"`
	ErrorCode      string          `json:"errorCode,omitempty"`
	ErrorMessage   string          `json:"errorMessage,omitempty"`
	Note           string          `json:"note,omitempty"`
}

var (
	reCaptchaHint = regexp.MustCompile(`(?i)(入群验证|验证|verify|robot|机器人|算术|计算题|not a robot|complete verification|= \?)`)
	reAdminBtn    = regexp.MustCompile(`(?i)^(通过|拒绝|同意|踢出|封禁|approve|reject|ban|pass|deny)|拒绝并举报`)
)

/**
 * 拉取目标群内待处理的入群验证题（机器人 inline 键盘）。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param target 群用户名或链接
 * @param limit 拉取最近消息条数
 * @return 验证题列表
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) ListJoinChallenges(ctx context.Context, accountID, target string, limit int) ([]JoinChallenge, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("target required")
	}
	if limit <= 0 {
		limit = 40
	}
	if limit > 80 {
		limit = 80
	}

	var out []JoinChallenge
	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		username := extractPublicUsername(target)
		var ch *tg.Channel
		var err error
		if username != "" {
			ch, err = m.lookupChannelByUsername(ctx, api, accountID, username)
			if err != nil {
				return err
			}
		} else if peerID, accessHash, ok := parsePeerTarget(target); ok {
			ch = &tg.Channel{ID: peerID}
			ch.SetAccessHash(accessHash)
		} else {
			return fmt.Errorf("expect @username or peer:id:hash")
		}
		ensureChannelAccessHash(ch)
		peer := ch.AsInputPeer()
		hist, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:  peer,
			Limit: limit,
		})
		if err != nil {
			return err
		}
		msgs, users := flattenHistory(hist)
		userByID := map[int64]*tg.User{}
		for _, u := range users {
			if user, ok := u.(*tg.User); ok && user != nil {
				userByID[user.ID] = user
			}
		}
		cutoff := int(time.Now().Add(-48 * time.Hour).Unix())
		for _, msg := range msgs {
			if msg == nil || msg.ID == 0 {
				continue
			}
			if msg.Date > 0 && msg.Date < cutoff {
				continue
			}
			btns := extractChallengeButtons(msg)
			if len(btns) == 0 {
				continue
			}
			text := strings.TrimSpace(msg.Message)
			fromID := peerUserID(msg.FromID)
			botName := ""
			botHash := int64(0)
			isBot := false
			if u, ok := userByID[fromID]; ok && u != nil {
				botName = firstNonEmpty(u.Username, strings.TrimSpace(u.FirstName+" "+u.LastName))
				isBot = u.Bot
				if h, ok := u.GetAccessHash(); ok {
					botHash = h
					_ = m.store.UpsertEntity(accountID, u.ID, h, "user", u.Username)
				}
			}
			hasWebOrURL := false
			for _, b := range btns {
				if b.Kind == "webview" || b.Kind == "simple_webview" || b.Kind == "url" {
					hasWebOrURL = true
					break
				}
			}
			kind := "inline"
			if reCaptchaHint.MatchString(text) || isBot || hasWebOrURL {
				kind = "captcha"
			}
			// 非验证文案且非机器人：跳过（避免普通群投票键盘干扰）
			if kind != "captcha" && !isBot {
				continue
			}
			item := JoinChallenge{
				AccountID:     accountID,
				Target:        target,
				PeerID:        ch.ID,
				AccessHash:    formatAccessHash(ch.AccessHash),
				PeerType:      "channel",
				MsgID:         msg.ID,
				Text:          text,
				BotID:         fromID,
				BotAccessHash: formatAccessHash(botHash),
				BotName:       botName,
				Date:          msg.Date,
				Buttons:       btns,
				Kind:          kind,
			}
			out = append(out, item)
		}
		return nil
	})
	return out, err
}

/**
 * 点击入群验证 inline 按钮（messages.getBotCallbackAnswer）。
 * peerType=user 时点机器人私信内验证按钮；默认 channel 为群内验证。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param target 群目标（@username / 链接，用于 CHANNEL_INVALID 时重解析）
 * @param peerType channel | user
 * @param peerID 群/频道或机器人 user ID
 * @param accessHash access_hash
 * @param msgID 消息 ID
 * @param dataB64 base64 编码的 callback_data
 * @return 点击结果
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) AnswerJoinChallenge(
	ctx context.Context,
	accountID, target, peerType string,
	peerID, accessHash int64,
	msgID int,
	dataB64 string,
) ChallengeAnswerResult {
	out := ChallengeAnswerResult{
		AccountID: accountID,
		PeerID:    peerID,
		MsgID:     msgID,
	}
	peerType = strings.ToLower(strings.TrimSpace(peerType))
	if peerType == "" {
		peerType = "channel"
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataB64))
	if err != nil || len(raw) == 0 {
		// 兼容 raw url encoding
		raw, err = base64.RawURLEncoding.DecodeString(strings.TrimSpace(dataB64))
	}
	if err != nil || len(raw) == 0 {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "invalid_data"
		out.ErrorMessage = "callback data 无效（需 base64）"
		return out
	}
	if peerID == 0 || msgID == 0 {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "params_required"
		out.ErrorMessage = "peerId 与 msgId 必填"
		return out
	}

	if peerType == "user" {
		return m.answerUserChallenge(ctx, accountID, peerID, accessHash, msgID, raw)
	}

	err = m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		peer, hash, errPeer := m.resolveChallengeChannelPeer(ctx, api, accountID, target, peerID, accessHash)
		if errPeer != nil {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "invalid_peer"
			out.ErrorMessage = errPeer.Error()
			return nil
		}
		accessHash = hash
		peerID = peer.ChannelID

		// 先把消息载入会话（与官方客户端点按钮前一致），再点 callback
		ans, errClick := m.clickChallengeCallback(ctx, api, peer, msgID, raw)
		if errClick != nil && isChannelInvalid(errClick) {
			// 刷新频道实体后再试（升级 min access_hash / 会话缓存）
			if refreshed, ok := m.refreshChallengeChannelPeer(ctx, api, accountID, peer); ok {
				peer = refreshed
				accessHash = refreshed.AccessHash
				peerID = refreshed.ChannelID
				ans, errClick = m.clickChallengeCallback(ctx, api, peer, msgID, raw)
			}
		}
		if errClick != nil && isChannelInvalid(errClick) && strings.TrimSpace(target) != "" {
			// 按 target 强制重解析（忽略本地缓存与前端 hash）
			if peer2, hash2, err2 := m.resolveChallengeChannelPeer(ctx, api, accountID, target, 0, 0); err2 == nil && peer2 != nil {
				peer = peer2
				accessHash = hash2
				peerID = peer2.ChannelID
				ans, errClick = m.clickChallengeCallback(ctx, api, peer, msgID, raw)
			}
		}
		if errClick != nil {
			biz, code, msg, _ := classifyGroupErr(errClick, true)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.PeerID = peerID
			return nil
		}
		out.OK = true
		out.BusinessResult = ResultOK
		out.PeerID = peerID
		if ans != nil {
			out.Alert = ans.Alert
			out.Message = strings.TrimSpace(ans.Message)
		}
		if out.Message == "" {
			out.Message = "已提交验证点击"
		}
		_ = m.store.UpsertEntity(accountID, peerID, accessHash, "channel", extractPublicUsername(target))
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
 * 点击机器人私信中的入群验证 callback 按钮。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param botID 机器人 user id
 * @param accessHash 机器人 access_hash
 * @param msgID 私信消息 ID
 * @param data callback_data
 * @return 点击结果
 */
func (m *Manager) answerUserChallenge(
	ctx context.Context,
	accountID string,
	botID, accessHash int64,
	msgID int,
	data []byte,
) ChallengeAnswerResult {
	out := ChallengeAnswerResult{
		AccountID: accountID,
		PeerID:    botID,
		MsgID:     msgID,
	}
	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		if accessHash == 0 {
			if ent, ok, _ := m.store.GetEntity(accountID, botID, "user"); ok {
				accessHash = ent.AccessHash
			}
		}
		if accessHash == 0 {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "invalid_peer"
			out.ErrorMessage = "缺少机器人 access_hash，请重新「在私信中验证」"
			return nil
		}
		peer := &tg.InputPeerUser{UserID: botID, AccessHash: accessHash}
		ans, errClick := m.clickUserChallengeCallback(ctx, api, peer, msgID, data)
		if errClick != nil {
			biz, code, msg, _ := classifyGroupErr(errClick, true)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			return nil
		}
		out.OK = true
		out.BusinessResult = ResultOK
		if ans != nil {
			out.Alert = ans.Alert
			out.Message = strings.TrimSpace(ans.Message)
		}
		if out.Message == "" {
			out.Message = "已提交私信验证点击"
		}
		_ = m.store.UpsertEntity(accountID, botID, accessHash, "user", "")
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
 * 载入群验证消息后点击 inline callback。
 *
 * @param ctx 上下文
 * @param api TG 客户端
 * @param peer 群 peer
 * @param msgID 消息 ID
 * @param data callback_data 原始字节
 * @return bot 应答、错误
 */
func (m *Manager) clickChallengeCallback(
	ctx context.Context,
	api *tg.Client,
	peer *tg.InputPeerChannel,
	msgID int,
	data []byte,
) (*tg.MessagesBotCallbackAnswer, error) {
	if peer == nil {
		return nil, fmt.Errorf("peer required")
	}
	// 1. channels.getMessages 预热消息+peer，避免裸点按钮 CHANNEL_INVALID
	_, _ = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}},
	})
	req := &tg.MessagesGetBotCallbackAnswerRequest{
		Peer:  peer,
		MsgID: msgID,
	}
	req.SetData(data)
	return api.MessagesGetBotCallbackAnswer(ctx, req)
}

/**
 * 点击机器人私信 inline callback。
 *
 * @param ctx 上下文
 * @param api TG 客户端
 * @param peer 机器人 peer
 * @param msgID 消息 ID
 * @param data callback_data
 * @return bot 应答、错误
 */
func (m *Manager) clickUserChallengeCallback(
	ctx context.Context,
	api *tg.Client,
	peer *tg.InputPeerUser,
	msgID int,
	data []byte,
) (*tg.MessagesBotCallbackAnswer, error) {
	if peer == nil {
		return nil, fmt.Errorf("peer required")
	}
	_, _ = api.MessagesGetMessages(ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}})
	req := &tg.MessagesGetBotCallbackAnswerRequest{
		Peer:  peer,
		MsgID: msgID,
	}
	req.SetData(data)
	return api.MessagesGetBotCallbackAnswer(ctx, req)
}

/**
 * 用 channels.getChannels 刷新 access_hash。
 *
 * @param ctx 上下文
 * @param api TG 客户端
 * @param accountID 协议号
 * @param peer 当前 peer
 * @return 刷新后的 peer、是否成功
 */
func (m *Manager) refreshChallengeChannelPeer(
	ctx context.Context,
	api *tg.Client,
	accountID string,
	peer *tg.InputPeerChannel,
) (*tg.InputPeerChannel, bool) {
	if peer == nil || peer.ChannelID == 0 {
		return nil, false
	}
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
	})
	if err != nil {
		return nil, false
	}
	var chats []tg.ChatClass
	switch v := res.(type) {
	case *tg.MessagesChats:
		chats = v.Chats
	case *tg.MessagesChatsSlice:
		chats = v.Chats
	}
	for _, c := range chats {
		ch, ok := c.(*tg.Channel)
		if !ok || ch == nil || ch.ID != peer.ChannelID {
			continue
		}
		ensureChannelAccessHash(ch)
		if ch.AccessHash == 0 {
			continue
		}
		_ = m.store.UpsertEntity(accountID, ch.ID, ch.AccessHash, "channel", ch.Username)
		return ch.AsInputPeer(), true
	}
	return nil, false
}

/**
 * 解析验证题所在群的 InputPeerChannel。
 *
 * @param ctx 上下文
 * @param api TG 客户端
 * @param accountID 协议号
 * @param target 群目标
 * @param peerID 群 ID
 * @param accessHash 前端传来的 hash（可能已精度丢失）
 * @return peer、有效 hash、错误
 */
func (m *Manager) resolveChallengeChannelPeer(
	ctx context.Context,
	api *tg.Client,
	accountID, target string,
	peerID, accessHash int64,
) (*tg.InputPeerChannel, int64, error) {
	// 1. 优先用 @username / 链接重新解析，拿到权威 access_hash
	if username := extractPublicUsername(target); username != "" {
		ch, err := m.lookupChannelByUsername(ctx, api, accountID, username)
		if err == nil && ch != nil {
			ensureChannelAccessHash(ch)
			if ch.AccessHash != 0 {
				_ = m.store.UpsertEntity(accountID, ch.ID, ch.AccessHash, "channel", ch.Username)
				p := ch.AsInputPeer()
				return p, p.AccessHash, nil
			}
		}
	}
	// 2. 本地实体缓存
	if peerID != 0 {
		if ent, ok, _ := m.store.GetEntity(accountID, peerID, "channel"); ok && ent.AccessHash != 0 {
			return &tg.InputPeerChannel{ChannelID: peerID, AccessHash: ent.AccessHash}, ent.AccessHash, nil
		}
	}
	// 3. 前端传来的 hash（字符串解析后）
	if peerID != 0 && accessHash != 0 {
		return &tg.InputPeerChannel{ChannelID: peerID, AccessHash: accessHash}, accessHash, nil
	}
	return nil, 0, fmt.Errorf("缺少群 access_hash，请重新拉取验证题")
}

func isChannelInvalid(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToUpper(err.Error())
	return strings.Contains(s, "CHANNEL_INVALID") || strings.Contains(s, "CHANNEL_PRIVATE")
}

/**
 * 打开入群验证小程序/链接（requestWebView / requestSimpleWebView / Mini App / 直链）。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param target 来源群标记
 * @param peerType channel|user（私信机器人为 user）
 * @param peerID 群或机器人 ID
 * @param accessHash peer access_hash
 * @param msgID 验证消息 ID
 * @param botID 机器人 user id
 * @param botAccessHash 机器人 access_hash
 * @param buttonKind webview | simple_webview | url
 * @param buttonURL 按钮上的原始 URL
 * @return 可打开的最终 URL
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) OpenChallengeWebView(
	ctx context.Context,
	accountID, target, peerType string,
	peerID, accessHash int64,
	msgID int,
	botID, botAccessHash int64,
	buttonKind, buttonURL string,
) ChallengeWebViewResult {
	out := ChallengeWebViewResult{
		AccountID: accountID,
		PeerID:    peerID,
		MsgID:     msgID,
	}
	buttonKind = strings.ToLower(strings.TrimSpace(buttonKind))
	buttonURL = strings.TrimSpace(buttonURL)
	peerType = strings.ToLower(strings.TrimSpace(peerType))
	if peerType == "" {
		peerType = "channel"
	}
	if buttonURL == "" {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "empty_url"
		out.ErrorMessage = "按钮 URL 为空"
		return out
	}
	// t.me/bot?start=xxx：用协议号 messages.startBot，勿开系统浏览器（否则用的是本机登录号）
	if buttonKind == "url" || buttonKind == "" {
		if _, _, isBotLink := ParseBotStartDeepLink(buttonURL); isBotLink {
			started := m.StartBotDeepLink(ctx, accountID, buttonURL, target, 3*time.Second)
			out.OK = started.OK
			out.BusinessResult = started.BusinessResult
			out.Mode = "bot_dm"
			out.BotUsername = started.BotUsername
			out.StartParam = started.StartParam
			out.ReplyText = started.ReplyText
			out.Challenges = started.Challenges
			out.ErrorCode = started.ErrorCode
			out.ErrorMessage = started.ErrorMessage
			out.Note = started.Note
			out.URL = buttonURL
			if started.BotID != 0 {
				out.PeerID = started.BotID
			}
			return out
		}
		// t.me/bot/app?startapp=：协议号 requestAppWebView，带登录态
		if _, _, _, isMini := ParseMiniAppDeepLink(buttonURL); isMini {
			opened := m.OpenMiniAppDeepLink(ctx, accountID, buttonURL)
			opened.MsgID = msgID
			return opened
		}
		// t.me/pionexcn 或邀请链接：协议号 ChannelsJoinChannel，勿开系统浏览器
		if joinTarget, isJoin := ParsePublicJoinLink(buttonURL); isJoin {
			joined := m.JoinGroup(ctx, accountID, joinTarget)
			out.OK = joined.OK
			out.BusinessResult = joined.BusinessResult
			out.Mode = "join_channel"
			out.JoinedTarget = joinTarget
			out.URL = buttonURL
			out.ErrorCode = joined.ErrorCode
			out.ErrorMessage = joined.ErrorMessage
			if joined.PeerID != 0 {
				out.PeerID = joined.PeerID
			}
			if joined.OK {
				if joined.BusinessResult == ResultAlreadyIn {
					out.Note = "协议号已在该频道/群：" + joinTarget
				} else {
					out.Note = "协议号已加入频道/群：" + joinTarget
				}
			} else {
				out.Note = "协议号加入失败：" + joinTarget
			}
			return out
		}
		out.OK = true
		out.BusinessResult = ResultOK
		out.Mode = "browser"
		out.URL = buttonURL
		out.Note = "请在浏览器完成验证"
		return out
	}

	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		if botAccessHash == 0 && botID != 0 {
			if ent, ok, _ := m.store.GetEntity(accountID, botID, "user"); ok {
				botAccessHash = ent.AccessHash
			}
		}
		// 私信机器人：peer 即 bot
		if peerType == "user" {
			if botID == 0 {
				botID = peerID
			}
			if botAccessHash == 0 {
				botAccessHash = accessHash
			}
			if botAccessHash == 0 {
				if ent, ok, _ := m.store.GetEntity(accountID, botID, "user"); ok {
					botAccessHash = ent.AccessHash
				}
			}
		}
		if botID == 0 || botAccessHash == 0 {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "bot_required"
			out.ErrorMessage = "缺少机器人 access_hash，请重新拉取验证题或刷新会话"
			return nil
		}
		bot := &tg.InputUser{UserID: botID, AccessHash: botAccessHash}
		var result *tg.WebViewResultURL
		var err error
		switch buttonKind {
		case "simple_webview":
			req := &tg.MessagesRequestSimpleWebViewRequest{
				Bot:      bot,
				Platform: "android",
			}
			req.SetURL(buttonURL)
			result, err = api.MessagesRequestSimpleWebView(ctx, req)
		default: // webview
			var peer tg.InputPeerClass
			if peerType == "user" {
				peer = &tg.InputPeerUser{UserID: botID, AccessHash: botAccessHash}
			} else {
				chPeer, hash, errPeer := m.resolveChallengeChannelPeer(ctx, api, accountID, target, peerID, accessHash)
				if errPeer != nil {
					out.BusinessResult = ResultFailed
					out.ErrorCode = "invalid_peer"
					out.ErrorMessage = errPeer.Error()
					return nil
				}
				accessHash = hash
				peer = chPeer
			}
			req := &tg.MessagesRequestWebViewRequest{
				Peer:     peer,
				Bot:      bot,
				Platform: "android",
			}
			req.SetURL(buttonURL)
			if msgID > 0 {
				req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: msgID})
			}
			result, err = api.MessagesRequestWebView(ctx, req)
		}
		if err != nil {
			biz, code, msg, _ := classifyGroupErr(err, true)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			return nil
		}
		if result == nil || strings.TrimSpace(result.URL) == "" {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "empty_webview_url"
			out.ErrorMessage = "未返回小程序地址"
			return nil
		}
		out.OK = true
		out.BusinessResult = ResultOK
		out.Mode = "webview"
		out.URL = result.URL
		if qid, ok := result.GetQueryID(); ok {
			out.QueryID = qid
		}
		out.Note = "已生成带登录态的小程序链接，请在浏览器完成验证"
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
 * 从消息 ReplyMarkup 提取验证相关按钮（callback / url / webview）。
 *
 * @param msg 消息
 * @return 按钮列表
 */
func extractChallengeButtons(msg *tg.Message) []ChallengeButton {
	if msg == nil {
		return nil
	}
	markup, ok := msg.GetReplyMarkup()
	if !ok || markup == nil {
		return nil
	}
	inline, ok := markup.(*tg.ReplyInlineMarkup)
	if !ok || inline == nil {
		return nil
	}
	var out []ChallengeButton
	for ri, row := range inline.Rows {
		for ci, btn := range row.Buttons {
			text := ""
			item := ChallengeButton{Row: ri, Col: ci}
			switch v := btn.(type) {
			case *tg.KeyboardButtonCallback:
				if v == nil || len(v.Data) == 0 || v.RequiresPassword {
					continue
				}
				text = strings.TrimSpace(v.Text)
				item.Kind = "callback"
				item.Data = base64.StdEncoding.EncodeToString(v.Data)
			case *tg.KeyboardButtonURL:
				if v == nil || strings.TrimSpace(v.URL) == "" {
					continue
				}
				text = strings.TrimSpace(v.Text)
				item.Kind = "url"
				item.URL = strings.TrimSpace(v.URL)
			case *tg.KeyboardButtonWebView:
				if v == nil || strings.TrimSpace(v.URL) == "" {
					continue
				}
				text = strings.TrimSpace(v.Text)
				item.Kind = "webview"
				item.URL = strings.TrimSpace(v.URL)
			case *tg.KeyboardButtonSimpleWebView:
				if v == nil || strings.TrimSpace(v.URL) == "" {
					continue
				}
				text = strings.TrimSpace(v.Text)
				item.Kind = "simple_webview"
				item.URL = strings.TrimSpace(v.URL)
			default:
				continue
			}
			item.Text = text
			item.AdminLikely = reAdminBtn.MatchString(text)
			out = append(out, item)
		}
	}
	return out
}
