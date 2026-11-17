const UNITS = ['B', 'KB', 'MB', 'GB', 'TB']

/** 1024-based, one decimal under 10: 1.5 KB, 12 MB */
export function formatBytes(n: number): string {
  if (!isFinite(n)) return '–'
  const neg = n < 0
  let v = Math.abs(n)
  let i = 0
  while (v >= 1024 && i < UNITS.length - 1) { v /= 1024; i++ }
  const s = v >= 100 || i === 0 ? v.toFixed(0) : v >= 10 ? v.toFixed(0) : v.toFixed(1)
  return (neg ? '-' : '') + s + ' ' + UNITS[i]
}

export const formatRate = (bps: number) => formatBytes(bps) + '/s'

export function formatPct(v: number): string {
  if (!isFinite(v)) return '–'
  return (v >= 10 ? v.toFixed(0) : v.toFixed(1)) + '%'
}

export function formatNumber(v: number): string {
  if (!isFinite(v)) return '–'
  if (Math.abs(v) >= 1e6) return (v / 1e6).toFixed(1) + 'M'
  if (Math.abs(v) >= 1e4) return (v / 1e3).toFixed(0) + 'K'
  if (Math.abs(v) >= 100) return v.toFixed(0)
  return v.toFixed(2).replace(/\.?0+$/, '') || '0'
}

export type Unit = 'percent' | 'bytes' | 'bps' | 'pps' | 'number'

export function formatUnit(v: number, unit: Unit): string {
  switch (unit) {
    case 'percent': return formatPct(v)
    case 'bytes': return formatBytes(v)
    case 'bps': return formatRate(v)
    case 'pps': return formatNumber(v) + ' pkt/s'
    default: return formatNumber(v)
  }
}

/** Compact axis labels: 0 / 10K / 20K, 1 MB/s ... */
export function formatAxis(v: number, unit: Unit): string {
  switch (unit) {
    case 'percent': return Math.round(v) + '%'
    case 'bytes': return formatBytes(v)
    case 'bps': return formatBytes(v) + '/s'
    default: return formatNumber(v)
  }
}

const MIN_BPS = 1e3  // <= 1 KB/s  -> 1px
const MAX_BPS = 1e8  // >= ~100 MB/s -> 24px
export const MIN_EDGE_PX = 1
export const MAX_EDGE_PX = 24

/** Log-scaled edge width in px for a byte rate (the topology width legend uses the same fn). */
export function edgeWidth(bps: number): number {
  if (!(bps > 0)) return MIN_EDGE_PX
  const t = Math.log10(1 + bps / MIN_BPS) / Math.log10(1 + MAX_BPS / MIN_BPS)
  return MIN_EDGE_PX + (MAX_EDGE_PX - MIN_EDGE_PX) * Math.min(1, Math.max(0, t))
}

export function formatTime(ms: number, withDate = false): string {
  const d = new Date(ms)
  const t = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
  return withDate ? d.toLocaleDateString([], { month: 'short', day: 'numeric' }) + ' ' + t : t
}

export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c] as string))
}
