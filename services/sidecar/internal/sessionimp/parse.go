package sessionimp

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gotd/td/crypto"
	"github.com/gotd/td/session"
	_ "modernc.org/sqlite"
)

// AccountMeta 协议号 JSON 元数据（常见于 .json + .session 成对导出）。
type AccountMeta struct {
	Phone          string `json:"phone"`
	Username       string `json:"username"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	UserID         int64  `json:"user_id"`
	ID             int64  `json:"id"`
	AppID          int    `json:"app_id"`
	APIID          int    `json:"api_id"`
	AppHash        string `json:"app_hash"`
	APIHash        string `json:"api_hash"`
	SessionString  string `json:"session_string"`
	SessionFile    string `json:"session_file"`
	DeviceModel    string `json:"device_model"`
	Device         string `json:"device"`
	SystemVersion  string `json:"system_version"`
	SDK            string `json:"sdk"`
	AppVersion     string `json:"app_version"`
	LangCode       string `json:"lang_code"`
	LangPack       string `json:"lang_pack"`
	SystemLangCode string `json:"system_lang_code"`
	SystemLangPack string `json:"system_lang_pack"`
	TwoFA          string `json:"twoFA"`
	TwoFAAlt       string `json:"two_fa"`
	Password       string `json:"password"`
	DCID           int    `json:"dc_id"`
	ServerAddress  string `json:"server_address"`
	ServerPort     int    `json:"server_port"`
}

// DeviceProfile 连接指纹，写入 gotd DeviceConfig。
type DeviceProfile struct {
	DeviceModel    string `json:"deviceModel"`
	SystemVersion  string `json:"systemVersion"`
	AppVersion     string `json:"appVersion"`
	LangCode       string `json:"langCode"`
	LangPack       string `json:"langPack"`
	SystemLangCode string `json:"systemLangCode"`
}

// ParsedAccount 解析后的可落盘协议号。
type ParsedAccount struct {
	PreferredID string
	Phone       string
	Username    string
	LabelHint   string
	AppID       int
	AppHash     string
	Device      DeviceProfile
	Data        *session.Data
	Source      string // json_string | telethon_sqlite | gotd_json | telethon_string
}

/**
 * 解析账号 JSON 文本（含 session_string 或仅元数据）。
 *
 * @param raw JSON 文本
 * @return 元数据
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func ParseAccountJSON(raw []byte) (AccountMeta, error) {
	var meta AccountMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return AccountMeta{}, fmt.Errorf("account json: %w", err)
	}
	return meta, nil
}

/**
 * 从元数据提取 API 与设备信息。
 *
 * @param meta 账号 JSON
 * @return appID、appHash、设备
 * @author Auto
 * @date 2026-10-09
 */
func MetaCredentials(meta AccountMeta) (appID int, appHash string, device DeviceProfile) {
	appID = meta.APIID
	if appID == 0 {
		appID = meta.AppID
	}
	appHash = strings.TrimSpace(meta.APIHash)
	if appHash == "" {
		appHash = strings.TrimSpace(meta.AppHash)
	}
	device = DeviceProfile{
		DeviceModel:    firstNonEmpty(meta.DeviceModel, meta.Device),
		SystemVersion:  firstNonEmpty(meta.SystemVersion, meta.SDK),
		AppVersion:     strings.TrimSpace(meta.AppVersion),
		LangCode:       firstNonEmpty(meta.LangCode, "en"),
		LangPack:       strings.TrimSpace(meta.LangPack),
		SystemLangCode: firstNonEmpty(meta.SystemLangCode, meta.SystemLangPack, "en"),
	}
	return appID, appHash, device
}

/**
 * 将 gotd session.Data 序列化为 FileSessionStorage JSON。
 *
 * @param data 会话数据
 * @return gotd 存储字节
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func EncodeGotdSession(data *session.Data) ([]byte, error) {
	storage := &session.StorageMemory{}
	loader := session.Loader{Storage: storage}
	if err := loader.Save(context.Background(), data); err != nil {
		return nil, err
	}
	buf, err := storage.Bytes(nil)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

/**
 * 判断内容是否已是 gotd FileSessionStorage JSON。
 *
 * @param raw 文件内容
 * @return 是否 gotd 格式
 * @author Auto
 * @date 2026-10-09
 */
func IsGotdSessionJSON(raw []byte) bool {
	var probe struct {
		Version int `json:"Version"`
		Data    struct {
			AuthKey []byte `json:"AuthKey"`
			DC      int    `json:"DC"`
		} `json:"Data"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Version == 1 && len(probe.Data.AuthKey) > 0
}

/**
 * 解析 Telethon SQLite .session 文件。
 *
 * @param path 或临时文件路径
 * @return session.Data
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func ParseTelethonSQLiteFile(path string) (*session.Data, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open session sqlite: %w", err)
	}
	defer db.Close()

	var dcID int
	var addr string
	var port int
	var authKey []byte
	err = db.QueryRow(`SELECT dc_id, server_address, port, auth_key FROM sessions LIMIT 1`).
		Scan(&dcID, &addr, &port, &authKey)
	if err != nil {
		return nil, fmt.Errorf("read sessions table: %w", err)
	}
	if len(authKey) != 256 {
		return nil, fmt.Errorf("invalid auth_key length: %d", len(authKey))
	}
	return dataFromKey(dcID, addr, port, authKey), nil
}

/**
 * 解析 Telethon SQLite 字节（写入临时路径由调用方负责，或用内存 URI）。
 * 使用 modernc 的 file path 打开；调用方应先落盘临时文件。
 *
 * @param path SQLite 路径
 * @return session.Data
 * @author Auto
 * @date 2026-10-09
 */
