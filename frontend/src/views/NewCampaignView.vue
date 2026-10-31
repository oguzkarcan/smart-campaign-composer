<script setup lang="ts">
import NewCampaignForm from '@/components/NewCampaignForm.vue'
import { t } from '@/lib/i18n'
</script>

<template>
  <section class="space-y-6">
    <RouterLink to="/" class="text-sm text-indigo-600 hover:underline">{{ t('nav.back') }}</RouterLink>
    <div class="space-y-2">
      <h1 class="text-2xl font-semibold tracking-tight">{{ t('new.title') }}</h1>
      <p class="text-slate-600">{{ t('new.intro') }}</p>
    </div>
    <NewCampaignForm />
  </section>
</template>
