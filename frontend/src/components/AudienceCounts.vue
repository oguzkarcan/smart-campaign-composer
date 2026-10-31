<script setup lang="ts">
import type { Audience, Channel } from '@/api/types'
import { channelLabels, formatNumber } from '@/lib/format'

defineProps<{ audience: Audience; channels: Channel[] }>()
</script>

<template>
  <div>
    <dl class="grid grid-cols-1 gap-3 sm:grid-cols-3">
      <div class="rounded-lg border border-slate-200 bg-white p-4">
        <dt class="text-sm text-slate-500">Matching users</dt>
        <dd class="mt-1 text-2xl font-semibold tabular-nums">{{ formatNumber(audience.matched) }}</dd>
      </div>
      <div v-for="ch in channels" :key="ch" class="rounded-lg border border-slate-200 bg-white p-4">
        <dt class="text-sm text-slate-500">{{ channelLabels[ch] }} recipients</dt>
        <dd class="mt-1 text-2xl font-semibold tabular-nums" :class="{ 'text-rose-600': audience[ch] === 0 }">
          {{ formatNumber(audience[ch]) }}
        </dd>
        <p class="mt-1 text-xs text-slate-500">
          <template v-if="audience[ch] === 0">Nobody in this audience opted in to {{ channelLabels[ch] }}.</template>
          <template v-else>Matching users who opted in to {{ channelLabels[ch] }}</template>
        </p>
      </div>
    </dl>
  </div>
</template>
