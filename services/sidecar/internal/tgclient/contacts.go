package tgclient

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/tg-matrix/sidecar/internal/store"
)

// ContactItemResult 单条联系人操作结果（F19/F20）。
type ContactItemResult struct {
	AccountID      string `json:"accountId"`
	Target         string `json:"target"`
	Action         string `json:"action"`
	OK             bool   `json:"ok"`
	BusinessResult string `json:"businessResult"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	PeerID         int64  `json:"peerId,omitempty"`
	Username       string `json:"username,omitempty"`
	Phone          string `json:"phone,omitempty"`
	FirstName      string `json:"firstName,omitempty"`
	LastName       string `json:"lastName,omitempty"`
	Mutual         *bool  `json:"mutual,omitempty"`
	WaitSeconds    int    `json:"waitSeconds,omitempty"`
}

// ContactListItem 通讯录中的联系人条目。
type ContactListItem struct {
	UserID    int64  `json:"userId"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	Phone     string `json:"phone,omitempty"`
	Mutual    bool   `json:"mutual,omitempty"`
	IsBot     bool   `json:"isBot,omitempty"`
	IsSelf    bool   `json:"isSelf,omitempty"`
	Status    string `json:"status,omitempty"` // online | recently | lastWeek | lastMonth | offline | empty
}

