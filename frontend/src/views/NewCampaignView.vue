<script setup lang="ts">
import NewCampaignForm from '@/components/NewCampaignForm.vue'
import { t } from '@/lib/i18n'
</script>

<template>
  <section class="mx-auto max-w-4xl space-y-6">
    <RouterLink to="/" class="inline-flex text-sm font-medium text-slate-500 hover:text-indigo-600">{{ t('nav.back') }}</RouterLink>
    <div class="space-y-2">
      <p class="text-xs font-bold tracking-[0.14em] text-indigo-600 uppercase">{{ t('app.title') }}</p>
      <h1 class="text-3xl font-bold tracking-tight text-slate-900">{{ t('new.title') }}</h1>
      <p class="max-w-2xl text-slate-600">{{ t('new.intro') }}</p>
    </div>
    <NewCampaignForm />
  </section>
</template>
