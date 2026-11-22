package tgclient

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/gotd/td/tg"
)

// SpamBot 状态常量（F12）。
const (
	SpamStatusGood     = "good"
	SpamStatusLimited  = "limited"
	SpamStatusUnknown  = "unknown"
	SpamStatusTimeout  = "timeout"
	SpamStatusFailed   = "failed"
)

// SpamBotResult SpamBot 检查结果。
type SpamBotResult struct {
	AccountID      string `json:"accountId"`
	OK             bool   `json:"ok"`
	Status         string `json:"status"`
	BusinessResult string `json:"businessResult"`
	Snippet        string `json:"snippet,omitempty"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	WaitSeconds    int    `json:"waitSeconds,omitempty"`
}

/**
 * 向 @SpamBot 发送 /start 并解析回复（F12）。支持中英俄关键词；超时记 timeout。
 *
 * @param ctx 上下文
 * @param accountID 协议号 ID
 * @param wait 等待机器人回复时长（建议 3～8s）
 * @return 检查结果
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) CheckSpamBot(ctx context.Context, accountID string, wait time.Duration) SpamBotResult {
	out := SpamBotResult{AccountID: accountID, Status: SpamStatusUnknown, BusinessResult: SpamStatusUnknown}
	if wait < 2*time.Second {
		wait = 3 * time.Second
	}
	if wait > 15*time.Second {
		wait = 15 * time.Second
	}

	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: "SpamBot"})
		if err != nil {
			biz, code, msg, wsec := classifyContactErr(err)
			out.BusinessResult = biz
			out.Status = SpamStatusFailed
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.WaitSeconds = wsec
			return nil
		}
		user, ok := findUserFromResolved(resolved)
		if !ok {
			out.Status = SpamStatusFailed
			out.BusinessResult = ResultFailed
			out.ErrorCode = "spambot_not_found"
			out.ErrorMessage = "SpamBot user not resolved"
			return nil
		}
		_ = m.store.UpsertEntity(accountID, user.ID, user.AccessHash, "user", user.Username)
		peer := &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}

		randomID, err := randomInt64()
		if err != nil {
			out.Status = SpamStatusFailed
			out.BusinessResult = ResultFailed
			out.ErrorCode = "random_id"
			out.ErrorMessage = err.Error()
			return nil
		}
		_, err = api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:     peer,
			Message:  "/start",
			RandomID: randomID,
		})
		if err != nil {
			biz, code, msg, wsec := classifySendErr(err)
			out.Status = SpamStatusFailed
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.WaitSeconds = wsec
			return nil
		}

		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			out.Status = SpamStatusTimeout
			out.BusinessResult = SpamStatusTimeout
			out.ErrorCode = "cancelled"
			out.ErrorMessage = ctx.Err().Error()
			return nil
		case <-timer.C:
		}

		hist, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:  peer,
			Limit: 8,
		})
		if err != nil {
			out.Status = SpamStatusTimeout
			out.BusinessResult = SpamStatusTimeout
			out.ErrorCode = "history_failed"
			out.ErrorMessage = err.Error()
			return nil
		}
		msgs, _ := flattenHistory(hist)
		snippet := latestBotText(msgs, user.ID)
		out.Snippet = truncateRunes(snippet, 280)
		status := parseSpamBotText(snippet)
		out.Status = status
		out.BusinessResult = status
		if status == SpamStatusGood || status == SpamStatusLimited {
			out.OK = true
		} else if snippet == "" {
			out.Status = SpamStatusTimeout
			out.BusinessResult = SpamStatusTimeout
			out.ErrorCode = "no_reply"
			out.ErrorMessage = "SpamBot 未在时限内回复可读文本"
		} else {
			out.OK = true // 有原文但无法归类，仍算检查完成
			out.Status = SpamStatusUnknown
			out.BusinessResult = SpamStatusUnknown
		}
		return nil
	})
	if err != nil {
		out.Status = SpamStatusFailed
		out.BusinessResult = ResultFailed
		out.ErrorCode = "client_error"
		out.ErrorMessage = err.Error()
	}
	return out
}

func latestBotText(msgs []*tg.Message, botID int64) string {
	for _, msg := range msgs {
		if msg == nil || strings.TrimSpace(msg.Message) == "" {
			continue
		}
		from := peerUserID(msg.FromID)
		// 无 FromID 时，对方发出的入站消息也接受
		if from == 0 || from == botID {
			return strings.TrimSpace(msg.Message)
		}
	}
	return ""
}

func parseSpamBotText(text string) string {
	if text == "" {
		return SpamStatusUnknown
	}
	lower := strings.ToLower(text)
	compact := spaceCompact(lower)

	limitedHints := []string{
		"limited", "restrict", "spam", "reported", "blocked",
		"限制", "被限制", "受限", "冻结", "禁止", "spam",
		"ограничен", "заблокирован", "спам",
	}
	goodHints := []string{
		"good news", "no limits", "not limited", "free", "unlimited",
		"没有限制", "无限制", "账号正常", "目前没有", "你的账号没有",
		"не ограничен", "нет ограничений", "хорошие новости",
	}

	for _, h := range limitedHints {
		if strings.Contains(compact, strings.ToLower(h)) || strings.Contains(lower, strings.ToLower(h)) {
			return SpamStatusLimited
		}
	}
	for _, h := range goodHints {
		if strings.Contains(compact, strings.ToLower(h)) || strings.Contains(lower, strings.ToLower(h)) {
			return SpamStatusGood
		}
	}
	return SpamStatusUnknown
}

func spaceCompact(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