/**
 * 从 Telegram 拉取通讯录并全量写入该协议号的本地 SQLite 缓存。
 * 仅应在用户手动「刷新」时调用。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @return 最新联系人列表
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) RefreshContacts(ctx context.Context, accountID string) ([]ContactListItem, error) {
	items, err := m.FetchContactsFromTG(ctx, accountID)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	rows := make([]store.Contact, 0, len(items))
	for _, it := range items {
		rows = append(rows, store.Contact{
			AccountID: accountID,
			UserID:    it.UserID,
			Username:  it.Username,
			FirstName: it.FirstName,
			LastName:  it.LastName,
			Phone:     it.Phone,
			Mutual:    it.Mutual,
			IsBot:     it.IsBot,
			IsSelf:    it.IsSelf,
			Status:    it.Status,
			SyncedAt:  now,
		})
	}
	if err := m.store.ReplaceContacts(accountID, rows); err != nil {
		return nil, err
	}
	return items, nil
}

/**
 * 从 Telegram 拉取通讯录（不写库）。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @return 联系人列表
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) FetchContactsFromTG(ctx context.Context, accountID string) ([]ContactListItem, error) {
	var out []ContactListItem
	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		res, err := api.ContactsGetContacts(ctx, 0)
		if err != nil {
			return err
		}
		contacts, ok := res.(*tg.ContactsContacts)
		if !ok {
			// hash 未变或其它类型：当作空列表
			return nil
		}
		mutualByID := map[int64]bool{}
		for _, c := range contacts.Contacts {
			mutualByID[c.UserID] = c.Mutual
		}
		for _, u := range contacts.Users {
			user, ok := u.(*tg.User)
			if !ok || user == nil || user.Deleted {
				continue
			}
			hash, _ := user.GetAccessHash()
			_ = m.store.UpsertEntity(accountID, user.ID, hash, "user", user.Username)
			out = append(out, ContactListItem{
				UserID:    user.ID,
				Username:  user.Username,
				FirstName: user.FirstName,
				LastName:  user.LastName,
				Phone:     user.Phone,
				Mutual:    mutualByID[user.ID],
				IsBot:     user.Bot,
				IsSelf:    user.Self,
				Status:    userStatusLabel(user.Status),
			})
		}
		return nil
	})
	return out, err
}

func userStatusLabel(status tg.UserStatusClass) string {
	switch status.(type) {
	case *tg.UserStatusOnline:
		return "online"
	case *tg.UserStatusOffline:
		return "offline"
	case *tg.UserStatusRecently:
		return "recently"
	case *tg.UserStatusLastWeek:
		return "lastWeek"
	case *tg.UserStatusLastMonth:
		return "lastMonth"
	case *tg.UserStatusEmpty:
		return "empty"
	default:
		return ""
	}
}

/**
 * 添加联系人（F19）：支持手机号导入或按用户名添加。
 * 手机号行格式：phone[,firstName[,lastName]]；用户名以 @ 开头或非纯数字。
 *
 * @param ctx 上下文
 * @param accountID 执行协议号
 * @param target 名单行
 * @return 单条结果
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) AddContact(ctx context.Context, accountID, target string) ContactItemResult {
	target = strings.TrimSpace(target)
	out := ContactItemResult{AccountID: accountID, Target: target, Action: "add"}
	if target == "" {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "empty_target"
		out.ErrorMessage = "target required"
		return out
	}

	phone, first, last, isPhone := parseContactLine(target)
	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		if isPhone {
			return m.addByPhone(ctx, api, accountID, phone, first, last, &out)
		}
		username := strings.TrimPrefix(target, "@")
		username = strings.TrimSpace(strings.Split(username, ",")[0])
		return m.addByUsername(ctx, api, accountID, username, first, last, &out)
	})
	if err != nil {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "client_error"
		out.ErrorMessage = err.Error()
	}
	return out
}

/**
 * 删除联系人（F20）：支持用户名 / 手机号 / 用户 ID。
 *
 * @param ctx 上下文
 * @param accountID 执行协议号
 * @param target 目标
 * @return 单条结果
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) DeleteContact(ctx context.Context, accountID, target string) ContactItemResult {
	target = strings.TrimSpace(target)
	out := ContactItemResult{AccountID: accountID, Target: target, Action: "delete"}
	if target == "" {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "empty_target"
		out.ErrorMessage = "target required"
		return out
	}

	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		input, peerID, biz, code, msg, wait, err := m.resolveContactUser(ctx, api, accountID, target)
		if err != nil || input == nil {
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
		_, err = api.ContactsDeleteContacts(ctx, []tg.InputUserClass{input})
		if err != nil {
			biz, code, msg, wait := classifyContactErr(err)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			out.WaitSeconds = wait
			return nil
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

func (m *Manager) addByPhone(
	ctx context.Context,
	api *tg.Client,
	accountID, phone, first, last string,
	out *ContactItemResult,
) error {
	out.Phone = phone
	out.FirstName = first
	out.LastName = last
	clientID := time.Now().UnixNano()
	imported, err := api.ContactsImportContacts(ctx, []tg.InputPhoneContact{{
		ClientID:  clientID,
		Phone:     phone,
		FirstName: first,
		LastName:  last,
	}})
	if err != nil {
		biz, code, msg, wait := classifyContactErr(err)
		out.BusinessResult = biz
		out.ErrorCode = code
		out.ErrorMessage = msg
		out.WaitSeconds = wait
		return nil
	}

	for _, id := range imported.RetryContacts {
		if id == clientID {
			out.BusinessResult = "retry"
			out.ErrorCode = "retry_contacts"
			out.ErrorMessage = "server asked to retry this contact"
			return nil
		}
	}

	var matched *tg.ImportedContact
	for i := range imported.Imported {
		if imported.Imported[i].ClientID == clientID {
			matched = &imported.Imported[i]
			break
		}
	}
	if matched == nil {
		// 未匹配 ≠ 未注册
		out.BusinessResult = ResultUnmatched
		out.ErrorCode = "phone_unmatched"
		out.ErrorMessage = "phone not matched to a visible user"
		return nil
	}

	user := findUserByID(imported.Users, matched.UserID)
	if user != nil {
		_ = m.store.UpsertEntity(accountID, user.ID, user.AccessHash, "user", user.Username)
		out.Username = user.Username
		out.PeerID = user.ID
		if user.Phone != "" {
			out.Phone = user.Phone
		}
	} else {
		out.PeerID = matched.UserID
	}
	out.OK = true
	out.BusinessResult = ResultMatched
	return nil
}

func (m *Manager) addByUsername(
	ctx context.Context,
	api *tg.Client,
	accountID, username, first, last string,
	out *ContactItemResult,
) error {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		out.BusinessResult = ResultFailed
		out.ErrorCode = "empty_username"
		out.ErrorMessage = "username required"
		return nil
	}
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		biz, code, msg, wait := classifyContactErr(err)
		out.BusinessResult = biz
		out.ErrorCode = code
		out.ErrorMessage = msg
		out.WaitSeconds = wait
		return nil
	}
	user, ok := findUserFromResolved(resolved)
	if !ok {
		out.BusinessResult = ResultNotFound
		out.ErrorCode = "peer_not_user"
		out.ErrorMessage = "resolved peer is not a user"
		return nil
	}
	_ = m.store.UpsertEntity(accountID, user.ID, user.AccessHash, "user", user.Username)
	if first == "" {
		first = user.FirstName
	}
	if last == "" {
		last = user.LastName
	}
	out.Username = user.Username
	out.PeerID = user.ID
	out.FirstName = first
	out.LastName = last
	out.Phone = user.Phone

	_, err = api.ContactsAddContact(ctx, &tg.ContactsAddContactRequest{
		ID:        &tg.InputUser{UserID: user.ID, AccessHash: user.AccessHash},
		FirstName: first,
		LastName:  last,
		Phone:     user.Phone,
	})
	if err != nil {
		biz, code, msg, wait := classifyContactErr(err)
		out.BusinessResult = biz
		out.ErrorCode = code
		out.ErrorMessage = msg
		out.WaitSeconds = wait
		return nil
	}
	out.OK = true
	out.BusinessResult = ResultOK
	return nil
}

func (m *Manager) resolveContactUser(
	ctx context.Context,
	api *tg.Client,
	accountID, target string,
) (*tg.InputUser, int64, string, string, string, int, error) {
	raw := strings.TrimSpace(target)

	// 用户名
	if strings.HasPrefix(raw, "@") || looksLikeUsername(raw) {
		username := strings.TrimPrefix(raw, "@")
		resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
		if err != nil {
			biz, code, msg, wait := classifyContactErr(err)
			return nil, 0, biz, code, msg, wait, err
		}
		user, ok := findUserFromResolved(resolved)
		if !ok {
			return nil, 0, ResultNotFound, "peer_not_user", "not a user", 0, nil
		}
		_ = m.store.UpsertEntity(accountID, user.ID, user.AccessHash, "user", user.Username)
		return &tg.InputUser{UserID: user.ID, AccessHash: user.AccessHash}, user.ID, ResultOK, "", "", 0, nil
	}

	// 用户 ID（纯数字，无 +）
	if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 && !strings.HasPrefix(raw, "+") {
		ent, ok, err := m.store.GetEntity(accountID, id, "user")
		if err != nil {
			return nil, 0, ResultFailed, "db_error", err.Error(), 0, err
		}
		if ok && ent.AccessHash != 0 {
			return &tg.InputUser{UserID: ent.PeerID, AccessHash: ent.AccessHash}, ent.PeerID, ResultOK, "", "", 0, nil
		}
		return m.findInContacts(ctx, api, accountID, func(u *tg.User) bool { return u.ID == id })
	}

	// 手机号：在本协议号通讯录中匹配
	phone := normalizePhone(raw)
	if phone != "" {
		return m.findInContacts(ctx, api, accountID, func(u *tg.User) bool {
			up := normalizePhone(u.Phone)
			return up == phone || strings.HasSuffix(up, strings.TrimPrefix(phone, "+"))
		})
	}

	return nil, 0, ResultFailed, "invalid_target", "expect phone, @username or user id", 0, fmt.Errorf("invalid target")
}

func (m *Manager) findInContacts(
	ctx context.Context,
	api *tg.Client,
	accountID string,
	match func(*tg.User) bool,
) (*tg.InputUser, int64, string, string, string, int, error) {
	res, err := api.ContactsGetContacts(ctx, 0)
	if err != nil {
		biz, code, msg, wait := classifyContactErr(err)
		return nil, 0, biz, code, msg, wait, err
	}
	contacts, ok := res.(*tg.ContactsContacts)
	if !ok {
		return nil, 0, ResultFailed, "contacts_not_modified", "contacts hash not modified or unexpected type", 0, nil
	}
	for _, u := range contacts.Users {
		user, ok := u.(*tg.User)
		if !ok || user == nil {
			continue
		}
		_ = m.store.UpsertEntity(accountID, user.ID, user.AccessHash, "user", user.Username)
		if match(user) {
			return &tg.InputUser{UserID: user.ID, AccessHash: user.AccessHash}, user.ID, ResultOK, "", "", 0, nil
		}
	}
	return nil, 0, ResultNotFound, "not_in_contacts", "target not found in account contacts", 0, nil
}

func parseContactLine(raw string) (phone, first, last string, isPhone bool) {
	parts := strings.Split(raw, ",")
	head := strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		first = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		last = strings.TrimSpace(parts[2])
	}
	if strings.HasPrefix(head, "@") || looksLikeUsername(head) {
		return "", first, last, false
	}
	phone = normalizePhone(head)
	// 电话：以 + 开头，或去符号后位数足够（≥8）
	digits := strings.TrimPrefix(phone, "+")
	if phone == "" || len(digits) < 8 {
		return "", first, last, false
	}
	if first == "" {
		if len(digits) >= 4 {
			first = digits[len(digits)-4:]
		} else {
			first = digits
		}
	}
	return phone, first, last, true
}

func looksLikeUsername(s string) bool {
	if s == "" || strings.ContainsAny(s, " /+") {
		return false
	}
	// 纯数字按 user id，不当用户名
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return false
	}
	return true
}

func findUserByID(users []tg.UserClass, id int64) *tg.User {
	for _, u := range users {
		user, ok := u.(*tg.User)
		if ok && user != nil && user.ID == id {
			return user
		}
	}
	return nil
}

func classifyContactErr(err error) (biz, code, msg string, waitSec int) {
	if d, ok := tgerr.AsFloodWait(err); ok {
		return ResultFlood, "FLOOD_WAIT", err.Error(), int(d / time.Second)
	}
	rpc, ok := tgerr.As(err)
	if !ok {
		return ResultFailed, "contact_failed", err.Error(), 0
	}
	code = rpc.Type
	msg = err.Error()
	switch {
	case strings.Contains(code, "USERNAME_NOT_OCCUPIED") || strings.Contains(code, "USERNAME_INVALID"):
		return ResultNotFound, code, msg, 0
	case strings.Contains(code, "USER_PRIVACY") || strings.Contains(code, "PRIVACY"):
		return ResultPrivacy, code, msg, 0
	case strings.Contains(code, "USER_IS_BLOCKED"):
		return ResultBlocked, code, msg, 0
	case strings.Contains(code, "USER_DEACTIVATED") || strings.Contains(code, "INPUT_USER_DEACTIVATED"):
		return ResultDeactivated, code, msg, 0
	default:
		return ResultFailed, code, msg, 0
	}
}
