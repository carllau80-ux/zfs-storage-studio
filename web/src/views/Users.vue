<template>
  <el-card shadow="never" header="用户与角色(admin)" class="page-card">
    <div style="margin-bottom:10px">
      <el-button type="primary" @click="addDlg = true">新建用户</el-button>
      <el-button @click="load">刷新</el-button>
    </div>
    <el-table :data="rows" v-loading="busy">
      <el-table-column prop="id" label="ID" width="70" />
      <el-table-column prop="username" label="用户名" width="180" />
      <el-table-column prop="role" label="角色" width="120">
        <template #default="{ row }">
          <el-tag size="small" :type="row.role === 'admin' ? 'danger' : (row.role === 'operator' ? 'warning' : 'info')">{{ row.role }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="disabled" label="状态" width="100">
        <template #default="{ row }">
          <el-tag size="small" :type="row.disabled ? 'danger' : 'success'">{{ row.disabled ? '禁用' : '启用' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" width="180" />
      <el-table-column label="操作" width="230">
        <template #default="{ row }">
          <el-button size="small" @click="editDlg(row)">改角色/口令</el-button>
          <el-button size="small" :type="row.disabled ? 'success' : 'danger'" plain :disabled="row.username === me"
                     @click="toggle(row)">{{ row.disabled ? '启用' : '禁用' }}</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="addDlg" title="新建用户" width="420px">
      <el-form label-width="90px">
        <el-form-item label="用户名"><el-input v-model="form.username" /></el-form-item>
        <el-form-item label="口令"><el-input v-model="form.password" type="password" show-password /></el-form-item>
        <el-form-item label="角色">
          <el-select v-model="form.role">
            <el-option value="admin" label="admin(全部)" /><el-option value="operator" label="operator(变更)" /><el-option value="viewer" label="viewer(只读)" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="addDlg = false">取消</el-button>
        <el-button type="primary" @click="createUser">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="editDlg2" title="修改用户" width="420px">
      <el-form label-width="90px">
        <el-form-item label="角色">
          <el-select v-model="ef.role">
            <el-option value="admin" label="admin" /><el-option value="operator" label="operator" /><el-option value="viewer" label="viewer" />
          </el-select>
        </el-form-item>
        <el-form-item label="新口令"><el-input v-model="ef.password" type="password" show-password placeholder="留空不修改" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editDlg2 = false">取消</el-button>
        <el-button type="primary" @click="saveUser">保存</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>
<script setup>
import { onMounted, ref } from 'vue'
import { api, store } from '../api.js'
import { ElMessage, ElMessageBox } from 'element-plus'

const rows = ref([]), busy = ref(false)
const addDlg = ref(false), editDlg2 = ref(false)
const form = ref({ username: '', password: '', role: 'viewer' })
const ef = ref({ id: null, role: 'viewer', password: '' })
const me = store.user?.username

async function load() {
  busy.value = true
  try { rows.value = (await api.get('/users')).users } finally { busy.value = false }
}
async function createUser() {
  await api.post('/users', form.value)
  addDlg.value = false; form.value = { username: '', password: '', role: 'viewer' }
  load()
}
function editDlg(row) {
  ef.value = { id: row.id, role: row.role, password: '' }
  editDlg2.value = true
}
async function saveUser() {
  const body = { role: ef.value.role }
  if (ef.value.password) body.password = ef.value.password
  await api.patch(`/users/${ef.value.id}`, body)
  editDlg2.value = false
  load()
}
async function toggle(row) {
  await ElMessageBox.confirm(`${row.disabled ? '启用' : '禁用'}用户 ${row.username}?`, '确认', { type: 'warning' })
  await api.patch(`/users/${row.id}`, { disabled: !row.disabled })
  load()
}
onMounted(load)
</script>
