import { describe, expect, it } from 'vitest'
import { edgeWidth, escapeHtml, formatBytes, formatPct, formatRate, formatUnit, MAX_EDGE_PX, MIN_EDGE_PX } from './format'

describe('edgeWidth', () => {
  it('is bounded by the min/max pixel width', () => {
    expect(edgeWidth(0)).toBe(MIN_EDGE_PX)
    expect(edgeWidth(-5)).toBe(MIN_EDGE_PX)
    expect(edgeWidth(10)).toBeCloseTo(MIN_EDGE_PX, 0)
    expect(edgeWidth(1e12)).toBe(MAX_EDGE_PX)
  })
  it('grows monotonically with traffic volume', () => {
    const rates = [0, 1e2, 1e3, 1e4, 4e5, 4e6, 4e7, 4e8]
    const w = rates.map(edgeWidth)
    for (let i = 1; i < w.length; i++) expect(w[i]).toBeGreaterThanOrEqual(w[i - 1])
  })
  it('keeps a 10x volume difference visibly apart', () => {
    expect(edgeWidth(4e6) - edgeWidth(4e5)).toBeGreaterThan(3)
  })
})

describe('formatters', () => {
  it('formats bytes and rates', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(4 * 1024 * 1024)).toBe('4.0 MB')
    expect(formatRate(2048)).toBe('2.0 KB/s')
    expect(formatBytes(NaN)).toBe('–')
  })
  it('formats percentages', () => {
    expect(formatPct(5.234)).toBe('5.2%')
    expect(formatPct(42.7)).toBe('43%')
  })
  it('formats IOPS', () => {
    expect(formatUnit(0, 'iops')).toBe('0 IOPS')
    expect(formatUnit(42, 'iops')).toBe('42 IOPS')
    expect(formatUnit(12500, 'iops')).toBe('13K IOPS')
  })
  it('escapes html in labels', () => {
    expect(escapeHtml('<img src=x onerror=1>&"')).toBe('&lt;img src=x onerror=1&gt;&amp;&quot;')
  })
})
