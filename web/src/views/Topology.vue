<script setup lang="ts">
import cytoscape from 'cytoscape'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { getHistoryTopology, openLive, type TopoEdge, type TopoNode, type Topology } from '../api'
import { edgeWidth, formatRate, formatNumber } from '../format'
import { nodes, nodeSlot, rangeSec, refreshTick, setNodes } from '../store'
import { cssVar, seriesColor, themeVersion } from '../theme'

const props = defineProps<{ compact?: boolean }>() // compact: just the live graph + legend (used by the loop page)

type Mode = 'live' | 'history'
const mode = ref<Mode>('live')
const windowSec = ref(30)
const paused = ref(false)
const connected = ref(false)
const topo = ref<Topology | null>(null)
const error = ref<string | null>(null)
const box = ref<HTMLDivElement>()
const tip = ref<{ x: number; y: number; title: string; lines: string[] } | null>(null)

let cy: cytoscape.Core | null = null
let es: EventSource | null = null
let ro: ResizeObserver | null = null

const WINDOWS = [10, 30, 60, 300]
const winLabel = (s: number) => (s < 60 ? `${s} s` : `${s / 60} min`)
const edgeId = (e: TopoEdge) => `${e.source}->${e.target}`

// ---- data sources --------------------------------------------------------
function connect() {
  es?.close()
  es = openLive(windowSec.value, snap => {
    if (paused.value) return
    topo.value = snap.topology
    setNodes(snap.nodes)
    error.value = null
  }, ok => { connected.value = ok })
}

async function loadHistory() {
  try {
    topo.value = await getHistoryTopology(rangeSec.value)
    error.value = null
  } catch (e) {
    error.value = (e as Error).message
  }
}

function applyMode() {
  if (mode.value === 'live') { topo.value = null; connect() }
  else { es?.close(); es = null; connected.value = false; topo.value = null; loadHistory() }
}

watch(mode, applyMode)
watch(windowSec, () => { if (mode.value === 'live') connect() })
watch([rangeSec, refreshTick], () => { if (mode.value === 'history') loadHistory() })

// ---- graph ---------------------------------------------------------------
function nodeColor(n: TopoNode): string {
  if (n.kind === 'agent') return seriesColor(nodeSlot(n.id))
  return cssVar(n.kind === 'server' ? '--text-secondary' : '--text-muted')
}

function buildStyle(): any[] {
  const surface = cssVar('--surface-1'), primary = cssVar('--text-primary'), secondary = cssVar('--text-secondary')
  return [
    { selector: 'node', style: {
      'background-color': 'data(color)', shape: 'data(shape)', width: 'data(size)', height: 'data(size)',
      'border-width': 3, 'border-color': surface, 'border-style': 'data(borderStyle)', opacity: 'data(opacity)',
      label: 'data(label)', color: primary, 'font-size': 12, 'text-valign': 'bottom', 'text-margin-y': 6,
      'text-wrap': 'ellipsis', 'text-max-width': '140px',
    } },
    { selector: 'node[borderStyle = "dashed"]', style: { 'border-color': 'data(color)', 'border-width': 2 } },
    { selector: 'edge', style: {
      width: 'data(width)', 'line-color': 'data(color)', 'target-arrow-color': 'data(color)', 'target-arrow-shape': 'triangle',
      'arrow-scale': 0.8, 'curve-style': 'bezier', 'control-point-step-size': 90, opacity: 0.85,
      label: 'data(label)', 'font-size': 11, color: secondary, 'text-background-color': surface, 'text-background-opacity': 0.9,
      'text-background-padding': '2px', 'text-rotation': 'autorotate', 'text-margin-y': -2,
    } },
    { selector: 'edge:selected, edge.hover', style: { opacity: 1 } },
  ]
}

