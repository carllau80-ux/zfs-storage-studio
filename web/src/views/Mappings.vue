<template>
  <el-card shadow="never" header="映射一览 (卷 ↔ 主机)" class="page-card">
    <div style="margin-bottom:10px"><el-button @click="load">刷新</el-button></div>
    <el-table :data="rows" v-loading="busy">
      <el-table-column prop="id" label="ID" width="70" />
      <el-table-column prop="host" label="Initiator" width="140" />
      <el-table-column label="传输" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="row.transport === 'fc' ? 'warning' : 'primary'">
            {{ row.transport === 'fc' ? 'FC' : 'iSCSI' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="node_name" label="节点" width="110" />
      <el-table-column prop="dataset" label="卷" min-width="200" show-overflow-tooltip />
      <el-table-column prop="iqn" label="Target IQN / WWPN" min-width="300" show-overflow-tooltip />
      <el-table-column prop="lun_id" label="LUN" width="60" />
      <el-table-column label="操作" width="120">
        <template #default="{ row }">
          <el-button v-if="canWrite()" size="small" type="danger" plain @click="unmap(row)">解除映射</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-card>
</template>
<script setup>
import { onMounted, ref } from 'vue'
import { api, canWrite } from '../api.js'
import { ElMessageBox } from 'element-plus'

const rows = ref([]), busy = ref(false)
async function load() {
  busy.value = true
  try { rows.value = (await api.get('/mappings')).mappings } finally { busy.value = false }
}
async function unmap(row) {
  await ElMessageBox.confirm(`解除 ${row.host} → ${row.dataset} 的映射?`, '确认', { type: 'warning' })
  await api.delete(`/mappings/${row.id}`)
  load()
}
onMounted(load)
</script>
