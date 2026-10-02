<template>
  <div>
    <el-row :gutter="12">
      <el-col :span="6" v-for="c in cards" :key="c.label">
        <el-card shadow="never" :class="['stat-card', { clickable: c.link }]" @click="c.link && $router.push(c.link)">
          <div class="num">{{ c.value }}</div><div class="lbl">{{ c.label }}</div>
        </el-card>
      </el-col>
    </el-row>
    <el-card shadow="never" header="实时监控" style="margin-top:12px">
      <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
        <el-radio-group v-model="mNode" size="small" v-if="metrics.length">
          <el-radio-button v-for="m in metrics" :key="m.node_id" :value="m.name">
            {{ m.name }} <span :style="{ color: m.online ? '#188038' : '#d93025' }">●</span>
          </el-radio-button>
        </el-radio-group>
        <span style="font-size:12px;color:var(--my-text-2)">最近 {{ curPts.length }} 个采样点(约 {{ Math.round(curPts.length * 12 / 60) }} 分钟)</span>
      </div>
      <template v-if="curPts.length >= 2">
        <div class="mon-grid">
          <Spark title="CPU 使用率" :series="cpuSeries" :fixedMax="100"
                 :format="v => v.toFixed(1) + ' %'" sub="采样间隔 12s" />
          <Spark title="内存使用率" :series="memSeries" :fixedMax="100"
                 :format="v => v.toFixed(1) + ' %'"
                 :sub="curMemText" />
          <Spark title="网络吞吐" :series="netSeries" :format="fmtRate" sub="RX / TX 每秒" />
          <Spark title="磁盘 IO" :series="diskSeries" :format="fmtRate" sub="读 / 写 每秒" />
        </div>
      </template>
      <el-empty v-else description="指标采集中(约需 1 分钟积累两个采样点)" :image-size="60" />
    </el-card>

    <el-card shadow="never" header="支持的存储能力" style="margin-top:12px">
      <div v-if="caps" class="cap-wrap">
        <div class="cap-group" v-for="g in capGroups" :key="g.label">
          <div class="cap-label">{{ g.label }}</div>
          <div class="cap-chips">
            <el-tag v-for="c in g.items" :key="c" size="small" effect="plain" round>{{ c }}</el-tag>
          </div>
        </div>
        <div class="cap-group">
          <div class="cap-label">节点能力</div>
          <div class="cap-chips">
            <el-tag v-for="n in caps.nodes || []" :key="n.name" size="small" round
                    :type="n.online ? 'success' : 'info'">
              {{ n.name }}: {{ n.online ? '在线' : '离线' }} · FC {{ n.fc_capable ? '可用' : '不可用' }}
              · mkfs {{ Object.keys(n.mkfs || {}).filter(k => n.mkfs[k]).join('/') || '未探测' }}
            </el-tag>
          </div>
        </div>
      </div>
    </el-card>

    <el-row :gutter="12" style="margin-top:12px">
      <el-col :span="14">
        <el-card shadow="never" header="最近操作">
          <el-table :data="recent" size="small" max-height="320">
            <el-table-column prop="username" label="用户" width="90" />
            <el-table-column prop="action" label="动作" min-width="170" show-overflow-tooltip />
            <el-table-column prop="resource" label="资源" show-overflow-tooltip />
            <el-table-column prop="result" label="结果" width="70">
              <template #default="{ row }">
                <el-tag size="small" :type="row.result === 'ok' ? 'success' : 'danger'">{{ row.result }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="at" label="时间" width="150" show-overflow-tooltip />
          </el-table>
        </el-card>
      </el-col>
      <el-col :span="10">
        <el-card shadow="never" header="告警">
          <el-empty v-if="!alerts.length" description="无告警" :image-size="60" />
          <div v-for="(a, i) in alerts" :key="i">
            <el-tag size="small" :type="a.level === 'error' ? 'danger' : 'warning'">{{ a.level }}</el-tag>
            <span style="margin-left:8px">{{ a.text }}</span>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>
<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api, fmtBytes } from '../api.js'

