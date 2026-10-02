import { createRouter, createWebHistory } from 'vue-router'
import { store } from '../api.js'

const routes = [
  { path: '/login', component: () => import('../views/Login.vue'), meta: { public: true } },
  {
    path: '/', component: () => import('../views/Layout.vue'),
    children: [
      { path: '', redirect: '/dashboard' },
      { path: 'dashboard', component: () => import('../views/Dashboard.vue'), meta: { title: '概览' } },
      { path: 'nodes', component: () => import('../views/Nodes.vue'), meta: { title: '节点' } },
      { path: 'pools', component: () => import('../views/Pools.vue'), meta: { title: '存储池' } },
      { path: 'volumes', component: () => import('../views/Volumes.vue'), meta: { title: '卷' } },
      { path: 'targets', component: () => import('../views/Targets.vue'), meta: { title: 'Target' } },
      { path: 'hosts', component: () => import('../views/Hosts.vue'), meta: { title: 'Initiator' } },
      { path: 'mappings', component: () => import('../views/Mappings.vue'), meta: { title: '映射' } },
      { path: 'shares', component: () => import('../views/Shares.vue'), meta: { title: '共享' } },
      { path: 'audit', component: () => import('../views/Audit.vue'), meta: { title: '审计' } },
      { path: 'users', component: () => import('../views/Users.vue'), meta: { title: '用户', admin: true } }
    ]
  },
  { path: '/:pathMatch(.*)*', redirect: '/' }
]

const router = createRouter({ history: createWebHistory(), routes })

router.beforeEach(to => {
  if (to.meta.public) return true
  if (!store.token) return '/login'
  if (to.meta.admin && store.user?.role !== 'admin') return '/dashboard'
  return true
})

export default router
