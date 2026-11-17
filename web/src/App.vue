<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { autoRefresh, nodes, nodesError, range, RANGES, refreshTick, setAutoRefresh, setRange, startPolling } from './store'
import { applyTheme, themeChoice, type ThemeChoice } from './theme'

startPolling()
const route = useRoute()
const router = useRouter()

const selectedNode = computed(() => (route.path.startsWith('/nodes/') ? String(route.params.id) : 'all'))
function onNode(e: Event) {
  const v = (e.target as HTMLSelectElement).value
  router.push(v === 'all' ? '/' : `/nodes/${encodeURIComponent(v)}`)
}
const online = computed(() => nodes.value.filter(n => n.online).length)
const themes: ThemeChoice[] = ['auto', 'light', 'dark']
function nextTheme() { applyTheme(themes[(themes.indexOf(themeChoice.value) + 1) % themes.length]) }
</script>

<template>
  <header class="top">
    <div class="page bar">
      <router-link to="/" class="brand">
        <svg width="18" height="18" viewBox="0 0 16 16" aria-hidden="true"><path d="M1 12l4-5 3 3 4-7 3 5" fill="none" stroke="var(--series-1)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        simplemonitor
      </router-link>
      <nav aria-label="Main">
        <router-link to="/" :class="{ active: route.path === '/' || route.path.startsWith('/nodes/') }">Metrics</router-link>
        <router-link to="/topology" :class="{ active: route.path === '/topology' }">Topology</router-link>
      </nav>
      <span class="grow"></span>
      <span class="muted num" :title="nodesError ?? ''">
        <template v-if="nodesError">⚠ API unreachable</template>
        <template v-else>{{ online }}/{{ nodes.length }} nodes online</template>
      </span>
    </div>
  </header>

  <div class="page">
    <div class="filters" role="group" aria-label="Filters">
      <div class="seg" role="group" aria-label="Time range">
        <button v-for="r in RANGES" :key="r.key" class="btn" :aria-pressed="range === r.key" @click="setRange(r.key)">{{ r.label }}</button>
      </div>
      <label class="field">Node
        <select :value="selectedNode" @change="onNode">
          <option value="all">All nodes</option>
          <option v-for="n in nodes" :key="n.id" :value="n.id">{{ n.hostname }}</option>
        </select>
      </label>
      <span class="grow"></span>
      <label class="field"><input type="checkbox" :checked="autoRefresh" @change="setAutoRefresh(($event.target as HTMLInputElement).checked)" /> Auto-refresh</label>
      <button class="btn" @click="refreshTick++">Refresh</button>
      <button class="btn" :title="'Theme: ' + themeChoice" @click="nextTheme">Theme: {{ themeChoice }}</button>
    </div>
    <router-view />
  </div>
</template>

<style scoped>
.top { background: var(--surface-1); border-bottom: 1px solid var(--border); }
.bar { display: flex; align-items: center; flex-wrap: wrap; gap: 4px 24px; min-height: 52px; padding-top: 0; padding-bottom: 0; } /* .page adds 48px bottom padding: not wanted here */
.brand { display: flex; align-items: center; gap: 8px; font-weight: 700; text-decoration: none; font-size: 16px; }
nav { display: flex; gap: 4px; }
.bar > .muted { white-space: nowrap; }
nav a { text-decoration: none; padding: 6px 12px; border-radius: 8px; color: var(--text-secondary); }
nav a:hover { background: var(--hover); }
nav a.active { color: var(--text-primary); background: var(--hover); font-weight: 600; }
.grow { flex: 1; }
.filters { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; padding: 16px 0; }
.seg { display: flex; flex-wrap: wrap; gap: 4px; }
.field { display: flex; align-items: center; gap: 6px; color: var(--text-secondary); }
select { background: var(--surface-1); border: 1px solid var(--border); border-radius: 8px; padding: 5px 8px; }
</style>
