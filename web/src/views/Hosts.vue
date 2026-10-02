<template>
  <el-card shadow="never" header="Initiator(iSCSI IQN / FC WWPN)" class="page-card">
    <div style="margin-bottom:10px">
      <el-button v-if="canWrite()" type="primary" @click="createDlg = true">新建主机</el-button>
      <el-button @click="load">刷新</el-button>
    </div>
    <el-table :data="hosts" v-loading="busy">
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="name" label="名称" width="150" />
      <el-table-column prop="os_type" label="系统" width="100" />
      <el-table-column prop="initiator_count" label="Initiator" width="100" />
      <el-table-column label="传输" width="110">
        <template #default="{ row }">
          <el-tag size="small" v-for="t in (row.transports || '').split(',').filter(Boolean)" :key="t"
                  :type="t === 'fc' ? 'warning' : 'primary'" style="margin-right:4px">
            {{ t === 'fc' ? 'FC' : 'iSCSI' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="mapping_count" label="已映射卷" width="100" />
      <el-table-column prop="description" label="备注" show-overflow-tooltip />
      <el-table-column label="操作" width="200" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="openDetail(row)">管理</el-button>
          <el-button v-if="canWrite()" size="small" type="danger" plain
                     :disabled="row.mapping_count > 0" @click="delHost(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="createDlg" title="新建主机档案" width="440px">
      <el-form label-width="80px">
        <el-form-item label="名称"><el-input v-model="hf.name" placeholder="如 backup-server-01" /></el-form-item>
        <el-form-item label="系统"><el-input v-model="hf.os_type" placeholder="如 Debian 13 / Windows 2019" /></el-form-item>
        <el-form-item label="备注"><el-input v-model="hf.description" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDlg = false">取消</el-button>
        <el-button type="primary" @click="createHost">保存</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="detailDlg" size="58%" :title="cur?.name || ''">
      <template v-if="detail">
        <el-divider content-position="left">Initiator 标识(录入 iSCSI IQN 或 FC WWPN)</el-divider>
        <el-table :data="detail.initiators" size="small">
          <el-table-column label="传输" width="90">
            <template #default="{ row }">
              <el-tag size="small" :type="row.transport === 'fc' ? 'warning' : 'primary'">
                {{ row.transport === 'fc' ? 'FC' : 'iSCSI' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="iqn" label="IQN / WWPN" min-width="260">
            <template #default="{ row }"><span class="mono">{{ row.iqn }}</span></template>
          </el-table-column>
          <el-table-column prop="chap_user" label="CHAP 用户" width="140">
            <template #default="{ row }">{{ row.has_chap ? row.chap_user : '(无)' }}</template>
          </el-table-column>
          <el-table-column label="操作" width="90">
            <template #default="{ row }">
              <el-button v-if="canWrite()" size="small" type="danger" plain :disabled="detail.volumes.length > 0"
                         @click="delInit(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div v-if="canWrite()" style="margin:8px 0">
          <el-form inline>
            <el-form-item label="传输">
              <el-radio-group v-model="init.transport">
                <el-radio-button value="iscsi">iSCSI</el-radio-button>
                <el-radio-button value="fc">FC</el-radio-button>
              </el-radio-group>
            </el-form-item>
            <el-form-item :label="init.transport === 'fc' ? 'WWPN' : 'IQN'">
              <el-input v-model="init.iqn" style="width:320px"
                        :placeholder="init.transport === 'fc' ? '0x10000000c9a1b2c3' : 'iqn.YYYY-MM.com.example:init-1'" />
            </el-form-item>
            <template v-if="init.transport === 'iscsi'">
              <el-form-item label="CHAP 用户"><el-input v-model="init.chap_user" style="width:130px" placeholder="可选" /></el-form-item>
              <el-form-item label="口令"><el-input v-model="init.chap_secret" type="password" show-password style="width:140px" placeholder="≥12位" /></el-form-item>
            </template>
            <span v-else style="font-size:12px;color:#999;margin-right:8px">FC 无需 CHAP</span>
            <el-button type="primary" :disabled="!init.iqn" @click="addInit">录入</el-button>
          </el-form>
        </div>

        <el-divider content-position="left">分配卷(自动建 Target + ACL,返回连接参数)</el-divider>
        <div v-if="canWrite()" style="display:flex;gap:6px;margin-bottom:8px">
          <el-select v-model="alloc.node_id" placeholder="节点" style="width:140px" @change="loadNodeFreeVols">
            <el-option v-for="n in nodes" :key="n.id" :value="n.id" :label="n.name" />
          </el-select>
          <el-select v-model="alloc.dataset_id" placeholder="选择未映射 zvol" style="width:300px">
            <el-option v-for="v in freeVols" :key="v.id" :value="v.id" :label="`${v.name} (${v.volsize_human})`" />
          </el-select>
          <el-button type="primary" :disabled="!alloc.dataset_id || !alloc.node_id" @click="allocVol">分配并映射</el-button>
        </div>
        <el-table v-if="detail.volumes.length" :data="detail.volumes" size="small">
          <el-table-column prop="dataset" label="卷" min-width="220" />
          <el-table-column prop="iqn" label="Target IQN" min-width="280" show-overflow-tooltip />
          <el-table-column prop="lun_id" label="LUN" width="70" />
          <el-table-column prop="filesystem_type" label="FS" width="80" />
          <el-table-column label="操作" width="100">
            <template #default="{ row }">
              <el-button v-if="canWrite()" size="small" type="danger" plain @click="unmap(row)">解除映射</el-button>
            </template>
          </el-table-column>
        </el-table>
      </template>
    </el-drawer>
  </el-card>
</template>
<script setup>
import { onMounted, ref } from 'vue'
import { api, canWrite, fmtBytes } from '../api.js'
import { ElMessage, ElMessageBox } from 'element-plus'

const hosts = ref([]), nodes = ref([]), freeVols = ref([]), busy = ref(false)
const createDlg = ref(false), detailDlg = ref(false)
const hf = ref({ name: '', os_type: '', description: '' })
const cur = ref(null), detail = ref(null)
const init = ref({ transport: 'iscsi', iqn: '', chap_user: '', chap_secret: '' })
const alloc = ref({ node_id: null, dataset_id: null })
const mappingsOf = ref([])

async function load() {
  busy.value = true
  try {
    const [h, n] = await Promise.all([api.get('/hosts'), api.get('/nodes')])
    hosts.value = h.hosts; nodes.value = n.nodes
  } finally { busy.value = false }
}
async function createHost() {
  await api.post('/hosts', hf.value)
  hf.value = { name: '', os_type: '', description: '' }
  createDlg.value = false
  load()
}
async function openDetail(row) {
  cur.value = row
  detailDlg.value = true
  await reloadDetail()
}
async function reloadDetail() {
  detail.value = await api.get(`/hosts/${cur.value.id}`)
  mappingsOf.value = (await api.get('/mappings?host_id=' + cur.value.id)).mappings
}
async function addInit() {
  await api.post(`/hosts/${cur.value.id}/initiators`, init.value)
  init.value = { transport: 'iscsi', iqn: '', chap_user: '', chap_secret: '' }
  reloadDetail()
}
async function delInit(row) {
  await ElMessageBox.confirm(`删除 Initiator ${row.iqn}?`, '确认', { type: 'warning' })
  await api.delete(`/hosts/${cur.value.id}/initiators/${row.id}`)
  reloadDetail()
}
async function delHost(row) {
  await ElMessageBox.confirm(`删除主机档案 ${row.name}?`, '确认', { type: 'warning' })
  await api.delete(`/hosts/${row.id}`)
  load()
}
async function loadNodeFreeVols() {
  freeVols.value = []
  if (!alloc.value.node_id) return
  const d = await api.get('/datasets?node_id=' + alloc.value.node_id)
  freeVols.value = d.datasets.filter(v => !v.mapped)
}
async function allocVol() {
  const d = await api.post('/mappings', { host_id: cur.value.id, dataset_id: alloc.value.dataset_id })
  ElMessageBox.alert(
    `连接参数(交付给该主机)\n\nIQN: ${d.iqn}\nPortal: ${d.portal}\nLUN ID: ${d.lun_id}\nCHAP: ${d.auth.has_chap ? d.auth.user : '无'}`,
    '映射成功', { type: 'success', width: '420px' }).catch(() => {})
  alloc.value = { node_id: null, dataset_id: null }
  reloadDetail(); load()
}
async function unmap(row) {
  const m = mappingsOf.value.find(x => x.dataset === row.dataset)
  if (!m) return
  await ElMessageBox.confirm(`解除主机对卷 ${row.dataset} 的映射?(将移除 ACL;专用 Target 会一并清理)`, '确认', { type: 'warning' })
  await api.delete(`/mappings/${m.id}`)
  reloadDetail(); load()
}
onMounted(load)
</script>
