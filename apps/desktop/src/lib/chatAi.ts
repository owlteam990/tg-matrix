/**
 * 会话 AI 助手：本地开关/设置持久化 + Prompt 组装 + 候选解析 + 自动发送限速。
 * P0：手动生成；P1：来消息自动建议 + 群聊建议；P2：私聊自动发送。
 */

import type { ChatMessage } from '../api/sidecar'

export const CHAT_AI_STORE_KEY = 'tgmatrix.chatAi.v1'

/** 自动发送倒计时秒数范围 */
export const AUTO_SEND_COUNTDOWN_MIN = 5
export const AUTO_SEND_COUNTDOWN_MAX = 15
export const AUTO_SEND_COUNTDOWN_DEFAULT = 8

/** 同会话自动发送最小间隔（毫秒） */
export const AUTO_SEND_MIN_INTERVAL_MS = 30_000

/** 单协议号每日 AI 自动发送上限 */
export const AUTO_SEND_DAILY_LIMIT = 50

/** 单会话 AI 配置 */
export type ChatAiSessionConfig = {
  enabled: boolean
  /** suggest=仅建议；auto=生成后倒计时发送（仅私聊） */
  mode: 'suggest' | 'auto'
  /** 对方新消息到达时自动生成建议 */
  autoSuggestOnNew: boolean
  /** 抓取最近条数 */
  contextLimit: number
  /** 可选人设短备注 */
  persona?: string
  /** 自动发送倒计时秒数 */
  autoSendCountdownSec: number
  updatedAt: number
}

type DailyCounter = {
  /** YYYY-MM-DD（本地日） */
  date: string
  count: number
}

type StoreShape = {
  sessions: Record<string, ChatAiSessionConfig>
  defaultContextLimit: number
  /** accountId → 当日自动发送计数 */
  autoSendDaily: Record<string, DailyCounter>
  /** sessionKey → 上次自动发送时间戳 ms */
  lastAutoSendAt: Record<string, number>
}

const DEFAULT_CONTEXT = 20
const MIN_CONTEXT = 5
const MAX_CONTEXT = 50

/**
 * 是否私聊 peer。
 *
 * @param peerType 类型
 */
export function isPrivatePeer(peerType: string): boolean {
  return String(peerType || '').toLowerCase() === 'user'
}

/**
 * 是否允许开启 AI（私聊 + 群/频道）。
 *
 * @param peerType 类型
 */
export function canEnableChatAi(peerType: string): boolean {
  const t = String(peerType || '').toLowerCase()
  return t === 'user' || t === 'channel' || t === 'chat'
}

/**
 * 是否允许自动发送（仅私聊）。
 *
 * @param peerType 类型
 */
export function canEnableAutoSend(peerType: string): boolean {
  return isPrivatePeer(peerType)
}

/**
 * 会话存储键。
 *
 * @param accountId 协议号
 * @param peerType user|channel|chat
 * @param peerId peer
 */
export function chatAiSessionKey(
  accountId: string,
  peerType: string,
  peerId: number | string,
): string {
  return `${accountId}|${String(peerType || 'user').toLowerCase()}|${String(peerId)}`
}

function clampContext(n: number): number {
  const v = Math.round(Number(n) || DEFAULT_CONTEXT)
  if (v < MIN_CONTEXT) {
    return MIN_CONTEXT
  }
  if (v > MAX_CONTEXT) {
    return MAX_CONTEXT
  }
  return v
}

/**
 * 限制倒计时秒数。
 *
 * @param n 秒
 */
export function clampAutoSendCountdown(n: number): number {
  const v = Math.round(Number(n) || AUTO_SEND_COUNTDOWN_DEFAULT)
  if (v < AUTO_SEND_COUNTDOWN_MIN) {
    return AUTO_SEND_COUNTDOWN_MIN
  }
  if (v > AUTO_SEND_COUNTDOWN_MAX) {
    return AUTO_SEND_COUNTDOWN_MAX
  }
  return v
}

function defaultSession(limit = DEFAULT_CONTEXT): ChatAiSessionConfig {
  return {
    enabled: false,
    mode: 'suggest',
    autoSuggestOnNew: true,
    contextLimit: clampContext(limit),
    persona: '',
    autoSendCountdownSec: AUTO_SEND_COUNTDOWN_DEFAULT,
    updatedAt: Date.now(),
  }
}