func ParseTelethonSQLite(path string) (*session.Data, error) {
	return ParseTelethonSQLiteFile(path)
}

/**
 * 解码 session_string：优先标准 Telethon，再试「IP 字符串」变体（市面协议号包常见）。
 *
 * @param s session_string
 * @return session.Data
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func DecodeSessionString(s string) (*session.Data, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty session_string")
	}

	// 1. 标准 Telethon StringSession（URL-safe base64）
	if data, err := session.TelethonSession(s); err == nil {
		return data, nil
	}

	// 2. 变体：StdEncoding + IP 以长度前缀字符串存储
	if data, err := decodeIPStringSession(s); err == nil {
		return data, nil
	}

	return nil, fmt.Errorf("unsupported session_string format")
}

/**
 * 解码「1 + std base64」且 IP 为 length-prefixed ASCII 的 session_string。
 *
 * 布局：dc(1) + addrLen(u16be) + addr + port(u16be) + auth_key(256)
 *
 * @param s 字符串
 * @return session.Data
 * @author Auto
 * @date 2026-10-09
 */
func decodeIPStringSession(s string) (*session.Data, error) {
	if len(s) < 2 || s[0] != '1' {
		return nil, fmt.Errorf("bad version")
	}
	raw, err := base64.StdEncoding.DecodeString(s[1:])
	if err != nil {
		// 部分导出带换行或 URL 字符，再试 RawStd / URL
		raw, err = base64.RawStdEncoding.DecodeString(s[1:])
		if err != nil {
			return nil, err
		}
	}
	if len(raw) < 1+2+2+256 {
		return nil, fmt.Errorf("payload too short: %d", len(raw))
	}
	dcID := int(raw[0])
	addrLen := int(binary.BigEndian.Uint16(raw[1:3]))
	if addrLen <= 0 || 3+addrLen+2+256 > len(raw) {
		return nil, fmt.Errorf("bad addr length: %d", addrLen)
	}
	addr := string(raw[3 : 3+addrLen])
	port := int(binary.BigEndian.Uint16(raw[3+addrLen : 5+addrLen]))
	authKey := raw[5+addrLen : 5+addrLen+256]
	if net.ParseIP(addr) == nil && !strings.Contains(addr, ".") {
		return nil, fmt.Errorf("invalid addr in session_string: %q", addr)
	}
	return dataFromKey(dcID, addr, port, authKey), nil
}

func dataFromKey(dcID int, addr string, port int, authKey []byte) *session.Data {
	var key crypto.Key
	copy(key[:], authKey)
	id := key.WithID().ID
	host := strings.TrimSpace(addr)
	if port <= 0 {
		port = 443
	}
	return &session.Data{
		DC:        dcID,
		Addr:      net.JoinHostPort(host, strconv.Itoa(port)),
		AuthKey:   key[:],
		AuthKeyID: id[:],
	}
}

/**
 * 组装解析结果：JSON 元数据 + 可选 SQLite 会话（SQLite 优先于 session_string）。
 *
 * @param meta 账号 JSON
 * @param sqliteData 已解析的 .session（可为 nil）
 * @param fileBase 文件名主干（作 label/id 兜底）
 * @return ParsedAccount
 * @return 错误
 * @author Auto
 * @date 2026-10-09
 */
func BuildFromMetaAndSession(meta AccountMeta, sqliteData *session.Data, fileBase string) (ParsedAccount, error) {
	appID, appHash, device := MetaCredentials(meta)
	var data *session.Data
	source := ""
	if sqliteData != nil {
		data = sqliteData
		source = "telethon_sqlite"
	} else if strings.TrimSpace(meta.SessionString) != "" {
		decoded, err := DecodeSessionString(meta.SessionString)
		if err != nil {
			return ParsedAccount{}, err
		}
		data = decoded
		source = "json_string"
	} else {
		return ParsedAccount{}, fmt.Errorf("json 缺少 session_string，且未提供同名 .session")
	}

	phone := digitsOnly(meta.Phone)
	uid := meta.UserID
	if uid == 0 {
		uid = meta.ID
	}
	id := phone
	if id == "" && uid > 0 {
		id = strconv.FormatInt(uid, 10)
	}
	if id == "" {
		id = fileBase
	}
	if id == "" {
		id = fmt.Sprintf("acc_%x", data.AuthKeyID[:4])
	}

	label := fileBase
	if meta.FirstName != "" || meta.LastName != "" {
		label = strings.TrimSpace(meta.FirstName + " " + meta.LastName)
	}
	if label == "" && phone != "" {
		label = phone
	}

	return ParsedAccount{
		PreferredID: id,
		Phone:       phone,
		Username:    strings.TrimPrefix(strings.TrimSpace(meta.Username), "@"),
		LabelHint:   label,
		AppID:       appID,
		AppHash:     appHash,
		Device:      device,
		Data:        data,
		Source:      source,
	}, nil
}

/**
 * 仅从 Telethon .session 构建（无 JSON 元数据）。
 *
 * @param data 会话
 * @param fileBase 文件名主干
 * @return ParsedAccount
 * @author Auto
 * @date 2026-10-09
 */
func BuildFromSQLiteOnly(data *session.Data, fileBase string) ParsedAccount {
	id := fileBase
	if id == "" {
		id = fmt.Sprintf("acc_%x", data.AuthKeyID[:4])
	}
	return ParsedAccount{
		PreferredID: id,
		LabelHint:   id,
		Data:        data,
		Source:      "telethon_sqlite",
	}
}

/**
 * 文件名去扩展名。
 *
 * @param name 文件名
 * @return 主干
 * @author Auto
 * @date 2026-10-09
 */
func FileBase(name string) string {
	base := filepath.Base(name)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
