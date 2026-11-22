package tgclient

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// 群组操作结果常量（F06/F07）。
const (
	ResultNeedApproval  = "need_approval"
	ResultAlreadyIn     = "already_in"
	ResultInviteExpired = "invite_expired"
	ResultJoinLimit     = "join_limit"
	ResultPrivate       = "private"
	ResultLeft          = "left"
)

// GroupItemResult 单条加群/退群结果。
type GroupItemResult struct {
	AccountID      string `json:"accountId"`
	Target         string `json:"target"`
	Action         string `json:"action"`
	OK             bool   `json:"ok"`
	BusinessResult string `json:"businessResult"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	PeerID         int64  `json:"peerId,omitempty"`
	WaitSeconds    int    `json:"waitSeconds,omitempty"`
}

var (
	reInvitePlus     = regexp.MustCompile(`(?i)(?:https?://)?(?:t\.me|telegram\.me)/\+([A-Za-z0-9_-]+)`)
	reInviteJoinchat = regexp.MustCompile(`(?i)(?:https?://)?(?:t\.me|telegram\.me)/joinchat/([A-Za-z0-9_-]+)`)
	rePublicLink     = regexp.MustCompile(`(?i)(?:https?://)?(?:t\.me|telegram\.me)/([A-Za-z][A-Za-z0-9_]{3,})/?$`)
)

/**
 * 加入群组/频道（F06）：支持公开用户名或邀请链接。
 *
 * @param ctx 上下文
 * @param accountID 执行协议号
 * @param target 用户名或邀请链接
 * @return 单条结果
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) JoinGroup(ctx context.Context, accountID, target string) GroupItemResult {
	target = strings.TrimSpace(target)
	out := GroupItemResult{AccountID: accountID, Target: target, Action: "join"}
	if target == "" {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "empty_target"
		out.ErrorMessage = "target required"
		return out
	}

	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		if hash, ok := extractInviteHash(target); ok {
			updates, err := api.MessagesImportChatInvite(ctx, hash)
			if err != nil {
				biz, code, msg, wait := classifyGroupErr(err, true)
				out.BusinessResult = biz
				out.ErrorCode = code
				out.ErrorMessage = msg
				out.WaitSeconds = wait
				// 已在群算成功；需审批仅提交申请，须手动「检查入群」后才算加入成功
				if biz == ResultAlreadyIn {
					out.OK = true
				}
				return nil
			}
			peerID := cacheChannelsFromUpdates(m, accountID, updates)
			out.OK = true
			out.BusinessResult = ResultOK
			out.PeerID = peerID
			return nil
		}

		username := extractPublicUsername(target)
		if username == "" {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "invalid_target"
			out.ErrorMessage = "expect @username or invite link"
			return nil
		}

		full, err := m.lookupChannelByUsername(ctx, api, accountID, username)
		if err != nil {
			biz, code, msg, wait := classifyGroupErr(err, true)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.WaitSeconds = wait
			return nil
		}
		// 必须用 AsInput，并确保 access_hash 标志位有效（合成 Forbidden 时需补齐）
		ensureChannelAccessHash(full)
		ch := full.AsInput()
		if ch == nil || ch.ChannelID == 0 {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "invalid_input_channel"
			out.ErrorMessage = "empty InputChannel"
			return nil
		}
		if _, ok := full.GetAccessHash(); !ok || ch.AccessHash == 0 {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "missing_access_hash"
			out.ErrorMessage = fmt.Sprintf("channel min=%v joinRequest=%v left=%v accessHash missing", full.Min, full.JoinRequest, full.Left)
			return nil
		}

		_, err = api.ChannelsJoinChannel(ctx, ch)
		if err != nil {
			biz, code, msg, wait := classifyGroupErr(err, true)
			// 需审批公开群：JoinChannel 成功提交申请时应为 INVITE_REQUEST_SENT。
			// 若返回 CHANNEL_PRIVATE，再 GetFullChannel 后重试一次；仍失败则给出可操作提示。
			if biz == ResultPrivate && full.JoinRequest {
				if retried := m.retryJoinAfterFull(ctx, api, accountID, full); retried != nil {
					out = *retried
					out.AccountID = accountID
					out.Target = target
					out.Action = "join"
					if out.BusinessResult == ResultPrivate {
						out.ErrorMessage = "需审批公开群拒绝公开加入(CHANNEL_PRIVATE)。请改用邀请链接 t.me/+hash，或换协议号重试"
					}
					return nil
				}
			}
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.WaitSeconds = wait
			out.PeerID = full.ID
			if biz == ResultAlreadyIn {
				out.OK = true
			}
			return nil
		}
		out.OK = true
		out.BusinessResult = ResultOK
		out.PeerID = ch.ChannelID
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
 * 退出群组/频道（F07）。
 *
 * @param ctx 上下文
 * @param accountID 执行协议号
 * @param target 用户名、邀请链接、peer:ID:accessHash（频道/超级群）或 chat:ID（基础群）
 * @return 单条结果
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) LeaveGroup(ctx context.Context, accountID, target string) GroupItemResult {
	target = strings.TrimSpace(target)
	out := GroupItemResult{AccountID: accountID, Target: target, Action: "leave"}
	if target == "" {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "empty_target"
		out.ErrorMessage = "target required"
		return out
	}

	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		// 1. 已加入列表勾选：peer:channelID:accessHash
		if peerID, accessHash, ok := parsePeerTarget(target); ok {
			_ = m.store.UpsertEntity(accountID, peerID, accessHash, "channel", "")
			return m.leaveChannel(ctx, api, &out, &tg.InputChannel{ChannelID: peerID, AccessHash: accessHash})
		}
		// 2. 基础群：chat:chatID
		if chatID, ok := parseChatTarget(target); ok {
			return m.leaveBasicChat(ctx, api, &out, chatID)
		}

		username := extractPublicUsername(target)
		if username == "" {
			// 邀请链接：先 check 拿频道再退出
			if hash, ok := extractInviteHash(target); ok {
				invite, err := api.MessagesCheckChatInvite(ctx, hash)
				if err != nil {
					biz, code, msg, wait := classifyGroupErr(err, false)
					out.BusinessResult = biz
					out.ErrorCode = code
					out.ErrorMessage = msg
					out.WaitSeconds = wait
					return nil
				}
				ch, ok := channelFromInvite(invite)
				if !ok {
					out.BusinessResult = ResultFailed
					out.ErrorCode = "invite_no_channel"
					out.ErrorMessage = "invite does not expose channel for leave"
					return nil
				}
				_ = m.store.UpsertEntity(accountID, ch.ID, ch.AccessHash, "channel", ch.Username)
				return m.leaveChannel(ctx, api, &out, &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash})
			}
			out.BusinessResult = ResultFailed
			out.ErrorCode = "invalid_target"
			out.ErrorMessage = "expect @username、invite link、peer:id:hash 或 chat:id"
			return nil
		}

		ch, err := m.resolveChannel(ctx, api, accountID, username)
		if err != nil {
			biz, code, msg, wait := classifyGroupErr(err, false)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.WaitSeconds = wait
			return nil
		}
		return m.leaveChannel(ctx, api, &out, ch)
	})
	if err != nil {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "client_error"
		out.ErrorMessage = err.Error()
	}
	return out
}

/**
 * 解析 peer:ID:accessHash 目标（频道/超级群）。
 *
 * @param target 原始目标串
 * @return peerID、accessHash、是否匹配
 */
func parsePeerTarget(target string) (peerID, accessHash int64, ok bool) {
	s := strings.TrimSpace(target)
	if !strings.HasPrefix(strings.ToLower(s), "peer:") {
		return 0, 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, 0, false
	}
	id, err1 := strconv.ParseInt(parts[1], 10, 64)
	hash, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || id == 0 {
		return 0, 0, false
	}
	return id, hash, true
}

/**
 * 解析 chat:ID 目标（基础群）。
 *
 * @param target 原始目标串
 * @return chatID、是否匹配
 */
func parseChatTarget(target string) (chatID int64, ok bool) {
	s := strings.TrimSpace(target)
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "chat:") {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(s[5:]), 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

func (m *Manager) leaveChannel(ctx context.Context, api *tg.Client, out *GroupItemResult, ch *tg.InputChannel) error {
	_, err := api.ChannelsLeaveChannel(ctx, ch)
	if err != nil {
		biz, code, msg, wait := classifyGroupErr(err, false)
		out.BusinessResult = biz
		out.ErrorCode = code
		out.ErrorMessage = msg
		out.WaitSeconds = wait
		return nil
	}
	out.OK = true
	out.BusinessResult = ResultLeft
	out.PeerID = ch.ChannelID
	return nil
}

/**
 * 退出基础群（messages.deleteChatUser 踢出自己）。
 *
 * @param ctx 上下文
 * @param api Telegram API 客户端
 * @param out 结果写入
 * @param chatID 基础群 ID
 * @return 始终 nil（业务错误写入 out）
 */
func (m *Manager) leaveBasicChat(ctx context.Context, api *tg.Client, out *GroupItemResult, chatID int64) error {
	_, err := api.MessagesDeleteChatUser(ctx, &tg.MessagesDeleteChatUserRequest{
		ChatID: chatID,
		UserID: &tg.InputUserSelf{},
	})
	if err != nil {
		biz, code, msg, wait := classifyGroupErr(err, false)
		out.BusinessResult = biz
		out.ErrorCode = code
		out.ErrorMessage = msg
		out.WaitSeconds = wait
		return nil
	}
	out.OK = true
	out.BusinessResult = ResultLeft
	out.PeerID = chatID
	return nil
}

func (m *Manager) resolveChannel(ctx context.Context, api *tg.Client, accountID, username string) (*tg.InputChannel, error) {
	ch, err := m.lookupChannelByUsername(ctx, api, accountID, username)
	if err != nil {
		return nil, err
	}
	ensureChannelAccessHash(ch)
	in := ch.AsInput()
	if in == nil || in.ChannelID == 0 {
		return nil, fmt.Errorf("empty InputChannel")
	}
	return in, nil
}

/**
 * 补齐 Channel.AccessHash 的 flags 位（从 Forbidden 合成时可能未置位）。
 *
 * @param ch 频道实体
 */
func ensureChannelAccessHash(ch *tg.Channel) {
	if ch == nil {
		return
	}
	if _, ok := ch.GetAccessHash(); ok {
		return
	}
	if ch.AccessHash != 0 {
		ch.SetAccessHash(ch.AccessHash)
	}
}

/**
 * GetFullChannel 刷新后再试 JoinChannel（处理需审批群的 CHANNEL_PRIVATE）。
 *
 * @return 非 nil 表示已产出最终结果
 */
func (m *Manager) retryJoinAfterFull(ctx context.Context, api *tg.Client, accountID string, full *tg.Channel) *GroupItemResult {
	ensureChannelAccessHash(full)
	in := full.AsInput()
	res, err := api.ChannelsGetFullChannel(ctx, in)
	if err == nil && res != nil {
		for _, c := range res.Chats {
			if ch, ok := c.(*tg.Channel); ok && ch != nil && ch.ID == full.ID {
				ensureChannelAccessHash(ch)
				_ = m.store.UpsertEntity(accountID, ch.ID, ch.AccessHash, "channel", ch.Username)
				full = ch
				in = ch.AsInput()
				break
			}
		}
	}
	_, err = api.ChannelsJoinChannel(ctx, in)
	out := &GroupItemResult{PeerID: full.ID}
	if err != nil {
		biz, code, msg, wait := classifyGroupErr(err, true)
		out.BusinessResult = biz
		out.ErrorCode = code
		out.ErrorMessage = msg
		out.WaitSeconds = wait
		if biz == ResultAlreadyIn {
			out.OK = true
		}
		return out
	}
	out.OK = true
	out.BusinessResult = ResultOK
	return out
}

/**
 * 按公开用户名解析频道/超级群实体。
 * 需管理员审批的公开群常返回 ChannelForbidden，仍可用 access_hash 加入/提交申请。
 *
 * @param ctx 上下文
 * @param api Telegram 客户端
 * @param accountID 协议号
 * @param username 不含 @ 的用户名
 * @return Channel 实体（Forbidden 时合成 Left+JoinRequest 便于预检）
 */
func (m *Manager) lookupChannelByUsername(ctx context.Context, api *tg.Client, accountID, username string) (*tg.Channel, error) {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return nil, err
	}

	// 1. 正常 Channel
	for _, c := range resolved.Chats {
		ch, ok := c.(*tg.Channel)
		if !ok || ch == nil {
			continue
		}
		_ = m.store.UpsertEntity(accountID, ch.ID, ch.AccessHash, "channel", firstNonEmpty(channelPublicUsername(ch), username))
		return ch, nil
	}

	// 2. ChannelForbidden：尝试 GetChannels 升级；失败则合成可加入实体
	for _, c := range resolved.Chats {
		fb, ok := c.(*tg.ChannelForbidden)
		if !ok || fb == nil {
			continue
		}
		if upgraded := m.tryUpgradeForbiddenChannel(ctx, api, accountID, fb, username); upgraded != nil {
			return upgraded, nil
		}
		ch := channelFromForbidden(fb, username)
		_ = m.store.UpsertEntity(accountID, ch.ID, ch.AccessHash, "channel", username)
		return ch, nil
	}

	// 3. Peer 指向频道但 chats 异常为空
	if p, ok := resolved.Peer.(*tg.PeerChannel); ok && p != nil {
		return nil, fmt.Errorf("频道 peer=%d 无实体（chats 空），无法加入", p.ChannelID)
	}
	if p, ok := resolved.Peer.(*tg.PeerUser); ok && p != nil {
		uname := ""
		for _, u := range resolved.Users {
			if user, ok := u.(*tg.User); ok && user != nil && user.ID == p.UserID {
				uname = user.Username
				break
			}
		}
		return nil, fmt.Errorf("用户名解析为用户 id=%d @%s（不是群/频道）", p.UserID, uname)
	}
	return nil, fmt.Errorf("resolved peer is not a channel/supergroup peer=%T chats=%d", resolved.Peer, len(resolved.Chats))
}

/**
 * 尝试用 channels.getChannels 把 Forbidden 升级为完整 Channel。
 */
func (m *Manager) tryUpgradeForbiddenChannel(
	ctx context.Context,
	api *tg.Client,
	accountID string,
	fb *tg.ChannelForbidden,
	username string,
) *tg.Channel {
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: fb.ID, AccessHash: fb.AccessHash},
	})
	if err != nil {
		return nil
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
		if !ok || ch == nil {
			continue
		}
		_ = m.store.UpsertEntity(accountID, ch.ID, ch.AccessHash, "channel", firstNonEmpty(ch.Username, username))
		return ch
	}
	return nil
}

