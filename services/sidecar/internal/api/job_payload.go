package api

import "encoding/json"

// MessagesJobPayload 私信批量任务可续跑载荷。
type MessagesJobPayload struct {
	Kind          string   `json:"kind"`
	AccountIDs    []string `json:"accountIds"`
	Mode          string   `json:"mode"`
	Targets       []string `json:"targets"`
	Text          string   `json:"text"`
	IntervalMs    int      `json:"intervalMs"`
	FailStopAfter int      `json:"failStopAfter"`
}

const jobKindMessages = "messages"

/**
 * 序列化私信任务 payload。
 *
 * @param p 载荷
 * @return JSON 字符串
 */
func encodeMessagesJob(p MessagesJobPayload) string {
	p.Kind = jobKindMessages
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

/**
 * 解析任务 payload 的 kind。
 *
 * @param raw JSON
 * @return kind
 */
func jobKindOf(raw string) string {
	var head struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal([]byte(raw), &head)
	return head.Kind
}
