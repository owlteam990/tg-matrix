package tgclient

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"
)

// ChatDialog 会话摘要（私聊优先，也含群/频道）。
// AccessHash 必须用字符串，避免 JS Number 丢精度导致群历史/已读失败。
type ChatDialog struct {
	PeerID       int64  `json:"peerId"`
	AccessHash   string `json:"accessHash,omitempty"`
	PeerType     string `json:"peerType"` // user | channel | chat
	Title        string `json:"title"`
	Username     string `json:"username,omitempty"`
	LastMessage  string `json:"lastMessage,omitempty"`
	LastDate     int    `json:"lastDate,omitempty"`
	UnreadCount  int    `json:"unreadCount,omitempty"`
	TopMessageID int    `json:"topMessageId,omitempty"`
	IsBot        bool   `json:"isBot,omitempty"`
	IsSelf       bool   `json:"isSelf,omitempty"`
}

// ChatMessage 聊天消息。
type ChatMessage struct {
	ID            int               `json:"id"`
	PeerID        int64             `json:"peerId"`
	Out           bool              `json:"out"`
	Text          string            `json:"text"`
	Date          int               `json:"date"`
	FromID        int64             `json:"fromId,omitempty"`
	FromName      string            `json:"fromName,omitempty"`
	Media         ChatMediaMeta     `json:"media,omitempty"`
	Buttons       []ChallengeButton `json:"buttons,omitempty"` // inline 键盘（验证/菜单等）
	BotID         int64             `json:"botId,omitempty"`
	BotAccessHash string            `json:"botAccessHash,omitempty"`
}

