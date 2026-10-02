<template>
  <el-card shadow="never" class="page-card">
    <template #header>
      <div style="display:flex;justify-content:space-between;align-items:center">
        <span>数据集 (zvol / ZFS 文件系统)</span>
        <el-radio-group v-model="typeTab" size="small" @change="load">
          <el-radio-button value="volume">zvol 卷</el-radio-button>
          <el-radio-button value="filesystem">文件系统</el-radio-button>
        </el-radio-group>
      </div>
    </template>
    <div style="margin-bottom:10px; display:flex; gap:8px">
      <el-select v-model="fNode" placeholder="全部节点" clearable style="width:180px" @change="load">
        <el-option v-for="n in nodes" :key="n.id" :value="n.id" :label="n.name" />
      </el-select>
      <el-button v-if="canWrite()" type="primary" @click="openCreate">创建{{ typeTab === 'volume' ? '卷' : '文件系统' }}</el-button>
      <el-button @click="load">刷新</el-button>
    </div>
    <el-table :data="rows" v-loading="busy" row-key="id" @expand-change="onExpand">
      <el-table-column type="expand" width="44">
        <template #default="{ row }">
          <div class="snap-expand">
            <div class="snap-head">
              <b>{{ row.name }}</b> 的快照
              <span v-if="!snapMap[row.id]" style="color:#999">加载中…</span>
              <span v-else-if="!snapMap[row.id].length" style="color:#999">暂无快照</span>
            </div>
            <el-table v-if="snapMap[row.id] && snapMap[row.id].length" :data="snapMap[row.id]" size="small" border>
              <el-table-column prop="name" label="快照名" min-width="220" />
              <el-table-column prop="creation" label="创建时间" width="180" />
              <el-table-column prop="used_human" label="占用空间" width="110" />
              <el-table-column prop="dataset" label="所属数据集" min-width="200" show-overflow-tooltip />
            </el-table>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="node_name" label="节点" width="100" />
      <el-table-column prop="pool" label="池" width="100" />
      <el-table-column prop="name" label="名称 (dataset)" min-width="240" show-overflow-tooltip />
      <el-table-column label="容量" width="110">
        <template #default="{ row }">
          <span v-if="row.type === 'volume'">{{ row.volsize_human }}</span><span v-else style="color:#bbb">-</span>
        </template>
      </el-table-column>
      <el-table-column label="已用" width="100">
        <template #default="{ row }">{{ row.used_human }}</template>
      </el-table-column>
      <el-table-column prop="compression" label="压缩" width="90" />
      <el-table-column label="类型 / 文件系统" width="150">
        <template #default="{ row }">
          <el-tag v-if="row.type === 'filesystem'" size="small" type="primary">文件系统</el-tag>
          <template v-else>
            <el-tag v-if="row.filesystem_type !== 'none'" size="small" type="success">{{ row.filesystem_type }}</el-tag>
            <el-tag v-else size="small" type="info">未格式化</el-tag>
          </template>
        </template>
      </el-table-column>
      <el-table-column label="映射" width="70">
        <template #default="{ row }">
          <el-tag v-if="row.type === 'volume' && row.mapped" size="small" type="warning">已映射</el-tag>
          <span v-else style="color:#bbb">-</span>
        </template>
      </el-table-column>
      <el-table-column prop="snapshot_count" label="快照" width="70" />
      <el-table-column label="操作" width="90" fixed="right">
        <template #default="{ row }"><el-button size="small" @click="openDetail(row)">详情</el-button></template>
      </el-table-column>
    </el-table>

    <!-- 创建 -->
    <el-dialog v-model="createDlg" :title="'创建 ' + (typeTab === 'volume' ? 'zvol' : '文件系统 dataset')" width="560px">
      <el-radio-group v-if="typeTab === 'volume'" v-model="createMode" size="small" style="margin-bottom:10px">
        <el-radio-button value="blank">新建空白卷</el-radio-button>
        <el-radio-button value="clone">从快照克隆</el-radio-button>
      </el-radio-group>
      <el-form label-width="110px">
        <el-form-item label="节点">
          <el-select v-model="cf.node_id" style="width:100%" @change="onNodeChange">
            <el-option v-for="n in nodes" :key="n.id" :value="n.id" :label="n.name" :disabled="!n.online" />
          </el-select>
        </el-form-item>

        <!-- 克隆模式:选源卷 + 快照 -->
        <template v-if="createMode === 'clone' && typeTab === 'volume'">
          <el-form-item label="源卷(带快照)">
            <el-select v-model="cf.source_dataset_id" style="width:100%" placeholder="选择带快照的 zvol"
                       :loading="srcBusy" @change="loadCloneSnaps">
              <el-option v-for="v in sourceVols" :key="v.id" :value="v.id"
                         :label="`${v.name} (${v.volsize_human}, ${v.snapshot_count} 个快照)`" />
            </el-select>
          </el-form-item>
          <el-form-item label="快照">
            <el-select v-model="cf.snapshot" style="width:100%" placeholder="选择要克隆的快照">
              <el-option v-for="sn in cloneSnaps" :key="sn.id" :value="sn.name"
                         :label="`${sn.name} (${sn.creation})`" />
            </el-select>
          </el-form-item>
          <el-form-item label="新卷名">
            <el-input v-model="cf.name" placeholder="克隆出的可写新卷名,如 vol-clone-01" />
          </el-form-item>
          <el-alert type="info" :closable="false" title="容量/压缩/块大小等继承自快照;克隆卷可写,可继续格式化或映射"
                    style="margin-bottom:8px" />
        </template>

        <!-- 空白卷 / 文件系统 -->
        <template v-else>
          <el-form-item label="存储池">
            <el-select v-model="cf.pool_id" style="width:100%">
              <el-option v-for="p in nodePools" :key="p.id" :value="p.id"
                         :label="`${p.name} (空闲 ${p.free_human})`" />
            </el-select>
          </el-form-item>
          <el-form-item :label="typeTab === 'volume' ? '卷名' : '数据集名'">
            <el-input v-model="cf.name" :placeholder="typeTab === 'volume' ? '如 vol-mysql01' : '如 data-01 / logs'" />
          </el-form-item>
          <template v-if="typeTab === 'volume'">
            <el-form-item label="容量"><el-input v-model="cf.size" placeholder="如 2G / 512M" /></el-form-item>
            <el-form-item label="块大小"><el-input v-model="cf.volblocksize" placeholder="可选 4k/16k/128k,默认继承" /></el-form-item>
            <el-form-item label="sparse"><el-switch v-model="cf.sparse" /></el-form-item>
          </template>
          <template v-else>
            <el-form-item label="挂载点">
              <el-radio-group v-model="cf.mountMode">
                <el-radio-button value="none">不挂载 none</el-radio-button>
                <el-radio-button value="auto">自动 /pool/name</el-radio-button>
                <el-radio-button value="custom">自定义路径</el-radio-button>
              </el-radio-group>
              <el-input v-if="cf.mountMode === 'custom'" v-model="cf.mountpoint" style="width:220px;margin-left:10px"
                        placeholder="如 /tank/share01" />
              <div style="font-size:12px;color:#999;margin-top:4px">
                需要 NFS / SMB 共享时必须可挂载(选"自动"或自定义路径);仅作快照源可选 none。
              </div>
            </el-form-item>
          </template>
          <el-form-item label="压缩">
            <el-select v-model="cf.compression" style="width:200px">
              <el-option value="inherit" label="继承" /><el-option value="off" label="off" />
              <el-option value="lz4" label="lz4" /><el-option value="zstd" label="zstd" />
              <el-option value="gzip" label="gzip" /><el-option value="lzjb" label="lzjb" /><el-option value="zle" label="zle" />
            </el-select>
          </el-form-item>
          <el-form-item v-if="typeTab === 'filesystem'" label="重删 dedup">
            <el-select v-model="cf.dedup" style="width:200px">
              <el-option value="inherit" label="继承" /><el-option value="off" label="off" />
              <el-option value="on" label="on" /><el-option value="verify" label="verify" />
            </el-select>
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="createDlg = false">取消</el-button>
        <el-button type="primary" :loading="busy" :disabled="!canSubmit" @click="create">创建</el-button>
      </template>
    </el-dialog>

    <!-- 详情 -->
    <el-drawer v-model="detailDlg" size="60%" :title="cur ? cur.name : ''">
      <template v-if="detail">
        <el-descriptions :column="3" border size="small">
          <el-descriptions-item label="类型">{{ detail.dataset.type }}</el-descriptions-item>
          <el-descriptions-item v-if="detail.dataset.origin && detail.dataset.origin !== '-'" label="克隆来源" :span="2">
            <el-tag size="small" type="warning">{{ detail.dataset.origin }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item v-if="isVol" label="容量">{{ detail.dataset.volsize_human }}</el-descriptions-item>
          <el-descriptions-item label="压缩">{{ propsOf('compression') }}</el-descriptions-item>
          <el-descriptions-item label="重删 dedup">{{ propsOf('dedup') || 'off' }}</el-descriptions-item>
          <el-descriptions-item label="压缩率">{{ propsOf('compressratio') || '-' }}</el-descriptions-item>
          <el-descriptions-item v-if="isVol" label="块大小">{{ propsOf('volblocksize') ? fmtKiB(propsOf('volblocksize')) : '-' }}</el-descriptions-item>
          <el-descriptions-item label="已用">{{ detail.dataset.used_human }}</el-descriptions-item>
          <el-descriptions-item label="节点">{{ detail.dataset.node_name }}</el-descriptions-item>
          <el-descriptions-item v-if="!isVol" label="记录大小 recordsize">{{ propsOf('recordsize') ? fmtKiB(propsOf('recordsize')) : '-' }}</el-descriptions-item>
          <el-descriptions-item v-if="isVol" label="文件系统" :span="2">
            <el-tag size="small" :type="detail.filesystem.type === 'none' ? 'info' : 'success'">
              {{ detail.filesystem.type === 'none' ? '未格式化' : detail.filesystem.type }}</el-tag>
            <el-tag v-if="detail.filesystem.mounted" size="small" type="warning" style="margin-left:6px">本机已挂载</el-tag>
          </el-descriptions-item>
        </el-descriptions>

        <!-- 属性配置:压缩 / 重删 -->
        <el-divider content-position="left">属性配置(compression / dedup)</el-divider>
        <div v-if="canWrite()" style="display:flex;gap:10px;align-items:center;flex-wrap:wrap">
          <span>压缩:</span>
          <el-select v-model="pf.compression" style="width:150px">
            <el-option value="inherit" label="继承" /><el-option value="off" label="off" />
            <el-option value="lz4" label="lz4" /><el-option value="zstd" label="zstd" />
            <el-option value="zstd-3" label="zstd-3" /><el-option value="gzip" label="gzip" />
            <el-option value="lzjb" label="lzjb" /><el-option value="zle" label="zle" />
          </el-select>
          <span>重删:</span>
          <el-select v-model="pf.dedup" style="width:150px">
            <el-option value="inherit" label="继承" /><el-option value="off" label="off" />
            <el-option value="on" label="on" /><el-option value="verify" label="verify" />
          </el-select>
          <el-button type="primary" size="small" @click="applyProps">应用</el-button>
          <span style="font-size:12px;color:#999">重删开启需键入名称二次确认;压缩即时生效(新写入)</span>
        </div>
        <div v-else style="color:#999;font-size:13px">只读视图</div>

        <template v-if="isVol">
          <el-divider content-position="left">文件系统(仅执行 mkfs,不负责挂载)</el-divider>
          <div v-if="canWrite()" style="display:flex;gap:6px">
            <el-select v-model="fmtFs" style="width:120px">
              <el-option value="ext4" label="ext4" /><el-option value="xfs" label="xfs" /><el-option value="ntfs" label="ntfs" />
            </el-select>
            <el-button type="danger" plain :disabled="detail.dataset.mapped || !!detail.filesystem.mounted"
                       @click="formatVol">格式化(破坏性)</el-button>
            <span style="font-size:12px;color:#c45656;line-height:32px">需键入卷名确认;已映射或已挂载时禁用</span>
          </div>
        </template>

        <el-divider content-position="left">快照与回滚</el-divider>
        <div v-if="canWrite()" style="display:flex;gap:6px;margin-bottom:8px">
          <el-input v-model="snapName" placeholder="新快照名,如 backup-20260906" style="width:260px" />
          <el-button type="primary" @click="snapCreate">创建快照</el-button>
          <el-button type="warning" plain :disabled="isVol && detail.dataset.mapped" @click="rollbackDlg = true">回滚到快照</el-button>
        </div>
        <el-table :data="snaps" size="small" max-height="260">
          <el-table-column prop="name" label="快照" min-width="200" />
          <el-table-column prop="creation" label="创建时间" width="180" />
          <el-table-column prop="used_human" label="占用" width="90" />
          <el-table-column label="操作" width="120">
            <template #default="{ row }">
              <el-button v-if="canWrite()" size="small" type="danger" plain @click="snapDel(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>

        <template v-if="isVol">
          <el-divider content-position="left">iSCSI 映射</el-divider>
          <template v-if="detail.targets.length">
            <el-table :data="detail.targets" size="small">
              <el-table-column prop="iqn" label="Target IQN" />
              <el-table-column prop="lun_id" label="LUN" width="70" />
            </el-table>
            <div style="margin-top:8px">
              <el-tag v-for="h in detail.authorized_hosts" :key="h.id" size="small" style="margin-right:6px">主机:{{ h.name }}</el-tag>
            </div>
          </template>
          <div v-else style="color:#999">未映射。可在「主机 → 分配卷」或此处直接映射:</div>
          <div v-if="canWrite() && !detail.targets.length" style="margin-top:10px;display:flex;gap:6px">
            <el-select v-model="mapHost" placeholder="选择主机" style="width:200px">
              <el-option v-for="h in hosts" :key="h.id" :value="h.id" :label="h.name" />
            </el-select>
            <el-button type="primary" :disabled="!mapHost" @click="mapVol">映射给主机</el-button>
          </div>
          <el-button v-if="canWrite()" style="margin-top:6px" size="small" type="danger" plain @click="resizeDlg = true">扩容/缩容</el-button>
        </template>

        <el-divider content-position="left">危险区</el-divider>
        <el-popconfirm title="删除不可恢复,确认?" @confirm="delVol" width="260">
          <template #reference>
            <el-button type="danger" :disabled="(isVol && detail.dataset.mapped) || detail.dataset.snapshot_count > 0">
              删除{{ isVol ? '卷' : '数据集' }}</el-button>
          </template>
        </el-popconfirm>
        <span style="font-size:12px;color:#999;margin-left:8px">需键入名称二次确认</span>
      </template>
    </el-drawer>

    <el-dialog v-model="rollbackDlg" title="回滚到快照" width="460px">
      <el-select v-model="rollSnap" style="width:100%">
        <el-option v-for="s in snaps" :key="s.id" :value="s.name" :label="`${s.name} (${s.creation})`" />
      </el-select>
      <div style="font-size:12px;color:#c45656;margin-top:8px">回滚将丢弃该快照之后写入的全部数据,且仅支持最新快照。</div>
      <template #footer>
        <el-button @click="rollbackDlg = false">取消</el-button>
        <el-button type="danger" :disabled="!rollSnap" @click="rollback">确认回滚</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="resizeDlg" title="调整容量" width="420px">
      <el-input v-model="newSize" placeholder="如 4G(缩容为破坏性操作,需二次输入卷名)">
        <template #prepend>目标容量</template>
      </el-input>
      <template #footer>
        <el-button @click="resizeDlg = false">取消</el-button>
        <el-button type="primary" @click="resize">调整</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>
<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { api, canWrite, fmtBytes, fmtKiB } from '../api.js'
import { ElMessage, ElMessageBox } from 'element-plus'

const rows = ref([]), nodes = ref([]), hosts = ref([]), nodePools = ref([])
const fNode = ref(null), typeTab = ref('volume'), busy = ref(false)
const createDlg = ref(false), detailDlg = ref(false), rollbackDlg = ref(false), resizeDlg = ref(false)
const cf = ref({ node_id: null, pool_id: null, name: '', size: '2G', compression: 'inherit', dedup: 'inherit', volblocksize: '', sparse: false, source_dataset_id: null, snapshot: '', mountMode: 'auto', mountpoint: '' })
const createMode = ref('blank'), sourceVols = ref([]), cloneSnaps = ref([]), srcBusy = ref(false)
const cur = ref(null), detail = ref(null), snaps = ref([])
const snapName = ref(''), rollSnap = ref(null), mapHost = ref(null), fmtFs = ref('ext4'), newSize = ref('')
const pf = ref({ compression: 'inherit', dedup: 'off' })
const ini = ref({ compression: '', dedup: '' })
const isVol = computed(() => detail.value?.dataset?.type === 'volume')

function propsOf(k) { return detail.value?.properties?.[k] ?? '' }

const snapMap = ref({})
async function onExpand(row, expandedRows) {
  const open = Array.isArray(expandedRows) ? expandedRows.some(r => r.id === row.id) : !!expandedRows
  if (!open) return
  if (snapMap.value[row.id]) return
  try {
    const d = await api.get(`/datasets/${row.id}/snapshots`)
    snapMap.value = { ...snapMap.value, [row.id]: d.snapshots }
  } catch (e) { snapMap.value = { ...snapMap.value, [row.id]: [] } }
}
async function load() {
  snapMap.value = {}
  busy.value = true
  try {
    const q = new URLSearchParams({ type: typeTab.value })
    if (fNode.value) q.set('node_id', fNode.value)
    const [d, n] = await Promise.all([api.get('/datasets?' + q.toString()), api.get('/nodes')])
    rows.value = d.datasets; nodes.value = n.nodes
  } finally { busy.value = false }
}
async function pickNodePools() {
  nodePools.value = []
  if (!cf.value.node_id) return
  const p = await api.get('/pools')
  nodePools.value = p.pools.filter(x => x.node_id === cf.value.node_id)
}
const canSubmit = computed(() => {
  if (!cf.value.name || !cf.value.node_id) return false
  if (createMode.value === 'clone' && typeTab.value === 'volume') {
    return !!cf.value.source_dataset_id && !!cf.value.snapshot
  }
  if (createMode.value !== 'clone') return !!cf.value.pool_id
  return true
})

function openCreate() {
  cf.value = { node_id: fNode.value || null, pool_id: null, name: '', size: '2G', compression: 'inherit', dedup: 'inherit', volblocksize: '', sparse: false, source_dataset_id: null, snapshot: '', mountMode: 'auto', mountpoint: '' }
  createMode.value = 'blank'
  createDlg.value = true
  pickNodePools()
  if (fNode.value) onNodeChange()
}
function onNodeChange() {
  pickNodePools()
  cf.value.source_dataset_id = null
  cf.value.snapshot = null
  cloneSnaps.value = []
  loadSourceVols()
}
async function loadSourceVols() {
  sourceVols.value = []
  if (!cf.value.node_id) return
  srcBusy.value = true
  try {
    const d = await api.get('/datasets?type=volume&node_id=' + cf.value.node_id)
    sourceVols.value = d.datasets.filter(v => v.snapshot_count > 0)
    if (!sourceVols.value.length) ElMessage.info('该节点暂无带快照的卷,请先为源卷创建快照')
  } finally { srcBusy.value = false }
}
async function loadCloneSnaps() {
  cloneSnaps.value = []
  cf.value.snapshot = null
  if (!cf.value.source_dataset_id) return
  cloneSnaps.value = (await api.get(`/datasets/${cf.value.source_dataset_id}/snapshots`)).snapshots
}
async function create() {
  if (!cf.value.name) return ElMessage.warning('请填写名称')
  if (createMode.value === 'clone' && typeTab.value === 'volume') {
    await api.post('/datasets/clone', {
      source_dataset_id: cf.value.source_dataset_id,
      snapshot: cf.value.snapshot,
      name: cf.value.name
    })
    ElMessage.success('克隆创建成功')
  } else {
    const body = {
      node_id: cf.value.node_id, pool_id: cf.value.pool_id,
      type: typeTab.value, name: cf.value.name,
      compression: cf.value.compression
    }
    if (typeTab.value === 'volume') {
      body.size = cf.value.size
      if (cf.value.volblocksize) body.volblocksize = cf.value.volblocksize
      if (cf.value.sparse) body.sparse = true
    } else {
      body.dedup = cf.value.dedup
      body.mountpoint = cf.value.mountMode === 'auto' ? 'auto' : (cf.value.mountMode === 'none' ? 'none' : cf.value.mountpoint)
    }
    await api.post('/datasets', body)
    ElMessage.success('创建成功')
  }
  createDlg.value = false
  load()
}
async function openDetail(row) {
  cur.value = row
  detailDlg.value = true
  await reloadDetail()
  hosts.value = (await api.get('/hosts')).hosts
  pf.value = {
    compression: detail.value.properties?.compression || row.compression || 'inherit',
    dedup: detail.value.properties?.dedup || 'off'
  }
  ini.value = { compression: pf.value.compression, dedup: pf.value.dedup }
}
async function reloadDetail() {
  detail.value = await api.get(`/datasets/${cur.value.id}`)
  snaps.value = (await api.get(`/datasets/${cur.value.id}/snapshots`)).snapshots
}
async function applyProps() {
  const actions = []
  if (pf.value.compression !== ini.value.compression) {
    actions.push({ property: 'compression', value: pf.value.compression })
  }
  if (pf.value.dedup !== ini.value.dedup) {
    const needConfirm = pf.value.dedup !== 'off' && pf.value.dedup !== 'inherit'
    if (needConfirm) {
      const v = await ElMessageBox.prompt(
        `开启重删将增加内存与 CPU 开销,请输入数据集短名 ${cur.value.name.split('/').pop()} 确认`,
        '重删确认', { inputPattern: new RegExp('^' + cur.value.name.split('/').pop() + '$'),
                     inputErrorMessage: '名称不匹配', type: 'warning' }).catch(() => null)
      if (!v) return
      actions[actions.length] = { property: 'dedup', value: pf.value.dedup, confirm: v.value }
    } else {
      actions[actions.length] = { property: 'dedup', value: pf.value.dedup }
    }
  }
  if (!actions.length) return ElMessage.info('属性未变化')
  for (const a of actions) await api.patch(`/datasets/${cur.value.id}/properties`, a)
  ElMessage.success('属性已应用')
  await reloadDetail(); load()
}
async function formatVol() {
  const v = await ElMessageBox.prompt(`格式化将清除 ${cur.value.name} 上全部数据,请输入卷名 ${cur.value.name.split('/').pop()} 确认`, '破坏性操作', {
    inputPattern: new RegExp('^' + cur.value.name.split('/').pop() + '$'), inputErrorMessage: '卷名不匹配', type: 'error' }).catch(() => null)
  if (!v) return
  await api.post(`/datasets/${cur.value.id}/format`, { filesystem: fmtFs.value, confirm: v.value })
  ElMessage.success('格式化完成')
  reloadDetail(); load()
}
async function snapCreate() {
  if (!snapName.value) return
  await api.post(`/datasets/${cur.value.id}/snapshots`, { name: snapName.value })
  snapName.value = ''
  reloadDetail(); load()
}
async function snapDel(row) {
  await ElMessageBox.confirm(`删除快照 ${row.name}?`, '确认', { type: 'warning' })
  await api.delete(`/snapshots/${row.id}`)
  reloadDetail(); load()
}
async function rollback() {
  await api.post(`/datasets/${cur.value.id}/rollback`,
    { snapshot: rollSnap.value, confirm: cur.value.name.split('/').pop() })
  rollbackDlg.value = false
  ElMessage.success('回滚完成')
  reloadDetail(); load()
}
async function mapVol() {
  const d = await api.post('/mappings', { host_id: mapHost.value, dataset_id: cur.value.id })
  ElMessageBox.alert(
    `映射成功\nIQN: ${d.iqn}\nPortal: ${d.portal}\nLUN: ${d.lun_id}\nCHAP: ${d.auth.has_chap ? d.auth.user : '无'}`,
    '连接参数', { type: 'success' }).catch(() => {})
  reloadDetail(); load()
}
async function resize() {
  const d = detail.value.dataset
  const confirm = (await ElMessageBox.prompt(
    `目标 ${newSize.value} 小于当前 ${d.volsize_human} 时属缩容(破坏性)。请输入卷名 ${d.name.split('/').pop()} 以继续`, '调整容量',
    { inputPattern: new RegExp('^' + d.name.split('/').pop() + '$'), inputErrorMessage: '卷名不匹配' })).catch(() => null)
  if (!confirm) return
  await api.patch(`/datasets/${cur.value.id}/resize`, { size: newSize.value, confirm: confirm.value })
  resizeDlg.value = false
  reloadDetail(); load()
}
async function delVol() {
  const v = await ElMessageBox.prompt(`请输入名称 ${cur.value.name.split('/').pop()} 确认删除(不可恢复)`, '危险操作', {
    inputPattern: new RegExp('^' + cur.value.name.split('/').pop() + '$'), inputErrorMessage: '名称不匹配', type: 'error' }).catch(() => null)
  if (!v) return
  await api.delete(`/datasets/${cur.value.id}`, { data: { confirm: v.value } })
  detailDlg.value = false
  ElMessage.success('已删除')
  load()
}
onMounted(load)
</script>

<style scoped>
.snap-expand { padding: 6px 16px 10px 52px; background: var(--my-surface-2, #f6f8fa); }
.snap-head { margin: 4px 0 8px; color: var(--my-text-2); font-size: 13px; }
</style>
