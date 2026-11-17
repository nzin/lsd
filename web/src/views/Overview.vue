<script setup lang="ts">
import MetricChart from '../components/MetricChart.vue'
import NodesHeader from '../components/NodesHeader.vue'
import { nodeName, nodeSlot, nodes } from '../store'

const mountNamer = (l: Record<string, string>) => ({
  key: `${l.node}|${l.mount}`, name: `${nodeName(l.node)} ${l.mount}`, slot: nodeSlot(l.node),
})
</script>

<template>
  <NodesHeader />

  <div v-if="!nodes.length" class="card muted empty">
    No agents have reported yet. Start one with
    <code>lsd agent --server &lt;server&gt;:7070</code>.
  </div>

  <div class="grid-2">
    <MetricChart title="CPU usage" metric="cpu" node="all" unit="percent" :y-max="100" />
    <MetricChart title="Memory usage" metric="mem_pct" node="all" unit="percent" :y-max="100" />
    <MetricChart title="Disk usage" subtitle="used / total per filesystem" metric="disk_pct" node="all" unit="percent" :y-max="100" :namer="mountNamer" />
    <MetricChart title="Disk IOPS" subtitle="read + write operations per second" metric="disk_iops" node="all" unit="iops" />
    <MetricChart title="Swap usage" subtitle="used / total swap" metric="swap_pct" node="all" unit="percent" :y-max="100" />
    <MetricChart title="Load average (1 min)" metric="load" node="all" unit="number"
      :filter="l => l.period === '1'" />
    <MetricChart title="Network receive" metric="net_bps" node="all" unit="bps" :filter="l => l.dir === 'rx'" />
    <MetricChart title="Network transmit" metric="net_bps" node="all" unit="bps" :filter="l => l.dir === 'tx'" />
  </div>
</template>

<style scoped>
.empty { margin-bottom: 16px; }
code { background: var(--hover); padding: 1px 6px; border-radius: 4px; }
</style>
