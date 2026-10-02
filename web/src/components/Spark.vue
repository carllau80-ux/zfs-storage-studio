<template>
  <div class="spark">
    <div class="spark-head">
      <span class="spark-title">{{ title }}</span>
      <span class="spark-vals">
        <span v-for="(s, i) in series" :key="s.name" class="spark-val">
          <i class="dot" :style="{ background: s.color }"></i>{{ s.name }} {{ last(s) }}
        </span>
      </span>
    </div>
    <svg :viewBox="`0 0 ${W} ${H}`" preserveAspectRatio="none" class="spark-svg">
      <line v-for="n in 3" :key="n" :x1="0" :x2="W" :y1="(H - pad) * n / 4 + pad / 2" :y2="(H - pad) * n / 4 + pad / 2"
            stroke="var(--my-outline, #e5e7eb)" stroke-width="0.6" />
      <polyline v-for="s in series" :key="s.name"
                :points="points(s)" fill="none" :stroke="s.color" stroke-width="1.6"
                stroke-linejoin="round" stroke-linecap="round" />
      <polygon v-if="series[0]" :points="area(series[0])" :fill="series[0].color" opacity="0.10" />
    </svg>
    <div class="spark-sub">{{ sub }}</div>
  </div>
</template>
<script setup>
import { computed } from 'vue'
const props = defineProps({
  title: String,
  sub: String,
  series: { type: Array, default: () => [] }, // [{name,color,data:number[]}]
  fixedMax: { type: Number, default: 0 },
  format: { type: Function, default: (v) => String(v) }
})
const W = 300, H = 72, pad = 6
const maxV = computed(() => {
  if (props.fixedMax) return props.fixedMax
  let m = 0
  props.series.forEach(s => s.data.forEach(v => { if (v > m) m = v }))
  return m > 0 ? m * 1.15 : 1
})
function xy(data, i) {
  const n = Math.max(data.length - 1, 1)
  const x = (i / n) * W
  const y = H - pad / 2 - (Math.min(data[i] / maxV.value, 1) * (H - pad))
  return [x.toFixed(1), y.toFixed(1)]
}
function points(s) {
  return s.data.map((_, i) => xy(s.data, i).join(',')).join(' ')
}
function area(s) {
  if (!s.data.length) return ''
  const pts = s.data.map((_, i) => xy(s.data, i).join(','))
  return `0,${H - pad / 2} ${pts.join(' ')} ${W},${H - pad / 2}`
}
function last(s) {
  const d = s.data
  return d.length ? props.format(d[d.length - 1]) : '-'
}
</script>
<style scoped>
.spark { padding: 2px 2px 6px; }
.spark-head { display: flex; justify-content: space-between; align-items: baseline; margin-bottom: 2px; }
.spark-title { font-size: 13px; color: var(--my-text-2); }
.spark-vals { display: flex; gap: 10px; }
.spark-val { font-size: 12px; color: var(--my-text); display: inline-flex; align-items: center; gap: 4px; }
.dot { width: 8px; height: 8px; border-radius: 2px; display: inline-block; }
.spark-svg { width: 100%; height: 72px; display: block; }
.spark-sub { font-size: 11px; color: var(--my-text-2); margin-top: 2px; }
</style>
