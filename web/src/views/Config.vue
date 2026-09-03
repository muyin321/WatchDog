<template>
  <div class="tg-enter">
    <div class="header">
      <h2 class="page-title">配置中心</h2>
      <div class="sub">所有配置保存后立即生效，无需重启服务</div>
    </div>

    <!-- ======== AI 服务 ======== -->
    <el-card shadow="never" class="cfg-card">
      <template #header>
        <div class="card-head"><span class="ch-ico ai">AI</span>AI 检测服务</div>
      </template>

      <el-form :label-width="labelWidth" :label-position="labelPos" class="cfg-form">
        <el-form-item label="AI 厂商">
          <el-select
            v-model="pv.provider"
            style="width: 100%"
            placeholder="选择厂商"
            :loading="loadingProviders"
          >
            <template v-for="g in groupedProviders" :key="g.label">
              <el-option-group :label="g.label">
                <el-option
                  v-for="p in g.items"
                  :key="p.id"
                  :value="p.id"
                  :label="p.name"
                >
                  <span class="prov-name">{{ p.name }}</span>
                  <span class="prov-model" v-if="p.default_model">{{ p.default_model }}</span>
                </el-option>
              </el-option-group>
            </template>
          </el-select>
        </el-form-item>

        <!-- 当前厂商提示 -->
        <div class="provider-tip" v-if="currentProvider">
          <template v-if="currentProvider.region === 'cn'">国内厂商，直连无需代理</template>
          <template v-else-if="currentProvider.region === 'global'">国际厂商，国内服务器可能需要配置代理网络</template>
          <template v-else>自定义 OpenAI 兼容端点：中转网关 / 私有化部署 / Ollama 等均可接入</template>
          <template v-if="currentProvider.default_model">
            ，默认模型 <code>{{ currentProvider.default_model }}</code>
          </template>
        </div>

        <el-form-item label="API Key">
          <el-input
            v-model="pv.api_key"
            type="password"
            show-password
            :placeholder="keyPlaceholder"
          />
        </el-form-item>

        <el-form-item label="模型名">
          <el-input
            v-model="pv.model"
            :placeholder="modelPlaceholder"
            :disabled="false"
          />
        </el-form-item>

        <el-form-item label="Base URL">
          <el-input
            v-model="pv.base_url"
            :placeholder="baseUrlPlaceholder"
          />
          <div class="field-hint" v-if="needBaseURL">
            自定义厂商必须填写，例如 <code>http://localhost:11434/v1</code>
          </div>
          <div class="field-hint" v-else>
            可选。用于中转网关或自建代理，留空使用官方地址
          </div>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- ======== Telegram 通知 ======== -->
    <el-card shadow="never" class="cfg-card">
      <template #header>
        <div class="card-head"><span class="ch-ico tg">TG</span>Telegram 通知</div>
      </template>

      <el-form :label-width="labelWidth" :label-position="labelPos" class="cfg-form">
        <el-form-item label="Bot Token">
          <el-input
            v-model="pv.tg_token"
            type="password"
            show-password
            placeholder="123456789:ABCdefGHIjklMNOpqrsTUVwxyz"
          />
          <div class="field-hint">
            在 Telegram 中找 <code>@BotFather</code> 发送 <code>/newbot</code> 创建机器人后获得
          </div>
        </el-form-item>

        <el-form-item label="Chat ID">
          <el-input v-model="pv.tg_chat_id" placeholder="可留空，通过 /start 自动绑定" />
          <div class="field-hint">
            <b>留空即可自动绑定：</b>保存 Token 后，直接在 Telegram 里给你的机器人发送
            <code>/start</code>，Chat ID 会自动写入此处。也可手动填入群组/频道 ID（群组为负数）
          </div>
        </el-form-item>

        <!-- 绑定引导卡片 -->
        <div class="tg-guide" v-if="pv.tg_token">
          <div class="tg-guide-title">机器人已配置，可用命令：</div>
          <div class="tg-cmds">
            <span class="tg-cmd"><code>/start</code> 绑定通知目标</span>
            <span class="tg-cmd"><code>/status</code> 查看全部项目状态</span>
            <span class="tg-cmd"><code>/check</code> 触发全部项目检查</span>
            <span class="tg-cmd"><code>/help</code> 查看帮助</span>
          </div>
        </div>
      </el-form>
    </el-card>

    <!-- ======== 备份策略 ======== -->
    <el-card shadow="never" class="cfg-card">
      <template #header>
        <div class="card-head"><span class="ch-ico bk">BK</span>备份策略</div>
      </template>

      <el-form :label-width="labelWidth" :label-position="labelPos" class="cfg-form">
        <el-form-item label="备份保留天数">
          <el-input-number v-model="pv.retain_days" :min="1" :max="365" />
          <div class="field-hint">
            每次覆盖修复前都会自动备份原文件，超过天数后自动清理
          </div>
        </el-form-item>
      </el-form>
    </el-card>

    <div class="save-bar">
      <el-button type="primary" size="large" :loading="saving" @click="save">
        <el-icon style="margin-right:6px"><CircleCheck /></el-icon>保存全部配置
      </el-button>
    </div>
  </div>
</template>