function render() {
  if (!cy || !topo.value) return
  const t = topo.value
  const edgeColor = cssVar('--edge')
  const wanted = new Set(t.nodes.map(n => n.id))
  const wantedEdges = new Set(t.edges.map(edgeId))
  // Label only the busiest edges so a busy graph stays readable.
  const labelled = new Set([...t.edges].sort((a, b) => b.bps - a.bps).slice(0, 8).map(edgeId))
  const firstDraw = cy.nodes().empty()
  let structural = false

  cy.batch(() => {
    for (const n of t.nodes) {
      const data = {
        id: n.id,
        label: n.label + (n.online ? '' : ' (offline)'),
        color: nodeColor(n),
        shape: n.kind === 'server' ? 'round-rectangle' : 'ellipse',
        size: n.kind === 'server' ? 54 : 46,
        borderStyle: n.kind === 'external' || !n.online ? 'dashed' : 'solid',
        opacity: n.online ? 1 : 0.5,
        kind: n.kind,
      }
      const ele = cy!.getElementById(n.id)
      if (ele.empty()) {
        cy!.add({ group: 'nodes', data, position: { x: 150 + Math.random() * 500, y: 100 + Math.random() * 300 } })
        structural = true
      } else ele.data(data)
    }
    cy!.nodes().filter(n => !wanted.has(n.id())).remove()

    for (const e of t.edges) {
      const id = edgeId(e)
      const data = {
        id, source: e.source, target: e.target, bps: e.bps, pps: e.pps, protos: e.protos.join(', '),
        width: edgeWidth(e.bps), color: edgeColor, label: labelled.has(id) ? formatRate(e.bps) : '',
      }
      const ele = cy!.getElementById(id)
      if (ele.empty()) cy!.add({ group: 'edges', data }); else ele.data(data)
    }
    cy!.edges().filter(e => !wantedEdges.has(e.id())).remove()
  })

  if (structural || firstDraw) {
    // Keep the user's arrangement when only data changes; re-layout when nodes come or go.
    // Small graphs get a deterministic ring (nothing overlaps); big ones a force layout.
    const rank = (n: cytoscape.NodeSingular) => (n.data('kind') === 'agent' ? 0 : n.data('kind') === 'server' ? 1 : 2)
    const layout = cy.nodes().length <= 12
      ? { name: 'circle', animate: false, fit: true, padding: 90, avoidOverlap: true, spacingFactor: 1.1,
          sort: (a: cytoscape.NodeSingular, b: cytoscape.NodeSingular) => rank(a) - rank(b) || a.id().localeCompare(b.id()) }
      : { name: 'cose', animate: false, randomize: firstDraw, fit: true, padding: 70,
          nodeRepulsion: () => 60000, idealEdgeLength: () => 220, numIter: 2000 }
    cy.layout(layout as any).run()
  }
}

watch(topo, render)
watch(themeVersion, () => { cy?.style(buildStyle() as any); render() })
watch(nodes, () => { if (topo.value) render() })

function showTip(evt: cytoscape.EventObject) {
  const el = evt.target
  const pos = evt.renderedPosition ?? el.renderedPosition()
  if (el.isEdge()) {
    const d = el.data()
    tip.value = {
      x: pos.x, y: pos.y,
      title: `${labelOf(d.source)} → ${labelOf(d.target)}`,
      lines: [formatRate(d.bps), `${formatNumber(d.pps)} pkt/s`, d.protos || ''],
    }
  } else {
    const d = el.data()
    const inBps = el.incomers('edge').reduce((a: number, e: any) => a + e.data('bps'), 0)
    const outBps = el.outgoers('edge').reduce((a: number, e: any) => a + e.data('bps'), 0)
    tip.value = { x: pos.x, y: pos.y, title: d.label, lines: [`in ${formatRate(inBps)}`, `out ${formatRate(outBps)}`, d.kind] }
  }
}

