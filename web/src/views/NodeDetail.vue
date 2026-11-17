<script setup lang="ts">
import { computed } from 'vue'
import MetricChart from '../components/MetricChart.vue'
import NodesHeader from '../components/NodesHeader.vue'
import { formatTime } from '../format'
import { nodes } from '../store'

const props = defineProps<{ id: string }>()
const node = computed(() => nodes.value.find(n => n.id === props.id))

const AUTO = -2
const modes: Record<string, [string, number]> = { user: ['User', 0], system: ['System', 1], iowait: ['I/O wait', 2] }
const modeNamer = (l: Record<string, string>) => ({ key: l.mode, name: modes[l.mode]?.[0] ?? l.mode, slot: modes[l.mode]?.[1] ?? AUTO })
const loadNamer = (l: Record<string, string>) => ({ key: l.period, name: `${l.period} min`, slot: ['1', '5', '15'].indexOf(l.period) })
const memKinds: Record<string, [string, number]> = { used: ['Used', 0], cached: ['Cached', 1], swap_used: ['Swap used', 2] }
const memNamer = (l: Record<string, string>) => ({ key: l.kind, name: memKinds[l.kind]?.[0] ?? l.kind, slot: memKinds[l.kind]?.[1] ?? AUTO })
const mountNamer = (l: Record<string, string>) => ({ key: l.mount, name: l.mount, slot: AUTO })
const ioNamer = (l: Record<string, string>) => ({ key: `${l.mount}|${l.op}`, name: `${l.mount} ${l.op}`, slot: AUTO })
const ifaceNamer = (l: Record<string, string>) => ({
  key: `${l.iface}|${l.dir}`, name: `${l.iface} ${l.dir === 'rx' ? 'receive' : 'transmit'}`, slot: AUTO,
})
</script>

<template>
  <NodesHeader />
  <div v-if="!node" class="card muted">Unknown node “{{ id }}”. <router-link to="/">Back to overview</router-link></div>
  <template v-else>
    <div class="card info">
      <div class="title">
        <h2>{{ node.hostname }}</h2>
        <span :class="node.online ? 'on' : 'off'">{{ node.online ? '● online' : '○ offline' }}</span>
      </div>
      <dl class="num">
        <div><dt>Node id</dt><dd>{{ node.id }}</dd></div>
        <div><dt>Addresses</dt><dd>{{ node.ips.join(', ') || '–' }}</dd></div>
        <div><dt>OS</dt><dd>{{ node.os || '–' }}</dd></div>
        <div><dt>Kernel</dt><dd>{{ node.kernel || '–' }}</dd></div>
        <div><dt>Agent</dt><dd>{{ node.version || '–' }}</dd></div>
        <div><dt>eBPF flows</dt><dd>{{ node.flowsEnabled ? 'enabled' : 'disabled' }}</dd></div>
        <div><dt>Last seen</dt><dd>{{ formatTime(node.lastSeen, true) }}</dd></div>
      </dl>
    </div>

    <h2 class="section-title">CPU &amp; memory</h2>
    <div class="grid-2">
      <MetricChart title="CPU usage by mode" :node="id" metric="cpu_modes" unit="percent" :y-max="100" stacked :namer="modeNamer" />
      <MetricChart title="Load average" :node="id" metric="load" unit="number" :namer="loadNamer" />
      <MetricChart title="Memory" subtitle="used, page cache, swap" :node="id" metric="mem_bytes" unit="bytes" :namer="memNamer" />
      <MetricChart title="Memory usage" :node="id" metric="mem_pct" unit="percent" :y-max="100" area
        :namer="() => ({ key: 'mem', name: 'Memory used', slot: 0 })" />
      <MetricChart title="Swap usage" subtitle="used / total swap" :node="id" metric="swap_pct" unit="percent" :y-max="100" area
        :namer="() => ({ key: 'swap', name: 'Swap used', slot: 1 })" />
    </div>

    <h2 class="section-title">Disk</h2>
    <div class="grid-2">
      <MetricChart title="Disk usage" :node="id" metric="disk_pct" unit="percent" :y-max="100" :namer="mountNamer" />
      <MetricChart title="Disk I/O" subtitle="throughput per filesystem" :node="id" metric="disk_io" unit="bps" :namer="ioNamer" />
    </div>

    <h2 class="section-title">Network</h2>
    <div class="grid-2">
      <MetricChart title="Network throughput" :node="id" metric="net_bps" unit="bps" :namer="ifaceNamer" />
      <MetricChart title="Packet rate" :node="id" metric="net_pps" unit="pps" :namer="ifaceNamer" />
    </div>
  </template>
</template>

<style scoped>
.title { display: flex; align-items: baseline; gap: 12px; margin-bottom: 8px; }
.on { color: var(--good-text); font-size: 12px; }
.off { color: var(--text-muted); font-size: 12px; }
dl { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 8px 24px; margin: 0; }
dt { font-size: 12px; color: var(--text-muted); }
dd { margin: 0; overflow-wrap: anywhere; }
</style>
