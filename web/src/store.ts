import { computed, ref } from 'vue'
import { getNodes, type NodeView } from './api'

export interface RangeDef { key: string; label: string; sec: number }
export const RANGES: RangeDef[] = [
  { key: '15m', label: '15 min', sec: 900 },
  { key: '1h', label: '1 hour', sec: 3600 },
  { key: '6h', label: '6 hours', sec: 6 * 3600 },
  { key: '24h', label: '24 hours', sec: 86400 },
  { key: '7d', label: '7 days', sec: 7 * 86400 },
  { key: '30d', label: '30 days', sec: 30 * 86400 },
]

function load(key: string, def: string): string {
  try { return localStorage.getItem(key) ?? def } catch { return def }
}
function save(key: string, v: string) {
  try { localStorage.setItem(key, v) } catch { /* storage unavailable */ }
}

const savedRange = load('lsd.range', '1h')
export const range = ref(RANGES.some(r => r.key === savedRange) ? savedRange : '1h')
export const rangeSec = computed(() => RANGES.find(r => r.key === range.value)!.sec)
export function setRange(k: string) { range.value = k; save('lsd.range', k) }

export const autoRefresh = ref(load('lsd.auto', '1') === '1')
export function setAutoRefresh(v: boolean) { autoRefresh.value = v; save('lsd.auto', v ? '1' : '0') }

/** Incremented on the refresh cadence; charts watch it to refetch. */
export const refreshTick = ref(0)

export const nodes = ref<NodeView[]>([])
export const nodesError = ref<string | null>(null)

export async function refreshNodes() {
  try {
    const list = await getNodes()
    // Stable order = first seen: a node keeps its color slot when others come and go.
    list.sort((a, b) => a.firstSeen - b.firstSeen || a.id.localeCompare(b.id))
    nodes.value = list
    nodesError.value = null
  } catch (e) {
    nodesError.value = (e as Error).message
  }
}

export function setNodes(list: NodeView[]) {
  list.sort((a, b) => a.firstSeen - b.firstSeen || a.id.localeCompare(b.id))
  nodes.value = list
  nodesError.value = null
}

/** Color slot of a node (by first-seen order). -1 = unknown node. */
export function nodeSlot(id: string): number {
  return nodes.value.findIndex(n => n.id === id)
}
export function nodeName(id: string): string {
  return nodes.value.find(n => n.id === id)?.hostname ?? id
}

let started = false
export function startPolling() {
  if (started) return
  started = true
  refreshNodes()
  setInterval(refreshNodes, 5000)
  setInterval(() => { if (autoRefresh.value && !document.hidden) refreshTick.value++ }, 10000)
}
