import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  { path: '/', redirect: '/overview' },
  { path: '/overview', name: 'overview', component: () => import('@/views/Overview.vue') },
  { path: '/config', name: 'config', component: () => import('@/views/Config.vue') },
  { path: '/audit', name: 'audit', component: () => import('@/views/Audit.vue') },
  { path: '/backups', name: 'backups', component: () => import('@/views/Backups.vue') }
]

export default createRouter({
  history: createWebHistory(),
  routes
})