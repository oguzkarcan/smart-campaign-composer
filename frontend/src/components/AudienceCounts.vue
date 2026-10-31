<script setup lang="ts">
import { computed } from 'vue'
import type { Audience, Channel } from '@/api/types'
import { LANGUAGES } from '@/api/types'
import { channelLabel, formatNumber, languageLabel } from '@/lib/format'
import { t } from '@/lib/i18n'

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
        <dt class="text-sm text-slate-500">{{ t('audience.matching') }}</dt>
        <dd class="mt-1 text-2xl font-semibold tabular-nums">{{ formatNumber(audience.matched) }}</dd>
      </div>
      <div v-for="ch in channels" :key="ch" class="rounded-lg border border-slate-200 bg-white p-4">
        <dt class="text-sm text-slate-500">{{ t('audience.recipients', { channel: channelLabel(ch) }) }}</dt>
        <dd class="mt-1 text-2xl font-semibold tabular-nums" :class="{ 'text-rose-600': audience[ch] === 0 }">
          {{ formatNumber(audience[ch]) }}
        </dd>
        <p class="mt-1 text-xs text-slate-500">
          <template v-if="audience[ch] === 0">{{ t('audience.nobodyOptedIn', { channel: channelLabel(ch) }) }}</template>
          <template v-else>{{ t('audience.optedIn', { channel: channelLabel(ch) }) }}</template>
        </p>
      </div>
    </dl>

    <div v-if="languages.length" class="overflow-x-auto rounded-lg border border-slate-200 bg-white">
      <table class="w-full text-sm">
        <caption class="px-4 pt-3 text-left text-sm text-slate-500">
          {{ t('audience.byLanguage') }}
        </caption>
        <thead class="text-left text-xs text-slate-500">
          <tr>
            <th class="px-4 py-2 font-medium">{{ t('audience.language') }}</th>
            <th class="px-4 py-2 text-right font-medium">{{ t('audience.matching') }}</th>
            <th v-for="ch in channels" :key="ch" class="px-4 py-2 text-right font-medium">{{ channelLabel(ch) }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100 tabular-nums">
          <tr v-for="row in languages" :key="row.language">
            <th scope="row" class="px-4 py-2 text-left font-medium">{{ languageLabel(row.language) }}</th>
            <td class="px-4 py-2 text-right">{{ formatNumber(row.counts.matched) }}</td>
            <td v-for="ch in channels" :key="ch" class="px-4 py-2 text-right">{{ formatNumber(row.counts[ch]) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