function todayKey(): string {
  const d = new Date()
  const m = `${d.getMonth() + 1}`.padStart(2, '0')
  const day = `${d.getDate()}`.padStart(2, '0')
  return `${d.getFullYear()}-${m}-${day}`
}

/**
 * 读取本地存储。
 */
function loadStore(): StoreShape {
  try {
    const raw = localStorage.getItem(CHAT_AI_STORE_KEY)
    if (!raw) {
      return {
        sessions: {},
        defaultContextLimit: DEFAULT_CONTEXT,
        autoSendDaily: {},
        lastAutoSendAt: {},
      }
    }
    const data = JSON.parse(raw) as Partial<StoreShape>
    return {
      sessions: data.sessions && typeof data.sessions === 'object' ? data.sessions : {},
      defaultContextLimit: clampContext(data.defaultContextLimit ?? DEFAULT_CONTEXT),
      autoSendDaily:
        data.autoSendDaily && typeof data.autoSendDaily === 'object' ? data.autoSendDaily : {},
      lastAutoSendAt:
        data.lastAutoSendAt && typeof data.lastAutoSendAt === 'object'
          ? data.lastAutoSendAt
          : {},
    }
  } catch {
    return {
      sessions: {},
      defaultContextLimit: DEFAULT_CONTEXT,
      autoSendDaily: {},
      lastAutoSendAt: {},
    }
  }
}

/**
 * 写入本地存储。
 *
 * @param store 完整存储
 */
function saveStore(store: StoreShape): void {
  localStorage.setItem(CHAT_AI_STORE_KEY, JSON.stringify(store))
}

/**
 * 规范化会话配置（群聊强制 suggest）。
 *
 * @param cur 原始
 * @param peerType 类型
 * @param defaultLimit 默认条数
 */
function normalizeSession(
  cur: Partial<ChatAiSessionConfig> | undefined,
  peerType: string,
  defaultLimit: number,
): ChatAiSessionConfig {
  const base = defaultSession(defaultLimit)
  if (!cur) {
    return base
  }
  let mode: 'suggest' | 'auto' = cur.mode === 'auto' ? 'auto' : 'suggest'
  // 群聊禁止自动发送
  if (!canEnableAutoSend(peerType)) {
    mode = 'suggest'
  }
  return {
    enabled: !!cur.enabled,
    mode,
    autoSuggestOnNew: cur.autoSuggestOnNew !== false,
    contextLimit: clampContext(cur.contextLimit ?? defaultLimit),
    persona: cur.persona ? String(cur.persona) : '',
    autoSendCountdownSec: clampAutoSendCountdown(
      cur.autoSendCountdownSec ?? AUTO_SEND_COUNTDOWN_DEFAULT,
    ),
    updatedAt: Number(cur.updatedAt) || Date.now(),
  }
}

/**
 * 读取会话 AI 配置（无则返回默认关闭）。
 *
 * @param accountId 协议号
 * @param peerType 类型
 * @param peerId peer
 */
export function getChatAiSession(
  accountId: string,
  peerType: string,
  peerId: number | string,
): ChatAiSessionConfig {
  if (!accountId || peerId === '' || peerId == null) {
    return defaultSession()
  }
  const store = loadStore()
  const key = chatAiSessionKey(accountId, peerType, peerId)
  return normalizeSession(store.sessions[key], peerType, store.defaultContextLimit)
}

/**
 * 保存会话 AI 配置。
 *
 * @param accountId 协议号
 * @param peerType 类型
 * @param peerId peer
 * @param patch 部分更新
 */
