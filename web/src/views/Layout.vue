<template>
  <div class="md-shell">
    <!-- 应用栏 -->
    <header class="md-topbar">
      <div class="md-brand" @click="$router.push('/dashboard')">
        <span class="md-logo"><i></i><i></i><i></i><i></i></span>
        <span class="md-brand-name">RunStor 存储管理</span>
      </div>
      <div class="md-actions">
        <el-button text size="small" style="color:var(--my-text-2)" @click="toggleLang">
          {{ langNow === 'en' ? '中文' : 'EN' }}
        </el-button>
        <el-button circle size="small" plain :title="'主题:' + themeLabel + '(点击切换)'"
                   class="theme-btn" @click="toggleTheme">
          <svg v-if="resolved() === 'dark'" width="16" height="16" viewBox="0 0 24 24" fill="none"
               stroke="currentColor" stroke-width="1.8"><circle cx="12" cy="12" r="4.2" /><path d="M12 2.5v2.4M12 19.1v2.4M2.5 12h2.4M19.1 12h2.4M4.9 4.9l1.7 1.7M17.4 17.4l1.7 1.7M19.1 4.9l-1.7 1.7M6.6 17.4l-1.7 1.7" /></svg>
          <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none"
               stroke="currentColor" stroke-width="1.8"><path d="M12 3a9 9 0 1 0 9 9c0-.5-.4-1-1-1a6 6 0 0 1-7-7c-.1-.6-.6-1-1-1Z" /></svg>
        </el-button>
        <el-dropdown trigger="click">
          <span class="md-user">
            <span class="md-avatar">{{ (store.user?.username || '?')[0].toUpperCase() }}</span>
            <span class="md-user-meta"><b>{{ store.user?.username }}</b><i>{{ roleText }}</i></span>
          </span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item @click="logout">退出登录</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </header>

    <div class="md-body">
      <!-- 导航轨 -->
      <nav class="md-rail">
        <button v-for="item in navItems" :key="item.path"
                :class="['md-rail-item', { active: $route.path === item.path }]"
                :style="{ '--rail': item.color }"
                :title="item.label" @click="$router.push(item.path)">
          <span class="md-rail-icon" :style="{ color: item.color }" v-html="item.icon"></span>
          <span class="md-rail-label">{{ item.label }}</span>
        </button>
      </nav>

      <main class="md-main">
        <h1 class="md-title">{{ $route.meta.title || '' }}</h1>
        <router-view />
      </main>
    </div>
  </div>
</template>
<script setup>
import { computed, ref } from 'vue'
import { store, logout, isAdmin } from '../api.js'
import { lang, setLang } from '../i18n.js'
import { getMode, cycle, onChange, resolved } from '../theme.js'
const langNow = ref(lang())
const themeMode = ref(getMode())
function toggleTheme() { themeMode.value = cycle() }
onChange(() => { themeMode.value = getMode() })
const themeLabel = computed(() => ({ light: '浅色', dark: '深色', auto: '跟随系统' }[themeMode.value] || '跟随系统'))
function toggleLang() { setLang(langNow.value === 'en' ? 'zh' : 'en') }

const roleText = computed(() => ({ admin: '管理员', operator: '运维员', viewer: '只读' }[store.user?.role] || ''))