/**
 * 从 ChannelForbidden 合成预检/加入用 Channel（按需审批公开群常见形态）。
 */
func channelFromForbidden(fb *tg.ChannelForbidden, username string) *tg.Channel {
	ch := &tg.Channel{
		ID:          fb.ID,
		Title:       fb.Title,
		Left:        true,
		JoinRequest: true, // Forbidden 场景无法读到开关，按需审批处理；实际以 JoinChannel 返回为准
		Broadcast:   fb.Broadcast,
		Megagroup:   fb.Megagroup,
	}
	ch.SetAccessHash(fb.AccessHash)
	if username != "" {
		ch.SetUsername(username)
	}
	return ch
}


func extractInviteHash(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if m := reInvitePlus.FindStringSubmatch(s); len(m) == 2 {
		return m[1], true
	}
	if m := reInviteJoinchat.FindStringSubmatch(s); len(m) == 2 {
		return m[1], true
	}
	// 纯 hash（较长、无 @）
	if !strings.ContainsAny(s, "/@.") && len(s) >= 16 {
		return s, true
	}
	return "", false
}

func extractPublicUsername(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "@")
	if m := rePublicLink.FindStringSubmatch(s); len(m) == 2 {
		name := m[1]
		lower := strings.ToLower(name)
		if lower == "joinchat" || lower == "addstickers" || lower == "share" {
			return ""
		}
		return name
	}
	// 可能是完整 URL，尝试 path
	if strings.Contains(s, "://") || strings.HasPrefix(strings.ToLower(s), "t.me/") {
		u, err := url.Parse(s)
		if err == nil {
			path := strings.Trim(u.Path, "/")
			if path != "" && !strings.HasPrefix(path, "+") && !strings.HasPrefix(path, "joinchat/") {
				parts := strings.Split(path, "/")
				if len(parts) > 0 && parts[0] != "" {
					return parts[0]
				}
			}
		}
		return ""
	}
	if strings.ContainsAny(s, "/ ") {
		return ""
	}
	return s
}

