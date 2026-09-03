import axios from 'axios'
import { ElMessage } from 'element-plus'

// 统一的 axios 实例：开发期走 Vite 代理，生产走同源
const http = axios.create({ baseURL: '/api', timeout: 30000 })

http.interceptors.response.use(
  (r) => r.data,
  (e) => {
    ElMessage.error(e?.response?.data?.error || '请求失败')
    return Promise.reject(e)
  }
)

// ---- 项目 ----
export const fetchProjects = () => http.get('/projects')
export const createProject = (data) => http.post('/projects', data)
export const getProject = (id) => http.get(`/projects/${id}`)
export const updateProject = (id, data) => http.put(`/projects/${id}`, data)
export const deleteProject = (id) => http.delete(`/projects/${id}`)
export const startProject = (id) => http.post(`/projects/${id}/start`)
export const stopProject = (id) => http.post(`/projects/${id}/stop`)

// ---- 一键检查 ----
export const checkProject = (id) => http.post(`/projects/${id}/check`)
export const checkAllProjects = () => http.post('/check-all')

// ---- AI 修复（异步受理，进度与结果走 WS：fixing / fixed / rollback / error）----
export const fixProject = (id, file) => http.post(`/projects/${id}/fix`, { file })

// ---- AI 厂商清单（含默认模型）----
export const fetchAIProviders = () => http.get('/ai/providers')

// ---- 全局配置 ----
export const fetchConfig = () => http.get('/config')
export const setConfig = (key, value) => http.put(`/config/${key}`, { value })

// ---- 备份 ----
export const fetchBackups = (projectId) =>
  http.get('/backups', projectId ? { params: { project_id: projectId } } : {})
export const restoreBackup = (id) => http.post(`/backups/${id}/restore`)

// ---- 操作日志 ----
export const fetchAudit = () => http.get('/audit')

// ---- WebSocket ----
export function connectWS(onMessage) {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const ws = new WebSocket(`${proto}://${location.host}/ws`)
  ws.onmessage = (ev) => {
    try { onMessage(JSON.parse(ev.data)) } catch (e) { /* 忽略脏数据 */ }
  }
  return ws
}