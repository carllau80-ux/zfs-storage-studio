<template>
  <el-card shadow="never" header="Target(iSCSI / FC)" class="page-card">
    <div style="margin-bottom:10px; display:flex; gap:8px">
      <el-button v-if="canWrite()" type="primary" @click="createDlg = true">新建 Target</el-button>
      <el-button @click="load">刷新</el-button>
    </div>
    <el-table :data="targets" v-loading="busy">
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="node_name" label="节点" width="110" />
      <el-table-column label="传输" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="row.transport === 'fc' ? 'warning' : 'primary'">
            {{ row.transport === 'fc' ? 'FC' : 'iSCSI' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="IQN / WWPN" min-width="320" show-overflow-tooltip>
        <template #default="{ row }"><span class="mono">{{ row.target_name }}</span></template>
      </el-table-column>
      <el-table-column prop="lun_count" label="LUN" width="70" />
      <el-table-column prop="acl_count" label="ACL" width="70" />
      <el-table-column prop="session_count" label="会话" width="70">
        <template #default="{ row }">
          <el-tag v-if="row.session_count" size="small" type="success">{{ row.session_count }}</el-tag>
          <span v-else style="color:#bbb">0</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="120" fixed="right">
        <template #default="{ row }"><el-button size="small" @click="openDetail(row)">管理</el-button></template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="createDlg" title="新建 Target(iSCSI / FC)" width="540px">
      <el-form label-width="110px">
        <el-form-item label="传输">
          <el-radio-group v-model="form.transport">
            <el-radio-button value="iscsi">iSCSI</el-radio-button>
            <el-radio-button value="fc" :disabled="!fcCapable">FC(目标 WWPN)</el-radio-button>
          </el-radio-group>
          <div v-if="!fcCapable" style="font-size:12px;color:#d93025;margin-top:4px">
            FC 不可用:{{ fcReason || '未检测到兼容 QLogic HBA' }}
          </div>
        </el-form-item>
        <el-form-item label="节点">
          <el-select v-model="form.node_id" style="width:100%" @change="onNodePick">
            <el-option v-for="n in nodes" :key="n.id" :value="n.id" :label="n.name" :disabled="!n.online" />
          </el-select>
        </el-form-item>
        <el-form-item label="挂载卷">
          <el-select v-model="form.dataset_id" style="width:100%" placeholder="选择尚未映射的 zvol(仅 zvol,可选)">
            <el-option v-for="v in freeVols" :key="v.id" :value="v.id"
                       :label="`${v.name} (zvol · ${v.volsize_human})`" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="form.transport === 'iscsi'" label="IQN">
          <el-input v-model="form.iqn" placeholder="留空自动生成 iqn.YYYY-MM.com.ustc:..." />
        </el-form-item>
        <el-form-item v-else label="目标 WWPN">
          <el-select v-model="form.iqn" style="width:100%" placeholder="选择本机 HBA 端口 WWPN" allow-create filterable>
            <el-option v-for="p in fcPorts" :key="p" :value="p" :label="p" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDlg = false">取消</el-button>
        <el-button type="primary" @click="create">创建</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="detailDlg" size="60%" :title="td?.target_name || ''">
      <template v-if="td">
        <el-descriptions :column="2" border size="small" style="margin-bottom:10px">
          <el-descriptions-item label="节点">{{ td.node_name }}</el-descriptions-item>
          <el-descriptions-item label="传输">
            <el-tag size="small" :type="td.transport === 'fc' ? 'warning' : 'primary'">
              {{ td.transport === 'fc' ? 'FC' : 'iSCSI' }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="td.transport === 'fc' ? '目标 WWPN' : 'Portal'">
            {{ td.transport === 'fc' ? td.target_name : (td.portals.join(', ') || '无') }}</el-descriptions-item>
          <el-descriptions-item label="启用">
            <el-tag size="small" :type="td.enabled ? 'success' : 'info'">{{ td.enabled ? '是' : '否' }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="会话数">{{ td.sessions.length }}</el-descriptions-item>
        </el-descriptions>

        <el-divider content-position="left">LUN(backstore)</el-divider>
        <el-table :data="td.luns" size="small">
          <el-table-column prop="lun_id" label="LUN ID" width="70" />
          <el-table-column prop="dataset" label="zvol / 设备" min-width="180" />
          <el-table-column label="backstore WWN (客户端 by-id)" min-width="240">
            <template #default="{ row }">
              <span v-if="row.wwn" class="mono" :title="'backstore UUID: ' + row.wwn">{{ 'wwn-0x6001405' + row.wwn.replace(/-/g, '').slice(0, 25) }}</span>
              <span v-else style="color:#bbb">-</span>
            </template>
          </el-table-column>
          <el-table-column prop="filesystem_type" label="FS" width="80" />
          <el-table-column label="操作" width="100">
            <template #default="{ row }">
              <el-button v-if="canWrite()" size="small" type="danger" plain
                         :disabled="td.sessions.length > 0" @click="lunRemove(row)">移除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div v-if="canWrite()" style="margin-top:6px;display:flex;gap:6px">
          <el-select v-model="lunVol" placeholder="挂载另一卷" style="width:220px">
            <el-option v-for="v in freeVols" :key="v.id" :value="v.id" :label="v.name" />
          </el-select>
          <el-button type="primary" :disabled="!lunVol" @click="lunAdd">挂载 LUN</el-button>
        </div>

        <el-divider content-position="left">ACL(授权 Initiator / CHAP)</el-divider>
        <el-table :data="td.acls" size="small">
          <el-table-column prop="initiator_iqn" label="Initiator IQN" min-width="260" />
          <el-table-column prop="chap_user" label="CHAP 用户" width="130">
            <template #default="{ row }">{{ row.has_chap ? row.chap_user : '(无 CHAP)' }}</template>
          </el-table-column>
          <el-table-column label="来源" width="100">
            <template #default="{ row }"><el-tag v-if="row.managed" size="small" type="warning">映射管理</el-tag><span v-else>手工</span></template>
          </el-table-column>
          <el-table-column label="操作" width="100">
            <template #default="{ row }">
              <el-button v-if="canWrite() && !row.managed" size="small" type="danger" plain
                         :disabled="td.sessions.length > 0" @click="aclRemove(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div v-if="canWrite()" style="margin-top:6px">
          <el-form inline size="default">
            <el-form-item label="Initiator">
              <el-select v-model="acl.iqn" filterable allow-create default-first-option style="width:300px" placeholder="选择主机 IQN 或输入">
                <el-option v-for="i in allInit" :key="i.iqn" :value="i.iqn"
                           :label="`${i.host_name} → ${i.iqn}`" />
              </el-select>
            </el-form-item>
            <el-form-item label="CHAP 用户"><el-input v-model="acl.chap_user" style="width:130px" placeholder="可选" /></el-form-item>
            <el-form-item label="口令"><el-input v-model="acl.chap_secret" type="password" show-password style="width:150px" placeholder="≥12位" /></el-form-item>
            <el-button type="primary" :disabled="!acl.iqn" @click="aclAdd">添加 ACL</el-button>
          </el-form>
        </div>

        <el-divider content-position="left">活动会话(实时)</el-divider>
        <el-table :data="td.sessions" size="small">
          <el-table-column prop="iqn" label="Initiator" min-width="240" />
          <el-table-column prop="address" label="来源 IP" width="140" />
          <el-table-column prop="sid" label="SID" width="70" />
          <el-table-column prop="state" label="状态" width="110" />
          <el-table-column label="操作" width="110">
            <template #default="{ row }">
              <el-button v-if="canWrite()" size="small" type="danger" plain @click="logoutSession(row)">断开</el-button>
            </template>
          </el-table-column>
        </el-table>

        <el-divider />
        <el-popconfirm title="删除该 Target(存在会话/映射时会被拒绝)?" @confirm="delTarget">
          <template #reference><el-button type="danger">删除 Target</el-button></template>
        </el-popconfirm>
      </template>
    </el-drawer>
  </el-card>
</template>
<script setup>
import { onMounted, ref } from 'vue'
import { api, canWrite } from '../api.js'
import { ElMessage, ElMessageBox } from 'element-plus'

const targets = ref([]), nodes = ref([]), freeVols = ref([]), allInit = ref([]), busy = ref(false)
const createDlg = ref(false), detailDlg = ref(false)
const form = ref({ node_id: null, dataset_id: null, iqn: '', transport: 'iscsi' })
const fcCapable = ref(false), fcReason = ref(''), fcPorts = ref([])
async function onNodePick() {
  await loadFreeVols()
  fcCapable.value = false; fcReason.value = ''; fcPorts.value = []
  if (!form.value.node_id) return
  try {
    const d = await api.get('/nodes/' + form.value.node_id)
    const fc = (d.capabilities && d.capabilities.fc) || {}
    fcCapable.value = !!fc.fc_capable
    fcReason.value = fc.reason || ''
    fcPorts.value = fc.hba_ports || []
  } catch (e) { /* ignore */ }
  if (!fcCapable.value) form.value.transport = 'iscsi'
}
const td = ref(null), curId = ref(null)
const lunVol = ref(null), acl = ref({ iqn: '', chap_user: '', chap_secret: '' })

async function load() {
  busy.value = true
  try {
    const [t, n] = await Promise.all([api.get('/targets'), api.get('/nodes')])
    targets.value = t.targets; nodes.value = n.nodes
  } finally { busy.value = false }
}
async function loadFreeVols() {
  freeVols.value = []
  if (!form.value.node_id) return
  // 仅 zvol 可作为 LUN 载体(排除 pool 根与文件系统 dataset)
  const d = await api.get('/datasets?type=volume')
  freeVols.value = d.datasets.filter(v => !v.mapped && v.node_id === form.value.node_id)
  const hi = await api.get('/hosts')
  allInit.value = []
  for (const h of hi.hosts) {
    const hd = await api.get(`/hosts/${h.id}`)
    for (const i of hd.initiators) allInit.value.push({ iqn: i.iqn, host_name: h.name })
  }
}
async function create() {
  await api.post('/targets', {
    node_id: form.value.node_id, transport: form.value.transport,
    iqn: form.value.transport === 'iscsi' ? form.value.iqn : '',
    wwn: form.value.transport === 'fc' ? form.value.iqn : '',
    dataset_id: form.value.dataset_id
  })
  createDlg.value = false
  ElMessage.success('创建成功')
  load()
}
async function openDetail(row) {
  curId.value = row.id
  detailDlg.value = true
  await reloadDetail()
  await loadFreeVols()
  form.value.node_id = row.node_id
}
async function reloadDetail() {
  td.value = (await api.get(`/targets/${curId.value}`)).target
}
async function lunAdd() {
  await api.post(`/targets/${curId.value}/luns`, { dataset_id: lunVol.value })
  reloadDetail(); load()
}
async function lunRemove(row) {
  await ElMessageBox.confirm(`从 Target 移除 LUN ${row.lun_id}(${row.dataset})?`, '确认', { type: 'warning' })
  await api.delete(`/targets/${curId.value}/luns/${row.lun_id}`)
  reloadDetail(); load()
}
async function aclAdd() {
  await api.post(`/targets/${curId.value}/acls`, acl.value)
  acl.value = { iqn: '', chap_user: '', chap_secret: '' }
  reloadDetail()
}
async function aclRemove(row) {
  await api.delete(`/targets/${curId.value}/acls/${encodeURIComponent(row.initiator_iqn)}`)
  reloadDetail()
}
async function logoutSession(row) {
  await api.delete(`/targets/${curId.value}/sessions/${row.sid}`)
  ElMessage.success('会话已断开')
  reloadDetail()
}
async function delTarget() {
  await api.delete(`/targets/${curId.value}`, { data: { confirm: td.value.target_name } })
  detailDlg.value = false
  load()
}
onMounted(load)
</script>