<script setup>
import { reactive, ref, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { CircleCheck } from '@element-plus/icons-vue'
import { fetchConfig, setConfig, fetchAIProviders } from '@/api'
import { toast } from '@/notifications'

const pv = reactive({
  provider: '', api_key: '', model: '', base_url: '',
  retain_days: 7, tg_token: '', tg_chat_id: ''
})
const saving = ref(false)
const providers = ref([])
const loadingProviders = ref(false)

// 响应式表单布局：手机标签置顶，桌面左对齐
const isMobile = ref(window.innerWidth <= 819)
const labelWidth = computed(() => (isMobile.value ? '' : '150px'))
const labelPos = computed(() => (isMobile.value ? 'top' : 'right'))
const onResize = () => { isMobile.value = window.innerWidth <= 819 }
window.addEventListener('resize', onResize)
onUnmounted(() => window.removeEventListener('resize', onResize))

const keyMap = {
  provider: 'ai.provider', api_key: 'ai.api_key', model: 'ai.model', base_url: 'ai.base_url',
  retain_days: 'backup.retain_days', tg_token: 'notify.telegram_bot_token', tg_chat_id: 'notify.telegram_chat_id'
}

// ---- 厂商分组（国内 / 国际 / 自定义）----
const groupedProviders = computed(() => {
  const g = [
    { label: '国内厂商', items: providers.value.filter(p => p.region === 'cn') },
    { label: '国际厂商', items: providers.value.filter(p => p.region === 'global') },
    { label: '自定义接入', items: providers.value.filter(p => p.region === 'custom') },
  ]
  return g.filter(x => x.items.length)
})

const currentProvider = computed(() =>
  providers.value.find(p => p.id === pv.provider)
)

const needBaseURL = computed(() => currentProvider.value?.need_base_url)
const modelPlaceholder = computed(() =>
  currentProvider.value?.default_model
    ? `留空使用默认：${currentProvider.value.default_model}`
    : '必填，例如 gpt-4o-mini / glm-4 / qwen-plus'
)
const keyPlaceholder = computed(() =>
  pv.provider === 'custom' ? '兼容端点的鉴权 Key（Ollama 本地可留空）' : '在对应厂商控制台获取'
)
const baseUrlPlaceholder = computed(() =>
  needBaseURL.value ? 'http://your-gateway/v1（必填）' : 'https://your-proxy.example.com/v1（可选）'
)

onMounted(async () => {
  // 并行拉取：既有配置 + 厂商清单
  loadingProviders.value = true
  const [kv, provs] = await Promise.all([
    fetchConfig().catch(() => ({})),
    fetchAIProviders().catch(() => []),
  ])
  loadingProviders.value = false
  providers.value = provs || []

  for (const k in keyMap) {
    const v = kv[keyMap[k]]
    if (v !== undefined && v !== '') pv[k] = v
  }
  if (!pv.provider && providers.value.length) {
    pv.provider = providers.value[0].id
  }
})

async function save() {
  // 自定义厂商必须有 Base URL
  if (needBaseURL.value && !pv.base_url) {
    toast({ kind: 'err', title: '自定义厂商需填写 Base URL', body: '请填写 OpenAI 兼容端点地址', life: 5000 })
    return
  }
  saving.value = true
  try {
    for (const k in keyMap) {
      await setConfig(keyMap[k], String(pv[k] ?? ''))
    }
    ElMessage.success('配置已保存并即时生效')
    // Telegram 配置成功提示
    if (pv.tg_token && !pv.tg_chat_id) {
      toast({
        kind: 'ok',
        title: 'Telegram 机器人已启动',
        body: '现在去 Telegram 给机器人发送 /start 即可完成绑定',
        life: 8000,
      })
    }
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.header { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 16px; }
.page-title { margin: 0 0 4px; }
.sub { color: var(--text-3); font-size: 12px; }

.cfg-card { margin-bottom: 18px; }
.card-head {
  display: flex;
  align-items: center;
  gap: 10px;
  font-weight: 600;
}
.ch-ico {
  width: 32px; height: 32px;
  border-radius: 8px;
  display: flex; align-items: center; justify-content: center;
  font-size: 11px; font-weight: 700;
  letter-spacing: 0.5px;
}
.ch-ico.ai { background: #eff6ff; color: var(--accent); }
.ch-ico.tg { background: #eff6ff; color: var(--accent); }
.ch-ico.bk { background: #fffbeb; color: var(--warn); }

.cfg-form { max-width: 620px; }

/* 厂商提示条 */
.provider-tip {
  margin: -6px 0 14px 150px;
  font-size: 12px;
  color: var(--text-3);
  line-height: 1.6;
}
.provider-tip code {
  color: var(--accent);
  background: rgba(42, 171, 238, 0.12);
  padding: 1px 6px;
  border-radius: 5px;
  font-size: 11px;
}

/* 字段下方说明 */
.field-hint {
  font-size: 12px;
  color: var(--text-3);
  line-height: 1.6;
  margin-top: 6px;
}
.field-hint code {
  color: var(--accent);
  background: rgba(42, 171, 238, 0.12);
  padding: 1px 5px;
  border-radius: 5px;
  font-size: 11px;
}

/* 下拉项内布局：名称 + 默认模型 */
.prov-name { float: left; }
.prov-model {
  float: right;
  font-size: 11px;
  color: var(--text-3);
}

/* Telegram 绑定引导 */
.tg-guide {
  margin: 4px 0 8px 150px;
  padding: 12px 14px;
  border-radius: 12px;
  background: rgba(42, 171, 238, 0.08);
  border: 1px solid rgba(42, 171, 238, 0.22);
}
.tg-guide-title {
  font-size: 12px;
  color: var(--accent);
  margin-bottom: 8px;
  font-weight: 600;
}
.tg-cmds { display: flex; flex-wrap: wrap; gap: 6px 16px; }
.tg-cmd { font-size: 12px; color: var(--text-2); }
.tg-cmd code {
  color: var(--accent);
  background: rgba(42, 171, 238, 0.14);
  padding: 1px 6px;
  border-radius: 5px;
  font-size: 11px;
}

.save-bar {
  display: flex;
  justify-content: flex-end;
  padding: 4px 2px 10px;
}

@media (max-width: 700px) {
  .provider-tip, .tg-guide { margin-left: 0; }
}
</style>
