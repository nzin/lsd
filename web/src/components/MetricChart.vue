<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { getMetrics, type Series } from '../api'
import { escapeHtml, formatAxis, formatTime, formatUnit, type Unit } from '../format'
import { nodeName, nodeSlot, rangeSec, refreshTick } from '../store'
import { cssVar, seriesColor, themeVersion } from '../theme'

use([LineChart, GridComponent, TooltipComponent, CanvasRenderer])

export interface Naming { key: string; name: string; slot: number } // slot -2 = auto

const props = withDefaults(defineProps<{
  title: string
  subtitle?: string
  metric: string
  node: string                       // 'all' or a node id
  unit: Unit
  yMax?: number
  area?: boolean
  stacked?: boolean
  integer?: boolean                  // whole-number y axis (counts)
  bare?: boolean                     // hide the card header (the page supplies the title)
  height?: number
  filter?: (labels: Record<string, string>) => boolean
  namer?: (labels: Record<string, string>) => Naming
}>(), { height: 220 })

// Default naming: one series per node, colored by the node's stable slot.
const naming = (l: Record<string, string>): Naming => props.namer
  ? props.namer(l)
  : { key: l.node, name: nodeName(l.node), slot: nodeSlot(l.node) }

interface Item { key: string; name: string; slot: number; points: [number, number][] }
const items = ref<Item[]>([])
const bounds = ref<{ from: number; to: number }>({ from: 0, to: 0 })
const loading = ref(false)
const error = ref<string | null>(null)
const showTable = ref(false)
let ctl: AbortController | null = null