import Spark from '../components/Spark.vue'
const s = ref({ summary: {} })
const caps = ref(null)
const metrics = ref([])
const mNode = ref('')
const curPts = computed(() => {
  const m = metrics.value.find(x => x.name === mNode.value) || metrics.value[0]
  return (m && m.points) || []
})
const seriesData = (f) => curPts.value.map(p => f(p))
const cpuSeries = computed(() => [{ name: 'CPU', color: '#4285f4', data: seriesData(p => p.cpu) }])
const memSeries = computed(() => [{ name: 'MEM', color: '#7b1fa2', data: seriesData(p => p.mem) }])
const netSeries = computed(() => [
  { name: 'RX', color: '#00897b', data: seriesData(p => p.net_rx) },
  { name: 'TX', color: '#f4511e', data: seriesData(p => p.net_tx) }
])
const diskSeries = computed(() => [
  { name: 'R', color: '#1a73e8', data: seriesData(p => p.disk_r) },
  { name: 'W', color: '#f9a825', data: seriesData(p => p.disk_w) }
])
const curMemText = computed(() => {
  const pts = curPts.value
  if (!pts.length) return ''
  return '采样间隔 12s'
})
function fmtRate(v) {
  const b = Number(v) || 0
  if (b >= 1073741824) return (b / 1073741824).toFixed(1) + ' GiB/s'
  if (b >= 1048576) return (b / 1048576).toFixed(1) + ' MiB/s'
  if (b >= 1024) return (b / 1024).toFixed(1) + ' KiB/s'
  return b.toFixed(0) + ' B/s'
}
const capGroups = ref([])
const cards = ref([])
const alerts = ref([])
const recent = ref([])
let timer = null

async function load() {
  try {
    const d = await api.get('/dashboard')
    s.value = d
    const sum = d.summary
    cards.value = [
      { label: '存储节点 (在线)', value: `${sum.nodes_online}/${sum.nodes}`, link: '/nodes' },
      { label: '存储池', value: sum.pools, link: '/pools' },
      { label: 'zvol 卷', value: sum.volumes, link: '/volumes' },
      { label: '容量已用 / 总', value: `${sum.allocated_human} / ${sum.total_size_human}`, link: '/pools' },
      { label: 'iSCSI 目标', value: sum.targets, link: '/targets' },
      { label: '活动会话', value: sum.sessions, link: '/targets' },
      { label: '快照', value: sum.snapshots, link: '/volumes' },
      { label: '主机 / 映射', value: `${sum.hosts} / ${sum.mappings}`, link: '/hosts' }
    ]
    caps.value = d.capabilities || null
    metrics.value = d.metrics || []
    if (metrics.value.length && !metrics.value.some(m => m.name === mNode.value)) {
      mNode.value = metrics.value[0].name
    }
    const cg = []
    if (d.capabilities) {
      cg.push({ label: '存储池布局', items: d.capabilities.pool_layouts || [] })
      cg.push({ label: '数据集特性', items: d.capabilities.dataset_features || [] })
      cg.push({ label: '块协议', items: d.capabilities.block_protocols || [] })
      cg.push({ label: '文件协议', items: d.capabilities.file_protocols || [] })
      cg.push({ label: '卷文件系统', items: d.capabilities.fs_types || [] })
    }
    capGroups.value = cg
    alerts.value = d.alerts || []
    recent.value = d.recent_audits || []
  } catch (e) { /* 已提示 */ }
}
onMounted(() => { load(); timer = setInterval(load, 15000) })
onUnmounted(() => clearInterval(timer))
</script>
<style scoped>
.num { font-size: 26px; font-weight: 700; color: var(--my-primary); }
.lbl { color: var(--my-text-2); font-size: 13px; margin-top: 4px; }
.cap-wrap { display: flex; flex-direction: column; gap: 10px; }
.cap-group { display: flex; align-items: baseline; gap: 10px; }
.cap-label { flex: 0 0 84px; color: var(--my-text-2); font-size: 12px; }
.cap-chips { display: flex; flex-wrap: wrap; gap: 6px; }
.stat-card.clickable { cursor: pointer; transition: box-shadow .15s, transform .15s; }
.stat-card.clickable:hover { box-shadow: 0 2px 10px rgba(60,64,67,.18) !important; transform: translateY(-1px); }
.mon-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 10px 22px; }
</style>
