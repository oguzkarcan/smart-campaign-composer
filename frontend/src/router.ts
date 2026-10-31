import { createRouter, createWebHistory } from 'vue-router'
import CampaignView from './views/CampaignView.vue'
import HomeView from './views/HomeView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: HomeView },
    {
      path: '/campaigns/:id(\\d+)',
      name: 'campaign',
      component: CampaignView,
      props: (route) => ({ id: Number(route.params.id) }),
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