async function load() {
  ctl?.abort()
  ctl = new AbortController()
  loading.value = true
  try {
    const res = await getMetrics(props.metric, props.node, rangeSec.value, ctl.signal)
    const raw: Series[] = props.filter ? res.series.filter(s => props.filter!(s.labels)) : res.series
    const mapped = raw.map(s => ({ ...naming(s.labels), points: s.points }))
    // Fixed slots first (in slot order), then "auto" ones by natural key order; auto slots are
    // then assigned by position, so colors stay stable between refreshes.
    const order = (m: { slot: number }) => (m.slot >= 0 ? m.slot : 1000)
    mapped.sort((a, b) => order(a) - order(b) || a.key.localeCompare(b.key, undefined, { numeric: true }))
    mapped.forEach((m, i) => { if (m.slot === -2) m.slot = i })
    items.value = mapped
    bounds.value = { from: res.from, to: res.to }
    error.value = null
  } catch (e) {
    if ((e as Error).name !== 'AbortError') error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}
watch([() => props.metric, () => props.node, rangeSec, refreshTick], load, { immediate: true })
onBeforeUnmount(() => ctl?.abort())

const hasData = computed(() => items.value.some(i => i.points.length > 0))

const colored = computed(() => {
  void themeVersion.value
  void items.value.length
  return items.value.map(i => ({ ...i, color: seriesColor(i.slot) }))
})

const option = computed<any>(() => {
  void themeVersion.value
  const surface = cssVar('--surface-1'), grid = cssVar('--grid'), muted = cssVar('--text-muted')
  const primary = cssVar('--text-primary'), secondary = cssVar('--text-secondary'), border = cssVar('--border')
  const unit = props.unit
  return {
    animation: false,
    textStyle: { fontFamily: 'inherit' },
    grid: { left: 8, right: 16, top: 12, bottom: 4, containLabel: true },
    tooltip: {
      trigger: 'axis',
      confine: true,
      backgroundColor: surface,
      borderColor: border,
      borderWidth: 1,
      padding: 10,
      textStyle: { color: primary, fontSize: 12 },
      axisPointer: { type: 'line', lineStyle: { color: muted, width: 1, type: 'solid' } },
      // Values lead, names follow; names are escaped (they come from the API).
      formatter: (ps: any[]) => {
        if (!ps.length) return ''
        const t = formatTime(ps[0].value[0], rangeSec.value > 86400)
        const rows = [...ps].sort((a, b) => b.value[1] - a.value[1]).slice(0, 14).map(p =>
          `<div style="display:flex;align-items:center;gap:8px;margin-top:4px">` +
          `<span style="display:inline-block;width:12px;height:2px;border-radius:1px;background:${p.color}"></span>` +
          `<b style="font-variant-numeric:tabular-nums">${escapeHtml(formatUnit(p.value[1], unit))}</b>` +
          `<span style="color:${secondary}">${escapeHtml(p.seriesName)}</span></div>`).join('')
        return `<div style="color:${secondary};font-size:11px">${escapeHtml(t)}</div>${rows}`
      },
    },
    xAxis: {
      type: 'time',
      min: bounds.value.from || undefined,
      max: bounds.value.to || undefined,
      axisLine: { lineStyle: { color: grid } },
      axisTick: { show: false },
      axisLabel: { color: muted, hideOverlap: true },
      splitLine: { show: false },
    },
    yAxis: {
      type: 'value',
      min: 0,
      // count charts get one unit of headroom so a flat line is not glued to the top edge
      max: props.yMax ?? (props.integer ? (v: { max: number }) => Math.max(v.max, 1) + 1 : undefined),
      minInterval: props.integer ? 1 : undefined,
      axisLine: { show: false },
      axisTick: { show: false },
      axisLabel: { color: muted, formatter: (v: number) => formatAxis(v, unit) },
      splitLine: { lineStyle: { color: grid, width: 1, type: 'solid' } },
    },
    series: colored.value.map(s => ({
      type: 'line',
      name: s.name,
      data: s.points,
      showSymbol: false,
      symbol: 'circle',
      symbolSize: 8,                         // hover dot, >= 8px with a surface ring
      lineStyle: { width: 2, color: s.color, cap: 'round', join: 'round' },
      itemStyle: { color: s.color, borderColor: surface, borderWidth: 2 },
      areaStyle: props.area || props.stacked ? { color: s.color, opacity: props.stacked ? 0.22 : 0.1 } : undefined,
      stack: props.stacked ? 'total' : undefined,
      emphasis: { disabled: false, focus: 'none' },
      z: 3,
    })),
  }
})

function stats(i: Item) {
  const v = i.points.map(p => p[1])
  if (!v.length) return { last: NaN, avg: NaN, max: NaN }
  return { last: v[v.length - 1], avg: v.reduce((a, b) => a + b, 0) / v.length, max: Math.max(...v) }
}
</script>

<template>
  <section class="card chart" :aria-label="title">
    <header v-if="!bare">
      <div>
        <h3>{{ title }}</h3>
        <div v-if="subtitle" class="muted sub">{{ subtitle }}</div>
      </div>
      <button class="btn small" :aria-pressed="showTable" @click="showTable = !showTable">{{ showTable ? 'Chart' : 'Table' }}</button>
    </header>

    <ul v-if="colored.length >= 2 && !showTable" class="legend">
      <li v-for="s in colored" :key="s.key"><i :style="{ background: s.color }"></i>{{ s.name }}</li>
    </ul>

    <div v-if="!showTable" class="plot" :class="{ dim: loading && hasData }" :style="{ height: height + 'px' }">
      <v-chart v-if="hasData" :option="option" autoresize />
      <div v-else class="empty muted">{{ error ? 'Error: ' + error : loading ? 'Loading…' : 'No data in this range yet' }}</div>
    </div>

    <table v-else class="num">
      <thead><tr><th>Series</th><th class="r">Latest</th><th class="r">Average</th><th class="r">Max</th></tr></thead>
      <tbody>
        <tr v-for="s in colored" :key="s.key">
          <td><i class="key" :style="{ background: s.color }"></i>{{ s.name }}</td>
          <td class="r">{{ formatUnit(stats(s).last, unit) }}</td>
          <td class="r">{{ formatUnit(stats(s).avg, unit) }}</td>
          <td class="r">{{ formatUnit(stats(s).max, unit) }}</td>
        </tr>
        <tr v-if="!colored.length"><td colspan="4" class="muted">No data in this range yet</td></tr>
      </tbody>
    </table>
    <div v-if="error && hasData" class="muted sub">⚠ refresh failed: {{ error }} (showing last data)</div>
  </section>
</template>

<style scoped>
.chart header { display: flex; justify-content: space-between; align-items: flex-start; gap: 8px; margin-bottom: 8px; }
h3 { font-size: 14px; }
.sub { font-size: 12px; }
.small { padding: 2px 10px; font-size: 12px; }
.legend { display: flex; flex-wrap: wrap; gap: 4px 16px; list-style: none; margin: 0 0 6px; padding: 0; font-size: 12px; color: var(--text-secondary); }
.legend li { display: flex; align-items: center; gap: 6px; }
.legend i, .key { display: inline-block; width: 12px; height: 2px; border-radius: 1px; }
.key { margin-right: 8px; vertical-align: middle; }
.plot { transition: opacity .15s; }
.plot.dim { opacity: .55; }
.plot :deep(.echarts), .plot > :deep(div) { height: 100%; }
.empty { display: grid; place-items: center; height: 100%; font-size: 13px; }
</style>
