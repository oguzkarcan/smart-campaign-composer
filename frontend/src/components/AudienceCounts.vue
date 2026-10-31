<script setup lang="ts">
import { computed } from 'vue'
import type { Audience, Channel } from '@/api/types'
import { LANGUAGES } from '@/api/types'
import { channelLabels, formatNumber, languageLabels } from '@/lib/format'

const props = defineProps<{ audience: Audience; channels: Channel[] }>()

// Readers per language; each gets the messages written in their language.
const languages = computed(() =>
  LANGUAGES.filter((l) => props.audience.languages?.[l]).map((l) => ({ language: l, counts: props.audience.languages![l]! })),
)
</script>

<template>
  <div class="space-y-3">
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

    <div v-if="languages.length" class="overflow-x-auto rounded-lg border border-slate-200 bg-white">
      <table class="w-full text-sm">
        <caption class="px-4 pt-3 text-left text-sm text-slate-500">
          By language: each recipient gets the message written in their language.
        </caption>
        <thead class="text-left text-xs text-slate-500">
          <tr>
            <th class="px-4 py-2 font-medium">Language</th>
            <th class="px-4 py-2 text-right font-medium">Matching</th>
            <th v-for="ch in channels" :key="ch" class="px-4 py-2 text-right font-medium">{{ channelLabels[ch] }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100 tabular-nums">
          <tr v-for="row in languages" :key="row.language">
            <th scope="row" class="px-4 py-2 text-left font-medium">{{ languageLabels[row.language] }}</th>
            <td class="px-4 py-2 text-right">{{ formatNumber(row.counts.matched) }}</td>
            <td v-for="ch in channels" :key="ch" class="px-4 py-2 text-right">{{ formatNumber(row.counts[ch]) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