export function saveChatAiSession(
  accountId: string,
  peerType: string,
  peerId: number | string,
  patch: Partial<ChatAiSessionConfig>,
): ChatAiSessionConfig {
  const store = loadStore()
  const key = chatAiSessionKey(accountId, peerType, peerId)
  const prev = normalizeSession(store.sessions[key], peerType, store.defaultContextLimit)
  const merged: ChatAiSessionConfig = {
    enabled: patch.enabled !== undefined ? !!patch.enabled : prev.enabled,
    mode: patch.mode === 'auto' ? 'auto' : patch.mode === 'suggest' ? 'suggest' : prev.mode,
    autoSuggestOnNew:
      patch.autoSuggestOnNew !== undefined ? !!patch.autoSuggestOnNew : prev.autoSuggestOnNew,
    contextLimit: clampContext(
      patch.contextLimit !== undefined ? patch.contextLimit : prev.contextLimit,
    ),
    persona:
      patch.persona !== undefined ? String(patch.persona || '') : prev.persona || '',
    autoSendCountdownSec: clampAutoSendCountdown(
      patch.autoSendCountdownSec !== undefined
        ? patch.autoSendCountdownSec
        : prev.autoSendCountdownSec,
    ),
    updatedAt: Date.now(),
  }
  const next = normalizeSession(merged, peerType, store.defaultContextLimit)
  store.sessions[key] = next
  saveStore(store)
  return next
}

/**
 * 检查当前是否允许自动发送（间隔 + 日上限）。
 *
 * @param accountId 协议号
 * @param peerType 类型
 * @param peerId peer
 * @return ok 与原因
 */
export function checkAutoSendAllowed(
  accountId: string,
  peerType: string,
  peerId: number | string,
): { ok: boolean; reason?: string; remainDaily?: number } {
  if (!canEnableAutoSend(peerType)) {
    return { ok: false, reason: '群聊禁止自动发送，已回落为建议模式' }
  }
  const store = loadStore()
  const key = chatAiSessionKey(accountId, peerType, peerId)
  const last = store.lastAutoSendAt[key] || 0
  const elapsed = Date.now() - last
  if (last > 0 && elapsed < AUTO_SEND_MIN_INTERVAL_MS) {
    const wait = Math.ceil((AUTO_SEND_MIN_INTERVAL_MS - elapsed) / 1000)
    return { ok: false, reason: `距上次自动发送不足 30 秒，请 ${wait}s 后再试` }
  }
  const day = todayKey()
  const daily = store.autoSendDaily[accountId]
  const count = daily && daily.date === day ? daily.count : 0
  if (count >= AUTO_SEND_DAILY_LIMIT) {
    return {
      ok: false,
      reason: `今日自动发送已达上限 ${AUTO_SEND_DAILY_LIMIT} 次`,
      remainDaily: 0,
    }
  }
  return { ok: true, remainDaily: AUTO_SEND_DAILY_LIMIT - count }
}

/**
 * 记录一次自动发送（限速计数）。
 *
 * @param accountId 协议号
 * @param peerType 类型
 * @param peerId peer
 */
export function recordAutoSend(
  accountId: string,
  peerType: string,
  peerId: number | string,
): void {
  const store = loadStore()
  const key = chatAiSessionKey(accountId, peerType, peerId)
  store.lastAutoSendAt[key] = Date.now()
  const day = todayKey()
  const prev = store.autoSendDaily[accountId]
  const count = prev && prev.date === day ? prev.count + 1 : 1
  store.autoSendDaily[accountId] = { date: day, count }
  saveStore(store)
}

/**
 * 今日剩余自动发送次数。
 *
 * @param accountId 协议号
 */
export function remainAutoSendDaily(accountId: string): number {
  if (!accountId) {
    return AUTO_SEND_DAILY_LIMIT
  }
  const store = loadStore()
  const day = todayKey()
  const daily = store.autoSendDaily[accountId]
  const count = daily && daily.date === day ? daily.count : 0
  return Math.max(0, AUTO_SEND_DAILY_LIMIT - count)
}

/**
 * 单条消息压成模型可读行。
 *
 * @param m 消息
 */
export function formatChatLineForAi(m: ChatMessage): string {
  const role = m.out ? '我' : m.fromName ? `对方(${m.fromName})` : '对方'
  let body = (m.text || '').trim()
  if (!body && m.media?.type) {
    const t = m.media.type
    if (t === 'photo' || t === 'sticker') {
      body = '[图片]'
    } else if (t === 'video') {
      body = '[视频]'
    } else if (t === 'audio' || t === 'voice') {
      body = '[语音]'
    } else if (t === 'document') {
      body = `[文件:${m.media.fileName || 'document'}]`
    } else if (t === 'location') {
      body = '[位置]'
    } else {
      body = `[${t}]`
    }
  }
  if (!body) {
    body = '[空消息]'
  }
  if (body.length > 500) {
    body = `${body.slice(0, 500)}…`
  }
  return `${role}: ${body}`
}

