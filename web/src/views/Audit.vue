<template>
  <div class="tg-enter">
    <div class="header">
      <h2 class="page-title">操作日志</h2>
      <div class="sub">最近 200 条关键动作（AI 修复/备份/回滚/拦截等）</div>
    </div>

    <!-- 桌面：表格 -->
    <el-table v-if="!isMobile" :data="logs" style="width:100%">
      <el-table-column prop="time" label="时间" width="180">
        <template #default="{ row }">{{ fmt(row.time) }}</template>
      </el-table-column>
      <el-table-column prop="action" label="动作" width="140" />
      <el-table-column prop="target" label="对象" min-width="200" show-overflow-tooltip />
      <el-table-column prop="detail" label="详情" min-width="240" show-overflow-tooltip />
      <el-table-column prop="level" label="级别" width="90">
        <template #default="{ row }">
          <el-tag :type="levelType(row.level)" size="small" effect="plain">{{ row.level }}</el-tag>
        </template>
      </el-table-column>
    </el-table>

    <!-- 手机：卡片列表 -->
    <div v-else class="log-list">
      <div v-for="l in logs" :key="l.id" class="log-item">
        <div class="log-head">
          <el-tag :type="levelType(l.level)" size="small" effect="plain">{{ l.level }}</el-tag>
          <b class="log-action">{{ l.action }}</b>
          <span class="log-time">{{ fmt(l.time) }}</span>
        </div>
        <div class="log-target" v-if="l.target">{{ l.target }}</div>
        <div class="log-detail" v-if="l.detail">{{ l.detail }}</div>
      </div>
      <el-empty v-if="!logs.length" description="暂无日志" />
    </div>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { fetchAudit } from '@/api'

const logs = ref([])
onMounted(async () => { logs.value = await fetchAudit() })

// 响应式：手机用卡片，桌面用表格
const isMobile = ref(window.innerWidth <= 819)
const onResize = () => { isMobile.value = window.innerWidth <= 819 }
window.addEventListener('resize', onResize)
onUnmounted(() => window.removeEventListener('resize', onResize))

function levelType(l) {
  return l === 'error' ? 'danger' : l === 'warn' ? 'warning' : 'info'
}
function fmt(t) { return t ? new Date(t).toLocaleString() : '' }
</script>

<style scoped>
.header { padding-bottom: 14px; }

/* 手机卡片列表 */
.log-list { display: flex; flex-direction: column; gap: 10px; }
.log-item {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 12px 14px;
}
.log-head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.log-action { font-size: 13px; }
.log-time { margin-left: auto; color: var(--text-3); font-size: 11px; }
.log-target { color: var(--text-2); font-size: 12px; margin-top: 6px; word-break: break-all; }
.log-detail { color: var(--text-3); font-size: 12px; margin-top: 4px; word-break: break-all; }
</style>
