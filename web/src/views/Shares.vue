<template>
  <el-card shadow="never" header="文件共享 (NFS / SMB-CIFS)" class="page-card">
    <div style="margin-bottom:10px; display:flex; gap:8px">
      <el-select v-model="fNode" placeholder="全部节点" clearable style="width:170px" @change="load">
        <el-option v-for="n in nodes" :key="n.id" :value="n.id" :label="n.name" />
      </el-select>
      <el-button v-if="canWrite()" type="primary" @click="openCreate">新建共享</el-button>
      <el-button @click="load">刷新</el-button>
    </div>
    <el-table :data="rows" v-loading="busy">
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="node_name" label="节点" width="100" />
      <el-table-column label="协议" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="row.type === 'nfs' ? 'primary' : 'warning'">{{ row.type === 'nfs' ? 'NFS' : 'SMB' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="name" label="共享名" width="150" />
      <el-table-column prop="dataset" label="数据集 (pool/dataset)" min-width="200" show-overflow-tooltip />
      <el-table-column label="配置" min-width="240">
        <template #default="{ row }">
          <span v-if="row.type === 'nfs'" class="mono">
            {{ row.config?.clients || '*' }} · {{ row.config?.access || 'rw' }} · {{ row.config?.sync_mode || 'sync' }}
          </span>
          <span v-else class="mono">
            {{ row.config?.read_only ? 'read-only' : 'read-write' }}{{ row.config?.guest_ok ? ' · guest' : '' }}{{ row.config?.valid_users ? ' · users:' + row.config.valid_users : '' }}
          </span>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="190" fixed="right">
        <template #default="{ row }">
          <el-button v-if="canWrite()" size="small" @click="toggle(row)">{{ row.enabled ? '停用' : '启用' }}</el-button>
          <el-button v-if="canWrite()" size="small" type="danger" plain @click="removeShare(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dlg" title="新建文件共享(创建在指定存储池的数据集上)" width="620px">
      <el-form label-width="120px">
        <el-form-item label="协议">
          <el-radio-group v-model="cf.type" @change="onType">
            <el-radio-button value="nfs">NFS</el-radio-button>
            <el-radio-button value="smb">SMB / CIFS</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="节点">
          <el-select v-model="cf.node_id" style="width:100%" @change="loadDs">
            <el-option v-for="n in nodes" :key="n.id" :value="n.id" :label="n.name" :disabled="!n.online" />
          </el-select>
        </el-form-item>
        <el-form-item :label="cf.type === 'nfs' ? '存储池数据集' : '共享目录(数据集)'">
          <el-select v-model="cf.dataset_id" style="width:100%" :loading="dsBusy" placeholder="选择文件系统数据集">
            <el-option v-for="d in fsDatasets" :key="d.id" :value="d.id" :disabled="!d.sharable"
                       :label="`${d.name}${d.sharable ? (d.mounted ? ' · 已挂载' : ' · 待挂载') + ' @' + d.mountpoint : ' · 未挂载(none),不可共享'}`" />
          </el-select>
          <div style="font-size:12px;color:#999;margin-top:4px">
            共享建立在池内文件系统数据集的挂载点上;
            <template v-if="noMountList.length">
              以下数据集未挂载,可快捷启用:
              <el-button v-for="d in noMountList" :key="d.id" size="small" text type="primary"
                         @click="enableMount(d)">{{ d.name }} 启用自动挂载</el-button>
            </template>
            <span v-else-if="!fsDatasets.length">该节点暂无文件系统数据集,请先在「卷 → 文件系统」创建。</span>
          </div>
        </el-form-item>
        <el-form-item label="共享名">
          <el-input v-model="cf.name" placeholder="留空默认取数据集短名" style="width:260px" />
        </el-form-item>

        <template v-if="cf.type === 'nfs'">
          <el-form-item label="客户端">
            <el-input v-model="cf.clients" placeholder="如 192.168.13.0/24 或 *(默认允许所有)" style="width:320px" />
          </el-form-item>
          <el-form-item label="访问权限">
            <el-radio-group v-model="cf.access">
              <el-radio-button value="rw">读写 rw</el-radio-button>
              <el-radio-button value="ro">只读 ro</el-radio-button>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="同步模式">
            <el-radio-group v-model="cf.sync_mode">
              <el-radio-button value="sync">sync(安全)</el-radio-button>
              <el-radio-button value="async">async(更快)</el-radio-button>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="root 用户">
            <el-radio-group v-model="cf.squash">
              <el-radio-button value="root_squash">压缩为匿名(推荐)</el-radio-button>
              <el-radio-button value="no_root_squash">保留 root</el-radio-button>
            </el-radio-group>
          </el-form-item>
        </template>
        <template v-else>
          <el-form-item label="访问权限">
            <el-radio-group v-model="cf.ro">
              <el-radio-button :value="false">读写</el-radio-button>
              <el-radio-button :value="true">只读</el-radio-button>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="允许匿名访问">
            <el-switch v-model="cf.guest_ok" />
            <span style="font-size:12px;color:#999;margin-left:10px">匿名写入将以 nobody 身份进行</span>
          </el-form-item>
          <el-form-item label="允许用户">
            <el-input v-model="cf.valid_users" placeholder="可选,Samba 用户名逗号分隔" style="width:320px" />
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="dlg = false">取消</el-button>
        <el-button type="primary" :disabled="!cf.dataset_id" :loading="busy" @click="create">创建共享</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>
<script setup>
import { computed, onMounted, ref } from 'vue'
import { api, canWrite } from '../api.js'
import { ElMessage, ElMessageBox } from 'element-plus'

const rows = ref([]), nodes = ref([]), fsDatasets = ref([]), busy = ref(false), dsBusy = ref(false)
const fNode = ref(null), dlg = ref(false)
const noMountList = computed(() => fsDatasets.value.filter(d => !d.sharable))
const cf = ref({ type: 'nfs', node_id: null, dataset_id: null, name: '', clients: '*', access: 'rw',
  sync_mode: 'sync', squash: 'root_squash', ro: false, guest_ok: false, valid_users: '' })

async function load() {
  busy.value = true
  try {
    const q = fNode.value ? '?node_id=' + fNode.value : ''
    const [s, n] = await Promise.all([api.get('/shares' + q), api.get('/nodes')])
    rows.value = s.shares; nodes.value = n.nodes
  } finally { busy.value = false }
}
function openCreate() {
  cf.value = { type: 'nfs', node_id: fNode.value || null, dataset_id: null, name: '', clients: '*', access: 'rw',
    sync_mode: 'sync', squash: 'root_squash', ro: false, guest_ok: false, valid_users: '' }
  dlg.value = true
  loadDs()
}
function onType() { /* 切换协议保留数据集选择 */ }
async function loadDs() {
  fsDatasets.value = []
  if (!cf.value.node_id) return
  dsBusy.value = true
  try {
    const d = await api.get('/datasets?type=filesystem&node_id=' + cf.value.node_id)
    const out = []
    for (const it of d.datasets) {
      if (it.name === it.pool) continue            // 跳过池根自身
      try {
        const dd = await api.get('/datasets/' + it.id)
        const mp = dd && dd.properties ? dd.properties.mountpoint : ''
        out.push({ ...it, mountpoint: mp || '', mounted: dd && dd.properties && dd.properties.mounted === 'yes',
                   sharable: !!mp && mp !== 'none' && mp !== '-' })
      } catch (e) {
        out.push({ ...it, mountpoint: '', mounted: false, sharable: false })
      }
    }
    fsDatasets.value = out
  } finally { dsBusy.value = false }
}
async function enableMount(ds) {
  await api.patch(`/datasets/${ds.id}/properties`, { property: 'mountpoint', value: 'auto' })
  ElMessage.success('已设置挂载点并挂载,正在刷新列表…')
  await loadDs()
}
async function create() {
  const body = { node_id: cf.value.node_id, dataset_id: cf.value.dataset_id, type: cf.value.type, name: cf.value.name }
  if (cf.value.type === 'nfs') {
    body.clients = cf.value.clients; body.access = cf.value.access
    body.sync_mode = cf.value.sync_mode; body.squash = cf.value.squash
  } else {
    body.read_only = cf.value.ro; body.guest_ok = cf.value.guest_ok; body.valid_users = cf.value.valid_users
  }
  const r = await api.post('/shares', body)
  const d = r.detail || {}
  ElMessageBox.alert(
    cf.value.type === 'nfs'
      ? `NFS 共享已创建\n挂载点: ${d.mountpoint}\n客户端: ${d.clients}\n选项: ${d.options}\n\n客户端挂载示例:\nmount -t nfs ${location.hostname}:/path ${d.mountpoint}`
      : `SMB 共享已创建\n共享名: ${d.name}\n路径: ${d.mountpoint}\n\n访问示例(Windows): \\\\${location.hostname}\\${d.name}`,
    '共享已创建', { type: 'success' }).catch(() => {})
  dlg.value = false
  load()
}
async function toggle(row) {
  await api.post(`/shares/${row.id}/toggle`, { enabled: !row.enabled })
  ElMessage.success(row.enabled ? '已停用' : '已启用')
  load()
}
async function removeShare(row) {
  await ElMessageBox.confirm(`删除共享 ${row.name}? (将移除 ${row.type.toUpperCase()} 导出/共享段)`, '确认', { type: 'warning' })
  await api.delete(`/shares/${row.id}`)
  load()
}
onMounted(load)
</script>
