package tgclient

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// 定位模式常量。
const (
	ModeUsername = "username"
	ModePhone    = "phone"
	ModeUserID   = "userId"
)

// 业务结果常量（F02/F25 五态 + 通用成功失败）。
const (
	ResultOK         = "ok"
	ResultMatched    = "matched"
	ResultUnmatched  = "unmatched"
	ResultPrivacy    = "privacy"
	ResultBlocked    = "blocked"
	ResultDeactivated = "deactivated"
	ResultNotFound   = "not_found"
	ResultNoHash     = "no_access_hash"
	ResultFlood      = "flood_wait"
	ResultFailed     = "failed"
)

// SendItemResult 单条发送结果。
type SendItemResult struct {
	AccountID      string `json:"accountId"`
	Target         string `json:"target"`
	Mode           string `json:"mode"`
	OK             bool   `json:"ok"`
	BusinessResult string `json:"businessResult"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	PeerID         int64  `json:"peerId,omitempty"`
	WaitSeconds    int    `json:"waitSeconds,omitempty"`
}

/**
 * 使用指定协议号向目标发送私信（F01/F02/F03）。
 *
 * @param ctx 上下文
 * @param accountID 执行协议号
 * @param mode 定位方式：username / phone / userId
 * @param target 目标字符串
 * @param text 正文
 * @return 单条结果
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) SendDirect(ctx context.Context, accountID, mode, target, text string) SendItemResult {
	target = strings.TrimSpace(target)
	text = strings.TrimSpace(text)
	out := SendItemResult{
		AccountID: accountID,
		Target:    target,
		Mode:      mode,
	}
	if target == "" || text == "" {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "invalid_input"
		out.ErrorMessage = "target and text required"
		return out
	}

	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		peer, peerID, biz, code, msg, waitSec, err := m.resolvePeer(ctx, api, accountID, mode, target)
		if err != nil || peer == nil {
			out.OK = false
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.WaitSeconds = waitSec
			if err != nil && msg == "" {
				out.ErrorMessage = err.Error()
			}
			return nil
		}
		out.PeerID = peerID

		randomID, err := randomInt64()
		if err != nil {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "random_id"
			out.ErrorMessage = err.Error()
			return nil
		}

		_, err = api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:     peer,
			Message:  text,
			RandomID: randomID,
		})
		if err != nil {
			biz, code, msg, waitSec := classifySendErr(err)
			out.OK = false
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.WaitSeconds = waitSec
			return nil
		}

		out.OK = true
		if mode == ModePhone {
			out.BusinessResult = ResultMatched
		} else {
			out.BusinessResult = ResultOK
		}
		return nil
	})
	if err != nil {
		out.OK = false
		out.BusinessResult = ResultFailed
		out.ErrorCode = "client_error"
		out.ErrorMessage = err.Error()
	}
	return out
}

func (m *Manager) resolvePeer(
	ctx context.Context,
	api *tg.Client,
	accountID, mode, target string,
) (tg.InputPeerClass, int64, string, string, string, int, error) {
	switch mode {
	case ModeUsername:
		return m.resolveByUsername(ctx, api, accountID, target)
	case ModePhone:
		return m.resolveByPhone(ctx, api, accountID, target)
	case ModeUserID:
		return m.resolveByUserID(accountID, target)
	default:
		return nil, 0, ResultFailed, "invalid_mode", "mode must be username|phone|userId", 0, fmt.Errorf("invalid mode")
	}
}

func (m *Manager) resolveByUsername(
	ctx context.Context,
	api *tg.Client,
	accountID, raw string,
) (tg.InputPeerClass, int64, string, string, string, int, error) {
	username := strings.TrimPrefix(strings.TrimSpace(raw), "@")
	if username == "" {
		return nil, 0, ResultFailed, "empty_username", "username required", 0, fmt.Errorf("empty username")
	}

	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		biz, code, msg, wait := classifyResolveErr(err, ModeUsername)
		return nil, 0, biz, code, msg, wait, err
	}

	user, ok := findUserFromResolved(resolved)
	if !ok {
		return nil, 0, ResultNotFound, "peer_not_user", "resolved peer is not a user", 0, fmt.Errorf("not user")
	}
	_ = m.store.UpsertEntity(accountID, user.ID, user.AccessHash, "user", user.Username)
	return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, user.ID, ResultOK, "", "", 0, nil
}

func (m *Manager) resolveByPhone(
	ctx context.Context,
	api *tg.Client,
	accountID, raw string,
) (tg.InputPeerClass, int64, string, string, string, int, error) {
	phone := normalizePhone(raw)
	if phone == "" {
		return nil, 0, ResultFailed, "empty_phone", "phone required", 0, fmt.Errorf("empty phone")
	}

	resolved, err := api.ContactsResolvePhone(ctx, phone)
	if err != nil {
		biz, code, msg, wait := classifyResolveErr(err, ModePhone)
		return nil, 0, biz, code, msg, wait, err
	}
	user, ok := findUserFromResolved(resolved)
	if !ok {
		// 未匹配 ≠ 未注册
		return nil, 0, ResultUnmatched, "phone_unmatched", "phone not matched to a visible user", 0, nil
	}
	_ = m.store.UpsertEntity(accountID, user.ID, user.AccessHash, "user", user.Username)
	return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, user.ID, ResultMatched, "", "", 0, nil
}

func (m *Manager) resolveByUserID(accountID, raw string) (tg.InputPeerClass, int64, string, string, string, int, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id == 0 {
		return nil, 0, ResultFailed, "invalid_user_id", "user id must be integer", 0, err
	}
	ent, ok, err := m.store.GetEntity(accountID, id, "user")
	if err != nil {
		return nil, 0, ResultFailed, "db_error", err.Error(), 0, err
	}
	if !ok || ent.AccessHash == 0 {
		// 禁止跨号复用 access_hash
		return nil, 0, ResultNoHash, "missing_access_hash",
			"缺少实体缓存，请先通过用户名/对话等方式建立实体", 0, nil
	}
	return &tg.InputPeerUser{UserID: ent.PeerID, AccessHash: ent.AccessHash}, ent.PeerID, ResultOK, "", "", 0, nil
}

func findUserFromResolved(resolved *tg.ContactsResolvedPeer) (*tg.User, bool) {
	if resolved == nil {
		return nil, false
	}
	for _, u := range resolved.Users {
		user, ok := u.(*tg.User)
		if ok && user != nil {
			return user, true
		}
	}
	return nil, false
}

func normalizePhone(raw string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if r == '+' && b.Len() == 0 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func classifyResolveErr(err error, mode string) (biz, code, msg string, waitSec int) {
	if d, ok := tgerr.AsFloodWait(err); ok {
		return ResultFlood, "FLOOD_WAIT", err.Error(), int(d / time.Second)
	}
	rpc, ok := tgerr.As(err)
	if !ok {
		return ResultFailed, "resolve_failed", err.Error(), 0
	}
	code = rpc.Type
	msg = err.Error()
	switch {
	case rpc.IsCode(400) && strings.Contains(code, "USERNAME_NOT_OCCUPIED"):
		return ResultNotFound, code, msg, 0
	case strings.Contains(code, "PHONE") && mode == ModePhone:
		return ResultUnmatched, code, msg, 0
	case strings.Contains(code, "USER_PRIVACY") || strings.Contains(code, "PRIVACY"):
		return ResultPrivacy, code, msg, 0
	default:
		return ResultFailed, code, msg, 0
	}
}

func classifySendErr(err error) (biz, code, msg string, waitSec int) {
	if d, ok := tgerr.AsFloodWait(err); ok {
		return ResultFlood, "FLOOD_WAIT", err.Error(), int(d / time.Second)
	}
	rpc, ok := tgerr.As(err)
	if !ok {
		return ResultFailed, "send_failed", err.Error(), 0
	}
	code = rpc.Type
	msg = err.Error()
	switch {
	case strings.Contains(code, "USER_IS_BLOCKED") || strings.Contains(code, "YOU_BLOCKED_USER"):
		return ResultBlocked, code, msg, 0
	case strings.Contains(code, "USER_PRIVACY") || strings.Contains(code, "PRIVACY"):
		return ResultPrivacy, code, msg, 0
	case strings.Contains(code, "USER_DEACTIVATED") || strings.Contains(code, "INPUT_USER_DEACTIVATED"):
		return ResultDeactivated, code, msg, 0
	case strings.Contains(code, "PEER_FLOOD"):
		// PEER_FLOOD 无官方等待秒数，且重复硬发会加重限制；业务层应失败停发
		return ResultFlood, code, msg, 0
	default:
		return ResultFailed, code, msg, 0
	}
}

/**
 * 将发送错误转为中文说明（供队列 / 任务展示）。
 *
 * @param code Telegram / 业务错误码
 * @param raw 原始错误信息
 * @return 中文说明
 */
func HumanizeSendError(code, raw string) string {
	c := strings.ToUpper(strings.TrimSpace(code))
	r := strings.ToUpper(raw)
	switch {
	case strings.Contains(c, "PEER_FLOOD") || strings.Contains(r, "PEER_FLOOD"):
		return "发送失败：该协议号触发 Telegram 防刷限制（PEER_FLOOD）。请勿对该目标重复再发；请换健康号或降低频率后再试。"
	case strings.Contains(c, "FLOOD_WAIT") || strings.Contains(r, "FLOOD_WAIT"):
		return "发送暂缓：触发限流（FLOOD_WAIT），请稍后再试，勿连续重发。"
	case strings.Contains(c, "USER_IS_BLOCKED") || strings.Contains(r, "USER_IS_BLOCKED"):
		return "发送失败：对方已将你拉黑，请勿重复发送。"
	case strings.Contains(c, "YOU_BLOCKED_USER") || strings.Contains(r, "YOU_BLOCKED_USER"):
		return "发送失败：你已拉黑对方，请勿重复发送。"
	case strings.Contains(c, "USER_PRIVACY") || strings.Contains(r, "PRIVACY"):
		return "发送失败：对方隐私设置限制私信，请勿重复发送。"
	case strings.Contains(c, "USER_DEACTIVATED") || strings.Contains(r, "DEACTIVATED"):
		return "发送失败：对方账号已停用/注销，请勿重复发送。"
	case strings.Contains(c, "USERNAME_NOT_OCCUPIED") || strings.Contains(r, "USERNAME_NOT_OCCUPIED"):
		return "发送失败：用户名不存在，请勿重复发送。"
	case strings.Contains(c, "USERNAME_INVALID") || strings.Contains(r, "USERNAME_INVALID"):
		return "发送失败：用户名无效，请勿重复发送。"
	case strings.Contains(c, "PEER_ID_INVALID") || strings.Contains(r, "PEER_ID_INVALID"):
		return "发送失败：目标无效，请勿重复发送。"
	case c == "NO_ACCESS_HASH" || strings.Contains(r, "NO_ACCESS_HASH"):
		return "发送失败：缺少 access_hash，无法定位该用户，请勿重复发送。"
	case strings.TrimSpace(raw) == "":
		if c != "" {
			return "发送失败：" + code
		}
		return "发送失败"
	default:
		// 已是中文说明则原样返回
		if strings.Contains(raw, "发送失败") || strings.Contains(raw, "发送暂缓") {
			return raw
		}
		if c != "" {
			return "发送失败：" + raw
		}
		return "发送失败：" + raw
	}
}

/**
 * 是否为不应自动重试的硬失败（如 PEER_FLOOD）。
 *
 * @param code 错误码
 * @param raw 原始信息
 */
func IsNonRetriableSendError(code, raw string) bool {
	u := strings.ToUpper(code + " " + raw)
	return strings.Contains(u, "PEER_FLOOD") ||
		strings.Contains(u, "USER_IS_BLOCKED") ||
		strings.Contains(u, "YOU_BLOCKED_USER") ||
		strings.Contains(u, "USER_PRIVACY") ||
		strings.Contains(u, "PRIVACY_PREMIUM_REQUIRED") ||
		strings.Contains(u, "USER_DEACTIVATED") ||
		strings.Contains(u, "INPUT_USER_DEACTIVATED") ||
		strings.Contains(u, "USERNAME_NOT_OCCUPIED") ||
		strings.Contains(u, "USERNAME_INVALID") ||
		strings.Contains(u, "PEER_ID_INVALID")
}

func randomInt64() (int64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	v := int64(binary.LittleEndian.Uint64(b[:]))
	if v == 0 {
		v = 1
	}
	return v, nil
}
