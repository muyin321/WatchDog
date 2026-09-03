<template>
  <div class="tg-enter">
    <div class="header">
      <div>
        <h2 class="page-title">项目总览</h2>
        <div class="sub">文件保存后自动检查，结果实时推送</div>
      </div>
      <div class="header-actions">
        <el-button :loading="checkingAll" @click="checkAll">
          <el-icon style="margin-right:5px"><Refresh /></el-icon>一键检查全部
        </el-button>
        <el-button type="primary" @click="openCreate">
          <el-icon style="margin-right:5px"><Plus /></el-icon>添加项目
        </el-button>
      </div>
    </div>

    <!-- 状态图例 -->
    <div class="legend">
      <span><i class="dot dot-green"></i>健康</span>
      <span><i class="dot dot-yellow"></i>检查中</span>
      <span><i class="dot dot-red"></i>发现错误</span>
    </div>

    <!-- 项目卡片：桌面三列 / 平板两列 / 手机单列 -->
    <el-row :gutter="16">
      <el-col :xs="24" :sm="12" :md="8" v-for="p in projects" :key="p.id" class="pcol">
        <el-card shadow="never" class="pcard" :class="{ 'is-red': p.status === 'red' }">
          <div class="prow">
            <i class="dot" :class="'dot-'+(p.status || 'green')"></i>
            <b class="pname">{{ p.name }}</b>
            <el-tag v-if="!p.enabled" size="small" type="info" effect="plain">已禁用</el-tag>
          </div>
          <div class="ppath">{{ p.path }}</div>
          <div class="purl" v-if="p.url">
            <a :href="p.url" target="_blank" rel="noopener">{{ p.url }}</a>
          </div>
          <div class="pactions">
            <el-switch
              :model-value="p.enabled"
              :loading="p._toggling"
              @change="(v) => toggle(p, v)"
            />
            <el-button size="small" :loading="p._checking" @click="check(p)">检查</el-button>
            <el-button size="small" @click="openEdit(p)">编辑</el-button>
            <el-button size="small" type="danger" plain @click="remove(p)">删除</el-button>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 空状态 -->
    <el-empty v-if="!projects.length" description="还没有项目，点击「添加项目」开始监控" />

    <!-- 新增/编辑对话框：宽度自适应（手机全宽） -->
    <el-dialog v-model="dialogVisible" :title="editing ? '编辑项目' : '添加项目'" width="min(520px, 94vw)">
      <el-form :label-width="labelWidth" label-position="top">
        <el-form-item label="项目名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="磁盘绝对路径">
          <el-input v-model="form.path" placeholder="/home/www/test" />
        </el-form-item>
        <el-form-item label="访问 URL"><el-input v-model="form.url" placeholder="https://example.com（可空）" /></el-form-item>
        <el-form-item label="启用监控"><el-switch v-model="form.enabled" /></el-form-item>
        <el-form-item label="全自动修复">
          <el-switch v-model="form.auto_fix" />
          <span class="hint">低风险错误直接修复，高风险仍需确认</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Plus } from '@element-plus/icons-vue'
import {
  fetchProjects, createProject, updateProject, deleteProject,
  startProject, stopProject, checkProject, checkAllProjects
} from '@/api'
import { onWS, toast } from '@/notifications'

const projects = ref([])
const dialogVisible = ref(false)
const editing = ref(null)
const checkingAll = ref(false)
const form = ref({ name: '', path: '', url: '', telegram_bot_pid: 0, enabled: false, auto_fix: false })

// 窄屏（手机）：表单标签置顶；宽屏：左侧对齐
const isMobile = ref(window.innerWidth <= 819)
const labelWidth = computed(() => (isMobile.value ? '' : '110px'))
const onResize = () => { isMobile.value = window.innerWidth <= 819 }
window.addEventListener('resize', onResize)
onUnmounted(() => window.removeEventListener('resize', onResize))

async function load() {
  const list = await fetchProjects()
  // 保留进行中的按钮 loading 状态
  const old = new Map(projects.value.map(p => [p.id, p]))
  projects.value = list.map(p => ({ ...p, _checking: old.get(p.id)?._checking || false, _toggling: false }))
}
onMounted(load)

// 实时状态变化：后端 status 消息直接点亮指示灯（无需整页刷新）
let offStatus = onWS('status', (msg) => {
  const p = projects.value.find(x => x.id === msg.project_id)
  if (p) {
    p.status = msg.status
    if (msg.status !== 'yellow') p._checking = false // 检查结束
  }
})
onUnmounted(() => offStatus && offStatus())

function openEdit(p) {
  editing.value = p
  form.value = { ...p }
  dialogVisible.value = true
}
function openCreate() {
  editing.value = null
  form.value = { name: '', path: '', url: '', telegram_bot_pid: 0, enabled: true, auto_fix: false }
  dialogVisible.value = true
}

async function save() {
  if (!form.value.name || !form.value.path) {
    toast({ kind: 'err', title: '名称与路径必填', life: 3000 })
    return
  }
  try {
    if (editing.value) await updateProject(editing.value.id, form.value)
    else await createProject(form.value)
    dialogVisible.value = false
    await load()
    ElMessage.success('已保存')
  } catch (e) { /* 拦截器已弹出错误提示 */ }
}

async function toggle(p, v) {
  p._toggling = true
  try {
    if (v) await startProject(p.id)
    else await stopProject(p.id)
    p.enabled = v
  } catch (e) { /* 拦截器已提示 */ }
  finally { p._toggling = false }
}

// 一键检查单个项目：结果通过 WS 实时推送
async function check(p) {
  p._checking = true
  try {
    const r = await checkProject(p.id)
    toast({ kind: 'info', title: `已开始检查 ${p.name}`, body: `共 ${r.queued} 个文件，结果将实时推送`, life: 3500 })
    setTimeout(() => { p._checking = false }, 30000)
  } catch (e) {
    p._checking = false
  }
}

// 一键检查全部启用项目
async function checkAll() {
  checkingAll.value = true
  try {
    const r = await checkAllProjects()
    toast({ kind: 'info', title: '已触发全量检查', body: `共 ${r.queued} 个文件，结果将实时推送`, life: 3500 })
    setTimeout(() => { checkingAll.value = false }, 3000)
  } catch (e) {
    checkingAll.value = false
  }
}

async function remove(p) {
  await ElMessageBox.confirm('仅删除配置，不会删除磁盘上的源码文件，确认删除？', '提示', { type: 'warning' })
  await deleteProject(p.id)
  await load()
}
</script>

<style scoped>
.header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; flex-wrap: wrap; }
.header-actions { display: flex; gap: 10px; flex-wrap: wrap; }
.legend { display: flex; gap: 16px; color: var(--text-2); margin: 12px 0 16px; font-size: 12px; }
.legend span { display: flex; align-items: center; gap: 6px; }
.pcol { margin-bottom: 16px; }
.prow { display: flex; align-items: center; gap: 8px; }
.pname { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pcard.is-red { border-color: #fecaca !important; }
.ppath { color: var(--text-2); font-size: 12px; margin: 10px 0; word-break: break-all; }
.purl { font-size: 12px; margin-bottom: 10px; }
.purl a { color: var(--accent); text-decoration: none; }
.pactions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.hint { color: var(--text-3); font-size: 12px; margin-left: 8px; }

@media (max-width: 819px) {
  .header-actions { width: 100%; }
  .header-actions .el-button { flex: 1; }
}
</style>
