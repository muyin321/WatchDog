<template>
  <el-container class="layout">
    <!-- 桌面：左侧极简菜单 -->
    <el-aside width="220px" class="aside">
      <div class="brand">
        <span class="dot" :class="'dot-'+overallStatus"></span>
        WatchDog<span class="brand-ai">AI</span>
      </div>
      <el-menu :default-active="$route.path" router class="menu">
        <el-menu-item index="/overview">
          <el-icon><Monitor /></el-icon><span>项目总览</span>
        </el-menu-item>
        <el-menu-item index="/config">
          <el-icon><Setting /></el-icon><span>配置中心</span>
        </el-menu-item>
        <el-menu-item index="/audit">
          <el-icon><Document /></el-icon><span>操作日志</span>
        </el-menu-item>
        <el-menu-item index="/backups">
          <el-icon><FolderOpened /></el-icon><span>备份仓库</span>
        </el-menu-item>
      </el-menu>
      <div class="ws-state" :class="wsOnline ? 'on' : 'off'">
        <i></i>{{ wsOnline ? '实时监控在线' : '连接中断，重连中…' }}
      </div>
    </el-aside>

    <el-container>
      <!-- 移动端：顶部品牌栏 -->
      <header class="m-top">
        <div class="brand">
          <span class="dot" :class="'dot-'+overallStatus"></span>
          WatchDog<span class="brand-ai">AI</span>
        </div>
        <div class="ws-state" :class="wsOnline ? 'on' : 'off'">
          <i></i>{{ wsOnline ? '在线' : '重连中' }}
        </div>
      </header>

      <el-main>
        <router-view v-slot="{ Component }">
          <transition name="fade-slide" mode="out-in">
            <component :is="Component" :key="$route.path" />
          </transition>
        </router-view>
      </el-main>
    </el-container>

    <!-- 移动端：底部 Tab 导航 -->
    <nav class="tabbar">
      <router-link
        v-for="t in tabs" :key="t.path" :to="t.path"
        class="tab" :class="{ on: $route.path === t.path }"
      >
        <el-icon><component :is="t.icon" /></el-icon>
        <span>{{ t.label }}</span>
      </router-link>
    </nav>

    <!-- 实时通知气泡 -->
    <div class="tg-toasts">
      <div
        v-for="t in toasts" :key="t.id"
        class="tg-toast" :class="[t.kind, { 'tg-toast-out': t.leaving }]"
        @click="dismiss(t.id)"
      >
        <div class="t-ico">
          <span v-if="t.fixState === 'fixing' || t.fixState === 'requested'" class="t-spin"></span>
          <template v-else>{{ icoOf(t.kind) }}</template>
        </div>
        <div class="t-main">
          <div class="t-title">{{ t.title }}</div>
          <div class="t-body" v-if="t.body">{{ t.body }}</div>
          <!-- 修复操作区：issue 气泡专属 -->
          <div class="t-actions" v-if="t.fix" @click.stop>
            <button
              v-if="t.fixState === 'idle'"
              class="t-fix-btn"
              @click="triggerFix(t)"
            >立即修复</button>
            <span v-else-if="t.fixState === 'requested' || t.fixState === 'fixing'" class="t-fix-wait">
              修复进行中，请稍候…
            </span>
          </div>
        </div>
      </div>
    </div>
  </el-container>
</template>

<script setup>
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { fetchProjects } from '@/api'
import { toasts, dismiss, triggerFix, startRealtime, onWS, wsState } from '@/notifications'
import { Monitor, Setting, Document, FolderOpened } from '@element-plus/icons-vue'

// 底部 Tab 配置（移动端）
const tabs = [
  { path: '/overview', label: '项目', icon: Monitor },
  { path: '/config',   label: '配置', icon: Setting },
  { path: '/audit',    label: '日志', icon: Document },
  { path: '/backups',  label: '备份', icon: FolderOpened },
]

// 顶部整体健康状态（供品牌灯展示）：任一项目 red -> 红，yellow -> 黄，否则绿
const projects = ref([])
const wsOnline = computed(() => wsState.online)

const overallStatus = computed(() => {
  if (projects.value.some(p => p.status === 'red')) return 'red'
  if (projects.value.some(p => p.status === 'yellow')) return 'yellow'
  return 'green'
})

// 图标映射（文本符号，零渲染开销）
function icoOf(kind) {
  return kind === 'ok' ? '✓' : kind === 'err' ? '!' : 'i'
}

let offStatus
let offAny
onMounted(async () => {
  startRealtime()

  // 项目状态实时刷新（指示灯）：status 消息直接更新本地列表
  offStatus = onWS('status', (msg) => {
    const p = projects.value.find(x => x.id === msg.project_id)
    if (p) p.status = msg.status
  })

  // 任一实时消息到达即刷新项目列表数据（保持与后端一致）
  offAny = onWS('*', () => {
    fetchProjects().then(list => { projects.value = list }).catch(() => {})
  })

  try { projects.value = await fetchProjects() } catch (e) { /* 未就绪 */ }
})

onUnmounted(() => {
  offStatus && offStatus()
  offAny && offAny()
})
</script>

<style src="./styles/global.css"></style>

<style scoped>
.ws-state {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 11px;
  color: var(--text-3);
}
/* 桌面：侧栏底部；移动端：顶栏右侧 */
.aside .ws-state { position: absolute; bottom: 16px; left: 22px; }
.ws-state i {
  width: 6px; height: 6px; border-radius: 50%;
  background: var(--text-3);
}
.ws-state.on i { background: var(--ok); }
.ws-state.on { color: var(--ok); }
.t-main { min-width: 0; }

/* ---- 修复按钮区 ---- */
.t-actions { margin-top: 8px; }
.t-fix-btn {
  appearance: none;
  border: 1px solid var(--accent);
  background: var(--accent);
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  padding: 5px 14px;
  border-radius: 7px;
  cursor: pointer;
  transition: background-color 0.15s var(--ease), transform 0.1s var(--ease);
}
.t-fix-btn:hover { background: var(--accent-deep); }
.t-fix-btn:active { transform: scale(0.97); }
.t-fix-wait {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-2);
}
/* 修复中转圈（GPU 友好） */
.t-spin {
  display: inline-block;
  width: 14px;
  height: 14px;
  border: 2px solid rgba(37, 99, 235, 0.2);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}
.t-ico { position: relative; }
.t-ico .t-spin {
  width: 16px;
  height: 16px;
}
@keyframes spin {
  to { transform: rotate(360deg); }
}
</style>
