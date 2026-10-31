import { createRouter, createWebHistory } from 'vue-router'
import CampaignsView from './views/CampaignsView.vue'
import CampaignView from './views/CampaignView.vue'
import NewCampaignView from './views/NewCampaignView.vue'

const APP = 'Smart Campaign Composer'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'campaigns', component: CampaignsView, meta: { title: 'Campaigns' } },
    { path: '/new', name: 'new', component: NewCampaignView, meta: { title: 'New campaign' } },
    {
      path: '/campaigns/:id(\\d+)',
      name: 'campaign',
      component: CampaignView,
      props: (route) => ({ id: Number(route.params.id) }),
      meta: { title: (id: string) => `Campaign #${id}` },
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior: (_to, _from, saved) => saved ?? { top: 0 },
})

router.afterEach((to) => {
  const t = to.meta.title
  const page = typeof t === 'function' ? t(String(to.params.id)) : t
  document.title = page ? `${page} · ${APP}` : APP
})

declare module 'vue-router' {
  interface RouteMeta {
    title?: string | ((id: string) => string)
  }
}
