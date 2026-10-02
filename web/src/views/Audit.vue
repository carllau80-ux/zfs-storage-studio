<template>
  <el-card shadow="never" header="审计日志" class="page-card">
    <div style="display:flex;gap:8px;margin-bottom:10px;flex-wrap:wrap">
      <el-input v-model="f.username" placeholder="用户名" style="width:140px" clearable />
      <el-select v-model="f.result" placeholder="结果" clearable style="width:110px">
        <el-option value="ok" label="成功" /><el-option value="err" label="失败" />
      </el-select>
      <el-select v-model="f.resource_type" placeholder="资源类型" clearable style="width:140px">
        <el-option v-for="t in types" :key="t" :value="t" :label="t" />
      </el-select>
      <el-button type="primary" @click="load">查询</el-button>
      <el-button @click="reset">重置</el-button>
    </div>
    <el-table :data="rows" v-loading="busy" size="small">
      <el-table-column prop="id" label="ID" width="80" />
      <el-table-column prop="created_at" label="时间" width="170" />
      <el-table-column prop="username" label="用户" width="100" />
      <el-table-column prop="action" label="动作" width="200" show-overflow-tooltip />
      <el-table-column prop="resource_type" label="类型" width="90" />
      <el-table-column prop="resource_name" label="资源" width="90" />
      <el-table-column prop="params" label="参数" min-width="200" show-overflow-tooltip class-name="mono" />
      <el-table-column label="结果" width="80">
        <template #default="{ row }">
          <el-tag size="small" :type="row.result === 'ok' ? 'success' : 'danger'">{{ row.result }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="detail" label="详情" min-width="120" show-overflow-tooltip />
    </el-table>
    <div style="margin-top:8px;text-align:right">
      <el-pagination background layout="prev, pager, next" :total="total"
                     :page-size="limit" :current-page="page" @current-change="onPage" />
    </div>
  </el-card>
</template>
<script setup>
import { onMounted, reactive, ref } from 'vue'
import { api } from '../api.js'

const rows = ref([]), busy = ref(false), total = ref(0)
const page = ref(1), limit = 50
const types = ['node', 'pool', 'dataset', 'snapshot', 'target', 'lun', 'acl', 'session', 'host', 'initiator', 'mapping', 'user']
const f = reactive({ username: '', result: '', resource_type: '' })

async function load() {
  busy.value = true
  try {
    const q = new URLSearchParams({ limit: String(limit), offset: String((page.value - 1) * limit) })
    for (const k of ['username', 'result', 'resource_type']) if (f[k]) q.set(k, f[k])
    const d = await api.get('/audit-logs?' + q.toString())
    rows.value = d.audit_logs; total.value = d.total
  } finally { busy.value = false }
}
function onPage(p) { page.value = p; load() }
function reset() {
  f.username = ''; f.result = ''; f.resource_type = ''
  page.value = 1; load()
}
onMounted(load)
</script>