const I = {
  dash: '<svg viewBox="0 0 24 24"><path d="M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z"/></svg>',
  node: '<svg viewBox="0 0 24 24"><rect x="5" y="5" width="14" height="14" rx="2.5"/><circle cx="12" cy="12" r="2.6"/><path d="M12 14.6V19M9.4 12H5"/></svg>',
  pool: '<svg viewBox="0 0 24 24"><path d="M12 3c4.4 0 7.5 1.6 7.5 3.6S16.4 10.2 12 10.2 4.5 8.6 4.5 6.6 7.6 3 12 3Z"/><path d="M4.5 12c0 2 3.1 3.6 7.5 3.6s7.5-1.6 7.5-3.6"/><path d="M4.5 16.4c0 2 3.1 3.6 7.5 3.6s7.5-1.6 7.5-3.6"/></svg>',
  vol: '<svg viewBox="0 0 24 24"><path d="M12 3 20 7.5v9L12 21 4 16.5v-9Z"/><path d="M4 7.5 12 12l8-4.5M12 12v9"/></svg>',
  iscsi: '<svg viewBox="0 0 24 24"><circle cx="6" cy="12" r="2.6"/><circle cx="18" cy="6" r="2.4"/><circle cx="18" cy="18" r="2.4"/><path d="M8.6 11 15.6 7M8.6 13l7 4"/></svg>',
  host: '<svg viewBox="0 0 24 24"><rect x="4" y="3" width="16" height="18" rx="2.5"/><path d="M4 9h16M4 15h16M9.5 6h.01M9.5 18h.01"/></svg>',
  map: '<svg viewBox="0 0 24 24"><rect x="3" y="3" width="8" height="8" rx="2"/><rect x="13" y="13" width="8" height="8" rx="2"/><path d="M7 11v2a4 4 0 0 0 4 4h2"/></svg>',
  audit: '<svg viewBox="0 0 24 24"><path d="M6 3h9l4 4v14H6z"/><path d="M14 3v5h5M9.5 12h5M9.5 16h5"/></svg>',
  share: '<svg viewBox="0 0 24 24"><path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><circle cx="10" cy="13" r="1.6"/><circle cx="16" cy="11.4" r="1.6"/><circle cx="15.4" cy="16.6" r="1.6"/><path d="M11.3 12.3l3.4-0.7M11.2 14.1l3 1.7M15.9 12.8l-0.4 2.2"/></svg>',
  user: '<svg viewBox="0 0 24 24"><circle cx="12" cy="8" r="4"/><path d="M4.5 20a7.5 7.5 0 0 1 15 0"/></svg>'
}
const navItems = computed(() => {
  const items = [
    { path: '/dashboard', label: '概览', icon: I.dash, color: '#4285f4' },    // 蓝:总览/入口
    { path: '/nodes', label: '节点', icon: I.node, color: '#7b1fa2' },        // 紫:服务器
    { path: '/pools', label: '存储池', icon: I.pool, color: '#0097a7' },      // 青:池/存储
    { path: '/volumes', label: '卷', icon: I.vol, color: '#43a047' },         // 绿:数据卷
    { path: '/targets', label: 'Target', icon: I.iscsi, color: '#f4511e' },   // 橙红:目标(iSCSI/FC)
    { path: '/hosts', label: 'Initiator', icon: I.host, color: '#3949ab' },   // 靛蓝:客户端(iSCSI IQN/FC WWPN)
    { path: '/mappings', label: '映射', icon: I.map, color: '#00897b' },      // 蓝绿:连接映射
    { path: '/shares', label: '共享', icon: I.share, color: '#f9a825' },      // 琥珀:文件共享 NFS/SMB
    { path: '/audit', label: '审计', icon: I.audit, color: '#c2185b' },       // 玫红:日志
    { path: '/users', label: '用户', icon: I.user, color: '#546e7a' }         // 蓝灰:账号
  ]
  return isAdmin() ? items : items.filter(i => i.path !== '/users')
})
</script>
<style scoped>
.md-shell { min-height: 100vh; background: var(--my-bg); color: var(--my-text); }
.md-topbar {
  position: sticky; top: 0; z-index: 30;
  display: flex; align-items: center; justify-content: space-between;
  height: 64px; padding: 0 20px;
  background: color-mix(in srgb, var(--my-surface) 88%, transparent);
  backdrop-filter: blur(10px);
  border-bottom: 1px solid var(--my-outline);
}
.md-brand { display: flex; align-items: center; gap: 12px; cursor: pointer; }
.md-logo { display: grid; grid-template-columns: 1fr 1fr; gap: 3px; width: 22px; height: 22px; }
.md-logo i { border-radius: 5px; }
.md-logo i:nth-child(1) { background: #4285f4; }
.md-logo i:nth-child(2) { background: #ea4335; }
.md-logo i:nth-child(3) { background: #fbbc04; }
.md-logo i:nth-child(4) { background: #34a853; }
.md-brand-name { font-size: 17px; font-weight: 500; color: var(--my-text); }
.md-actions { display: flex; align-items: center; gap: 14px; }
.theme-btn { border-radius: 999px; }
.md-user { display: flex; align-items: center; gap: 9px; cursor: pointer; outline: none; }
.md-avatar {
  width: 32px; height: 32px; border-radius: 50%;
  background: var(--my-primary);
  color: var(--my-on-primary);
  display: inline-flex; align-items: center; justify-content: center;
  font-weight: 600; font-size: 15px;
}
.md-user-meta { display: flex; flex-direction: column; line-height: 1.2; font-size: 12px; }
.md-user-meta b { color: var(--my-text); font-weight: 500; font-size: 13px; }
.md-user-meta i { color: var(--my-text-2); font-style: normal; }

.md-body { display: flex; min-height: calc(100vh - 64px); }

/* 导航轨 */
.md-rail {
  flex: 0 0 92px;
  border-right: 1px solid var(--my-outline);
  padding: 14px 0;
  display: flex; flex-direction: column; align-items: center; gap: 6px;
  background: var(--my-surface);
}
/* 图标着色区:半透明色块悬浮提示 */
.md-rail-icon { width: 30px; height: 30px; border-radius: 9px; display: inline-flex; align-items: center; justify-content: center; transition: background .15s; }
.md-rail-icon :deep(svg) { width: 21px; height: 21px; fill: currentColor; }
.md-rail-item {
  width: 76px; padding: 7px 2px 6px; margin: 1px 0;
  display: flex; flex-direction: column; align-items: center; gap: 4px;
  border: none; background: transparent; border-radius: 16px;
  cursor: pointer; color: var(--my-text-2);
  font: inherit; font-size: 11px;
}
.md-rail-item:hover .md-rail-icon { background: color-mix(in srgb, var(--rail) 14%, transparent); }
.md-rail-item:hover { background: var(--my-surface-2); }
.md-rail-item.active { background: color-mix(in srgb, var(--rail) 16%, transparent); }
.md-rail-item.active .md-rail-icon { background: color-mix(in srgb, var(--rail) 22%, transparent); }
.md-rail-item.active .md-rail-label { color: var(--my-text); font-weight: 600; }
.md-rail-label { color: inherit; }

.md-main { flex: 1; min-width: 0; padding: 26px 34px 60px; max-width: 1440px; }
.md-title { margin: 6px 0 18px; font-size: 27px; font-weight: 500; color: var(--my-text); letter-spacing: .1px; }
</style>
