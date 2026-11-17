<script setup lang="ts">
import { computed } from 'vue'
import type { NodeView } from '../api'
import { formatPct, formatRate } from '../format'
import { nodeSlot } from '../store'
import { seriesColor, themeVersion } from '../theme'

const props = defineProps<{ node: NodeView }>()
const l = computed(() => props.node.latest)
const memPct = computed(() => (l.value && l.value.memTotal > 0 ? (l.value.memUsed / l.value.memTotal) * 100 : NaN))
const swatch = computed(() => { void themeVersion.value; return seriesColor(nodeSlot(props.node.id)) })

// Severity rides on the fill; the value is always printed next to it.
function sev(p: number): string {
  if (!(p >= 0)) return 'var(--text-muted)'
  if (p >= 90) return 'var(--critical)'
  if (p >= 75) return 'var(--warning)'
  return 'var(--series-1)'
}
const meters = computed(() => [
  { label: 'CPU', v: l.value?.cpu ?? NaN },
  { label: 'Memory', v: memPct.value },
  { label: 'Disk', v: l.value?.diskMaxPct ?? NaN },
])
</script>

<template>
  <router-link :to="`/nodes/${encodeURIComponent(node.id)}`" class="card tile" :class="{ off: !node.online }">
    <div class="head">
      <i class="swatch" :style="{ background: swatch }"></i>
      <strong class="name">{{ node.hostname }}</strong>
      <span class="status" :class="node.online ? 'on' : 'off'">{{ node.online ? '● online' : '○ offline' }}</span>
    </div>
    <div class="ips muted num">{{ node.ips.join(', ') || node.id }}</div>
    <div v-for="m in meters" :key="m.label" class="meter">
      <span class="lbl">{{ m.label }}</span>
      <div class="track" :style="{ '--m': sev(m.v) }"><div class="fill" :style="{ width: Math.min(100, Math.max(0, m.v || 0)) + '%' }"></div></div>
      <span class="val num">{{ formatPct(m.v) }}</span>
    </div>
    <div class="net num">
      <span>↓ {{ l ? formatRate(l.rxBps) : '–' }}</span>
      <span>↑ {{ l ? formatRate(l.txBps) : '–' }}</span>
    </div>
  </router-link>
</template>

<style scoped>
.tile { display: block; text-decoration: none; }
.tile:hover { border-color: var(--text-muted); }
.tile.off { opacity: .65; }
.head { display: flex; align-items: center; gap: 8px; }
.swatch { width: 10px; height: 10px; border-radius: 50%; display: inline-block; }
.name { font-size: 16px; }
.status { margin-left: auto; font-size: 12px; }
.status.on { color: var(--good-text); }
.status.off { color: var(--text-muted); }
.ips { font-size: 12px; margin: 2px 0 10px; }
.meter { display: grid; grid-template-columns: 56px 1fr 44px; align-items: center; gap: 8px; margin: 6px 0; font-size: 12px; }
.lbl { color: var(--text-secondary); }
.track { height: 8px; border-radius: 4px; background: color-mix(in srgb, var(--m) 18%, var(--surface-1)); overflow: hidden; }
.fill { height: 100%; border-radius: 4px; background: var(--m); transition: width .4s; }
.val { text-align: right; }
.net { display: flex; gap: 16px; margin-top: 10px; font-size: 12px; color: var(--text-secondary); }
</style>
