package tgclient

import (
	"context"

	"github.com/gotd/td/tg"
)

// PhoneCheckItem 手机号检查结果（F25，与 F02 共用五态枚举）。
type PhoneCheckItem struct {
	AccountID      string `json:"accountId"`
	Phone          string `json:"phone"`
	OK             bool   `json:"ok"`
	BusinessResult string `json:"businessResult"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	PeerID         int64  `json:"peerId,omitempty"`
	Username       string `json:"username,omitempty"`
	FirstName      string `json:"firstName,omitempty"`
	LastName       string `json:"lastName,omitempty"`
	WaitSeconds    int    `json:"waitSeconds,omitempty"`
}

/**
 * 检查手机号是否可匹配到可见用户（F25）。未匹配 ≠ 未注册。
 *
 * @param ctx 上下文
 * @param accountID 执行协议号
 * @param phoneRaw 手机号
 * @return 检查结果
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) CheckPhone(ctx context.Context, accountID, phoneRaw string) PhoneCheckItem {
	phone := normalizePhone(phoneRaw)
	out := PhoneCheckItem{AccountID: accountID, Phone: phone}
	if phone == "" {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "empty_phone"
		out.ErrorMessage = "phone required"
		return out
	}

	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		resolved, err := api.ContactsResolvePhone(ctx, phone)
		if err != nil {
			biz, code, msg, wait := classifyResolveErr(err, ModePhone)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.WaitSeconds = wait
			return nil
		}
		user, ok := findUserFromResolved(resolved)
		if !ok {
			out.BusinessResult = ResultUnmatched
			out.ErrorCode = "phone_unmatched"
			out.ErrorMessage = "phone not matched to a visible user"
			return nil
		}
		_ = m.store.UpsertEntity(accountID, user.ID, user.AccessHash, "user", user.Username)
		out.OK = true
		out.BusinessResult = ResultMatched
		out.PeerID = user.ID
		out.Username = user.Username
		out.FirstName = user.FirstName
		out.LastName = user.LastName
		if user.Phone != "" {
			out.Phone = user.Phone
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
