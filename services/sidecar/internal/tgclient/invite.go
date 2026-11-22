package tgclient

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// 邀请模式与结果常量（F08/F09）。
const (
	InviteModeContact  = "contact"
	InviteModeUsername = "username"

	ResultNoPermission = "no_permission"
	ResultNotMutual    = "not_mutual"
	ResultInvited      = "invited"
)

// InviteItemResult 单条邀请结果。
type InviteItemResult struct {
	AccountID      string `json:"accountId"`
	Group          string `json:"group"`
	Member         string `json:"member"`
	Mode           string `json:"mode"`
	OK             bool   `json:"ok"`
	BusinessResult string `json:"businessResult"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	PeerID         int64  `json:"peerId,omitempty"`
	WaitSeconds    int    `json:"waitSeconds,omitempty"`
}

/**
 * 邀请成员入群/频道（F08 联系人 / F09 用户名）。
 *
 * @param ctx 上下文
 * @param accountID 执行协议号
 * @param group 目标群用户名或链接
 * @param member 成员标识（联系人手机号/用户名/ID，或公开用户名）
 * @param mode contact | username
 * @return 单条结果
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) InviteToGroup(ctx context.Context, accountID, group, member, mode string) InviteItemResult {
	group = strings.TrimSpace(group)
	member = strings.TrimSpace(member)
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = InviteModeUsername
	}
	out := InviteItemResult{
		AccountID: accountID,
		Group:     group,
		Member:    member,
		Mode:      mode,
	}
	if group == "" || member == "" {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "invalid_input"
		out.ErrorMessage = "group and member required"
		return out
	}
	if mode != InviteModeContact && mode != InviteModeUsername {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "invalid_mode"
		out.ErrorMessage = "mode must be contact|username"
		return out
	}

	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		ch, err := m.resolveGroupChannel(ctx, api, accountID, group)
		if err != nil {
			biz, code, msg, wait := classifyInviteErr(err)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.WaitSeconds = wait
			if msg == "" {
				out.ErrorMessage = err.Error()
			}
			return nil
		}

		user, peerID, biz, code, msg, wait, err := m.resolveInviteMember(ctx, api, accountID, member, mode)
		if err != nil || user == nil {
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.WaitSeconds = wait
			if msg == "" && err != nil {
				out.ErrorMessage = err.Error()
			}
			return nil
		}
		out.PeerID = peerID

		_, err = api.ChannelsInviteToChannel(ctx, &tg.ChannelsInviteToChannelRequest{
			Channel: ch,
			Users:   []tg.InputUserClass{user},
		})
		if err != nil {
			biz, code, msg, wait := classifyInviteErr(err)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.WaitSeconds = wait
			// 已在群视为可接受成功
			if biz == ResultAlreadyIn {
				out.OK = true
			}
			return nil
		}
		out.OK = true
		out.BusinessResult = ResultInvited
		return nil
	})
	if err != nil {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "client_error"
		out.ErrorMessage = err.Error()
	}
	return out
}

func (m *Manager) resolveGroupChannel(
	ctx context.Context,
	api *tg.Client,
	accountID, group string,
) (*tg.InputChannel, error) {
	username := extractPublicUsername(group)
	if username == "" {
		return nil, fmt.Errorf("group must be public @username (invite links not supported for invite action)")
	}
	return m.resolveChannel(ctx, api, accountID, username)
}

func (m *Manager) resolveInviteMember(
	ctx context.Context,
	api *tg.Client,
	accountID, member, mode string,
) (*tg.InputUser, int64, string, string, string, int, error) {
	if mode == InviteModeContact {
		return m.resolveMemberAsContact(ctx, api, accountID, member)
	}
	// F09：按用户名（也允许本号实体缓存的 user id）
	raw := strings.TrimSpace(member)
	if strings.HasPrefix(raw, "@") || looksLikeUsername(raw) {
		username := strings.TrimPrefix(raw, "@")
		resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
		if err != nil {
			biz, code, msg, wait := classifyInviteErr(err)
			return nil, 0, biz, code, msg, wait, err
		}
		user, ok := findUserFromResolved(resolved)
		if !ok {
			return nil, 0, ResultNotFound, "peer_not_user", "resolved peer is not a user", 0, nil
		}
		_ = m.store.UpsertEntity(accountID, user.ID, user.AccessHash, "user", user.Username)
		return &tg.InputUser{UserID: user.ID, AccessHash: user.AccessHash}, user.ID, ResultOK, "", "", 0, nil
	}
	if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
		ent, ok, err := m.store.GetEntity(accountID, id, "user")
		if err != nil {
			return nil, 0, ResultFailed, "db_error", err.Error(), 0, err
		}
		if !ok || ent.AccessHash == 0 {
			return nil, 0, ResultNoHash, "missing_access_hash",
				"缺少实体缓存，请先通过用户名建立实体", 0, nil
		}
		return &tg.InputUser{UserID: ent.PeerID, AccessHash: ent.AccessHash}, ent.PeerID, ResultOK, "", "", 0, nil
	}
	return nil, 0, ResultFailed, "invalid_member", "expect @username or user id", 0, fmt.Errorf("invalid member")
}

func (m *Manager) resolveMemberAsContact(
	ctx context.Context,
	api *tg.Client,
	accountID, member string,
) (*tg.InputUser, int64, string, string, string, int, error) {
	raw := strings.TrimSpace(member)

	// 仅从本协议号通讯录匹配（单向保存也可能邀请失败）
	if strings.HasPrefix(raw, "@") || looksLikeUsername(raw) {
		username := strings.ToLower(strings.TrimPrefix(raw, "@"))
		return m.findInContacts(ctx, api, accountID, func(u *tg.User) bool {
			return strings.EqualFold(u.Username, username)
		})
	}
	if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 && !strings.HasPrefix(raw, "+") {
		return m.findInContacts(ctx, api, accountID, func(u *tg.User) bool { return u.ID == id })
	}
	phone := normalizePhone(raw)
	if phone != "" {
		return m.findInContacts(ctx, api, accountID, func(u *tg.User) bool {
			up := normalizePhone(u.Phone)
			return up == phone || strings.HasSuffix(up, strings.TrimPrefix(phone, "+"))
		})
	}
	return nil, 0, ResultFailed, "invalid_member", "expect contact phone, @username or user id", 0, fmt.Errorf("invalid member")
}

func classifyInviteErr(err error) (biz, code, msg string, waitSec int) {
	if d, ok := tgerr.AsFloodWait(err); ok {
		return ResultFlood, "FLOOD_WAIT", err.Error(), int(d / time.Second)
	}
	rpc, ok := tgerr.As(err)
	if !ok {
		return ResultFailed, "invite_failed", err.Error(), 0
	}
	code = rpc.Type
	msg = err.Error()
	switch {
	case strings.Contains(code, "CHAT_ADMIN_REQUIRED") || strings.Contains(code, "CHAT_WRITE_FORBIDDEN"):
		return ResultNoPermission, code, msg, 0
	case strings.Contains(code, "USER_PRIVACY") || strings.Contains(code, "PRIVACY"):
		return ResultPrivacy, code, msg, 0
	case strings.Contains(code, "USER_NOT_MUTUAL_CONTACT"):
		return ResultNotMutual, code, msg, 0
	case strings.Contains(code, "USER_ALREADY_PARTICIPANT"):
		return ResultAlreadyIn, code, msg, 0
	case strings.Contains(code, "USERS_TOO_MUCH") || strings.Contains(code, "USER_CHANNELS_TOO_MUCH"):
		return ResultJoinLimit, code, msg, 0
	case strings.Contains(code, "USERNAME_NOT_OCCUPIED") || strings.Contains(code, "USERNAME_INVALID"):
		return ResultNotFound, code, msg, 0
	case strings.Contains(code, "USER_IS_BLOCKED"):
		return ResultBlocked, code, msg, 0
	case strings.Contains(code, "USER_DEACTIVATED") || strings.Contains(code, "INPUT_USER_DEACTIVATED"):
		return ResultDeactivated, code, msg, 0
	default:
		return ResultFailed, code, msg, 0
	}
}
