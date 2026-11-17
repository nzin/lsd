<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { nodes } from '../store'

// "Nodes" section heading with a selector: all nodes, or one node (its own page, so it's linkable).
const route = useRoute()
const router = useRouter()
const selected = computed(() => (route.path.startsWith('/nodes/') ? String(route.params.id) : 'all'))

function onChange(e: Event) {
  const v = (e.target as HTMLSelectElement).value
  router.push(v === 'all' ? '/' : `/nodes/${encodeURIComponent(v)}`)
}
</script>

<template>
  <div class="head">
    <h2>Nodes</h2>
    <select :value="selected" aria-label="Select nodes to show" @change="onChange">
      <option value="all">All nodes</option>
      <option v-for="n in nodes" :key="n.id" :value="n.id">{{ n.hostname }}{{ n.online ? '' : ' (offline)' }}</option>
      <!-- keep the current choice visible even if the node list has not loaded / the id is unknown -->
      <option v-if="selected !== 'all' && !nodes.some(n => n.id === selected)" :value="selected">{{ selected }}</option>
    </select>
  </div>
</template>

<style scoped>
.head { display: flex; align-items: center; gap: 12px; margin: 4px 0 16px; }
h2 { font-size: 18px; }
select { background: var(--surface-1); border: 1px solid var(--border); border-radius: 8px; padding: 5px 8px; min-width: 160px; }
</style>
