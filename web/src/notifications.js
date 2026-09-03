// notifications.js —— 通知中心 + WebSocket 实时消息处理
//
// 职责：
//  1. 全局 toast 队列（含 issue 气泡上的「立即修复」按钮与状态流转）
//  2. WebSocket 连接与消息分发（issue/fixing/fixed/rollback/summary/status/error）
//  3. 供各页面订阅实时事件（如项目状态变化时刷新指示灯）

import { reactive } from 'vue'
import { fixProject } from '@/api'

// ---- Toast 队列（响应式，由 App.vue 渲染）----
export const toasts = reactive([])

let toastSeq = 0
const MAX_TOASTS = 5 // 同屏最多 5 条，避免堆积

/**
 * 弹出一个通知气泡
 * @param {object} opt
 *   kind: 'ok' | 'err' | 'info'
 *   fix: { projectId, file } —— 提供时 issue 气泡展示「立即修复」按钮
 *   fixState 由本模块维护：idle -> requested -> fixing -> done | failed
 */
export function toast({ kind = 'info', title = '', body = '', life = 5000, fix = null }) {
  if (toasts.length >= MAX_TOASTS) toasts.shift() // 超限挤掉最旧的
  const id = ++toastSeq
  const t = { id, kind, title, body, fix, fixState: fix ? 'idle' : null, leaving: false }
  toasts.push(t)
  // 到期自动滑出移除（修复进行中的气泡不自动消失，等结果）
  setTimeout(() => {
    const cur = toasts.find(x => x.id === id)
    if (!cur) return
    if (cur.fixState === 'requested' || cur.fixState === 'fixing') {
      setTimeout(() => dismiss(id), 15000) // 修复中：再等 15s（AI 耗时兜底）
      return
    }
    dismiss(id)
  }, life)
  return id
}

/** 手动关闭一条通知 */
export function dismiss(id) {
  const cur = toasts.find(t => t.id === id)
  if (!cur) return
  // 修复进行中不允许手动关（防止丢失结果反馈入口）
  if (cur.fixState === 'requested' || cur.fixState === 'fixing') return
  cur.leaving = true // 触发滑出动画
  setTimeout(() => {
    const i = toasts.findIndex(t => t.id === id)
    if (i !== -1) toasts.splice(i, 1)
  }, 200)
}

/**
 * 点击「立即修复」：提交修复请求。
 * 后端异步执行（备份 -> AI 生成 -> 写入 -> 复检），结果经 WS 推回。
 */
export async function triggerFix(t) {
  if (!t.fix || t.fixState !== 'idle') return
  t.fixState = 'requested'
  // 保留原有项目前缀（如 [smoke-site]）
  const m = t.title.match(/^(\[[^\]]+\])\s*/)
  t.title = (m ? m[1] + ' ' : '') + '已提交修复请求…'
  t.body = 'AI 正在准备修复（会先自动备份原文件）'
  try {
    await fixProject(t.fix.projectId, t.fix.file)
    // 已受理：等待 WS 的 fixing/fixed/rollback 消息接管气泡
  } catch (e) {
    t.fixState = 'failed'
    t.kind = 'err'
    t.title = (m ? m[1] + ' ' : '') + '修复请求失败'
    t.body = e?.response?.data?.error || '请稍后重试'
    setTimeout(() => dismiss(t.id), 6000)
  }
}

/** 按 projectId + file 找到正在修复流程中的气泡 */
function findFixToast(projectId, file) {
  return toasts.find(
    (t) => t.fix && t.fix.projectId === projectId && t.fix.file === file &&
      (t.fixState === 'requested' || t.fixState === 'fixing')
  )
}

// ---- 实时事件总线：WS 消息 -> 页面订阅 ----
const listeners = new Map() // type -> Set<fn>

export function onWS(type, fn) {
  if (!listeners.has(type)) listeners.set(type, new Set())
  listeners.get(type).add(fn)
  return () => listeners.get(type)?.delete(fn)
}

function emit(type, msg) {
  listeners.get(type)?.forEach(fn => {
    try { fn(msg) } catch (e) { console.warn('[ws] handler error', e) }
  })
}

