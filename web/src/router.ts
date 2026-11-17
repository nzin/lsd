import { createRouter, createWebHistory } from 'vue-router'
import Overview from './views/Overview.vue'
import NodeDetail from './views/NodeDetail.vue'
import Topology from './views/Topology.vue'
import Loop from './views/Loop.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: Overview },
    { path: '/nodes/:id', component: NodeDetail, props: true },
    { path: '/topology', component: Topology },
    { path: '/loop', component: Loop },
    { path: '/:rest(.*)*', redirect: '/' },
  ],
})