onMounted(() => {
  cy = cytoscape({
    container: box.value, elements: [], style: buildStyle() as any,
    minZoom: 0.3, maxZoom: 3, wheelSensitivity: 0.25,
  })
  cy.on('mouseover', 'node, edge', e => { e.target.addClass('hover'); showTip(e) })
  cy.on('mousemove', 'node, edge', e => { if (tip.value) { tip.value.x = e.renderedPosition.x; tip.value.y = e.renderedPosition.y } })
  cy.on('mouseout', 'node, edge', e => { e.target.removeClass('hover'); tip.value = null })
  cy.on('tap', evt => { if (evt.target === cy) tip.value = null })
  ro = new ResizeObserver(() => { cy?.resize() })
  if (box.value) ro.observe(box.value)
  applyMode()
})

onBeforeUnmount(() => { es?.close(); ro?.disconnect(); cy?.destroy(); cy = null })

// ---- derived UI ----------------------------------------------------------
const labelOf = (id: string) => topo.value?.nodes.find(n => n.id === id)?.label ?? id
const sortedEdges = computed(() => [...(topo.value?.edges ?? [])].sort((a, b) => b.bps - a.bps))
const empty = computed(() => !!topo.value && topo.value.edges.length === 0)
const noFlows = computed(() => nodes.value.filter(n => !n.flowsEnabled))
const legendRates = [1e3, 1e5, 1e7]
const maxRate = computed(() => Math.max(0, ...sortedEdges.value.map(e => e.bps)))
const modeNote = computed(() => {
  if (!topo.value) return ''
  const w = topo.value.windowSeconds
  const span = w >= 3600 ? `${(w / 3600).toFixed(w % 3600 ? 1 : 0)} h` : w >= 60 ? `${Math.round(w / 60)} min` : `${Math.round(w)} s`
  return mode.value === 'live' ? `rate averaged over the last ${span}` : `average rate over the selected range (${span})`
})
function fit() { cy?.fit(undefined, 70) }
</script>

<template>
  <div v-if="!compact" class="bar">
    <div class="seg" role="group" aria-label="Mode">
      <button class="btn" :aria-pressed="mode === 'live'" @click="mode = 'live'">Live</button>
      <button class="btn" :aria-pressed="mode === 'history'" @click="mode = 'history'">History</button>
    </div>
    <label v-if="mode === 'live'" class="field">Window
      <select v-model.number="windowSec">
        <option v-for="w in WINDOWS" :key="w" :value="w">{{ winLabel(w) }}</option>
      </select>
    </label>
    <button v-if="mode === 'live'" class="btn" :aria-pressed="paused" @click="paused = !paused">{{ paused ? '▶ Resume' : '⏸ Pause' }}</button>
    <button class="btn" @click="fit">Fit</button>
    <span class="grow"></span>
    <span v-if="mode === 'live'" class="muted state" :class="{ ok: connected }">{{ connected ? (paused ? '● connected (paused)' : '● live') : '○ reconnecting…' }}</span>
    <span class="muted note">{{ modeNote }}<template v-if="mode === 'history'"> · uses the range selected above</template></span>
  </div>

  <div class="card stage" :class="{ compact }">
    <div ref="box" class="graph" role="img" aria-label="Network topology graph; the same data is in the table below"></div>
    <div v-if="tip" class="tip" :style="{ left: tip.x + 14 + 'px', top: tip.y + 14 + 'px' }">
      <strong>{{ tip.title }}</strong>
      <div v-for="(l, i) in tip.lines" :key="i" class="num">{{ l }}</div>
    </div>
    <div v-if="empty || error || !topo" class="overlay muted">
      <template v-if="error">Error: {{ error }}</template>
      <template v-else-if="!topo">Loading…</template>
      <template v-else>
        No traffic between hosts seen {{ mode === 'live' ? 'in this window' : 'in this range' }}.
        <div v-if="noFlows.length" class="hint">Flow capture is off on: {{ noFlows.map(n => n.hostname).join(', ') }} (needs CAP_BPF / CAP_NET_ADMIN / CAP_PERFMON).</div>
      </template>
    </div>

    <div class="legend" aria-label="Legend">
      <div class="lg-group">
        <span class="lg-title">Traffic (width)</span>
        <span v-for="r in legendRates" :key="r" class="lg-item num">
          <svg width="34" height="26" aria-hidden="true"><line x1="2" y1="13" x2="32" y2="13" :stroke-width="edgeWidth(r)" stroke="var(--edge)" /></svg>{{ formatRate(r) }}
        </span>
      </div>
      <div class="lg-group">
        <span class="lg-title">Nodes</span>
        <span class="lg-item"><i class="dot" style="background: var(--series-1)"></i>agent (color = chart color)</span>
        <span class="lg-item"><i class="dot sq" style="background: var(--text-secondary)"></i>server</span>
        <span class="lg-item"><i class="dot dash"></i>external / offline</span>
      </div>
      <div class="lg-group muted">arrow = direction of traffic</div>
    </div>
  </div>

  <h2 v-if="!compact" class="section-title">Connections</h2>
  <div v-if="!compact" class="card tbl">
    <table class="num">
      <thead><tr><th>From</th><th>To</th><th class="r">Rate</th><th class="r">Packets/s</th><th>Protocols</th><th class="bar-col" aria-hidden="true"></th></tr></thead>
      <tbody>
        <tr v-for="e in sortedEdges" :key="edgeId(e)">
          <td>{{ labelOf(e.source) }}</td>
          <td>→ {{ labelOf(e.target) }}</td>
          <td class="r"><strong>{{ formatRate(e.bps) }}</strong></td>
          <td class="r">{{ formatNumber(e.pps) }}</td>
          <td>{{ e.protos.join(', ') }}</td>
          <td class="bar-col"><div class="share" :style="{ width: (maxRate ? (e.bps / maxRate) * 100 : 0) + '%' }"></div></td>
        </tr>
        <tr v-if="!sortedEdges.length"><td colspan="6" class="muted">No connections</td></tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.bar { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; margin-bottom: 12px; }