// ChatSendResult 单聊发送结果。
type ChatSendResult struct {
	AccountID      string `json:"accountId"`
	PeerID         int64  `json:"peerId"`
	OK             bool   `json:"ok"`
	BusinessResult string `json:"businessResult"`
	MessageID      int    `json:"messageId,omitempty"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
}

/**
 * 列出协议号会话（私聊 / 群频道 / 全部）。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param limit 最多条数
 * @param peerKind user=私信 / group=群与频道 / all=全部
 * @return 会话列表
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) ListChatDialogs(ctx context.Context, accountID string, limit int, peerKind string) ([]ChatDialog, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	peerKind = normalizeDialogPeerKind(peerKind)

	var out []ChatDialog
	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		offsetDate := 0
		offsetID := 0
		var offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
		seen := map[string]struct{}{}

		for len(out) < limit {
			chunk := limit - len(out)
			if chunk > 100 {
				chunk = 100
			}
			res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
				OffsetDate: offsetDate,
				OffsetID:   offsetID,
				OffsetPeer: offsetPeer,
				Limit:      chunk,
			})
			if err != nil {
				return err
			}
			dialogs, chats, messages, users := unpackDialogsFull(res)
			if len(dialogs) == 0 {
				break
			}

			chatMap := map[int64]tg.ChatClass{}
			for _, c := range chats {
				switch v := c.(type) {
				case *tg.Channel:
					chatMap[v.ID] = v
					_ = m.store.UpsertEntity(accountID, v.ID, v.AccessHash, "channel", channelPublicUsername(v))
				case *tg.Chat:
					chatMap[v.ID] = v
				}
			}
			userMap := map[int64]*tg.User{}
			for _, u := range users {
				if user, ok := u.(*tg.User); ok && user != nil {
					userMap[user.ID] = user
					if h, ok := user.GetAccessHash(); ok {
						_ = m.store.UpsertEntity(accountID, user.ID, h, "user", user.Username)
					}
				}
			}
			msgMap := map[int]*tg.Message{}
			for _, mc := range messages {
				if msg, ok := mc.(*tg.Message); ok && msg != nil {
					msgMap[msg.ID] = msg
				}
			}

			for _, d := range dialogs {
				item, key, ok := dialogFromPeer(d, chatMap, userMap, msgMap, peerKind)
				if !ok {
					continue
				}
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, item)
				if len(out) >= limit {
					break
				}
			}

			last := dialogs[len(dialogs)-1]
			topMsgID := last.GetTopMessage()
			offsetID = topMsgID
			offsetPeer = inputPeerFromPeer(last.GetPeer(), chatMap)
			// 私聊 offsetPeer 需用户 access_hash
			if p, ok := last.GetPeer().(*tg.PeerUser); ok {
				if u, ok := userMap[p.UserID]; ok && u != nil {
					if h, ok := u.GetAccessHash(); ok {
						offsetPeer = &tg.InputPeerUser{UserID: u.ID, AccessHash: h}
					}
				}
			}
			offsetDate = 0
			if msg, ok := msgMap[topMsgID]; ok {
				offsetDate = msg.Date
			}
			if len(dialogs) < chunk {
				break
			}
		}
		return nil
	})
	return out, err
}

/**
 * 拉取会话历史消息（新→旧；前端可倒序展示）。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param peerType user|channel|chat
 * @param peerID peer id
 * @param accessHash access_hash（user/channel 需要）
 * @param limit 条数
 * @param offsetID 分页：从此 id 往更旧拉；0=最新
 * @param minID 仅返回 id > minID 的消息（轮询收新消息）
 * @return 消息列表（按 id 升序，便于 UI 从上到下）
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) GetChatHistory(
	ctx context.Context,
	accountID, peerType string,
	peerID, accessHash int64,
	limit, offsetID, minID int,
) ([]ChatMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	var out []ChatMessage
	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		peer, err := m.inputPeerForChat(accountID, peerType, peerID, accessHash)
		if err != nil {
			return err
		}
		res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     peer,
			OffsetID: offsetID,
			MinID:    minID,
			Limit:    limit,
		})
		if err != nil {
			return fmt.Errorf("getHistory %s:%d: %w", peerType, peerID, err)
		}
		msgs, users := flattenHistory(res)
		userMap := map[int64]*tg.User{}
		for _, u := range users {
			if user, ok := u.(*tg.User); ok && user != nil {
				userMap[user.ID] = user
			}
		}
		// API 通常返回新→旧，转为升序
		for i := len(msgs) - 1; i >= 0; i-- {
			msg := msgs[i]
			if msg == nil || msg.ID == 0 {
				continue
			}
			if minID > 0 && msg.ID <= minID {
				continue
			}
			text, media := parseMessageContent(msg)
			fromID := peerUserID(msg.FromID)
			fromName := ""
			var botID int64
			var botAccessHash string
			if u, ok := userMap[fromID]; ok && u != nil {
				fromName = strings.TrimSpace(u.FirstName + " " + u.LastName)
				if fromName == "" {
					fromName = u.Username
				}
				// 机器人消息：带上 bot access_hash，便于前端点 webview / callback
				if u.Bot {
					ensureUserAccessHash(u)
					botID = u.ID
					botAccessHash = formatAccessHash(u.AccessHash)
					_ = m.store.UpsertEntity(accountID, u.ID, u.AccessHash, "user", u.Username)
				}
			}
			btns := extractChallengeButtons(msg)
			// 私信会话本身就是机器人，或有 inline 按钮时补齐 bot 信息（webview 需要）
			if botID == 0 && strings.EqualFold(peerType, "user") {
				if u, ok := userMap[peerID]; ok && u != nil && u.Bot {
					ensureUserAccessHash(u)
					botID = u.ID
					botAccessHash = formatAccessHash(u.AccessHash)
					_ = m.store.UpsertEntity(accountID, u.ID, u.AccessHash, "user", u.Username)
				} else if len(btns) > 0 {
					hash := accessHash
					if hash == 0 {
						if ent, ok, _ := m.store.GetEntity(accountID, peerID, "user"); ok {
							hash = ent.AccessHash
						}
					}
					if hash != 0 {
						botID = peerID
						botAccessHash = formatAccessHash(hash)
					}
				}
			}
			out = append(out, ChatMessage{
				ID:            msg.ID,
				PeerID:        peerID,
				Out:           msg.Out,
				Text:          text,
				Date:          msg.Date,
				FromID:        fromID,
				FromName:      fromName,
				Media:         media,
				Buttons:       btns,
				BotID:         botID,
				BotAccessHash: botAccessHash,
			})
		}
		return nil
	})
	return out, err
}

/**
 * 将会话标记为已读（私聊/小群 messages.readHistory；超群/频道 channels.readHistory）。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param peerType user|channel|chat
 * @param peerID peer id
 * @param accessHash access_hash
 * @param maxID 读到该消息 id（含）；0 表示读到当前最新
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) MarkChatRead(
	ctx context.Context,
	accountID, peerType string,
	peerID, accessHash int64,
	maxID int,
) error {
	return m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		peerType = strings.ToLower(strings.TrimSpace(peerType))
		switch peerType {
		case "channel":
			hash := accessHash
			if hash == 0 {
				if ent, ok, _ := m.store.GetEntity(accountID, peerID, "channel"); ok {
					hash = ent.AccessHash
				}
			}
			if hash == 0 {
				return fmt.Errorf("channel access_hash required")
			}
			_, err := api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{
				Channel: &tg.InputChannel{ChannelID: peerID, AccessHash: hash},
				MaxID:   maxID,
			})
			return err
		case "user", "chat":
			peer, err := m.inputPeerForChat(accountID, peerType, peerID, accessHash)
			if err != nil {
				return err
			}
			_, err = api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{
				Peer:  peer,
				MaxID: maxID,
			})
			return err
		default:
			return fmt.Errorf("peerType must be user|channel|chat")
		}
	})
}

/**
 * 向指定会话发送文本消息。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param peerType user|channel|chat
 * @param peerID peer id
 * @param accessHash access_hash
 * @param text 正文
 * @return 发送结果
 * @author Auto
 * @date 2026-10-09
 */
/**
 * 解析前端传来的 accessHash（字符串优先，避免精度丢失）。
 *
 * @param raw 原始字符串
 * @return int64
 */
func ParseAccessHash(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func formatAccessHash(h int64) string {
	if h == 0 {
		return ""
	}
	return strconv.FormatInt(h, 10)
}

func (m *Manager) SendChatMessage(
	ctx context.Context,
	accountID, peerType string,
	peerID, accessHash int64,
	text string,
) ChatSendResult {
	text = strings.TrimSpace(text)
	out := ChatSendResult{AccountID: accountID, PeerID: peerID}
	if text == "" {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "empty_text"
		out.ErrorMessage = "text required"
		return out
	}
	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		peer, err := m.inputPeerForChat(accountID, peerType, peerID, accessHash)
		if err != nil {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "invalid_peer"
			out.ErrorMessage = err.Error()
			return nil
		}
		randomID, err := randomInt64()
		if err != nil {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "random_id"
			out.ErrorMessage = err.Error()
			return nil
		}
		updates, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:     peer,
			Message:  text,
			RandomID: randomID,
		})
		if err != nil {
			biz, code, msg, _ := classifySendErr(err)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			return nil
		}
		out.OK = true
		out.BusinessResult = ResultOK
		out.MessageID = messageIDFromUpdates(updates)
		return nil
	})
	if err != nil {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "client_error"
		out.ErrorMessage = err.Error()
	}
	return out
}

func (m *Manager) inputPeerForChat(accountID, peerType string, peerID, accessHash int64) (tg.InputPeerClass, error) {
	peerType = strings.ToLower(strings.TrimSpace(peerType))
	if peerID == 0 {
		return nil, fmt.Errorf("peerId required")
	}
	if accessHash == 0 && (peerType == "user" || peerType == "channel") {
		if ent, ok, _ := m.store.GetEntity(accountID, peerID, peerType); ok {
			accessHash = ent.AccessHash
		}
	}
	// megagroup 也走 channel；兼容旧缓存 peer_type=chat 误存
	if accessHash == 0 && peerType == "channel" {
		if ent, ok, _ := m.store.GetEntity(accountID, peerID, "chat"); ok {
			accessHash = ent.AccessHash
		}
	}
	switch peerType {
	case "user":
		if accessHash == 0 {
			return nil, fmt.Errorf("user access_hash required")
		}
		return &tg.InputPeerUser{UserID: peerID, AccessHash: accessHash}, nil
	case "channel":
		if accessHash == 0 {
			return nil, fmt.Errorf("channel/megagroup access_hash required（请先刷新会话列表）")
		}
		return &tg.InputPeerChannel{ChannelID: peerID, AccessHash: accessHash}, nil
	case "chat":
		return &tg.InputPeerChat{ChatID: peerID}, nil
	default:
		return nil, fmt.Errorf("peerType must be user|channel|chat")
	}
}

func unpackDialogsFull(res tg.MessagesDialogsClass) (dialogs []tg.DialogClass, chats []tg.ChatClass, messages []tg.MessageClass, users []tg.UserClass) {
	switch v := res.(type) {
	case *tg.MessagesDialogs:
		return v.Dialogs, v.Chats, v.Messages, v.Users
	case *tg.MessagesDialogsSlice:
		return v.Dialogs, v.Chats, v.Messages, v.Users
	default:
		return nil, nil, nil, nil
	}
}

/**
 * 规范化会话列表筛选：user / group / all。
 *
 * @param raw 原始参数
 * @return 规范值
 */
func normalizeDialogPeerKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "user", "users", "dm", "private":
		return "user"
	case "group", "groups", "channel", "channels", "chat", "chats":
		return "group"
	default:
		return "all"
	}
}

func dialogFromPeer(
	d tg.DialogClass,
	chatMap map[int64]tg.ChatClass,
	userMap map[int64]*tg.User,
	msgMap map[int]*tg.Message,
	peerKind string,
) (ChatDialog, string, bool) {
	peer := d.GetPeer()
	unread := 0
	if dialog, ok := d.(*tg.Dialog); ok && dialog != nil {
		unread = dialog.UnreadCount
	}
	topID := d.GetTopMessage()
	lastText := ""
	lastDate := 0
	if msg, ok := msgMap[topID]; ok && msg != nil {
		lastDate = msg.Date
		lastText = dialogLastPreview(msg)
	}

	switch p := peer.(type) {
	case *tg.PeerUser:
		if peerKind == "group" {
			return ChatDialog{}, "", false
		}
		u, ok := userMap[p.UserID]
		if !ok || u == nil {
			return ChatDialog{}, "", false
		}
		hash, _ := u.GetAccessHash()
		title := strings.TrimSpace(u.FirstName + " " + u.LastName)
		if title == "" {
			title = u.Username
		}
		if title == "" {
			title = fmt.Sprintf("用户 %d", u.ID)
		}
		return ChatDialog{
			PeerID:       u.ID,
			AccessHash:   formatAccessHash(hash),
			PeerType:     "user",
			Title:        title,
			Username:     u.Username,
			LastMessage:  lastText,
			LastDate:     lastDate,
			UnreadCount:  unread,
			TopMessageID: topID,
			IsBot:        u.Bot,
			IsSelf:       u.Self,
		}, fmt.Sprintf("user:%d", u.ID), true
	case *tg.PeerChannel:
		if peerKind == "user" {
			return ChatDialog{}, "", false
		}
		c, ok := chatMap[p.ChannelID].(*tg.Channel)
		if !ok || c == nil {
			return ChatDialog{}, "", false
		}
		return ChatDialog{
			PeerID:       c.ID,
			AccessHash:   formatAccessHash(c.AccessHash),
			PeerType:     "channel",
			Title:        c.Title,
			Username:     channelPublicUsername(c),
			LastMessage:  lastText,
			LastDate:     lastDate,
			UnreadCount:  unread,
			TopMessageID: topID,
		}, fmt.Sprintf("channel:%d", c.ID), true
	case *tg.PeerChat:
		if peerKind == "user" {
			return ChatDialog{}, "", false
		}
		c, ok := chatMap[p.ChatID].(*tg.Chat)
		if !ok || c == nil || c.Deactivated {
			return ChatDialog{}, "", false
		}
		return ChatDialog{
			PeerID:       c.ID,
			PeerType:     "chat",
			Title:        c.Title,
			LastMessage:  lastText,
			LastDate:     lastDate,
			UnreadCount:  unread,
			TopMessageID: topID,
		}, fmt.Sprintf("chat:%d", c.ID), true
	default:
		return ChatDialog{}, "", false
	}
}

func messageIDFromUpdates(u tg.UpdatesClass) int {
	switch v := u.(type) {
	case *tg.UpdateShortSentMessage:
		return v.ID
	case *tg.UpdateShortMessage:
		return v.ID
	case *tg.Updates:
		for _, up := range v.Updates {
			if m, ok := up.(*tg.UpdateNewMessage); ok {
				if msg, ok := m.Message.(*tg.Message); ok && msg != nil {
					return msg.ID
				}
			}
			if m, ok := up.(*tg.UpdateNewChannelMessage); ok {
				if msg, ok := m.Message.(*tg.Message); ok && msg != nil {
					return msg.ID
				}
			}
		}
	}
	return 0
}