func channelFromInvite(invite tg.ChatInviteClass) (*tg.Channel, bool) {
	switch v := invite.(type) {
	case *tg.ChatInviteAlready:
		ch, ok := v.Chat.(*tg.Channel)
		return ch, ok && ch != nil
	case *tg.ChatInvitePeek:
		ch, ok := v.Chat.(*tg.Channel)
		return ch, ok && ch != nil
	default:
		return nil, false
	}
}

func cacheChannelsFromUpdates(m *Manager, accountID string, updates tg.UpdatesClass) int64 {
	var peerID int64
	switch u := updates.(type) {
	case *tg.Updates:
		for _, c := range u.Chats {
			if ch, ok := c.(*tg.Channel); ok && ch != nil {
				_ = m.store.UpsertEntity(accountID, ch.ID, ch.AccessHash, "channel", ch.Username)
				peerID = ch.ID
			}
		}
	case *tg.UpdatesCombined:
		for _, c := range u.Chats {
			if ch, ok := c.(*tg.Channel); ok && ch != nil {
				_ = m.store.UpsertEntity(accountID, ch.ID, ch.AccessHash, "channel", ch.Username)
				peerID = ch.ID
			}
		}
	}
	return peerID
}

func classifyGroupErr(err error, isJoin bool) (biz, code, msg string, waitSec int) {
	if d, ok := tgerr.AsFloodWait(err); ok {
		return ResultFlood, "FLOOD_WAIT", err.Error(), int(d / time.Second)
	}
	rpc, ok := tgerr.As(err)
	if !ok {
		return ResultFailed, "group_failed", err.Error(), 0
	}
	code = rpc.Type
	msg = err.Error()
	switch {
	case strings.Contains(code, "INVITE_REQUEST_SENT"):
		return ResultNeedApproval, code, msg, 0
	case strings.Contains(code, "USER_ALREADY_PARTICIPANT"):
		return ResultAlreadyIn, code, msg, 0
	case strings.Contains(code, "INVITE_HASH_EXPIRED") || strings.Contains(code, "INVITE_HASH_INVALID"):
		return ResultInviteExpired, code, msg, 0
	case strings.Contains(code, "CHANNELS_TOO_MUCH") || strings.Contains(code, "USER_CHANNELS_TOO_MUCH"):
		return ResultJoinLimit, code, msg, 0
	case strings.Contains(code, "CHANNEL_PRIVATE") || strings.Contains(code, "CHAT_WRITE_FORBIDDEN"):
		return ResultPrivate, code, msg, 0
	case strings.Contains(code, "USERNAME_NOT_OCCUPIED") || strings.Contains(code, "USERNAME_INVALID"):
		return ResultNotFound, code, msg, 0
	default:
		_ = isJoin
		return ResultFailed, code, msg, 0
	}
}
