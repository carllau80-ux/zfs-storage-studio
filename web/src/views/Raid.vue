<template>
  <div>
    <el-card shadow="never" header="RAID 管理(经节点 Agent / storcli)" class="page-card">
      <div class="toolbar">
        <el-select v-model="nodeId" placeholder="选择节点" style="width:190px" @change="onNodeChange">
          <el-option v-for="n in raidNodes" :key="n.id" :value="n.id"
                     :label="`${n.name} · ${n.online ? '在线' : '离线'}`" :disabled="!n.online" />
        </el-select>
        <el-select v-if="controllers.length > 1" v-model="ctrl" style="width:210px" @change="loadAll">
          <el-option v-for="c in controllers" :key="c.id" :value="c.id" :label="`控制器 ${c.id} · ${c.model}`" />
        </el-select>
        <el-button @click="loadAll" :loading="busy">刷新</el-button>
        <span style="flex:1"></span>
        <el-tag v-if="allowMutation" size="small" type="success">可写节点</el-tag>
        <el-tag v-else size="small" type="warning">只读节点</el-tag>
      </div>

      <el-alert v-if="nodeId && !allowMutation" type="warning" :closable="false" show-icon
                title="只读模式:该节点被配置为禁止 RAID 写操作(生产/验证环境),创建/删除/改属性已禁用,仅可查看。"
                style="margin-bottom:14px" />

      <el-empty v-if="!raidNodes.length" description="暂无支持 RAID 的节点(未发现 storcli 或控制器)" />

      <template v-if="curCtrl">
        <el-descriptions :column="3" border size="small" class="ctrl-grid">
          <el-descriptions-item label="型号">{{ curCtrl.model }}</el-descriptions-item>
          <el-descriptions-item label="控制器 ID">{{ curCtrl.id }}</el-descriptions-item>
          <el-descriptions-item label="健康">{{ curCtrl.health || '-' }}</el-descriptions-item>
          <el-descriptions-item label="序列号">{{ curCtrl.serial || '-' }}</el-descriptions-item>
          <el-descriptions-item label="固件">{{ curCtrl.fw || '-' }}</el-descriptions-item>
          <el-descriptions-item label="BIOS">{{ curCtrl.bios || '-' }}</el-descriptions-item>
          <el-descriptions-item label="PCI 地址">{{ curCtrl.pci || '-' }}</el-descriptions-item>
          <el-descriptions-item label="SAS 地址">{{ curCtrl.sas_address || '-' }}</el-descriptions-item>
          <el-descriptions-item label="驱动">{{ curCtrl.driver }} {{ curCtrl.driver_ver }}</el-descriptions-item>
          <el-descriptions-item label="端口 / PD / DG / VD">
            {{ curCtrl.ports ?? '-' }} / {{ curCtrl.pds ?? '-' }} / {{ curCtrl.dgs ?? '-' }} / {{ curCtrl.vds ?? '-' }}
          </el-descriptions-item>
          <el-descriptions-item label="BBU">{{ curCtrl.bbu || '无' }}</el-descriptions-item>
          <el-descriptions-item label="温度">{{ curCtrl.temperature || '-' }}</el-descriptions-item>
          <el-descriptions-item label="告警" :span="3">{{ curCtrl.alarm || '无' }}</el-descriptions-item>
          <el-descriptions-item label="内存" :span="3">{{ curCtrl.memory || '-' }}</el-descriptions-item>
        </el-descriptions>

        <el-tabs v-model="tab" style="margin-top:8px" @tab-change="onTab">
          <el-tab-pane label="虚拟磁盘 VD" name="vds">
            <div class="toolbar">
              <el-button type="primary" :disabled="!allowMutation" @click="openCreate">创建 RAID 组</el-button>
              <span v-if="!allowMutation" class="hint">只读节点:创建/删除/属性修改已禁用</span>
              <span style="flex:1"></span>
              <span class="hint">VD 数:{{ vds.length }}</span>
            </div>
            <el-table :data="vds" v-loading="busy" size="small">
              <el-table-column prop="vd" label="VD" width="60" />
              <el-table-column prop="dg" label="DG" width="60" />
              <el-table-column prop="name" label="名称" min-width="120">
                <template #default="{ row }">{{ row.name || '-' }}</template>
              </el-table-column>
              <el-table-column prop="type" label="RAID" width="90" />
              <el-table-column label="状态" width="130">
                <template #default="{ row }">
                  <el-tag size="small" :type="vdStateType(row.state)">{{ row.state }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="size" label="容量" width="110" />
              <el-table-column prop="access" label="访问" width="70" />
              <el-table-column prop="cache" label="缓存策略" width="90" />
              <el-table-column prop="consist" label="一致性" width="90" />
              <el-table-column label="操作" width="170" fixed="right">
                <template #default="{ row }">
                  <el-button size="small" :disabled="!allowMutation" @click="openSet(row)">属性</el-button>
                  <el-button size="small" type="danger" plain :disabled="!allowMutation"
                             @click="removeVD(row)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>

          <el-tab-pane :label="`物理磁盘 PD(${pds.length})`" name="pds">
            <el-table :data="pds" v-loading="busy" size="small">
              <el-table-column prop="eid_slot" label="EID:Slt" width="90" />
              <el-table-column prop="did" label="DID" width="60" />
              <el-table-column label="状态" width="120">
                <template #default="{ row }">
                  <el-tag size="small" :type="pdStateType(row.state)">{{ row.state }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="dg" label="DG" width="60" />
              <el-table-column prop="size" label="容量" width="110" />
              <el-table-column prop="intf" label="接口" width="80" />
              <el-table-column prop="media" label="介质" width="80" />
              <el-table-column prop="sector" label="扇区" width="70" />
              <el-table-column prop="model" label="型号" min-width="200" show-overflow-tooltip />
              <el-table-column prop="spare" label="热备" width="70" />
              <el-table-column prop="sed" label="SED" width="60" />
            </el-table>
          </el-tab-pane>

          <el-tab-pane label="RAID 日志" name="events">
            <div class="toolbar">
              <el-button @click="loadEvents" :loading="evBusy">刷新日志</el-button>
              <el-select v-model="evLimit" style="width:120px" @change="loadEvents">
                <el-option :value="50" label="最近 50 条" />
                <el-option :value="100" label="最近 100 条" />
                <el-option :value="200" label="最近 200 条" />
              </el-select>
              <span class="hint">日志总数:{{ evTotal }}</span>
            </div>
            <el-table :data="events" v-loading="evBusy" size="small" empty-text="暂无日志">
              <el-table-column type="expand">
                <template #default="{ row }"><pre class="events mono">{{ row.raw }}</pre></template>
              </el-table-column>
              <el-table-column prop="seq" label="序号" width="130" />
              <el-table-column prop="time" label="时间" width="230" />
              <el-table-column prop="event" label="事件" min-width="360" show-overflow-tooltip />
            </el-table>
          </el-tab-pane>
        </el-tabs>
      </template>

      <!-- 创建 VD -->
      <el-dialog v-model="createDlg" title="创建 RAID 组(写操作)" width="620px">
        <el-alert v-if="!allowMutation" type="error" :closable="false"
                  title="只读节点,写操作已禁用" style="margin-bottom:10px" />
        <el-form label-width="120px">
          <el-form-item label="RAID 级别">
            <el-radio-group v-model="createForm.level">
              <el-radio-button v-for="l in ['0','1','5','6','10','50','60']" :key="l" :value="l">RAID {{ l }}</el-radio-button>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="物理磁盘">
            <el-select v-model="createForm.drives" multiple filterable style="width:100%"
                       placeholder="选择 EID:Slt(勿选已入组的磁盘)">
              <el-option v-for="p in pds" :key="p.eid_slot" :value="p.eid_slot"
                         :label="`${p.eid_slot} · ${p.state} · DG ${p.dg || '-'} · ${p.size} · ${p.model}`" />
            </el-select>
            <div class="hint">已属于 DG 的磁盘会被控制器拒绝;创建会清空所选磁盘数据。</div>
          </el-form-item>
          <el-form-item label="VD 名称">
            <el-input v-model="createForm.name" maxlength="15" placeholder="可选,≤15 位字母数字_.-" />
          </el-form-item>
          <el-form-item label="条带大小">
            <el-select v-model="createForm.stripesize" style="width:180px">
              <el-option value="" label="默认" />
              <el-option v-for="s in ['64','128','256','512','1024']" :key="s" :value="s" :label="s + ' KiB'" />
            </el-select>
          </el-form-item>
          <el-form-item label="每阵列磁盘数">
            <el-input v-model="createForm.pdperarray" style="width:180px"
                      placeholder="RAID10/50/60 可选,如 2" />
          </el-form-item>
          <el-form-item label="确认串">
            <el-input v-model="createForm.confirm" placeholder="键入 CREATE 名称(无名称则 CREATE 级别)" />
            <div class="hint">期望:{{ createConfirmHint }};提交后由 Agent 再次校验,只读模式双端拒绝。</div>
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="createDlg = false">取消</el-button>
          <el-button type="primary" :disabled="!allowMutation" @click="createVD">创建</el-button>
        </template>
      </el-dialog>

      <!-- VD 属性 -->
      <el-dialog v-model="setDlg" :title="`VD${setForm.vd} 属性(写操作)`" width="520px">
        <el-form label-width="110px">
          <el-form-item label="名称"><el-input v-model="setForm.name" maxlength="15" /></el-form-item>
          <el-form-item label="写策略">
            <el-select v-model="setForm.writepolicy" style="width:100%" clearable placeholder="不修改">
              <el-option value="WT" label="WT(Write Through)" />
              <el-option value="WB" label="WB(Write Back)" />
            </el-select>
          </el-form-item>
          <el-form-item label="读策略">
            <el-select v-model="setForm.readpolicy" style="width:100%" clearable placeholder="不修改">
              <el-option value="NoReadAhead" label="NoReadAhead" />
              <el-option value="ReadAhead" label="ReadAhead" />
              <el-option value="Adaptive" label="Adaptive" />
            </el-select>
          </el-form-item>
          <el-form-item label="IO 策略">
            <el-select v-model="setForm.iopolicy" style="width:100%" clearable placeholder="不修改">
              <el-option value="Direct" label="Direct" />
              <el-option value="Cached" label="Cached" />
            </el-select>
          </el-form-item>
          <el-form-item label="访问策略">
            <el-select v-model="setForm.access" style="width:100%" clearable placeholder="不修改">
              <el-option value="RW" label="RW" />
              <el-option value="RO" label="RO" />
            </el-select>
          </el-form-item>
          <el-form-item label="确认串">
            <el-input v-model="setForm.confirm" :placeholder="`键入 VD${setForm.vd}`" />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="setDlg = false">取消</el-button>
          <el-button type="primary" :disabled="!allowMutation" @click="saveSet">保存</el-button>
        </template>
      </el-dialog>
    </el-card>
  </div>
</template>
<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { api, canWrite } from '../api.js'
import { ElMessage, ElMessageBox } from 'element-plus'

const nodes = ref([])
const raidNodes = ref([])
const nodeId = ref(null)
const allowMutation = ref(false)
const controllers = ref([])
const ctrl = ref(0)
const vds = ref([])
const pds = ref([])
const tab = ref('vds')
const busy = ref(false)
const evBusy = ref(false)
const events = ref([])
const evTotal = ref(0)
const evLimit = ref(100)
const createDlg = ref(false)
const setDlg = ref(false)
const createForm = reactive({ level: '5', drives: [], name: '', stripesize: '', pdperarray: '', confirm: '' })
const setForm = reactive({ vd: '', name: '', writepolicy: '', readpolicy: '', iopolicy: '', access: '', confirm: '' })

const curCtrl = computed(() => controllers.value.find(c => c.id === ctrl.value) || null)
const createConfirmHint = computed(() =>
  'CREATE ' + (createForm.name ? createForm.name : createForm.level))

function vdStateType(s) {
  s = (s || '').toLowerCase()
  if (s === 'optl' || s === 'optimal') return 'success'
  if (s === 'dgrd' || s === 'degraded') return 'warning'
  if (s === 'pdgd' || s === 'offln' || s === 'failed') return 'danger'
  return 'info'
}
function pdStateType(s) {
  s = s || ''
  if (s === 'Onln') return 'success'
  if (s === 'UGood' || s === 'GHS' || s === 'DHS') return 'info'
  if (s === 'Rebuild' || s === 'Copyback' || s === 'JBOD') return 'warning'
  return 'danger'
}

async function loadNodes() {
  nodes.value = (await api.get('/nodes')).nodes
  const details = await Promise.all(nodes.value.map(async n => {
    try { return await api.get(`/nodes/${n.id}`) } catch { return null }
  }))
  raidNodes.value = details.filter(d => d && d.capabilities?.raid?.available)
  if (raidNodes.value.length) {
    const keep = raidNodes.value.find(n => n.id === nodeId.value)
    nodeId.value = keep ? keep.id : (raidNodes.value.find(n => n.online) || raidNodes.value[0]).id
  }
}

async function onNodeChange() {
  controllers.value = []
  ctrl.value = 0
  vds.value = []; pds.value = []; events.value = []
  await loadAll()
}

async function loadAll() {
  if (!nodeId.value) return
  busy.value = true
  try {
    const detail = raidNodes.value.find(n => n.id === nodeId.value)
    allowMutation.value = !!detail?.capabilities?.raid?.allow_mutation
    const cres = await api.get(`/nodes/${nodeId.value}/raid/controllers`)
    controllers.value = cres.controllers || []
    allowMutation.value = !!cres.allow_mutation
    if (!controllers.value.length) return
    if (!controllers.value.some(c => c.id === ctrl.value)) ctrl.value = controllers.value[0].id
    const base = `/nodes/${nodeId.value}/raid/controllers/${ctrl.value}`
    const [v, p] = await Promise.all([api.get(base + '/vds'), api.get(base + '/pds')])
    vds.value = v.vds || []
    pds.value = p.pds || []
  } finally { busy.value = false }
}

async function onTab(name) {
  if (name === 'events') await loadEvents()
}

async function loadEvents() {
  if (!nodeId.value || !controllers.value.length) return
  evBusy.value = true
  try {
    const r = await api.get(`/nodes/${nodeId.value}/raid/controllers/${ctrl.value}/events`,
      { params: { limit: evLimit.value } })
    evTotal.value = r.total || 0
    events.value = r.entries || []
  } finally { evBusy.value = false }
}

function openCreate() {
  if (!canWrite()) { ElMessage.warning('当前用户无写权限'); return }
  Object.assign(createForm, { level: '5', drives: [], name: '', stripesize: '', pdperarray: '', confirm: '' })
  createDlg.value = true
}
async function createVD() {
  try {
    await api.post(`/nodes/${nodeId.value}/raid/vds`, {
      controller: ctrl.value, level: createForm.level, drives: createForm.drives,
      name: createForm.name, stripesize: createForm.stripesize,
      pdperarray: createForm.pdperarray, confirm: createForm.confirm
    })
    ElMessage.success('RAID 组创建命令已完成并通过回读校验')
    createDlg.value = false
    await loadAll()
  } catch { /* 拦截器已提示 */ }
}
function openSet(row) {
  Object.assign(setForm, { vd: row.vd, name: row.name, writepolicy: '', readpolicy: '', iopolicy: '', access: '', confirm: '' })
  setDlg.value = true
}
async function saveSet() {
  try {
    await api.patch(`/nodes/${nodeId.value}/raid/vds/${setForm.vd}`, {
      controller: ctrl.value, name: setForm.name, writepolicy: setForm.writepolicy,
      readpolicy: setForm.readpolicy, iopolicy: setForm.iopolicy, access: setForm.access,
      confirm: setForm.confirm
    })
    ElMessage.success('VD 属性已更新')
    setDlg.value = false
    await loadAll()
  } catch { /* 拦截器已提示 */ }
}
async function removeVD(row) {
  let confirm
  try {
    ({ value: confirm } = await ElMessageBox.prompt(
      `删除 VD${row.vd}(${row.name || row.type}, ${row.size})会清空其上全部数据且不可恢复。请键入 VD${row.vd} 确认。`,
      '删除 RAID 组', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning',
        inputPlaceholder: `VD${row.vd}` }))
  } catch { return }
  try {
    await api.delete(`/nodes/${nodeId.value}/raid/vds/${row.vd}`,
      { params: { controller: ctrl.value, confirm } })
    ElMessage.success('RAID 组已删除并通过回读校验')
    await loadAll()
  } catch { /* 拦截器已提示 */ }
}

onMounted(async () => {
  await loadNodes()
  await loadAll()
})
</script>
<style scoped>
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 12px; flex-wrap: wrap; }
.hint { color: var(--my-text-2); font-size: 12px; }
.ctrl-grid { margin-bottom: 10px; }
.events { max-height: 56vh; overflow: auto; background: var(--my-surface-2); padding: 12px;
  border-radius: 10px; font-size: 12px; line-height: 1.55; white-space: pre-wrap; }
</style>
