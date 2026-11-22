package tgclient

import (
	"context"
	"fmt"
	"strings"
)

const (
	groupHistoryPageSize = 100
	groupHistoryMaxTotal = 1000
)

/**
 * 将已加入群 kind 映射为 chat history 的 peerType。
 *
 * @param kind joined_groups.kind
 * @return channel | chat
 */
func PeerTypeFromJoinedKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "group", "chat":
		return "chat"
	default:
		return "channel"
	}
}

/**
 * 分页拉取群历史消息（新→旧翻页），最多 limit 条，上限 1000。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param peerType channel|chat
 * @param peerID 群 id
 * @param accessHash access_hash
 * @param limit 目标条数
 * @return 消息（按 id 升序）
 */
func (m *Manager) FetchGroupHistoryPaged(
	ctx context.Context,
	accountID, peerType string,
	peerID, accessHash int64,
	limit int,
) ([]ChatMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > groupHistoryMaxTotal {
		limit = groupHistoryMaxTotal
	}
	collected := make([]ChatMessage, 0, limit)
	offsetID := 0
	for len(collected) < limit {
		batch := groupHistoryPageSize
		remain := limit - len(collected)
		if batch > remain {
			batch = remain
		}
		page, err := m.GetChatHistory(ctx, accountID, peerType, peerID, accessHash, batch, offsetID, 0)
		if err != nil {
			return trimNewestChatMessages(dedupeChatMessagesByID(sortChatMessagesAsc(collected)), limit), err
		}
		if len(page) == 0 {
			break
		}
		collected = append(collected, page...)
		// 升序：page[0] 为本页最旧
		oldest := page[0].ID
		if oldest <= 0 || oldest == offsetID {
			break
		}
		offsetID = oldest
		if len(page) < batch {
			break
		}
		select {
		case <-ctx.Done():
			return trimNewestChatMessages(dedupeChatMessagesByID(sortChatMessagesAsc(collected)), limit), ctx.Err()
		default:
		}
	}
	return trimNewestChatMessages(dedupeChatMessagesByID(sortChatMessagesAsc(collected)), limit), nil
}

/**
 * 拉取 id > minID 的新消息（监控轮询）。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param peerType channel|chat
 * @param peerID 群 id
 * @param accessHash access_hash
 * @param minID 已见最大 id
 * @param limit 单次上限
 */
func (m *Manager) FetchGroupHistorySince(
	ctx context.Context,
	accountID, peerType string,
	peerID, accessHash int64,
	minID, limit int,
) ([]ChatMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > groupHistoryPageSize {
		limit = groupHistoryPageSize
	}
	if minID < 0 {
		minID = 0
	}
	msgs, err := m.GetChatHistory(ctx, accountID, peerType, peerID, accessHash, limit, 0, minID)
	if err != nil {
		return nil, fmt.Errorf("fetch since %d: %w", minID, err)
	}
	return msgs, nil
}

func sortChatMessagesAsc(in []ChatMessage) []ChatMessage {
	if len(in) < 2 {
		return in
	}
	out := make([]ChatMessage, len(in))
	copy(out, in)
	// 简单插入排序即可（单页量级）
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j-1].ID > out[j].ID {
			out[j-1], out[j] = out[j], out[j-1]
			j--
		}
	}
	return out
}

func dedupeChatMessagesByID(in []ChatMessage) []ChatMessage {
	seen := make(map[int]struct{}, len(in))
	out := make([]ChatMessage, 0, len(in))
	for _, m := range in {
		if m.ID == 0 {
			continue
		}
		if _, ok := seen[m.ID]; ok {
			continue
		}
		seen[m.ID] = struct{}{}
		out = append(out, m)
	}
	return out
}

func trimNewestChatMessages(in []ChatMessage, limit int) []ChatMessage {
	if limit <= 0 || len(in) <= limit {
		return in
	}
	return in[len(in)-limit:]
}

/**
 * 媒体元数据压成短字符串，便于 CSV。
 *
 * @param media 媒体
 */
func FormatChatMediaLabel(media ChatMediaMeta) string {
	if media.Type == "" {
		return ""
	}
	parts := []string{media.Type}
	if media.FileName != "" {
		parts = append(parts, media.FileName)
	} else if media.MimeType != "" {
		parts = append(parts, media.MimeType)
	}
	return strings.Join(parts, ":")
}
