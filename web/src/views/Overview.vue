<script setup lang="ts">
import MetricChart from '../components/MetricChart.vue'
import NodeCard from '../components/NodeCard.vue'
import { nodeName, nodeSlot, nodes } from '../store'

const mountNamer = (l: Record<string, string>) => ({
  key: `${l.node}|${l.mount}`, name: `${nodeName(l.node)} ${l.mount}`, slot: nodeSlot(l.node),
})
</script>

<template>
  <h2 class="section-title first">Nodes</h2>
  <div v-if="nodes.length" class="tiles">
    <NodeCard v-for="n in nodes" :key="n.id" :node="n" />
  </div>
  <div v-else class="card muted">
    No agents have reported yet. Start one with
    <code>simplemonitor agent --server &lt;server&gt;:7070</code>.
  </div>

  <h2 class="section-title">All nodes</h2>
  <div class="grid-2">
    <MetricChart title="CPU usage" metric="cpu" node="all" unit="percent" :y-max="100" />
    <MetricChart title="Memory usage" metric="mem_pct" node="all" unit="percent" :y-max="100" />
    <MetricChart title="Disk usage" subtitle="used / total per filesystem" metric="disk_pct" node="all" unit="percent" :y-max="100" :namer="mountNamer" />
    <MetricChart title="Load average (1 min)" metric="load" node="all" unit="number"
      :filter="l => l.period === '1'" />
    <MetricChart title="Network receive" metric="net_bps" node="all" unit="bps" :filter="l => l.dir === 'rx'" />
    <MetricChart title="Network transmit" metric="net_bps" node="all" unit="bps" :filter="l => l.dir === 'tx'" />
  </div>
</template>

<style scoped>
.first { margin-top: 4px; }
.tiles { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 16px; }
code { background: var(--hover); padding: 1px 6px; border-radius: 4px; }
</style>
