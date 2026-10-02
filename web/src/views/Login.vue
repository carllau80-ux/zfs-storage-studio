<template>
  <div class="login-wrap">
    <div class="login-card">
      <svg width="40" height="40" viewBox="0 0 24 24" style="margin-bottom:18px">
        <rect x="3" y="3" width="18" height="18" rx="5" fill="#1a73e8" />
        <path d="M7.5 12.5l3.2 3.2 5.8-6" stroke="#fff" stroke-width="2" fill="none"
              stroke-linecap="round" stroke-linejoin="round" />
      </svg>
      <h1>RunStor 存储管理</h1>
      <p class="login-sub">登录以继续</p>
      <a class="lang-link" @click="toggleLang">{{ langNow === 'en' ? '中文' : 'EN' }}</a>
      <el-form @keyup.enter="doLogin">
        <el-form-item><el-input v-model="username" placeholder="用户名" size="large" /></el-form-item>
        <el-form-item><el-input v-model="password" type="password" show-password placeholder="口令" size="large" /></el-form-item>
        <el-button type="primary" size="large" style="width:100%" :loading="busy" @click="doLogin">登录</el-button>
      </el-form>
    </div>
    <p class="login-foot">存储管理员控制台 · 卷 / 快照 / iSCSI 映射</p>
  </div>
</template>
<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, setAuth } from '../api.js'
import { lang, setLang } from '../i18n.js'
const langNow = ref(lang())
function toggleLang() { setLang(langNow.value === 'en' ? 'zh' : 'en') }

const router = useRouter()
const username = ref('')
const password = ref('')
const busy = ref(false)

async function doLogin() {
  if (!username.value || !password.value) return
  busy.value = true
  try {
    const d = await api.post('/auth/login', { username: username.value, password: password.value })
    setAuth(d.token, d.user)
    router.push('/dashboard')
  } finally { busy.value = false }
}
</script>
<style scoped>
.login-wrap { min-height: 100vh; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; background: var(--my-bg); padding: 24px; }
.login-card { width: 400px; display: flex; flex-direction: column; align-items: center; padding: 34px 40px; border-radius: 28px; background: var(--my-surface); border: 1px solid var(--my-outline); }
.login-card h1 { margin: 0 0 6px; font-size: 26px; font-weight: 500; color: var(--my-text); }
.login-sub { color: var(--my-text-2); font-size: 13px; margin: 0 0 30px; }
.login-card :deep(.el-form) { width: 100%; }
.login-card :deep(.el-button) { height: 46px; border-radius: 999px; font-size: 15px; }
.login-foot { color: var(--my-text-2); font-size: 12px; margin-top: 30px; }
.lang-link { position: absolute; top: 22px; right: 26px; color: var(--my-text-2); cursor: pointer; font-size: 13px; }
.login-wrap { position: relative; }
</style>
