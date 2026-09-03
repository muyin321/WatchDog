<template>
  <div class="tg-enter">
    <div class="header">
      <div>
        <h2 class="page-title">备份仓库</h2>
        <div class="sub">覆盖修复前自动备份，可一键回滚</div>
      </div>
      <el-select v-model="pid" clearable placeholder="全部项目" class="proj-select" @change="load">
        <el-option v-for="p in projects" :key="p.id" :label="p.name" :value="p.id" />
      </el-select>
    </div>

    <!-- 时间轴展示 -->
    <el-timeline v-if="backs.length">
      <el-timeline-item v-for="b in backs" :key="b.id" :timestamp="fmt(b.created_at)" placement="top">
        <el-card shadow="never">
          <div class="brow">
            <span class="bname">{{ b.project_name }}</span>
            <span class="breason">{{ b.reason }}</span>
            <el-tag size="small" v-if="b.rolled_back" type="success" effect="plain">已回滚</el-tag>
            <el-button size="small" type="warning" plain :disabled="b.rolled_back"
              class="restore-btn" @click="restore(b)">一键回滚</el-button>
          </div>
          <div class="bfile">{{ b.file_path }}</div>
          <div class="bbak">备份位置：{{ b.backup_path }}</div>
        </el-card>
      </el-timeline-item>
    </el-timeline>
    <el-empty v-else description="暂无备份记录（触发修复时自动生成）" />
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { fetchBackups, restoreBackup, fetchProjects } from '@/api'
import { onWS } from '@/notifications'

const backs = ref([])
const projects = ref([])
const pid = ref(null)

async function load() {
  backs.value = await fetchBackups(pid.value ?? undefined)
}
async function restore(b) {
  await ElMessageBox.confirm('确定还原该备份到原位置？当前文件会被覆盖。', '一键回滚', { type: 'warning' })
  await restoreBackup(b.id)
  ElMessage.success('已回滚')
  await load()
}
function fmt(t) { return t ? new Date(t).toLocaleString() : '' }

onMounted(() => {
  fetchProjects().then((p) => (projects.value = p)).catch(() => {})
  load()
})

// 复用全局 WS 总线（与通知中心同一条连接，含断线重连），新备份/错误即时刷新
let offAny = onWS('*', () => { load().catch(() => {}) })
onUnmounted(() => offAny && offAny())
</script>

<style scoped>
.header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; flex-wrap: wrap; padding-bottom: 14px; }
.proj-select { width: 200px; }
.brow { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.bname { font-weight: 600; }
.breason { color: var(--text-2); font-size: 12px; }
.restore-btn { margin-left: auto; }
.bfile, .bbak { color: var(--text-3); font-size: 12px; margin-top: 4px; word-break: break-all; }

@media (max-width: 819px) {
  .proj-select { width: 100%; }
  .restore-btn { margin-left: 0; }
}
</style>