/**
 * 取最近 N 条并格式化为上下文。
 *
 * @param messages 当前历史（升序）
 * @param limit 条数
 */
export function buildChatContextLines(messages: ChatMessage[], limit: number): string[] {
  const n = clampContext(limit)
  const slice = messages.slice(-n)
  return slice.map(formatChatLineForAi)
}

/**
 * 组装会话回复用的 prompt（开源版保留工具函数，默认不调用远程模型）。
 *
 * @param opts 上下文与人设
 */
export function buildChatReplyPrompt(opts: {
  peerTitle: string
  peerType: string
  contextLines: string[]
  persona?: string
}): string {
  const persona = (opts.persona || '').trim()
  const ctx = opts.contextLines.join('\n')
  const isGroup = !isPrivatePeer(opts.peerType)
  return [
    isGroup
      ? '你是 Telegram 群聊助手。根据最近群消息，为「我」生成 1～3 条可直接发送的候选回复。'
      : '你是 Telegram 私聊助手。根据最近对话，为「我」生成 1～3 条可直接发送的候选回复。',
    '要求：',
    '- 语气自然、简短，不要扮演官方客服或做出违规承诺；',
    '- 每条不超过 200 字；',
    '- 三条候选尽量区分：简洁跟进 / 热情回应 / 推进下一步；',
    '- 若对方在提问，优先直接回答；',
    isGroup
      ? '- 群聊中避免刷屏与 @ 全员；必要时可点名一人，不要连发多句；'
      : '',
    '- 只输出 JSON，不要 Markdown 代码块，格式严格为：',
    '{"replies":["候选1","候选2","候选3"]}',
    '',
    `会话：${opts.peerTitle || (isGroup ? '群聊' : '私聊')}（类型 ${opts.peerType}）`,
    persona ? `人设/产品要点：${persona}` : '人设/产品要点：（无）',
    '',
    '最近对话（旧→新）：',
    ctx || '（暂无消息）',
  ]
    .filter(Boolean)
    .join('\n')
}

/**
 * 从模型文本解析候选回复列表。
 *
 * @param text 模型原文
 * @return 1～3 条
 */
export function parseChatReplyCandidates(text: string): string[] {
  const raw = String(text || '').trim()
  if (!raw) {
    return []
  }
  let body = raw
  const fence = raw.match(/```(?:json)?\s*([\s\S]*?)```/i)
  if (fence?.[1]) {
    body = fence[1].trim()
  }
  try {
    const obj = JSON.parse(body) as { replies?: unknown }
    if (Array.isArray(obj.replies)) {
      return obj.replies
        .map((x) => String(x || '').trim())
        .filter(Boolean)
        .slice(0, 3)
    }
  } catch {
    // fallthrough
  }
  const brace = body.match(/\{[\s\S]*\}/)
  if (brace) {
    try {
      const obj = JSON.parse(brace[0]) as { replies?: unknown }
      if (Array.isArray(obj.replies)) {
        return obj.replies
          .map((x) => String(x || '').trim())
          .filter(Boolean)
          .slice(0, 3)
      }
    } catch {
      // ignore
    }
  }
  const lines = body
    .split(/\n+/)
    .map((l) => l.replace(/^\s*[\d①②③]+[.、)\s]*/, '').trim())
    .filter((l) => l && !l.startsWith('{') && !l.startsWith('}'))
  if (lines.length) {
    return lines.slice(0, 3)
  }
  return [raw.slice(0, 500)]
}

/**
 * 生成幂等键。
 *
 * @param accountId 协议号
 * @param peerId peer
 * @param salt 额外盐
 */
export function chatReplyIdempotencyKey(
  accountId: string,
  peerId: number | string,
  salt: string,
): string {
  return `chat_reply:${accountId}:${peerId}:${salt}`
}
