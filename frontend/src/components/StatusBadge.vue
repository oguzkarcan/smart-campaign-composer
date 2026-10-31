<script setup lang="ts">
import type { CampaignStatus } from '@/api/types'
import { statusLabel } from '@/lib/format'

defineProps<{ status: CampaignStatus }>()

const colors: Record<CampaignStatus, string> = {
  draft: 'bg-slate-100 text-slate-700',
  audience_ready: 'bg-sky-100 text-sky-800',
  content_ready: 'bg-indigo-100 text-indigo-800',
  approved: 'bg-violet-100 text-violet-800',
  scheduled: 'bg-cyan-100 text-cyan-800',
  sending: 'bg-amber-100 text-amber-800',
  completed: 'bg-emerald-100 text-emerald-800',
  failed: 'bg-rose-100 text-rose-800',
}
</script>

<template>
  <span
    class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-semibold ring-1 ring-current/15 ring-inset"
    :class="colors[status]"
  >
    <span class="size-1.5 rounded-full bg-current" :class="{ 'animate-pulse': status === 'sending' }" aria-hidden="true" />
    {{ statusLabel(status) }}
  </span>
</template>
