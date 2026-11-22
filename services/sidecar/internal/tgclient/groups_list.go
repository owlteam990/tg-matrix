package tgclient

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/tg-matrix/sidecar/internal/store"
)

// JoinedGroup 协议号已加入的群/频道摘要。
type JoinedGroup struct {
	PeerID         int64  `json:"peerId"`
	AccessHash     int64  `json:"accessHash,omitempty"`
	Title          string `json:"title"`
	Username       string `json:"username,omitempty"`
	Kind           string `json:"kind"` // channel | megagroup | group
	Participants   int    `json:"participants,omitempty"`
	IsCreator      bool   `json:"isCreator,omitempty"`
	IsAdmin        bool   `json:"isAdmin,omitempty"`
	JoinRequestOut bool   `json:"joinRequestOut,omitempty"` // 已发申请待审批
}

// JoinPreview 加群预检结果（手动验证用）。
type JoinPreview struct {
	AccountID      string `json:"accountId"`
	Target         string `json:"target"`
	OK             bool   `json:"ok"`
	Status         string `json:"status"` // can_join | need_approval | already_in | invite_expired | not_found | failed
	Title          string `json:"title,omitempty"`
	Username       string `json:"username,omitempty"`
	PeerID         int64  `json:"peerId,omitempty"`
	Participants   int    `json:"participants,omitempty"`
	RequestNeeded  bool   `json:"requestNeeded"`
	AlreadyIn      bool   `json:"alreadyIn"`
	InviteHash     string `json:"inviteHash,omitempty"`
	IsPublic       bool   `json:"isPublic"`
	BusinessResult string `json:"businessResult"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	Note           string `json:"note,omitempty"`
}

/**
 * 从 Telegram 拉取已加入群组并全量写入该协议号的本地 SQLite。
 * 仅应在用户手动「刷新」时调用。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param limit 最多条数
 * @return 最新列表
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) RefreshJoinedGroups(ctx context.Context, accountID string, limit int) ([]JoinedGroup, error) {
	items, err := m.FetchJoinedGroupsFromTG(ctx, accountID, limit)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	rows := make([]store.JoinedGroup, 0, len(items))
	for _, it := range items {
		rows = append(rows, store.JoinedGroup{
			AccountID:      accountID,
			PeerID:         it.PeerID,
			AccessHash:     it.AccessHash,
			Title:          it.Title,
			Username:       it.Username,
			Kind:           it.Kind,
			Participants:   it.Participants,
			IsCreator:      it.IsCreator,
			IsAdmin:        it.IsAdmin,
			JoinRequestOut: it.JoinRequestOut,
			SyncedAt:       now,
		})
		if it.AccessHash != 0 {
			peerType := "channel"
			if it.Kind == "group" {
				peerType = "chat"
			}
			_ = m.store.UpsertEntity(accountID, it.PeerID, it.AccessHash, peerType, it.Username)
		}
	}
	if err := m.store.ReplaceJoinedGroups(accountID, rows); err != nil {
		return nil, err
	}
	return items, nil
}

/**
 * 从 Telegram 拉取已加入群组/频道（不写库）。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param limit 最多条数（默认 100，上限 300）
 * @return 列表与错误
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) FetchJoinedGroupsFromTG(ctx context.Context, accountID string, limit int) ([]JoinedGroup, error) {
	return m.ListJoinedGroups(ctx, accountID, limit)
}

/**
 * 列出协议号已加入的群组/频道（含待审批申请中的频道）。向后兼容：内部仍打 TG。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param limit 最多条数（默认 100，上限 300）
 * @return 列表与错误
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) ListJoinedGroups(ctx context.Context, accountID string, limit int) ([]JoinedGroup, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 300 {
		limit = 300
	}

	var out []JoinedGroup
	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		offsetDate := 0
		offsetID := 0
		var offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
		seen := map[int64]struct{}{}

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

			dialogs, chats, messages := unpackDialogs(res)
			if len(dialogs) == 0 {
				break
			}

			chatMap := map[int64]tg.ChatClass{}
			for _, c := range chats {
				switch v := c.(type) {
				case *tg.Channel:
					chatMap[v.ID] = v
				case *tg.Chat:
					chatMap[v.ID] = v
				}
			}

			for _, d := range dialogs {
				peer := d.GetPeer()
				var id int64
				switch p := peer.(type) {
				case *tg.PeerChannel:
					id = p.ChannelID
				case *tg.PeerChat:
					id = p.ChatID
				default:
					continue
				}
				if _, ok := seen[id]; ok {
					continue
				}
				seen[id] = struct{}{}

				c, ok := chatMap[id]
				if !ok {
					continue
				}
				item, ok := joinedFromChat(c)
				if !ok {
					continue
				}
				out = append(out, item)
				if len(out) >= limit {
					break
				}
			}

			// 分页：取本批最后一条 dialog 的 top message
			last := dialogs[len(dialogs)-1]
			topMsgID := last.GetTopMessage()
			offsetID = topMsgID
			offsetPeer = inputPeerFromPeer(last.GetPeer(), chatMap)
			offsetDate = 0
			for _, msg := range messages {
				if m, ok := msg.(*tg.Message); ok && m.ID == topMsgID {
					offsetDate = m.Date
					break
				}
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
 * 预检加群目标：是否需管理员审批、是否已在群、邀请是否有效。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param target 用户名或邀请链接
 * @return 预检结果
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) PreviewJoin(ctx context.Context, accountID, target string) JoinPreview {
	target = strings.TrimSpace(target)
	out := JoinPreview{AccountID: accountID, Target: target, Status: "failed"}
	if target == "" {
		out.ErrorCode = "empty_target"
		out.ErrorMessage = "target required"
		out.BusinessResult = ResultFailed
		return out
	}

	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		if hash, ok := extractInviteHash(target); ok {
			out.InviteHash = hash
			invite, err := api.MessagesCheckChatInvite(ctx, hash)
			if err != nil {
				biz, code, msg, _ := classifyGroupErr(err, true)
				out.Status = biz
				out.BusinessResult = biz
				out.ErrorCode = code
				out.ErrorMessage = msg
				return nil
			}
			return fillPreviewFromInvite(&out, invite, m, accountID)
		}

		username := extractPublicUsername(target)
		if username == "" {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "invalid_target"
			out.ErrorMessage = "expect @username or invite link"
			out.Status = "failed"
			return nil
		}
		out.Username = username
		out.IsPublic = true

		full, err := m.lookupChannelByUsername(ctx, api, accountID, username)
		if err != nil {
			biz, code, msg, _ := classifyGroupErr(err, true)
			if code == "" || biz == ResultFailed {
				out.Status = ResultNotFound
				out.BusinessResult = ResultNotFound
				out.ErrorCode = "not_channel"
				out.ErrorMessage = err.Error()
				out.Note = err.Error()
			} else {
				out.Status = biz
				out.BusinessResult = biz
				out.ErrorCode = code
				out.ErrorMessage = msg
			}
			return nil
		}
		out.Title = full.Title
		out.PeerID = full.ID
		if uname := channelPublicUsername(full); uname != "" {
			out.Username = uname
		}
		out.Participants = full.ParticipantsCount
		fillPreviewFromChannel(&out, full)
		return nil
	})
	if err != nil {
		out.BusinessResult = ResultFailed
		out.Status = "failed"
		out.ErrorCode = "client_error"
		out.ErrorMessage = err.Error()
	}
	return out
}

/**
 * 检查目标群是否已真正入群（用于 need_approval 后的手动验证）。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param target 用户名或邀请链接
 * @return 预检形态结果（already_in / need_approval / failed）
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) CheckJoinStatus(ctx context.Context, accountID, target string) JoinPreview {
	preview := m.PreviewJoin(ctx, accountID, target)
	if preview.AlreadyIn || preview.Status == ResultAlreadyIn {
		preview.OK = true
		preview.Status = ResultAlreadyIn
		preview.BusinessResult = ResultAlreadyIn
		preview.Note = "已确认在群内"
		return preview
	}

	// 进一步用 GetParticipant 确认（公开频道）
	_ = m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		username := extractPublicUsername(target)
		if username == "" && preview.PeerID == 0 {
			return nil
		}
		var ch *tg.InputChannel
		var err error
		if username != "" {
			ch, err = m.resolveChannel(ctx, api, accountID, username)
			if err != nil {
				return nil
			}
		} else if preview.PeerID != 0 {
			ent, ok, _ := m.store.GetEntity(accountID, preview.PeerID, "channel")
			if !ok {
				return nil
			}
			ch = &tg.InputChannel{ChannelID: preview.PeerID, AccessHash: ent.AccessHash}
		}
		if ch == nil {
			return nil
		}
		_, err = api.ChannelsGetParticipant(ctx, &tg.ChannelsGetParticipantRequest{
			Channel:     ch,
			Participant: &tg.InputPeerSelf{},
		})
		if err == nil {
			preview.OK = true
			preview.AlreadyIn = true
			preview.Status = ResultAlreadyIn
			preview.BusinessResult = ResultAlreadyIn
			preview.PeerID = ch.ChannelID
			preview.Note = "已确认在群内（participant）"
			return nil
		}
		if rpc, ok := tgerr.As(err); ok && strings.Contains(rpc.Type, "USER_NOT_PARTICIPANT") {
			preview.OK = false
			preview.AlreadyIn = false
			if preview.RequestNeeded || preview.Status == ResultNeedApproval {
				preview.Status = ResultNeedApproval
				preview.BusinessResult = ResultNeedApproval
				preview.Note = "仍未入群：申请可能待管理员审批，请稍后再检查"
			} else {
				preview.Status = "not_joined"
				preview.BusinessResult = ResultFailed
				preview.Note = "尚未加入该群"
			}
			preview.ErrorCode = rpc.Type
			return nil
		}
		return nil
	})
	return preview
}

/**
 * 根据 Channel 实体填充预检状态（含需管理员审批的公开群）。
 *
 * @param out 预检结果
 * @param ch 频道/超级群
 */
func fillPreviewFromChannel(out *JoinPreview, ch *tg.Channel) {
	if ch == nil {
		out.Status = "failed"
		out.BusinessResult = ResultFailed
		out.ErrorCode = "nil_channel"
		return
	}
	// 已是成员（Left=false）
	if !ch.Left {
		out.AlreadyIn = true
		out.Status = ResultAlreadyIn
		out.BusinessResult = ResultAlreadyIn
		out.OK = true
		out.RequestNeeded = false
		out.Note = "已在群内"
		return
	}
	// JoinRequest=true 表示该群开启了「管理员审批加群」（不是机器人算术题）
	if ch.JoinRequest {
		out.RequestNeeded = true
		out.Status = ResultNeedApproval
		out.BusinessResult = ResultNeedApproval
		out.OK = true
		out.Note = "需管理员审批（非机器人验证题）。确认后提交申请；若报 CHANNEL_PRIVATE 请改用 t.me/+ 邀请链接。入群后若还有算术题再点「拉取验证题」"
		return
	}
	out.Status = "can_join"
	out.BusinessResult = ResultOK
	out.OK = true
	out.RequestNeeded = false
	out.Note = "可直接加入，确认后执行"
}

func fillPreviewFromInvite(out *JoinPreview, invite tg.ChatInviteClass, m *Manager, accountID string) error {
	switch v := invite.(type) {
	case *tg.ChatInviteAlready:
		out.AlreadyIn = true
		out.OK = true
		out.Status = ResultAlreadyIn
		out.BusinessResult = ResultAlreadyIn
		out.Note = "已在群内"
		if ch, ok := v.Chat.(*tg.Channel); ok && ch != nil {
			out.Title = ch.Title
			out.PeerID = ch.ID
			out.Username = channelPublicUsername(ch)
			_ = m.store.UpsertEntity(accountID, ch.ID, ch.AccessHash, "channel", out.Username)
		} else if chat, ok := v.Chat.(*tg.Chat); ok && chat != nil {
			out.Title = chat.Title
			out.PeerID = chat.ID
		}
	case *tg.ChatInvitePeek:
		out.Status = "can_join"
		out.BusinessResult = ResultOK
		out.OK = true
		out.Note = "可预览该群，确认后加入"
		if ch, ok := v.Chat.(*tg.Channel); ok && ch != nil {
			out.Title = ch.Title
			out.PeerID = ch.ID
			out.Username = channelPublicUsername(ch)
			_ = m.store.UpsertEntity(accountID, ch.ID, ch.AccessHash, "channel", out.Username)
		}
	case *tg.ChatInvite:
		out.Title = v.Title
		out.Participants = v.ParticipantsCount
		out.RequestNeeded = v.RequestNeeded
		out.IsPublic = v.Public
		if v.RequestNeeded {
			out.Status = ResultNeedApproval
			out.BusinessResult = ResultNeedApproval
			out.OK = true
			out.Note = "需管理员审批：确认后仅提交申请，入群后请点「检查入群」"
		} else {
			out.Status = "can_join"
			out.BusinessResult = ResultOK
			out.OK = true
			out.Note = "邀请有效，确认后直接加入"
		}
	default:
		out.Status = "failed"
		out.BusinessResult = ResultFailed
		out.ErrorCode = "unknown_invite"
		out.ErrorMessage = fmt.Sprintf("%T", invite)
	}
	return nil
}

func unpackDialogs(res tg.MessagesDialogsClass) (dialogs []tg.DialogClass, chats []tg.ChatClass, messages []tg.MessageClass) {
	switch v := res.(type) {
	case *tg.MessagesDialogs:
		return v.Dialogs, v.Chats, v.Messages
	case *tg.MessagesDialogsSlice:
		return v.Dialogs, v.Chats, v.Messages
	default:
		return nil, nil, nil
	}
}

/**
 * 读取频道/超群公开用户名（兼容 Username 与 Usernames[] 新字段）。
 *
 * @param ch 频道实体
 * @return 活跃公开用户名（不含 @）
 */
func channelPublicUsername(ch *tg.Channel) string {
	if ch == nil {
		return ""
	}
	if ch.Username != "" {
		return ch.Username
	}
	if list, ok := ch.GetUsernames(); ok {
		for _, u := range list {
			if u.Active && strings.TrimSpace(u.Username) != "" {
				return strings.TrimSpace(u.Username)
			}
		}
		for _, u := range list {
			if strings.TrimSpace(u.Username) != "" {
				return strings.TrimSpace(u.Username)
			}
		}
	}
	return ""
}

func joinedFromChat(c tg.ChatClass) (JoinedGroup, bool) {
	switch v := c.(type) {
	case *tg.Channel:
		if v == nil {
			return JoinedGroup{}, false
		}
		kind := "channel"
		if v.Megagroup {
			kind = "megagroup"
		}
		_, isAdmin := v.GetAdminRights()
		return JoinedGroup{
			PeerID:         v.ID,
			AccessHash:     v.AccessHash,
			Title:          v.Title,
			Username:       channelPublicUsername(v),
			Kind:           kind,
			Participants:   v.ParticipantsCount,
			IsCreator:      v.Creator,
			IsAdmin:        isAdmin,
			JoinRequestOut: v.JoinRequest && v.Left,
		}, true
	case *tg.Chat:
		if v == nil || v.Deactivated {
			return JoinedGroup{}, false
		}
		_, isAdmin := v.GetAdminRights()
		return JoinedGroup{
			PeerID:       v.ID,
			Title:        v.Title,
			Kind:         "group",
			Participants: v.ParticipantsCount,
			IsCreator:    v.Creator,
			IsAdmin:      isAdmin,
		}, true
	default:
		return JoinedGroup{}, false
	}
}

func inputPeerFromPeer(peer tg.PeerClass, chats map[int64]tg.ChatClass) tg.InputPeerClass {
	switch p := peer.(type) {
	case *tg.PeerChannel:
		if c, ok := chats[p.ChannelID].(*tg.Channel); ok && c != nil {
			return &tg.InputPeerChannel{ChannelID: c.ID, AccessHash: c.AccessHash}
		}
		return &tg.InputPeerChannel{ChannelID: p.ChannelID}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}
	case *tg.PeerUser:
		return &tg.InputPeerUser{UserID: p.UserID}
	default:
		return &tg.InputPeerEmpty{}
	}
}