// ---- WebSocket 连接管理（断线自动重连）----
export const wsState = reactive({ online: false })

let ws = null
let retry = 0
let started = false

export function startRealtime() {
  if (started) return
  started = true
  connect()
}

function connect() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/ws`)

  ws.onopen = () => {
    retry = 0
    wsState.online = true
    toast({ kind: 'info', title: '实时监控已连接', body: '文件变更将实时推送到此处', life: 3000 })
  }

  ws.onmessage = (ev) => {
    let msg
    try { msg = JSON.parse(ev.data) } catch (e) { return }
    if (!msg || !msg.type) return
    handle(msg)
    emit(msg.type, msg) // 分发给页面订阅者
    emit('*', msg)      // 通配订阅
  }

  ws.onclose = () => {
    wsState.online = false
    // 指数退避重连（上限 30s），保持面板可用
    const delay = Math.min(30000, 1000 * Math.pow(2, retry++))
    setTimeout(connect, delay)
  }

  ws.onerror = () => { try { ws.close() } catch (e) { /* noop */ } }
}

/** 把后端消息翻译成用户可感知的通知 */
function handle(msg) {
  const file = shortFile(msg.file)
  const proj = msg.project ? `[${msg.project}] ` : ''

  switch (msg.type) {
    case 'issue':
      toast({
        kind: 'err',
        title: `${proj}发现 ${msg.issues?.length || 0} 处问题`,
        body: (file ? file + '\n' : '') + (msg.issues || []).join('\n'),
        life: 12000,
        // 挂上修复按钮（后端校验路径与 AI 配置）
        fix: msg.project_id && msg.file ? { projectId: msg.project_id, file: msg.file } : null,
      })
      break

    case 'fixing': {
      // 优先更新已有气泡的进度；气泡已过期则弹提示
      const t = findFixToast(msg.project_id, msg.file)
      if (t) {
        t.fixState = 'fixing'
        t.kind = 'info'
        t.title = `${proj}AI 修复中…`
        t.body = msg.text || file
      } else {
        toast({ kind: 'info', title: `${proj}AI 修复中…`, body: msg.text || file, life: 8000 })
      }
      break
    }

    case 'fixed': {
      const t = findFixToast(msg.project_id, msg.file)
      if (t) {
        t.fixState = 'done'
        t.kind = 'ok'
        t.title = `${proj}AI 修复成功`
        t.body = msg.text || file
        setTimeout(() => dismiss(t.id), 8000)
      } else {
        toast({ kind: 'ok', title: `${proj}AI 修复成功`, body: (file ? file + '\n' : '') + (msg.text || ''), life: 8000 })
      }
      break
    }

    case 'rollback': {
      const t = findFixToast(msg.project_id, msg.file)
      if (t) {
        t.fixState = 'failed'
        t.kind = 'err'
        t.title = `${proj}修复失败已回滚`
        t.body = msg.text || file
        setTimeout(() => dismiss(t.id), 10000)
      } else {
        toast({ kind: 'err', title: `${proj}修复失败已回滚`, body: (file ? file + '\n' : '') + (msg.text || ''), life: 10000 })
      }
      break
    }

    case 'error':
      // 运行层错误（AI 调用失败/文件不可读）：与代码问题区分，独立红色告警
      toast({
        kind: 'err',
        title: `${proj}运行错误`,
        body: (file ? file + '\n' : '') + (msg.text || '未知错误'),
        life: 12000,
      })
      break

    case 'summary':
      toast({
        kind: 'ok',
        title: `${proj}检查通过`,
        body: (file ? file + ' · ' : '') + (msg.text || ''),
        life: 4500,
      })
      break

    case 'status':
      // 状态变化不弹 toast（由指示灯动画表达），只分发事件
      break
  }
}

/** 路径裁剪：只显示相对尾部，避免气泡过宽 */
function shortFile(f) {
  if (!f) return ''
  const parts = f.split('/')
  return parts.length > 3 ? '.../' + parts.slice(-3).join('/') : f
}
