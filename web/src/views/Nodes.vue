<template>
  <el-card shadow="never" header="存储节点 (Agent 启动后自动注册)" class="page-card">
    <el-table :data="nodes" v-loading="busy">
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="name" label="名称" width="130" />
      <el-table-column prop="agent_addr" label="Agent 地址" width="150" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.online ? 'success' : 'danger'" size="small">{{ row.online ? '在线' : '离线' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="agent_version" label="Agent" width="90" />
      <el-table-column prop="zfs_version" label="ZFS" width="120" />
      <el-table-column prop="pool_count" label="池" width="60" />
      <el-table-column prop="volume_count" label="卷" width="60" />
      <el-table-column prop="target_count" label="目标" width="60" />
      <el-table-column label="最后心跳" min-width="160">
        <template #default="{ row }">{{ row.last_seen_at ? new Date(row.last_seen_at).toLocaleString() : '-' }}</template>
      </el-table-column>
      <el-table-column label="操作" width="190" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="showDetail(row)">详情</el-button>
          <el-button v-if="canWrite()" size="small" type="danger" plain :disabled="row.online"
                     @click="removeNode(row)">移除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-drawer v-model="drawer" size="70%" :title="'节点 ' + (cur?.name || '')">
      <div v-loading="hwBusy">
        <template v-if="detail">
          <div class="sec-head">
            <h4>系统</h4>
            <el-button size="small" @click="loadHardware(cur)">刷新硬件</el-button>
          </div>
          <el-alert v-if="hwError" type="warning" :closable="false" show-icon
                    :title="hwError" style="margin-bottom:10px" />
          <el-descriptions :column="3" border size="small">
            <el-descriptions-item label="主机名">{{ hardware?.os?.hostname || detail.name }}</el-descriptions-item>
            <el-descriptions-item label="操作系统">
              <div class="os-cell">
                <img v-if="osIcon" :src="osIcon" class="os-logo" :class="{ wide: osId === 'openeuler' }" alt="" />
                <span>{{ hardware?.os?.pretty || detail.capabilities.os || '-' }}</span>
              </div>
            </el-descriptions-item>
            <el-descriptions-item label="状态">
              <el-tag size="small" :type="detail.online ? 'success' : 'danger'">{{ detail.online ? '在线' : '离线' }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="内核 / 架构">
              {{ hardware?.os ? hardware.os.kernel + ' / ' + hardware.os.arch : '-' }}</el-descriptions-item>
            <el-descriptions-item label="运行时间">{{ fmtUptime(hardware?.os?.uptime_s) }}</el-descriptions-item>
            <el-descriptions-item label="最后心跳">
              {{ detail.last_seen_at ? new Date(detail.last_seen_at).toLocaleString() : '-' }}</el-descriptions-item>
            <el-descriptions-item label="Agent 地址">{{ detail.agent_addr }}</el-descriptions-item>
            <el-descriptions-item label="Agent 版本">{{ detail.agent_version }}</el-descriptions-item>
            <el-descriptions-item label="ZFS 版本">{{ detail.zfs_version }}</el-descriptions-item>
          </el-descriptions>

          <h4>CPU</h4>
          <el-descriptions v-if="hardware?.cpu" :column="3" border size="small">
            <el-descriptions-item label="型号" :span="3">{{ hardware.cpu.model || '-' }}</el-descriptions-item>
            <el-descriptions-item label="逻辑处理器">{{ hardware.cpu.logical }}</el-descriptions-item>
            <el-descriptions-item label="物理核心">{{ hardware.cpu.physical }}</el-descriptions-item>
            <el-descriptions-item label="插槽 / 每插槽核心">
              {{ hardware.cpu.sockets }} / {{ hardware.cpu.cores_per_socket }}</el-descriptions-item>
            <el-descriptions-item label="主频">
              {{ hardware.cpu.mhz ? hardware.cpu.mhz + ' MHz' : '-' }}</el-descriptions-item>
          </el-descriptions>
          <div v-else class="hint">未获取到 CPU 信息</div>

          <h4>内存</h4>
          <div v-if="hardware?.memory">
            <el-progress :percentage="memoryPct" :stroke-width="16"
                         :format="() => `${fmtBytes(hardware.memory.used)} / ${fmtBytes(hardware.memory.total)}`" />
            <div class="hint" style="margin-top:6px">
              可用 {{ fmtBytes(hardware.memory.available) }} · 空闲 {{ fmtBytes(hardware.memory.free) }} ·
              交换 {{ fmtBytes(hardware.memory.swap_used) }} / {{ fmtBytes(hardware.memory.swap_total) }}
            </div>
          </div>
          <div v-else class="hint">未获取到内存信息</div>

          <div class="sec-head">
            <h4>RAID 卡</h4>
            <el-button v-if="detail.capabilities.raid?.available" size="small" text type="primary"
                       @click="$router.push('/raid')">管理 RAID</el-button>
          </div>
          <el-table v-if="raidCtrls.length" :data="raidCtrls" size="small" border>
            <el-table-column prop="model" label="型号" min-width="220" show-overflow-tooltip />
            <el-table-column prop="serial" label="序列号" width="120" />
            <el-table-column prop="fw" label="固件" width="130" />
            <el-table-column prop="health" label="健康" width="90" />
            <el-table-column label="BBU" width="120">
              <template #default="{ row }">{{ raidBbu(row) }}</template>
            </el-table-column>
            <el-table-column label="温度" width="80">
              <template #default="{ row }">{{ row.temperature === '' || row.temperature == null ? '-' : row.temperature + ' °C' }}</template>
            </el-table-column>
            <el-table-column prop="memory" label="内存" width="90" />
            <el-table-column label="PD / DG / VD" width="110">
              <template #default="{ row }">{{ row.pds ?? '-' }} / {{ row.dgs ?? '-' }} / {{ row.vds ?? '-' }}</template>
            </el-table-column>
          </el-table>
          <el-alert v-else type="info" :closable="false" show-icon
                    :title="detail.capabilities.raid?.available ? 'RAID 信息读取失败或暂无控制器' : '未检测到 RAID 卡(storcli 不可用)'" />

          <h4>磁盘</h4>
          <el-table v-if="hardware?.disks?.length" :data="hardware.disks" size="small" border>
            <el-table-column prop="name" label="设备" width="90" />
            <el-table-column label="容量" width="110">
              <template #default="{ row }">{{ fmtBytes(row.size) }}</template>
            </el-table-column>
            <el-table-column prop="model" label="型号" min-width="150" show-overflow-tooltip />
            <el-table-column prop="serial" label="序列号" min-width="130" show-overflow-tooltip />
            <el-table-column label="WWN" min-width="190">
              <template #default="{ row }"><span class="mono">{{ row.wwn || '-' }}</span></template>
            </el-table-column>
            <el-table-column label="接口/介质" width="100">
              <template #default="{ row }">{{ (row.tran || '-') + ' / ' + (row.media || '-') }}</template>
            </el-table-column>
            <el-table-column label="文件系统" width="100">
              <template #default="{ row }">{{ row.fs || '-' }}</template>
            </el-table-column>
            <el-table-column label="挂载点" min-width="130" show-overflow-tooltip>
              <template #default="{ row }">{{ row.mountpoints?.join(', ') || '-' }}</template>
            </el-table-column>
            <el-table-column prop="children" label="分区" width="60" />
          </el-table>
          <div v-else class="hint">未获取到物理磁盘信息</div>

          <h4>HBA / 存储控制器</h4>
          <template v-if="hardware?.hba?.fc_ports?.length || hardware?.hba?.adapters?.length">
            <el-table v-if="hardware.hba.fc_ports?.length" :data="hardware.hba.fc_ports" size="small" border style="margin-bottom:8px">
              <el-table-column prop="host" label="Host" width="70" />
              <el-table-column label="WWPN" min-width="190">
                <template #default="{ row }"><span class="mono">{{ row.port_name || '-' }}</span></template>
              </el-table-column>
              <el-table-column label="WWNN" min-width="190">
                <template #default="{ row }"><span class="mono">{{ row.node_name || '-' }}</span></template>
              </el-table-column>
              <el-table-column prop="port_state" label="状态" width="100" />
              <el-table-column prop="speed" label="速率" width="100" />
              <el-table-column prop="driver" label="驱动" width="120" />
            </el-table>
            <el-table v-if="hardware.hba.adapters?.length" :data="hardware.hba.adapters" size="small" border>
              <el-table-column prop="slot" label="PCI 地址" width="130" />
              <el-table-column prop="class" label="类型" width="190" />
              <el-table-column prop="description" label="描述" min-width="300" show-overflow-tooltip />
              <el-table-column prop="driver" label="驱动" width="140" />
            </el-table>
          </template>
          <el-alert v-else type="info" :closable="false" show-icon
                    title="未检测到 FC/SAS HBA 卡(RAID 卡请见上方)" />

          <h4>能力探测</h4>
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="ZFS JSON 输出">
              <el-tag size="small" :type="detail.capabilities.json_supported ? 'success' : 'danger'">
                {{ detail.capabilities.json_supported ? '支持 (OpenZFS ≥ 2.3)' : '不支持' }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="mkfs 工具">
              <el-tag v-for="(ok, name) in detail.capabilities.mkfs || {}" :key="name" size="small"
                      :type="ok ? 'success' : 'info'" style="margin-right:4px">{{ name }}{{ ok ? '' : ' 缺失' }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="RAID 管理">
              <el-tag size="small" :type="detail.capabilities.raid?.available ? 'success' : 'info'">
                {{ detail.capabilities.raid?.available ? 'storcli 可用' : '未检测到 storcli' }}</el-tag>
              <el-tag v-if="detail.capabilities.raid?.available" size="small" style="margin-left:4px"
                      :type="detail.capabilities.raid?.allow_mutation ? 'success' : 'warning'">
                {{ detail.capabilities.raid?.allow_mutation ? '可写' : '只读(写保护)' }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="节点写保护">
              <el-tag size="small" :type="detail.capabilities.allow_mutation === false ? 'warning' : 'success'">
                {{ detail.capabilities.allow_mutation === false ? '只读(全部变更已禁用)' : '可写' }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="LIO / rtslib">
              <el-tag size="small" :type="detail.capabilities.lio_available === false ? 'info' : 'success'">
                {{ detail.capabilities.lio_available === false ? '不可用(跳过 Target 同步)' : '可用' }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="FC 目标模式">
              <el-tag size="small" :type="detail.capabilities.fc?.fc_capable ? 'success' : 'info'">
                {{ detail.capabilities.fc?.fc_capable ? '可用' : '不可用' }}</el-tag>
              <span v-if="detail.capabilities.fc?.reason" style="color:#5f6368;font-size:12px;margin-left:8px">
                {{ detail.capabilities.fc.reason }}</span>
            </el-descriptions-item>
          </el-descriptions>
          <el-collapse style="margin-top:8px">
            <el-collapse-item title="原始 capabilities JSON" name="raw">
              <pre class="mono cap">{{ JSON.stringify(detail.capabilities, null, 2) }}</pre>
            </el-collapse-item>
          </el-collapse>
        </template>
        <el-empty v-else description="加载中…" />
      </div>
    </el-drawer>
  </el-card>
</template>
<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api, canWrite, fmtBytes } from '../api.js'
import { ElMessageBox } from 'element-plus'

const nodes = ref([])
const busy = ref(false)
const drawer = ref(false)
const cur = ref(null)
const detail = ref(null)
const hardware = ref(null)
const raidCtrls = ref([])
const hwBusy = ref(false)
const hwError = ref('')
let timer = null

const memoryPct = computed(() => Number(hardware.value?.memory?.used_pct) || 0)
const OS_ICONS = {
  debian: '/os/debian.svg', ubuntu: '/os/ubuntu.svg', centos: '/os/centos.svg',
  fedora: '/os/fedora.svg', rhel: '/os/redhat.svg', redhat: '/os/redhat.svg',
  almalinux: '/os/almalinux.svg', rocky: '/os/rocky.svg',
  opensuse: '/os/opensuse.svg', suse: '/os/suse.svg', openeuler: '/os/openeuler.svg',
  linux: '/os/linux.svg'
}
const osId = computed(() => {
  const id = (hardware.value?.os?.id || '').toLowerCase()
  const name = (hardware.value?.os?.pretty || detail.value?.capabilities?.os || '').toLowerCase()
  return OS_ICONS[id] ? id : (OS_ICONS[name] ? name : 'linux')
})
const osIcon = computed(() => OS_ICONS[osId.value] || '/os/linux.svg')

function fmtUptime(sec) {
  sec = Number(sec) || 0
  if (!sec) return '-'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  return (d ? `${d} 天 ` : '') + `${h} 小时 ${m} 分`
}
function raidBbu(c) {
  const v = (c?.bbu || '').toString()
  if (!v) return '-'
  const k = v.toLowerCase()
  if (k === 'absent' || k === 'msng') return '缺失'
  if (k === 'na') return '不可用'
  return v
}

async function load() {
  busy.value = true
  try { nodes.value = (await api.get('/nodes')).nodes } finally { busy.value = false }
}
async function showDetail(row) {
  cur.value = row
  detail.value = null
  hardware.value = null
  raidCtrls.value = []
  hwError.value = ''
  drawer.value = true
  detail.value = await api.get(`/nodes/${row.id}`)
  await loadHardware(row)
}
async function loadHardware(row) {
  if (!row) return
  hwBusy.value = true
  hwError.value = ''
  try {
    const hwReq = api.get(`/nodes/${row.id}/hardware`)
      .catch(() => { hwError.value = '实时硬件信息获取失败(节点可能离线或 Agent 版本过旧)'; return null })
    const raidReq = detail.value?.capabilities?.raid?.available
      ? api.get(`/nodes/${row.id}/raid/controllers`).catch(() => null)
      : Promise.resolve(null)
    const [hw, raid] = await Promise.all([hwReq, raidReq])
    hardware.value = hw
    raidCtrls.value = raid?.controllers || []
  } finally { hwBusy.value = false }
}
async function removeNode(row) {
  await ElMessageBox.confirm(`移除节点 ${row.name} 的记录?(需先停止该节点上的 Agent 服务)`, '确认', { type: 'warning' })
  await api.delete(`/nodes/${row.id}`)
  load()
}
onMounted(() => { load(); timer = setInterval(load, 8000) })
onUnmounted(() => clearInterval(timer))
</script>
<style scoped>
.cap { max-height: 60vh; overflow: auto; }
h4 { margin: 20px 0 10px; font-size: 14px; color: var(--my-text); }
.sec-head { display: flex; align-items: center; justify-content: space-between; }
.sec-head h4 { margin-bottom: 10px; }
.hint { color: var(--my-text-2); font-size: 12px; }
.os-cell { display: flex; align-items: center; gap: 8px; }
.os-logo { width: 26px; height: 26px; object-fit: contain; background: #fff; border-radius: 6px; padding: 2px; }
.os-logo.wide { width: 30px; height: 28px; }
</style>
