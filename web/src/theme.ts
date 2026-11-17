import { ref } from 'vue'

/** Bumped whenever the effective theme changes so charts can recompute colors. */
export const themeVersion = ref(0)
export type ThemeChoice = 'auto' | 'light' | 'dark'
export const themeChoice = ref<ThemeChoice>('auto')

export function cssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

/** Categorical slot -> color. Slots past 8 fall back to muted gray (never a generated hue). */
export function seriesColor(slot: number): string {
  return slot >= 0 && slot < 8 ? cssVar(`--series-${slot + 1}`) : cssVar('--text-muted')
}

export function applyTheme(choice: ThemeChoice) {
  themeChoice.value = choice
  if (choice === 'auto') document.documentElement.removeAttribute('data-theme')
  else document.documentElement.setAttribute('data-theme', choice)
  try { localStorage.setItem('sm.theme', choice) } catch { /* storage unavailable */ }
  themeVersion.value++
}

export function initTheme() {
  let saved: string | null = null
  try { saved = localStorage.getItem('sm.theme') } catch { /* storage unavailable */ }
  applyTheme(saved === 'light' || saved === 'dark' ? saved : 'auto')
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => themeVersion.value++)
}
