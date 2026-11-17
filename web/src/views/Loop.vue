<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import MetricChart from '../components/MetricChart.vue'
import { agentNodes, nodeName, nodeSlot } from '../store'
import Topology from './Topology.vue'

// Auto-rotating dashboards (for a wall display): one slide every SECONDS seconds.
const SECONDS = 5
const slides = [
  { key: 'agents', title: 'Connected agents', sub: 'agents that reported recently' },
  { key: 'cpu', title: 'CPU usage', sub: 'all nodes' },
  { key: 'mem', title: 'Memory usage', sub: 'all nodes' },
  { key: 'disk', title: 'Disk usage', sub: 'all nodes, per filesystem' },
  { key: 'iops', title: 'Disk IOPS', sub: 'read + write operations per second, all nodes' },
  { key: 'swap', title: 'Swap usage', sub: 'all nodes' },
  { key: 'load', title: 'Load average', sub: '1 minute, all nodes' },
  { key: 'topology', title: 'Live topology', sub: 'host-to-host traffic, last 30 s' },
] as const

const index = ref(0)
const paused = ref(false)
const runId = ref(0) // bumped on every (re)start so the progress bar animation restarts
let timer: number | undefined

function schedule() {
  window.clearTimeout(timer)
  runId.value++
  if (!paused.value) timer = window.setTimeout(next, SECONDS * 1000)
}
function go(i: number) { index.value = (i + slides.length) % slides.length; schedule() }
const next = () => go(index.value + 1)
const prev = () => go(index.value - 1)
function togglePause() { paused.value = !paused.value; schedule() }

function onKey(e: KeyboardEvent) {
  const t = e.target as HTMLElement | null
  if (t && ['INPUT', 'SELECT', 'TEXTAREA'].includes(t.tagName)) return
  if (e.key === 'ArrowRight') { next(); e.preventDefault() }
  else if (e.key === 'ArrowLeft') { prev(); e.preventDefault() }
  else if (e.key === ' ' && t === document.body) { togglePause(); e.preventDefault() }
}

// Charts fill the viewport below the controls.
const chartH = ref(420)
const fit = () => { chartH.value = Math.max(300, window.innerHeight - 330) }

onMounted(() => { fit(); window.addEventListener('resize', fit); window.addEventListener('keydown', onKey); schedule() })
onBeforeUnmount(() => { window.clearTimeout(timer); window.removeEventListener('resize', fit); window.removeEventListener('keydown', onKey) })

const online = computed(() => agentNodes.value.filter(n => n.online))
const mountNamer = (l: Record<string, string>) => ({
  key: `${l.node}|${l.mount}`, name: `${nodeName(l.node)} ${l.mount}`, slot: nodeSlot(l.node),
})
const current = computed(() => slides[index.value])
</script>

<template>
  <div class="controls">
    <div class="titles">
      <h2>{{ current.title }}</h2>
      <span class="muted">{{ current.sub }}</span>
    </div>
    <span class="grow"></span>
    <div class="dots" role="tablist" aria-label="Dashboards">
      <button v-for="(s, i) in slides" :key="s.key" class="dot" :class="{ on: i === index }" role="tab"
        :aria-selected="i === index" :aria-label="s.title" :title="s.title" @click="go(i)"></button>
    </div>
    <button class="btn" aria-label="Previous dashboard" title="Previous (←)" @click="prev">‹</button>
    <button class="btn" :aria-pressed="paused" :title="paused ? 'Resume (space)' : 'Pause (space)'" @click="togglePause">{{ paused ? '▶ Play' : '⏸ Pause' }}</button>
    <button class="btn" aria-label="Next dashboard" title="Next (→)" @click="next">›</button>
  </div>
  <div class="progress" aria-hidden="true">
    <div :key="runId" class="fill" :class="{ idle: paused }" :style="{ animationDuration: SECONDS + 's' }"></div>
  </div>

  <!-- Chart slides stay mounted (hidden) so their data is always fresh and there is no loading flash. -->
  <section v-show="current.key === 'agents'" class="agents">
    <div class="card hero" role="status">
      <div class="big num">{{ online.length }}</div>
      <div class="of">of {{ agentNodes.length }} agent{{ agentNodes.length === 1 ? '' : 's' }} connected</div>
      <ul class="names">
        <li v-for="n in agentNodes.slice(0, 12)" :key="n.id">
          <span :class="n.online ? 'on' : 'off'">{{ n.online ? '●' : '○' }}</span> {{ n.hostname }}<span v-if="!n.online" class="muted"> (offline)</span>
        </li>
        <li v-if="agentNodes.length > 12" class="muted">+ {{ agentNodes.length - 12 }} more</li>
      </ul>
    </div>
    <MetricChart bare title="Connected agents" metric="agents" node="all" unit="number" integer area :height="chartH"
      :namer="() => ({ key: 'agents', name: 'Connected agents', slot: 0 })" />
  </section>
  <MetricChart v-show="current.key === 'cpu'" bare title="CPU usage" metric="cpu" node="all" unit="percent" :y-max="100" :height="chartH" />
  <MetricChart v-show="current.key === 'mem'" bare title="Memory usage" metric="mem_pct" node="all" unit="percent" :y-max="100" :height="chartH" />
  <MetricChart v-show="current.key === 'disk'" bare title="Disk usage" metric="disk_pct" node="all" unit="percent" :y-max="100" :height="chartH" :namer="mountNamer" />
  <MetricChart v-show="current.key === 'iops'" bare title="Disk IOPS" metric="disk_iops" node="all" unit="iops" :height="chartH" />
  <MetricChart v-show="current.key === 'swap'" bare title="Swap usage" metric="swap_pct" node="all" unit="percent" :y-max="100" :height="chartH" />
  <MetricChart v-show="current.key === 'load'" bare title="Load average" metric="load" node="all" unit="number" :height="chartH"
    :filter="l => l.period === '1'" />
  <!-- The graph needs a visible container to lay out, so it is created when its slide comes up. -->
  <Topology v-if="current.key === 'topology'" compact />
</template>

<style scoped>
.controls { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; margin: 4px 0 8px; }
.titles { display: flex; align-items: baseline; gap: 12px; flex-wrap: wrap; }
h2 { font-size: 26px; }
.grow { flex: 1; }
.dots { display: flex; gap: 8px; margin-right: 8px; }
.dot { width: 10px; height: 10px; padding: 0; border-radius: 50%; border: 1px solid var(--text-muted); background: transparent; cursor: pointer; }
.dot.on { background: var(--series-1); border-color: var(--series-1); }
.dot:focus-visible { outline: 2px solid var(--series-1); outline-offset: 2px; }
.progress { height: 3px; background: var(--border); border-radius: 2px; overflow: hidden; margin-bottom: 16px; }
.fill { height: 100%; width: 0; background: var(--series-1); animation: fill linear forwards; }
.fill.idle { animation: none; }
@keyframes fill { from { width: 0 } to { width: 100% } }
@media (prefers-reduced-motion: reduce) { .fill { animation: none; width: 100%; opacity: .35 } }

.agents { display: grid; grid-template-columns: minmax(260px, 340px) 1fr; gap: 16px; }
@media (max-width: 800px) { .agents { grid-template-columns: 1fr; } }
.hero { display: flex; flex-direction: column; justify-content: center; }
.big { font-size: 112px; line-height: 1; font-weight: 700; }
.of { color: var(--text-secondary); font-size: 16px; margin: 4px 0 16px; }
.names { list-style: none; margin: 0; padding: 0; font-size: 14px; display: grid; gap: 4px; }
.on { color: var(--good-text); }
.off { color: var(--text-muted); }
</style>
