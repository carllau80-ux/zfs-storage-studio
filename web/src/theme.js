// 主题:light / dark / auto(跟随系统)
const KEY = 'zfsmgr_theme'
const mq = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null
let listeners = []

export function getMode() {
  const v = localStorage.getItem(KEY)
  return v === 'dark' || v === 'light' || v === 'auto' ? v : 'auto'
}
export function resolved() {
  const m = getMode()
  if (m === 'auto') return mq && mq.matches ? 'dark' : 'light'
  return m
}
export function apply() {
  document.documentElement.dataset.theme = resolved()
  document.documentElement.dataset.themeMode = getMode()
}
export function setMode(m) {
  localStorage.setItem(KEY, m)
  apply()
}
export function cycle() {
  const order = ['light', 'dark', 'auto']
  const next = order[(order.indexOf(getMode()) + 1) % order.length]
  setMode(next)
  return next
}
export function onChange(fn) { listeners.push(fn) }
export function initTheme() {
  apply()
  if (mq && mq.addEventListener) {
    mq.addEventListener('change', () => { if (getMode() === 'auto') { apply(); listeners.forEach(f => f()) } })
  }
}
