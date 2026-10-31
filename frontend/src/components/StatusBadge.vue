<script setup lang="ts">
import type { CampaignStatus } from '@/api/types'
import { statusLabels } from '@/lib/format'

defineProps<{ status: CampaignStatus }>()

const colors: Record<CampaignStatus, string> = {
  draft: 'bg-slate-100 text-slate-700',
  audience_ready: 'bg-sky-100 text-sky-800',
  content_ready: 'bg-indigo-100 text-indigo-800',
  approved: 'bg-violet-100 text-violet-800',
  sending: 'bg-amber-100 text-amber-800',
  completed: 'bg-emerald-100 text-emerald-800',
  failed: 'bg-rose-100 text-rose-800',
}
</script>

<template>
  <span class="inline-flex rounded-full px-2.5 py-0.5 text-xs font-medium" :class="colors[status]">
    {{ statusLabels[status] }}
  </span>
</template>
