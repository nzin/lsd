export interface Latest {
  at: number; cpu: number; load1: number; memUsed: number; memTotal: number
  diskMaxPct: number; rxBps: number; txBps: number
}
export interface NodeView {
  id: string; hostname: string; ips: string[]; os: string; kernel: string; version: string
  flowsEnabled: boolean; firstSeen: number; lastSeen: number; intervalMs: number
  online: boolean; latest?: Latest
  role?: 'agent' | 'server' // 'server' = the machine the server itself runs on; absent = agent
}
export interface Series { labels: Record<string, string>; points: [number, number][] }
export interface MetricsResponse { metric: string; node: string; from: number; to: number; stepSeconds: number; series: Series[] }
export interface TopoNode { id: string; label: string; kind: 'agent' | 'server' | 'external'; online: boolean; private: boolean }
export interface TopoEdge { source: string; target: string; bps: number; pps: number; protos: string[] }
export interface Topology { mode: 'live' | 'history'; windowSeconds: number; generatedAt: number; nodes: TopoNode[]; edges: TopoEdge[] }

async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const r = await fetch(url, { signal })
  if (!r.ok) {
    let msg = r.statusText
    try { msg = (await r.json()).error ?? msg } catch { /* not json */ }
    throw new Error(msg)
  }
  return r.json() as Promise<T>
}

export const getNodes = (signal?: AbortSignal) => getJSON<NodeView[]>('/api/nodes', signal)

export function getMetrics(metric: string, node: string, rangeSec: number, signal?: AbortSignal) {
  const q = new URLSearchParams({ metric, node, range: `${rangeSec}s` })
  return getJSON<MetricsResponse>(`/api/metrics?${q}`, signal)
}

export function getHistoryTopology(rangeSec: number, signal?: AbortSignal) {
  return getJSON<Topology>(`/api/topology?range=${rangeSec}s`, signal)
}

export function openLive(windowSec: number, onSnapshot: (s: { topology: Topology; nodes: NodeView[] }) => void,
  onState: (connected: boolean) => void): EventSource {
  const es = new EventSource(`/api/live?window=${windowSec}s`)
  es.addEventListener('snapshot', ev => onSnapshot(JSON.parse((ev as MessageEvent).data)))
  es.onopen = () => onState(true)
  es.onerror = () => onState(false) // EventSource retries on its own
  return es
}
