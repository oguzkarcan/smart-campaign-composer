import { createRouter, createWebHistory } from 'vue-router'
import CampaignsView from './views/CampaignsView.vue'
import CampaignView from './views/CampaignView.vue'
import NewCampaignView from './views/NewCampaignView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'campaigns', component: CampaignsView },
    { path: '/new', name: 'new', component: NewCampaignView },
    {
      path: '/campaigns/:id(\\d+)',
      name: 'campaign',
      component: CampaignView,
      props: (route) => ({ id: Number(route.params.id) }),
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
