import axios from 'axios'
import { ElMessage } from 'element-plus'
import { reactive } from 'vue'
import router from './router/index.js'
import { lang, trErr } from './i18n.js'

function errText(err) {
  if (!err) return '请求失败'
  if (lang() === 'en' && err.key) return trErr(err.key, err.params, err.message || err.code)
  return err.message || err.code
}

export const store = reactive({
  token: localStorage.getItem('zfsmgr_token') || '',
  user: JSON.parse(localStorage.getItem('zfsmgr_user') || 'null')
})

export function role() { return store.user ? store.user.role : 'viewer' }
export function canWrite() { return ['admin', 'operator'].includes(role()) }
export function isAdmin() { return role() === 'admin' }

export const api = axios.create({ baseURL: '/api/v1', timeout: 120000 })

api.interceptors.request.use(cfg => {
  if (store.token) cfg.headers.Authorization = 'Bearer ' + store.token
  return cfg
})

api.interceptors.response.use(
  resp => {
    const d = resp.data
    if (d && d.ok === false && d.error) {
      ElMessage.error(errText(d.error))
      return Promise.reject(d.error)
    }
    return d && d.data !== undefined ? d.data : d
  },
  err => {
    const m = err.response?.data?.error
    ElMessage.error(m ? errText(m) : (err.message || '请求失败'))
    if (err.response?.status === 401) logout()
    return Promise.reject(err)
  }
)

export function setAuth(token, user) {
  store.token = token; store.user = user
  localStorage.setItem('zfsmgr_token', token)
  localStorage.setItem('zfsmgr_user', JSON.stringify(user))
}

export function logout() {
  store.token = ''; store.user = null
  localStorage.removeItem('zfsmgr_token')
  localStorage.removeItem('zfsmgr_user')
  router.push('/login')
}

// 容量统一以 GiB 展示(与后端 humanBytes 口径一致)
export const fmtBytes = b => (Number(b) / 1073741824).toFixed(1) + ' GiB'
// 块大小/记录大小等非容量参数仍按自然单位
export const fmtKiB = b => {
  b = Number(b) || 0
  if (b >= 1073741824) return (b / 1073741824).toFixed(1) + ' GiB'
  if (b >= 1048576) return (b / 1048576).toFixed(1) + ' MiB'
  if (b >= 1024) return (b / 1024).toFixed(1) + ' KiB'
  return b + ' B'
}
