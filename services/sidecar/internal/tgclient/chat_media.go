package tgclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

// 媒体类型常量（与前端约定）。
const (
	MediaNone        = ""
	MediaPhoto       = "photo"
	MediaVideo       = "video"
	MediaDocument    = "document"
	MediaAudio       = "audio"
	MediaVoice       = "voice"
	MediaSticker     = "sticker"
	MediaLocation    = "location"
	MediaContact     = "contact"
	MediaPoll        = "poll"
	MediaWebpage     = "webpage"
	MediaUnsupported = "unsupported"
)

// ChatMediaMeta 消息媒体摘要（不含二进制）。
type ChatMediaMeta struct {
	Type     string  `json:"type,omitempty"`
	FileName string  `json:"fileName,omitempty"`
	MimeType string  `json:"mimeType,omitempty"`
	FileSize int64   `json:"fileSize,omitempty"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	Duration int     `json:"duration,omitempty"`
	Lat      float64 `json:"lat,omitempty"`
	Long     float64 `json:"long,omitempty"`
	Title    string  `json:"title,omitempty"`
	Address  string  `json:"address,omitempty"`
}

// ChatMediaBytes 下载到的媒体内容。
type ChatMediaBytes struct {
	MimeType string
	FileName string
	Data     []byte
}

// ChatSendMediaInput 发送媒体入参。
type ChatSendMediaInput struct {
	AccountID  string
	PeerType   string
	PeerID     int64
	AccessHash int64
	Kind       string // photo | video | document | location
	Caption    string
	FileName   string
	MimeType   string
	File       io.Reader
	FileSize   int64
	Lat        float64
	Long       float64
}

/**
 * 从 Telegram Message 解析媒体摘要与展示文案。
 *
 * @param msg 消息
 * @return 正文、媒体摘要
 * @author Auto
 * @date 2026-10-09
 */
func parseMessageContent(msg *tg.Message) (text string, media ChatMediaMeta) {
	if msg == nil {
		return "", ChatMediaMeta{}
	}
	text = strings.TrimSpace(msg.Message)
	mediaClass, ok := msg.GetMedia()
	if !ok || mediaClass == nil {
		if text == "" {
			text = "[非文本消息]"
		}
		return text, ChatMediaMeta{}
	}
	media = extractMediaMeta(mediaClass)
	if text == "" {
		text = mediaPreviewLabel(media)
	}
	return text, media
}

/**
 * 会话列表最后一条预览文案。
 *
 * @param msg 消息
 * @return 预览
 */
func dialogLastPreview(msg *tg.Message) string {
	if msg == nil {
		return ""
	}
	text, media := parseMessageContent(msg)
	if media.Type != MediaNone && strings.TrimSpace(msg.Message) == "" {
		text = mediaPreviewLabel(media)
	}
	runes := []rune(text)
	if len(runes) > 80 {
		return string(runes[:80]) + "…"
	}
	return text
}

/**
 * 媒体类型中文短标签。
 *
 * @param media 摘要
 * @return 标签
 */
func mediaPreviewLabel(media ChatMediaMeta) string {
	switch media.Type {
	case MediaPhoto:
		return "[图片]"
	case MediaVideo:
		return "[视频]"
	case MediaDocument:
		if media.FileName != "" {
			return "[文件] " + media.FileName
		}
		return "[文件]"
	case MediaAudio:
		return "[音频]"
	case MediaVoice:
		return "[语音]"
	case MediaSticker:
		return "[贴纸]"
	case MediaLocation:
		if media.Title != "" {
			return "[位置] " + media.Title
		}
		return "[位置]"
	case MediaContact:
		return "[联系人]"
	case MediaPoll:
		return "[投票]"
	case MediaWebpage:
		return "[链接]"
	default:
		return "[媒体]"
	}
}

/**
 * 解析 MessageMedia 为摘要。
 *
 * @param mediaClass 媒体
 * @return 摘要
 */
func extractMediaMeta(mediaClass tg.MessageMediaClass) ChatMediaMeta {
	switch m := mediaClass.(type) {
	case *tg.MessageMediaPhoto:
		meta := ChatMediaMeta{Type: MediaPhoto, MimeType: "image/jpeg"}
		if photo, ok := m.Photo.(*tg.Photo); ok && photo != nil {
			w, h, size := pickPhotoDims(photo.Sizes)
			meta.Width = w
			meta.Height = h
			meta.FileSize = int64(size)
			meta.FileName = fmt.Sprintf("photo_%d.jpg", photo.ID)
		}
		return meta
	case *tg.MessageMediaDocument:
		return metaFromDocumentMedia(m)
	case *tg.MessageMediaGeo:
		meta := ChatMediaMeta{Type: MediaLocation}
		if g, ok := m.Geo.(*tg.GeoPoint); ok && g != nil {
			meta.Lat = g.Lat
			meta.Long = g.Long
		}
		return meta
	case *tg.MessageMediaVenue:
		meta := ChatMediaMeta{Type: MediaLocation, Title: m.Title, Address: m.Address}
		if g, ok := m.Geo.(*tg.GeoPoint); ok && g != nil {
			meta.Lat = g.Lat
			meta.Long = g.Long
		}
		return meta
	case *tg.MessageMediaContact:
		name := strings.TrimSpace(m.FirstName + " " + m.LastName)
		return ChatMediaMeta{Type: MediaContact, Title: name, FileName: m.PhoneNumber}
	case *tg.MessageMediaPoll:
		q := ""
		if m.Poll.Question.Text != "" {
			q = m.Poll.Question.Text
		}
		return ChatMediaMeta{Type: MediaPoll, Title: q}
	case *tg.MessageMediaWebPage:
		return ChatMediaMeta{Type: MediaWebpage}
	default:
		return ChatMediaMeta{Type: MediaUnsupported}
	}
}

/**
 * 从 Document 媒体解析类型与属性。
 *
 * @param m MessageMediaDocument
 * @return 摘要
 */
func metaFromDocumentMedia(m *tg.MessageMediaDocument) ChatMediaMeta {
	meta := ChatMediaMeta{Type: MediaDocument}
	doc, ok := m.Document.(*tg.Document)
	if !ok || doc == nil {
		if m.Video {
			meta.Type = MediaVideo
		} else if m.Voice {
			meta.Type = MediaVoice
		}
		return meta
	}
	meta.MimeType = doc.MimeType
	meta.FileSize = doc.Size
	for _, attr := range doc.Attributes {
		switch a := attr.(type) {
		case *tg.DocumentAttributeFilename:
			meta.FileName = a.FileName
		case *tg.DocumentAttributeImageSize:
			meta.Width = a.W
			meta.Height = a.H
		case *tg.DocumentAttributeVideo:
			meta.Type = MediaVideo
			meta.Width = a.W
			meta.Height = a.H
			meta.Duration = int(a.Duration)
		case *tg.DocumentAttributeAudio:
			if a.Voice {
				meta.Type = MediaVoice
			} else {
				meta.Type = MediaAudio
			}
			meta.Duration = a.Duration
			if a.Title != "" {
				meta.Title = a.Title
			}
		case *tg.DocumentAttributeSticker:
			meta.Type = MediaSticker
			if a.Alt != "" {
				meta.Title = a.Alt
			}
		}
	}
	if meta.Type == MediaDocument {
		if m.Video || strings.HasPrefix(strings.ToLower(doc.MimeType), "video/") {
			meta.Type = MediaVideo
		} else if m.Voice {
			meta.Type = MediaVoice
		} else if strings.HasPrefix(strings.ToLower(doc.MimeType), "image/") {
			meta.Type = MediaPhoto
		}
	}
	if meta.FileName == "" {
		ext := extensionForMIME(meta.MimeType)
		meta.FileName = fmt.Sprintf("file_%d%s", doc.ID, ext)
	}
	return meta
}

/**
 * 选取照片最大可用尺寸。
 *
 * @param sizes 尺寸列表
 * @return 宽、高、文件大小
 */
func pickPhotoDims(sizes []tg.PhotoSizeClass) (w, h, size int) {
	bestType := ""
	bestArea := -1
	for _, s := range sizes {
		sw, sh, sz, typ, ok := photoSizeInfo(s)
		if !ok {
			continue
		}
		area := sw * sh
		if area > bestArea || (area == bestArea && typ > bestType) {
			bestArea = area
			w, h, size = sw, sh, sz
			bestType = typ
		}
	}
	return w, h, size
}

/**
 * 选取照片下载用的最大 ThumbSize type。
 *
 * @param sizes 尺寸列表
 * @return type 字符串
 */
func pickPhotoThumbType(sizes []tg.PhotoSizeClass) string {
	bestType := "x"
	bestArea := -1
	for _, s := range sizes {
		sw, sh, _, typ, ok := photoSizeInfo(s)
		if !ok || typ == "" {
			continue
		}
		area := sw * sh
		if area > bestArea {
			bestArea = area
			bestType = typ
		}
	}
	return bestType
}

/**
 * 读取 PhotoSize 宽高类型。
 *
 * @param s PhotoSizeClass
 * @return w,h,size,type,ok
 */
func photoSizeInfo(s tg.PhotoSizeClass) (w, h, size int, typ string, ok bool) {
	switch v := s.(type) {
	case *tg.PhotoSize:
		return v.W, v.H, v.Size, v.Type, true
	case *tg.PhotoSizeProgressive:
		sz := 0
		if len(v.Sizes) > 0 {
			sz = v.Sizes[len(v.Sizes)-1]
		}
		return v.W, v.H, sz, v.Type, true
	case *tg.PhotoCachedSize:
		return v.W, v.H, len(v.Bytes), v.Type, true
	default:
		return 0, 0, 0, "", false
	}
}

/**
 * 按 MIME 猜测扩展名。
 *
 * @param mimeType MIME
 * @return 扩展名（含点）
 */
func extensionForMIME(mimeType string) string {
	mimeType = strings.TrimSpace(strings.ToLower(mimeType))
	if mimeType == "" {
		return ""
	}
	exts, _ := mime.ExtensionsByType(mimeType)
	if len(exts) > 0 {
		return exts[0]
	}
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "audio/ogg":
		return ".ogg"
	default:
		return ""
	}
}

/**
 * 下载会话中某条消息的媒体文件。
 *
 * @param ctx 上下文
 * @param accountID 协议号
 * @param peerType user|channel|chat
 * @param peerID peer
 * @param accessHash hash
 * @param messageID 消息 id
 * @return 文件内容
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) DownloadChatMedia(
	ctx context.Context,
	accountID, peerType string,
	peerID, accessHash int64,
	messageID int,
) (ChatMediaBytes, error) {
	var out ChatMediaBytes
	if messageID <= 0 {
		return out, fmt.Errorf("messageId required")
	}
	err := m.WithAPI(ctx, accountID, func(ctx context.Context, api *tg.Client) error {
		msg, err := m.fetchChatMessage(ctx, api, accountID, peerType, peerID, accessHash, messageID)
		if err != nil {
			return err
		}
		mediaClass, ok := msg.GetMedia()
		if !ok || mediaClass == nil {
			return fmt.Errorf("message has no media")
		}
		loc, meta, err := inputLocationFromMedia(mediaClass)
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		if _, err := downloader.NewDownloader().Download(api, loc).Stream(ctx, &buf); err != nil {
			return fmt.Errorf("download media: %w", err)
		}
		out = ChatMediaBytes{
			MimeType: meta.MimeType,
			FileName: meta.FileName,
			Data:     buf.Bytes(),
		}
		if out.MimeType == "" {
			out.MimeType = "application/octet-stream"
		}
		return nil
	})
	return out, err
}

/**
 * 向会话发送媒体（图片/视频/文档/位置）。
 *
 * @param ctx 上下文
 * @param in 入参
 * @return 发送结果
 * @author Auto
 * @date 2026-10-09
 */
func (m *Manager) SendChatMedia(ctx context.Context, in ChatSendMediaInput) ChatSendResult {
	out := ChatSendResult{AccountID: in.AccountID, PeerID: in.PeerID}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	caption := strings.TrimSpace(in.Caption)

	err := m.WithAPI(ctx, in.AccountID, func(ctx context.Context, api *tg.Client) error {
		peer, err := m.inputPeerForChat(in.AccountID, in.PeerType, in.PeerID, in.AccessHash)
		if err != nil {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "invalid_peer"
			out.ErrorMessage = err.Error()
			return nil
		}
		var media tg.InputMediaClass
		switch kind {
		case MediaLocation:
			media = &tg.InputMediaGeoPoint{
				GeoPoint: &tg.InputGeoPoint{Lat: in.Lat, Long: in.Long},
			}
		case MediaPhoto, MediaVideo, MediaDocument:
			if in.File == nil {
				out.BusinessResult = ResultFailed
				out.ErrorCode = "file_required"
				out.ErrorMessage = "file required"
				return nil
			}
			fileName := strings.TrimSpace(in.FileName)
			if fileName == "" {
				fileName = "upload.bin"
			}
			up := uploader.NewUploader(api)
			var uploaded tg.InputFileClass
			if in.FileSize > 0 {
				uploaded, err = up.Upload(ctx, uploader.NewUpload(fileName, in.File, in.FileSize))
			} else {
				uploaded, err = up.FromReader(ctx, fileName, in.File)
			}
			if err != nil {
				out.BusinessResult = ResultFailed
				out.ErrorCode = "upload_failed"
				out.ErrorMessage = err.Error()
				return nil
			}
			mimeType := strings.TrimSpace(in.MimeType)
			if mimeType == "" {
				mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName)))
			}
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			// 按 kind 与 MIME 选择上传构造
			if kind == MediaPhoto || (kind != MediaDocument && strings.HasPrefix(mimeType, "image/") && !strings.Contains(mimeType, "svg")) {
				media = &tg.InputMediaUploadedPhoto{File: uploaded}
			} else if kind == MediaVideo || (kind != MediaDocument && strings.HasPrefix(mimeType, "video/")) {
				media = &tg.InputMediaUploadedDocument{
					File:     uploaded,
					MimeType: mimeType,
					Attributes: []tg.DocumentAttributeClass{
						&tg.DocumentAttributeFilename{FileName: fileName},
						&tg.DocumentAttributeVideo{
							SupportsStreaming: true,
							Duration:          0,
							W:                 0,
							H:                 0,
						},
					},
				}
			} else {
				media = &tg.InputMediaUploadedDocument{
					ForceFile: true,
					File:      uploaded,
					MimeType:  mimeType,
					Attributes: []tg.DocumentAttributeClass{
						&tg.DocumentAttributeFilename{FileName: fileName},
					},
				}
			}
		default:
			out.BusinessResult = ResultFailed
			out.ErrorCode = "invalid_kind"
			out.ErrorMessage = "kind must be photo|video|document|location"
			return nil
		}

		randomID, err := randomInt64()
		if err != nil {
			out.BusinessResult = ResultFailed
			out.ErrorCode = "random_id"
			out.ErrorMessage = err.Error()
			return nil
		}
		updates, err := api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
			Peer:     peer,
			Media:    media,
			Message:  caption,
			RandomID: randomID,
		})
		if err != nil {
			biz, code, msg, _ := classifySendErr(err)
			out.BusinessResult = biz
			out.ErrorCode = code
			out.ErrorMessage = msg
			return nil
		}
		out.OK = true
		out.BusinessResult = ResultOK
		out.MessageID = messageIDFromUpdates(updates)
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
 * 拉取单条会话消息。
 *
 * @param ctx 上下文
 * @param api TG API
 * @param accountID 协议号
 * @param peerType 类型
 * @param peerID id
 * @param accessHash hash
 * @param messageID 消息 id
 * @return 消息
 */
func (m *Manager) fetchChatMessage(
	ctx context.Context,
	api *tg.Client,
	accountID, peerType string,
	peerID, accessHash int64,
	messageID int,
) (*tg.Message, error) {
	peerType = strings.ToLower(strings.TrimSpace(peerType))
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}}
	var res tg.MessagesMessagesClass
	var err error
	if peerType == "channel" {
		hash := accessHash
		if hash == 0 {
			if ent, ok, _ := m.store.GetEntity(accountID, peerID, "channel"); ok {
				hash = ent.AccessHash
			}
		}
		if hash == 0 {
			return nil, fmt.Errorf("channel access_hash required")
		}
		res, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: peerID, AccessHash: hash},
			ID:      ids,
		})
	} else {
		res, err = api.MessagesGetMessages(ctx, ids)
	}
	if err != nil {
		return nil, err
	}
	msgs, _ := flattenHistory(res)
	for _, msg := range msgs {
		if msg != nil && msg.ID == messageID {
			return msg, nil
		}
	}
	return nil, fmt.Errorf("message %d not found", messageID)
}

/**
 * 从媒体构造下载 location。
 *
 * @param mediaClass 媒体
 * @return location、摘要、错误
 */
func inputLocationFromMedia(mediaClass tg.MessageMediaClass) (tg.InputFileLocationClass, ChatMediaMeta, error) {
	meta := extractMediaMeta(mediaClass)
	switch m := mediaClass.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := m.Photo.(*tg.Photo)
		if !ok || photo == nil {
			return nil, meta, fmt.Errorf("photo unavailable")
		}
		thumb := pickPhotoThumbType(photo.Sizes)
		return &tg.InputPhotoFileLocation{
			ID:            photo.ID,
			AccessHash:    photo.AccessHash,
			FileReference: photo.FileReference,
			ThumbSize:     thumb,
		}, meta, nil
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok || doc == nil {
			return nil, meta, fmt.Errorf("document unavailable")
		}
		return &tg.InputDocumentFileLocation{
			ID:            doc.ID,
			AccessHash:    doc.AccessHash,
			FileReference: doc.FileReference,
			ThumbSize:     "",
		}, meta, nil
	default:
		return nil, meta, fmt.Errorf("media type %s is not downloadable", meta.Type)
	}
}
