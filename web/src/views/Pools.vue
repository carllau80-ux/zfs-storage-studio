<template>
  <el-card shadow="never" header="存储池" class="page-card">
    <div style="margin-bottom:10px">
      <el-button v-if="canWrite()" type="primary" @click="createDlg = true">新建池</el-button>
      <el-button @click="load">刷新</el-button>
    </div>
    <el-table :data="pools" v-loading="busy" row-key="id" @expand-change="onExpand">
      <el-table-column type="expand" width="44">
        <template #default="{ row }">
          <div class="pool-expand">
            <div class="pool-expand-head">
              <b>{{ row.name }}</b> 池内卷 / 数据集
              <span v-if="!poolDs[row.id]" style="color:#999">加载中…</span>
              <span v-else-if="!poolDs[row.id].length" style="color:#999">暂无卷/数据集</span>
            </div>
            <el-table v-if="poolDs[row.id] && poolDs[row.id].length" :data="poolDs[row.id]" size="small" border>
              <el-table-column prop="name" label="名称 (dataset)" min-width="220" show-overflow-tooltip />
              <el-table-column label="类型" width="110">
                <template #default="{ row: d }">
                  <el-tag size="small" :type="d.type === 'volume' ? 'primary' : 'success'">
                    {{ d.type === 'volume' ? 'zvol 卷' : '文件系统' }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="容量" width="100">
                <template #default="{ row: d }">
                  <span v-if="d.type === 'volume'">{{ d.volsize_human }}</span><span v-else style="color:#bbb">-</span>
                </template>
              </el-table-column>
              <el-table-column prop="used_human" label="已用" width="100" />
              <el-table-column prop="compression" label="压缩" width="90" />
              <el-table-column label="文件系统" width="110">
                <template #default="{ row: d }">
                  <el-tag v-if="d.type === 'volume' && d.filesystem_type !== 'none'" size="small" type="success">{{ d.filesystem_type }}</el-tag>
                  <span v-else-if="d.type === 'volume'" style="color:#bbb">未格式化</span>
                  <span v-else style="color:#bbb">-</span>
                </template>
              </el-table-column>
              <el-table-column prop="snapshot_count" label="快照" width="70" />
              <el-table-column label="映射" width="80">
                <template #default="{ row: d }">
                  <el-tag v-if="d.mapped" size="small" type="warning">已映射</el-tag>
                  <span v-else style="color:#bbb">-</span>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="node_name" label="节点" width="110" />
      <el-table-column prop="name" label="池名" width="110" />
      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-tag size="small" :type="row.health === 'ONLINE' ? 'success' : 'danger'">{{ row.health }}</el-tag>
          <span style="font-size:12px;color:#999;margin-left:4px">{{ row.state }}</span>
        </template>
      </el-table-column>
      <el-table-column label="容量" min-width="200">
        <template #default="{ row }">
          <el-progress :percentage="row.size ? Math.round(row.allocated * 100 / row.size) : 0" :stroke-width="14"
                       :format="() => `${row.used_human} / ${row.size_human}`" />
        </template>
      </el-table-column>
      <el-table-column prop="volume_count" label="卷数" width="70" />
      <el-table-column label="操作" width="280" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="detail(row)">状态</el-button>
          <el-button v-if="canWrite()" size="small" @click="openProps(row)">属性</el-button>
          <el-button v-if="canWrite()" size="small" @click="scrub(row, 'start')">Scrub</el-button>
          <el-button v-if="canWrite()" size="small" type="danger" plain @click="destroy(row)">销毁</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 新建池:多 vdev 组 + slog/cache + 参数 -->
    <el-dialog v-model="createDlg" title="新建存储池" width="680px" top="6vh">
      <el-form label-width="96px">
        <el-form-item label="节点">
          <el-select v-model="form.node_id" style="width:100%" @change="onNodePick">
            <el-option v-for="n in nodes" :key="n.id" :value="n.id" :label="`${n.name} (${n.agent_addr})`" :disabled="!n.online" />
          </el-select>
        </el-form-item>
        <el-form-item label="池名"><el-input v-model="form.name" placeholder="tank" style="width:320px" /></el-form-item>

        <el-divider content-position="left">数据 vdev 组(1~n 组,每组可不同类型)</el-divider>
        <div v-for="(g, gi) in form.groups" :key="gi" style="display:flex;gap:8px;margin-bottom:8px;align-items:center">
          <el-select v-model="g.type" style="width:120px" @change="syncGroups">
            <el-option value="stripe" label="条带 stripe" />
            <el-option value="mirror" label="镜像 mirror" />
            <el-option value="raidz" label="raidz (raidz1)" />
            <el-option value="raidz2" label="raidz2" />
            <el-option value="raidz3" label="raidz3" />
          </el-select>
          <el-select v-model="g.disks" multiple style="flex:1;min-width:260px" placeholder="选择该组设备"
                     @change="syncGroups">
            <el-option v-for="d in candDisks" :key="d.path" :value="d.path"
                       :label="`${d.path}  ${fmtBytes(d.size)}`"
                       :disabled="isTaken('g' + gi, d.path)" />
          </el-select>
          <el-button v-if="form.groups.length > 1" text type="danger" @click="form.groups.splice(gi, 1); syncGroups()">移除</el-button>
        </div>
        <div style="margin-bottom:4px">
          <el-button size="small" @click="form.groups.push({ type: 'stripe', disks: [] })">+ 添加 vdev 组</el-button>
          <span style="font-size:12px;color:#999;margin-left:10px">
            组内设备数:stripe ≥1、mirror ≥2、raidz ≥3、raidz2 ≥4、raidz3 ≥5(以实际校验为准)
          </span>
        </div>

        <el-divider content-position="left">附加设备(可选)</el-divider>
        <el-form-item label="slog (log)">
          <el-switch v-model="form.logOn" style="margin-right:10px" />
          <el-select v-if="form.logOn" v-model="form.log" multiple style="width:380px" placeholder="日志盘(建议 1~2 块,可用小容量 SSD)">
            <el-option v-for="d in candDisks" :key="d.path" :value="d.path"
                       :label="`${d.path}  ${fmtBytes(d.size)}`" :disabled="isTaken('log', d.path)" />
          </el-select>
        </el-form-item>
        <el-form-item label="cache">
          <el-switch v-model="form.cacheOn" style="margin-right:10px" />
          <el-select v-if="form.cacheOn" v-model="form.cache" multiple style="width:380px" placeholder="L2ARC 缓存盘(可选)">
            <el-option v-for="d in candDisks" :key="d.path" :value="d.path"
                       :label="`${d.path}  ${fmtBytes(d.size)}`" :disabled="isTaken('cache', d.path)" />
          </el-select>
        </el-form-item>

        <el-divider content-position="left">建池参数</el-divider>
        <el-form-item label="ashift">
          <el-select v-model="form.options.ashift" style="width:140px">
            <el-option v-for="a in [9,10,11,12,13,14,15,16]" :key="a" :value="String(a)" :label="`${a} (${Math.pow(2, a)} B 扇区)`" />
          </el-select>
          <span style="font-size:12px;color:#999;margin-left:10px">4Kn 盘选 12;512e 盘常选 12~13</span>
        </el-form-item>
        <el-form-item label="压缩">
          <el-select v-model="form.options.compression" style="width:180px">
            <el-option value="lz4" label="lz4(默认推荐)" /><el-option value="off" label="off" />
            <el-option value="zstd" label="zstd" /><el-option value="zstd-3" label="zstd-3" />
            <el-option value="gzip" label="gzip" /><el-option value="lzjb" label="lzjb" /><el-option value="zle" label="zle" />
          </el-select>
        </el-form-item>
        <el-form-item label="recordsize">
          <el-select v-model="form.options.recordsize" style="width:180px">
            <el-option value="" label="默认 128k" />
            <el-option v-for="r in ['4k','8k','16k','32k','64k','128k','256k','512k','1m']" :key="r" :value="r" :label="r" />
          </el-select>
          <span style="font-size:12px;color:#999;margin-left:10px">卷/数据库负载建议 16k~64k</span>
        </el-form-item>
        <el-form-item label="重删 dedup">
          <el-select v-model="form.options.dedup" style="width:180px">
            <el-option value="off" label="off(默认)" /><el-option value="on" label="on" />
            <el-option value="verify" label="verify" />
          </el-select>
          <span v-if="form.options.dedup !== 'off'" style="font-size:12px;color:#d93025;margin-left:10px">
            重删显著增加内存开销,请确认内存充足
          </span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDlg = false">取消</el-button>
        <el-button type="danger" :loading="busy" :disabled="!canCreate" @click="create">创建(清空所选磁盘!)</el-button>
      </template>
    </el-dialog>

    <!-- 池状态(格式化) -->
    <el-drawer v-model="detailDlg" title="池状态" size="62%">
      <template v-if="st">
        <el-descriptions :column="3" border size="small" style="margin-bottom:12px">
          <el-descriptions-item label="状态">
            <el-tag size="small" :type="st.state === 'ONLINE' ? 'success' : 'danger'">{{ st.state }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="错误计数">{{ st.error_count }}</el-descriptions-item>
          <el-descriptions-item label="扫描 scan">
            <span v-if="st.scan">{{ st.scan.function }} · {{ st.scan.state }} <template v-if="st.scan.percentage !== undefined && st.scan.percentage !== null">· {{ st.scan.percentage }}%</template></span>
            <span v-else style="color:#999">无进行中扫描</span>
          </el-descriptions-item>
        </el-descriptions>
        <el-progress v-if="st.scan && st.scan.percentage !== undefined && st.scan.percentage !== null"
                     :percentage="Number(st.scan.percentage)" style="margin-bottom:12px" />
        <div v-if="st.scan" style="font-size:12px;color:#777;margin-bottom:12px">
          <template v-if="st.scan.passed !== undefined">通过 {{ st.scan.passed }} · 错误 {{ st.scan.errors }}</template>
          <template v-if="st.scan.start_time || st.scan.end_time">
            {{ st.scan.start_time ? '开始 ' + st.scan.start_time + ' ' : '' }}{{ st.scan.end_time ? '结束 ' + st.scan.end_time : '' }}
          </template>
        </div>

        <el-divider content-position="left">vdev 拓扑与错误</el-divider>
        <el-table :data="st.vdev_rows" size="small" max-height="380" border>
          <el-table-column label="名称 / 拓扑层级" min-width="260">
            <template #default="{ row }">
              <span :style="{ paddingLeft: (row.level * 18) + 'px' }">
                <span v-if="row.level > 0" style="margin-right:6px;color:#b0b8c4">└─</span>
                <span :style="{ fontWeight: row.level === 0 ? 600 : 400 }">{{ row.name }}</span>
                <span v-if="row.path" style="color:#999;font-size:12px;margin-left:6px">{{ row.path }}</span>
              </span>
            </template>
          </el-table-column>
          <el-table-column label="类型" width="90">
            <template #default="{ row }"><el-tag size="small" effect="plain">{{ row.type }}</el-tag></template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="row.state === 'ONLINE' ? 'success' : 'danger'">{{ row.state }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="读写错误" width="150">
            <template #default="{ row }">
              <span :class="{ err: (Number(row.read) || 0) + (Number(row.write) || 0) > 0 }">
                R {{ row.read }} / W {{ row.write }}
              </span>
            </template>
          </el-table-column>
          <el-table-column label="校验错误" width="90">
            <template #default="{ row }">
              <span :class="{ err: Number(row.cksum) > 0 }">{{ row.cksum }}</span>
            </template>
          </el-table-column>
          <el-table-column label="已分配 / 容量" width="150">
            <template #default="{ row }">{{ fmtBytes(row.alloc_bytes) }} / {{ fmtBytes(row.total_bytes) }}</template>
          </el-table-column>
        </el-table>

        <el-collapse style="margin-top:10px">
          <el-collapse-item title="原始 zpool status JSON">
            <pre class="mono raw">{{ JSON.stringify(st.raw, null, 2) }}</pre>
          </el-collapse-item>
        </el-collapse>
      </template>
    </el-drawer>

    <!-- 池属性(压缩/重删,作用于池根文件系统) -->
    <el-dialog v-model="propsDlg" :title="'池属性配置: ' + (propsPool?.name || '')" width="460px">
      <el-alert type="info" :closable="false" style="margin-bottom:10px"
                title="作用于池根文件系统(新建子数据集默认继承)" />
      <el-form label-width="90px">
        <el-form-item label="压缩">
          <el-select v-model="pp.compression" style="width:220px">
            <el-option value="inherit" label="继承(上一层)" /><el-option value="off" label="off" />
            <el-option value="lz4" label="lz4" /><el-option value="zstd" label="zstd" />
            <el-option value="zstd-3" label="zstd-3" /><el-option value="gzip" label="gzip" />
            <el-option value="lzjb" label="lzjb" /><el-option value="zle" label="zle" />
          </el-select>
          <div style="font-size:12px;color:#999;margin-top:4px">当前:{{ pp.cur_compression || '-' }}</div>
        </el-form-item>
        <el-form-item label="重删 dedup">
          <el-select v-model="pp.dedup" style="width:220px">
            <el-option value="inherit" label="继承(上一层)" /><el-option value="off" label="off" />
            <el-option value="on" label="on" /><el-option value="verify" label="verify" />
          </el-select>
          <div style="font-size:12px;color:#999;margin-top:4px">当前:{{ pp.cur_dedup || 'off' }}</div>
        </el-form-item>
      </el-form>
      <div style="font-size:12px;color:#c45656">开启重删会显著增加内存与 CPU 开销,建议先在小数据集验证。</div>
      <template #footer>
        <el-button @click="propsDlg = false">取消</el-button>
        <el-button type="primary" :loading="ppBusy" @click="applyPoolProps">应用</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>
<script setup>
import { computed, onMounted, ref } from 'vue'
import { api, canWrite, fmtBytes } from '../api.js'
import { ElMessage, ElMessageBox } from 'element-plus'

const pools = ref([]), nodes = ref([]), disks = ref([]), busy = ref(false), diskBusy = ref(false)
const candDisks = computed(() => disks.value.filter(d => !d.fs && !(d.children > 0) && !d.mounted))
const excludedCount = computed(() => disks.value.length - candDisks.value.length)
const createDlg = ref(false), detailDlg = ref(false), propsDlg = ref(false)
const st = ref(null), scanNote = ref('')
const form = ref({
  node_id: null, name: '',
  groups: [{ type: 'stripe', disks: [] }],
  logOn: false, log: [],
  cacheOn: false, cache: [],
  options: { ashift: '12', compression: 'lz4', recordsize: '', dedup: 'off' }
})
const propsPool = ref(null), propsRootId = ref(0), ppBusy = ref(false)
const pp = ref({ compression: 'inherit', dedup: 'off', cur_compression: '', cur_dedup: '' })

const poolDs = ref({})
async function onExpand(row, expandedRows) {
  const open = Array.isArray(expandedRows) ? expandedRows.some(r => r.id === row.id) : !!expandedRows
  if (!open || poolDs.value[row.id]) return
  try {
    const d = await api.get('/datasets?pool_id=' + row.id)
    poolDs.value = { ...poolDs.value, [row.id]: (d.datasets || []).filter(x => x.name !== row.name) }
  } catch (e) { poolDs.value = { ...poolDs.value, [row.id]: [] } }
}
async function load() {
  poolDs.value = {}
  busy.value = true
  try {
    const [p, n] = await Promise.all([api.get('/pools'), api.get('/nodes')])
    pools.value = p.pools; nodes.value = n.nodes
  } finally { busy.value = false }
}
async function loadDisks() {
  disks.value = []
  if (!form.value.node_id) return
  diskBusy.value = true
  try { disks.value = (await api.get(`/nodes/${form.value.node_id}/disks`)).disks } finally { diskBusy.value = false }
}
async function onNodePick() {
  await loadDisks()
  form.value.groups = [{ type: 'stripe', disks: [] }]
  form.value.log = []
  form.value.cache = []
}
function syncGroups() { /* v-model 联动;占位确保设备互斥实时刷新 */ }
function isTaken(key, path) {
  const taken = []
  form.value.groups.forEach((g, gi) => { if ('g' + gi !== key) taken.push(...g.disks) })
  if (key !== 'log') taken.push(...form.value.log)
  if (key !== 'cache') taken.push(...form.value.cache)
  return taken.includes(path)
}
const canCreate = computed(() =>
  !!form.value.node_id && !!form.value.name &&
  form.value.groups.some(g => g.disks && g.disks.length) &&
  (!form.value.logOn || form.value.log.length) &&
  (!form.value.cacheOn || form.value.cache.length)
)
async function create() {
  const payload = {
    node_id: form.value.node_id,
    name: form.value.name,
    layout: form.value.groups.filter(g => g.disks.length).map(g => ({ type: g.type, disks: g.disks })),
    options: form.value.options,
    log: form.value.logOn ? form.value.log : [],
    cache: form.value.cacheOn ? form.value.cache : [],
    spare: []
  }
  await api.post('/pools', payload)
  ElMessageBox.alert('池创建成功', '完成', { type: 'success' }).catch(() => {})
  createDlg.value = false
  load()
}
async function detail(row) {
  const d = await api.get(`/pools/${row.id}/status`)
  st.value = (d.status || {})[row.name] || null
  if (!st.value) { ElMessage.warning('未取得池状态'); return }
  detailDlg.value = true
}
async function scrub(row, action) {
  if (action === 'start') {
    await ElMessageBox.confirm(`对 ${row.name} 启动 scrub?`, '确认', { type: 'info' })
  }
  await api.post(`/pools/${row.id}/scrub`, { action })
}
async function destroy(row) {
  const v = await ElMessageBox.prompt(`销毁池 ${row.name} 将不可恢复,请输入池名确认`, '危险操作', {
    inputPattern: new RegExp('^' + row.name + '$'), inputErrorMessage: '池名不匹配', type: 'error' }).catch(() => null)
  if (!v) return
  await api.delete(`/pools/${row.id}`, { data: { confirm: v.value } })
  load()
}
async function openProps(row) {
  propsPool.value = row
  // 池根文件系统 = 该池下 name == pool 名的 filesystem 数据集
  const d = await api.get(`/datasets?node_id=${row.node_id}&pool_id=${row.id}&type=filesystem`)
  const root = d.datasets.find(x => x.name === row.name) || d.datasets[0]
  if (!root) { ElMessage.warning('未找到池根文件系统(轮询后自动出现,请稍后刷新)'); return }
  propsRootId.value = root.id
  propsDlg.value = true
  const dd = await api.get(`/datasets/${root.id}`).catch(() => null)
  const props = dd?.properties || {}
  pp.value = {
    compression: props.compression || root.compression || 'inherit',
    dedup: props.dedup || 'off',
    cur_compression: props.compression || root.compression || '-',
    cur_dedup: props.dedup || 'off'
  }
}
async function applyPoolProps() {
  ppBusy.value = true
  try {
    const actions = []
    if (pp.value.compression !== pp.value.cur_compression) {
      actions.push({ property: 'compression', value: pp.value.compression })
    }
    if (pp.value.dedup !== pp.value.cur_dedup) {
      if (pp.value.dedup !== 'off' && pp.value.dedup !== 'inherit') {
        const v = await ElMessageBox.prompt(`开启重删请键入池名 ${propsPool.value.name} 确认`, '重删确认', {
          inputPattern: new RegExp('^' + propsPool.value.name + '$'), inputErrorMessage: '池名不匹配', type: 'warning' }).catch(() => null)
        if (!v) return
        actions.push({ property: 'dedup', value: pp.value.dedup, confirm: v.value })
      } else {
        actions.push({ property: 'dedup', value: pp.value.dedup })
      }
    }
    if (!actions.length) { ElMessage.info('属性未变化'); return }
    for (const a of actions) await api.patch(`/datasets/${propsRootId.value}/properties`, a)
    ElMessage.success('池属性已应用(默认被子数据集继承)')
    await openProps(propsPool.value)
  } finally { ppBusy.value = false }
}
onMounted(load)
</script>
<style scoped>
.err { color: #f56c6c; font-weight: 600; }
.pool-expand { padding: 6px 16px 10px 52px; background: var(--my-surface-2, #f6f8fa); }
.pool-expand-head { margin: 4px 0 8px; color: var(--my-text-2); font-size: 13px; }
.raw { max-height: 40vh; overflow: auto; }
</style>