.seg { display: flex; gap: 4px; }
.field { display: flex; align-items: center; gap: 6px; color: var(--text-secondary); }
select { background: var(--surface-1); border: 1px solid var(--border); border-radius: 8px; padding: 5px 8px; }
.grow { flex: 1; }
.state.ok { color: var(--good-text); }
.note { font-size: 12px; }
.stage { position: relative; padding: 0; overflow: hidden; }
.graph { height: min(62vh, 620px); min-height: 380px; width: 100%; }
.compact .graph { height: max(380px, calc(100vh - 340px)); }
.tip { position: absolute; pointer-events: none; background: var(--surface-1); border: 1px solid var(--border); border-radius: 8px;
  padding: 8px 10px; font-size: 12px; box-shadow: 0 4px 14px rgb(0 0 0 / 15%); z-index: 5; max-width: 260px; }
.tip strong { display: block; margin-bottom: 2px; }
.overlay { position: absolute; inset: 0 0 56px; display: grid; place-content: center; text-align: center; pointer-events: none; padding: 24px; }
.hint { margin-top: 8px; font-size: 12px; }
.legend { display: flex; flex-wrap: wrap; gap: 8px 28px; align-items: center; padding: 10px 16px; border-top: 1px solid var(--border);
  font-size: 12px; color: var(--text-secondary); }
.lg-group { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; }
.lg-title { color: var(--text-muted); }
.lg-item { display: inline-flex; align-items: center; gap: 6px; }
.dot { width: 12px; height: 12px; border-radius: 50%; display: inline-block; }
.dot.sq { border-radius: 3px; }
.dot.dash { background: transparent; border: 2px dashed var(--text-muted); width: 12px; height: 12px; }
.tbl { padding: 4px 8px; overflow-x: auto; }
.bar-col { width: 22%; min-width: 80px; }
.share { height: 6px; border-radius: 3px; background: var(--edge); opacity: .8; }
</style>
