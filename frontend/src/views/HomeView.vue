<script setup lang="ts">
import NewCampaignForm from '@/components/NewCampaignForm.vue'
</script>

<template>
  <section class="space-y-6">
    <div class="space-y-2">
      <h1 class="text-2xl font-semibold tracking-tight">New campaign</h1>
      <p class="text-slate-600">
        Describe a campaign in plain language. Claude proposes the audience and the messages, and you approve them
        before anything is sent.
      </p>
    </div>
    <NewCampaignForm />
  </section>
</template>
