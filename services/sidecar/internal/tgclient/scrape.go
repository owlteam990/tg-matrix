package tgclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"
)

// 采集来源常量（F23，每行必带）。
const (
	SourceMembers  = "members"
	SourceSpeakers = "speakers"
)

// ScrapeRow 采集结果行；隐私字段为空则保持空，不补假数据。
type ScrapeRow struct {
	Source    string `json:"source"`
	UserID    int64  `json:"userId"`
	Username  string `json:"username"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Phone     string `json:"phone"`
	IsBot     bool   `json:"isBot"`
	AccountID string `json:"accountId"`
	Group     string `json:"group"`
	// Digest 发言摘要（仅 speakers 有值；多条用 " | " 拼接，供 AI 分析）
	Digest string `json:"digest,omitempty"`
	// MessageCount 该用户在本次历史窗内被采到的文本条数
	MessageCount int `json:"messageCount,omitempty"`
}

// ScrapeResult 采集任务结果。
type ScrapeResult struct {
	OK             bool        `json:"ok"`
	AccountID      string      `json:"accountId"`
	Group          string      `json:"group"`
	Sources        []string    `json:"sources"`
	Rows           []ScrapeRow `json:"rows"`
	MembersCount   int         `json:"membersCount"`
	SpeakersCount  int         `json:"speakersCount"`
	BusinessResult string      `json:"businessResult,omitempty"`
	ErrorCode      string      `json:"errorCode,omitempty"`
	ErrorMessage   string      `json:"errorMessage,omitempty"`
	Note           string      `json:"note"`
}

/**
 * 采集群成员与/或历史发言者（F23）。每行带 source；不承诺隐藏成员完整。
 *
 * @param ctx 上下文
 * @param accountID 执行协议号
 * @param group 公开群用户名，或 peer:id:accessHash（已加入无用户名群）
 * @param sources 来源：members / speakers（可多选）
 * @param memberLimit 成员页上限（建议 ≤1000）
 * @param historyLimit 历史消息条数上限（用于发言者）
 * @return 采集结果
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) ScrapeGroup(
	ctx context.Context,
	accountID, group string,
	sources []string,
	memberLimit, historyLimit int,
) ScrapeResult {
	group = strings.TrimSpace(group)
	out := ScrapeResult{
		AccountID: accountID,
		Group:     group,
		Sources:   sources,
		Rows:      []ScrapeRow{},
		Note:      "成员列表与发言者集合必须区分；隐藏成员不保证完整；空隐私字段保持空",
	}
	if group == "" {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "group_required"
		out.ErrorMessage = "group required"
		return out
	}
	wantMembers, wantSpeakers := false, false
	for _, s := range sources {
		switch strings.TrimSpace(s) {
		case SourceMembers:
			wantMembers = true
		case SourceSpeakers:
			wantSpeakers = true
		}
	}
	if !wantMembers && !wantSpeakers {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "sources_required"
		out.ErrorMessage = "select members and/or speakers"
		return out
	}
	if memberLimit <= 0 {
		memberLimit = 200
	}
	if memberLimit > 2000 {
		memberLimit = 2000
	}
	if historyLimit <= 0 {
		historyLimit = 200
	}
	if historyLimit > 2000 {
		historyLimit = 2000
	}

	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		target, err := m.resolveScrapeTarget(ctx, api, accountID, group)
		if err != nil {
			biz, code, msg, _ := classifyGroupErr(err, false)
			out.BusinessResult = biz
			out.ErrorCode = code
			if code == "" {
				out.ErrorCode = "invalid_group"
			}
			out.ErrorMessage = msg
			if msg == "" {
				out.ErrorMessage = err.Error()
			}
			return nil
		}

		seen := map[string]struct{}{} // source:userId
		if wantMembers {
			rows, err := m.scrapeMembers(ctx, api, accountID, group, target, memberLimit)
			if err != nil {
				biz, code, msg, _ := classifyGroupErr(err, false)
				out.BusinessResult = biz
				out.ErrorCode = code
				out.ErrorMessage = msg
				if msg == "" {
					out.ErrorMessage = err.Error()
				}
				return nil
			}
			for _, row := range rows {
				key := row.Source + ":" + fmt.Sprintf("%d", row.UserID)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out.Rows = append(out.Rows, row)
				out.MembersCount++
			}
		}
		if wantSpeakers {
			rows, err := m.scrapeSpeakers(ctx, api, accountID, group, target, historyLimit)
			if err != nil {
				biz, code, msg, _ := classifyGroupErr(err, false)
				out.BusinessResult = biz
				out.ErrorCode = code
				out.ErrorMessage = msg
				if msg == "" {
					out.ErrorMessage = err.Error()
				}
				return nil
			}
			for _, row := range rows {
				key := row.Source + ":" + fmt.Sprintf("%d", row.UserID)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out.Rows = append(out.Rows, row)
				out.SpeakersCount++
			}
		}
		out.OK = true
		out.BusinessResult = ResultOK
		return nil
	})
	if err != nil {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "client_error"
		out.ErrorMessage = err.Error()
	}
	return out
}

func (m *Manager) scrapeMembers(
	ctx context.Context,
	api *tg.Client,
	accountID, group string,
	target scrapeTarget,
	limit int,
) ([]ScrapeRow, error) {
	// 基础群：messages.getFullChat 一次拉全员（无分页 API）
	if target.ChatID != 0 {
		return m.scrapeBasicChatMembers(ctx, api, accountID, group, target.ChatID, limit)
	}
	if target.Channel == nil {
		return nil, fmt.Errorf("channel required for member scrape")
	}
	ch := target.Channel
	rows := make([]ScrapeRow, 0, limit)
	offset := 0
	page := 200
	for offset < limit {
		batch := page
		if offset+batch > limit {
			batch = limit - offset
		}
		res, err := api.ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{
			Channel: ch,
			Filter:  &tg.ChannelParticipantsRecent{},
			Offset:  offset,
			Limit:   batch,
		})
		if err != nil {
			return rows, err
		}
		part, ok := res.(*tg.ChannelsChannelParticipants)
		if !ok || part == nil {
			break
		}
		users := indexUsers(part.Users)
		if len(part.Participants) == 0 {
			break
		}
		for _, p := range part.Participants {
			uid := participantUserID(p)
			if uid == 0 {
				continue
			}
			u := users[uid]
			row := scrapeRowFromUser(accountID, group, SourceMembers, u, uid)
			rows = append(rows, row)
			if u != nil {
				_ = m.store.UpsertEntity(accountID, u.ID, u.AccessHash, "user", u.Username)
			}
		}
		offset += len(part.Participants)
		if offset >= part.Count || len(part.Participants) < batch {
			break
		}
	}
	return rows, nil
}

/**
 * 采集基础群成员（Chat，非超级群）。
 *
 * @param ctx 上下文
 * @param api 客户端
 * @param accountID 协议号
 * @param group 目标串
 * @param chatID 基础群 ID
 * @param limit 上限
 * @return 成员行
 */
func (m *Manager) scrapeBasicChatMembers(
	ctx context.Context,
	api *tg.Client,
	accountID, group string,
	chatID int64,
	limit int,
) ([]ScrapeRow, error) {
	full, err := api.MessagesGetFullChat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	users := indexUsers(full.Users)
	rows := make([]ScrapeRow, 0)
	cf, ok := full.FullChat.(*tg.ChatFull)
	if !ok || cf == nil {
		return rows, fmt.Errorf("unexpected full chat type %T", full.FullChat)
	}
	parts, ok := cf.Participants.(*tg.ChatParticipants)
	if !ok || parts == nil {
		// 禁止查看成员时仍允许发言者采集；成员返回空
		return rows, nil
	}
	for _, p := range parts.Participants {
		uid := chatParticipantUserID(p)
		if uid == 0 {
			continue
		}
		u := users[uid]
		row := scrapeRowFromUser(accountID, group, SourceMembers, u, uid)
		rows = append(rows, row)
		if u != nil {
			_ = m.store.UpsertEntity(accountID, u.ID, u.AccessHash, "user", u.Username)
		}
		if limit > 0 && len(rows) >= limit {
			break
		}
	}
	return rows, nil
}

const (
	// speakerDigestMaxSnippets 每位发言者最多保留的文本条数
	speakerDigestMaxSnippets = 5
	// speakerDigestMaxChars 单条文本截断长度
	speakerDigestMaxChars = 120
	// speakerDigestTotalMax 整段 digest 上限，控制 AI Token
	speakerDigestTotalMax = 800
)

type speakerBucket struct {
	row   ScrapeRow
	texts []string
}

func (m *Manager) scrapeSpeakers(
	ctx context.Context,
	api *tg.Client,
	accountID, group string,
	target scrapeTarget,
	historyLimit int,
) ([]ScrapeRow, error) {
	if target.Peer == nil {
		return nil, fmt.Errorf("peer required for speaker scrape")
	}
	peer := target.Peer
	buckets := map[int64]*speakerBucket{}
	order := make([]int64, 0)
	offsetID := 0
	fetched := 0
	page := 100

	for fetched < historyLimit {
		batch := page
		if fetched+batch > historyLimit {
			batch = historyLimit - fetched
		}
		res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     peer,
			OffsetID: offsetID,
			Limit:    batch,
		})
		if err != nil {
			return finalizeSpeakerRows(buckets, order), err
		}
		msgs, users := flattenHistory(res)
		userIdx := indexUsers(users)
		if len(msgs) == 0 {
			break
		}
		for _, msg := range msgs {
			uid := peerUserID(msg.FromID)
			if uid == 0 {
				continue
			}
			u := userIdx[uid]
			b, ok := buckets[uid]
			if !ok {
				row := scrapeRowFromUser(accountID, group, SourceSpeakers, u, uid)
				b = &speakerBucket{row: row, texts: nil}
				buckets[uid] = b
				order = append(order, uid)
				if u != nil {
					_ = m.store.UpsertEntity(accountID, u.ID, u.AccessHash, "user", u.Username)
				}
			}
			// 1. 累积发言原文（控条数与长度，供 AI digest）
			text := strings.TrimSpace(msg.Message)
			if text == "" {
				continue
			}
			if len(b.texts) >= speakerDigestMaxSnippets {
				continue
			}
			b.texts = append(b.texts, truncateRunes(text, speakerDigestMaxChars))
		}
		fetched += len(msgs)
		last := msgs[len(msgs)-1]
		if last.ID == 0 || last.ID == offsetID {
			break
		}
		offsetID = last.ID
		if len(msgs) < batch {
			break
		}
	}
	return finalizeSpeakerRows(buckets, order), nil
}

/**
 * 将发言者桶转为带 digest 的结果行。
 *
 * @param buckets 用户 → 行+文本
 * @param order 首次出现顺序
 * @return 结果行
 */
func finalizeSpeakerRows(buckets map[int64]*speakerBucket, order []int64) []ScrapeRow {
	rows := make([]ScrapeRow, 0, len(order))
	for _, uid := range order {
		b := buckets[uid]
		if b == nil {
			continue
		}
		b.row.MessageCount = len(b.texts)
		b.row.Digest = joinSpeakerDigest(b.texts)
		rows = append(rows, b.row)
	}
	return rows
}

/**
 * 拼接发言摘要并截断总长。
 *
 * @param texts 文本片段
 * @return digest
 */
func joinSpeakerDigest(texts []string) string {
	if len(texts) == 0 {
		return ""
	}
	joined := strings.Join(texts, " | ")
	return truncateRunes(joined, speakerDigestTotalMax)
}

// scrapeTarget 采集目标：超级群/频道用 Channel，基础群用 ChatID + InputPeerChat。
type scrapeTarget struct {
	Peer    tg.InputPeerClass
	Channel *tg.InputChannel
	ChatID  int64
}

/**
 * 解析采集目标（@username / peer:id:hash / chat:id；并兼容误用 peer: 指向基础群）。
 *
 * @param ctx 上下文
 * @param api Telegram 客户端
 * @param accountID 协议号
 * @param group 目标串
 * @return 解析后的 peer
 */
func (m *Manager) resolveScrapeTarget(
	ctx context.Context,
	api *tg.Client,
	accountID, group string,
) (scrapeTarget, error) {
	// 1. 基础群 chat:id
	if chatID, ok := parseChatTarget(group); ok {
		_ = m.store.UpsertEntity(accountID, chatID, 0, "chat", "")
		return scrapeTarget{
			Peer:   &tg.InputPeerChat{ChatID: chatID},
			ChatID: chatID,
		}, nil
	}
	// 2. 频道/超级群 peer:id:hash；若本地已加入表标记为 group，则改走基础群
	if peerID, accessHash, ok := parsePeerTarget(group); ok {
		if m.joinedGroupKind(accountID, peerID) == "group" {
			_ = m.store.UpsertEntity(accountID, peerID, 0, "chat", "")
			return scrapeTarget{
				Peer:   &tg.InputPeerChat{ChatID: peerID},
				ChatID: peerID,
			}, nil
		}
		_ = m.store.UpsertEntity(accountID, peerID, accessHash, "channel", "")
		ch := &tg.InputChannel{ChannelID: peerID, AccessHash: accessHash}
		return scrapeTarget{
			Peer:    &tg.InputPeerChannel{ChannelID: peerID, AccessHash: accessHash},
			Channel: ch,
		}, nil
	}
	// 3. 公开 @username
	username := extractPublicUsername(group)
	if username == "" {
		return scrapeTarget{}, fmt.Errorf("expect @username、peer:id:hash 或 chat:id")
	}
	ch, err := m.resolveChannel(ctx, api, accountID, username)
	if err != nil {
		return scrapeTarget{}, err
	}
	return scrapeTarget{
		Peer:    &tg.InputPeerChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash},
		Channel: ch,
	}, nil
}

/**
 * 查本地已加入群 kind（channel|megagroup|group）。
 *
 * @param accountID 协议号
 * @param peerID 群 ID
 * @return kind，未知为空
 */
func (m *Manager) joinedGroupKind(accountID string, peerID int64) string {
	if m.store == nil {
		return ""
	}
	items, err := m.store.ListJoinedGroups(accountID)
	if err != nil {
		return ""
	}
	for _, g := range items {
		if g.PeerID == peerID {
			return g.Kind
		}
	}
	return ""
}

/**
 * 基础群参与者 userId。
 *
 * @param p 参与者
 * @return userId
 */
func chatParticipantUserID(p tg.ChatParticipantClass) int64 {
	switch v := p.(type) {
	case *tg.ChatParticipant:
		return v.UserID
	case *tg.ChatParticipantCreator:
		return v.UserID
	case *tg.ChatParticipantAdmin:
		return v.UserID
	default:
		return 0
	}
}

func scrapeRowFromUser(accountID, group, source string, u *tg.User, fallbackID int64) ScrapeRow {
	row := ScrapeRow{
		Source:    source,
		AccountID: accountID,
		Group:     group,
		UserID:    fallbackID,
	}
	if u == nil {
		return row
	}
	row.UserID = u.ID
	row.Username = u.Username
	row.FirstName = u.FirstName
	row.LastName = u.LastName
	row.Phone = u.Phone // 可能为空，保持空
	row.IsBot = u.Bot
	return row
}

func indexUsers(list []tg.UserClass) map[int64]*tg.User {
	out := map[int64]*tg.User{}
	for _, item := range list {
		u, ok := item.(*tg.User)
		if ok && u != nil {
			out[u.ID] = u
		}
	}
	return out
}

func participantUserID(p tg.ChannelParticipantClass) int64 {
	switch v := p.(type) {
	case *tg.ChannelParticipant:
		return v.UserID
	case *tg.ChannelParticipantSelf:
		return v.UserID
	case *tg.ChannelParticipantCreator:
		return v.UserID
	case *tg.ChannelParticipantAdmin:
		return v.UserID
	case *tg.ChannelParticipantBanned:
		return peerUserID(v.Peer)
	case *tg.ChannelParticipantLeft:
		return peerUserID(v.Peer)
	default:
		return 0
	}
}

func peerUserID(p tg.PeerClass) int64 {
	if p == nil {
		return 0
	}
	if u, ok := p.(*tg.PeerUser); ok && u != nil {
		return u.UserID
	}
	return 0
}

func flattenHistory(res tg.MessagesMessagesClass) ([]*tg.Message, []tg.UserClass) {
	switch v := res.(type) {
	case *tg.MessagesMessages:
		return messageSlice(v.Messages), v.Users
	case *tg.MessagesMessagesSlice:
		return messageSlice(v.Messages), v.Users
	case *tg.MessagesChannelMessages:
		return messageSlice(v.Messages), v.Users
	default:
		return nil, nil
	}
}

func messageSlice(list []tg.MessageClass) []*tg.Message {
	out := make([]*tg.Message, 0, len(list))
	for _, item := range list {
		if m, ok := item.(*tg.Message); ok && m != nil {
			out = append(out, m)
		}
	}
	return out
}
