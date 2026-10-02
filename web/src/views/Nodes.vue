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

    <el-drawer v-model="drawer" size="46%" :title="'节点 ' + (cur?.name || '')">
      <template v-if="detail">
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="主机">{{ detail.name }}</el-descriptions-item>
          <el-descriptions-item label="Agent 地址">{{ detail.agent_addr }}</el-descriptions-item>
          <el-descriptions-item label="Agent 版本">{{ detail.agent_version }}</el-descriptions-item>
          <el-descriptions-item label="ZFS 版本">{{ detail.zfs_version }}</el-descriptions-item>
          <el-descriptions-item label="注册时间">{{ detail.last_seen_at ? new Date(detail.last_seen_at).toLocaleString() : '-' }}</el-descriptions-item>
        </el-descriptions>
        <h4 style="margin:18px 0 10px">能力探测 capabilities</h4>
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="操作系统">{{ detail.capabilities.os || '-' }}</el-descriptions-item>
          <el-descriptions-item label="ZFS JSON 输出">
            <el-tag size="small" :type="detail.capabilities.json_supported ? 'success' : 'danger'">
              {{ detail.capabilities.json_supported ? '支持 (OpenZFS ≥ 2.3)' : '不支持' }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="mkfs 工具">
            <el-tag v-for="(ok, name) in detail.capabilities.mkfs || {}" :key="name" size="small"
                    :type="ok ? 'success' : 'info'" style="margin-right:4px">{{ name }}{{ ok ? '' : ' 缺失' }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="sparse 卷">
            <el-tag size="small" type="success">zfs create -s(属性不可见)</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="FC 目标模式" :span="2">
            <el-tag size="small" :type="detail.capabilities.fc?.fc_capable ? 'success' : 'info'">
              {{ detail.capabilities.fc?.fc_capable ? '可用' : '不可用' }}</el-tag>
            <span v-if="detail.capabilities.fc?.reason" style="color:#5f6368;font-size:12px;margin-left:8px">
              {{ detail.capabilities.fc.reason }}</span>
            <el-tag v-for="h in detail.capabilities.fc?.hba_ports || []" :key="h" size="small"
                    effect="plain" style="margin-left:4px">{{ h }}</el-tag>
          </el-descriptions-item>
        </el-descriptions>
        <el-collapse style="margin-top:8px">
          <el-collapse-item title="原始 capabilities JSON" name="raw">
            <pre class="mono cap">{{ JSON.stringify(detail.capabilities, null, 2) }}</pre>
          </el-collapse-item>
        </el-collapse>
      </template>
    </el-drawer>
  </el-card>
</template>
<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { api, canWrite, fmtBytes } from '../api.js'
import { ElMessageBox } from 'element-plus'

const nodes = ref([])
const busy = ref(false)
const drawer = ref(false)
const cur = ref(null)
const detail = ref(null)
let timer = null

async function load() {
  busy.value = true
  try { nodes.value = (await api.get('/nodes')).nodes } finally { busy.value = false }
}
async function showDetail(row) {
  cur.value = row
  detail.value = await api.get(`/nodes/${row.id}`)
  drawer.value = true
}
async function removeNode(row) {
  await ElMessageBox.confirm(`移除节点 ${row.name} 的记录?(需先停止该节点上的 Agent 服务)`, '确认', { type: 'warning' })
  await api.delete(`/nodes/${row.id}`)
  load()
}
onMounted(() => { load(); timer = setInterval(load, 8000) })
onUnmounted(() => clearInterval(timer))
</script>
<style scoped>.cap { max-height: 60vh; overflow: auto; }</style>
