<script setup lang="ts">
import { computed } from 'vue'
import type { Audience, Channel } from '@/api/types'
import { LANGUAGES } from '@/api/types'
import { channelLabel, formatNumber, languageLabel } from '@/lib/format'
import { t } from '@/lib/i18n'
import ChannelIcon from './ChannelIcon.vue'

const props = defineProps<{ audience: Audience; channels: Channel[] }>()

// Readers per language; each gets the messages written in their language.
const languages = computed(() =>
  LANGUAGES.filter((l) => props.audience.languages?.[l]).map((l) => ({ language: l, counts: props.audience.languages![l]! })),
)
</script>

<template>
  <div class="space-y-3">
    <dl class="grid grid-cols-2 gap-3">
      <div
        class="col-span-2 flex items-center justify-between gap-4 rounded-xl border border-indigo-100 bg-linear-to-br from-indigo-50 to-violet-50 px-4 py-3.5"
      >
        <dt class="text-sm font-medium text-indigo-900">{{ t('audience.matching') }}</dt>
        <dd class="text-3xl font-bold tracking-tight text-indigo-950 tabular-nums">{{ formatNumber(audience.matched) }}</dd>
      </div>
      <div
        v-for="ch in channels"
        :key="ch"
        class="rounded-xl border border-slate-200 bg-white p-3.5 shadow-xs"
        :class="{ 'col-span-2': channels.length === 1 }"
      >
        <dt class="flex items-center gap-1.5 text-xs font-medium text-slate-500">
          <ChannelIcon :channel="ch" class="size-4 text-slate-400" />
          {{ t('audience.recipients', { channel: channelLabel(ch) }) }}
        </dt>
        <dd class="mt-1 text-2xl font-bold tracking-tight tabular-nums" :class="audience[ch] === 0 ? 'text-rose-600' : 'text-slate-900'">
          {{ formatNumber(audience[ch]) }}
        </dd>
        <p class="mt-0.5 text-[11px] leading-snug text-slate-500">
          <template v-if="audience[ch] === 0">{{ t('audience.nobodyOptedIn', { channel: channelLabel(ch) }) }}</template>
          <template v-else>{{ t('audience.optedIn', { channel: channelLabel(ch) }) }}</template>
        </p>
      </div>
    </dl>

    <div v-if="languages.length" class="overflow-x-auto rounded-xl border border-slate-200 bg-white shadow-xs">
      <table class="w-full text-sm">
        <caption class="px-4 pt-3 text-left text-xs leading-snug text-slate-500">
          {{ t('audience.byLanguage') }}
        </caption>
        <thead class="text-left text-[11px] tracking-wide text-slate-500 uppercase">
          <tr>
            <th class="px-4 py-2 font-semibold">{{ t('audience.language') }}</th>
            <th class="px-4 py-2 text-right font-semibold">{{ t('audience.matching') }}</th>
            <th v-for="ch in channels" :key="ch" class="px-4 py-2 text-right font-semibold">{{ channelLabel(ch) }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100 tabular-nums">
          <tr v-for="row in languages" :key="row.language">
            <th scope="row" class="px-4 py-2 text-left font-medium">
              <span class="mr-2 rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] font-medium text-slate-600">
                {{ row.language.toUpperCase() }}
              </span>
              {{ languageLabel(row.language) }}
            </th>
            <td class="px-4 py-2 text-right font-medium">{{ formatNumber(row.counts.matched) }}</td>
            <td v-for="ch in channels" :key="ch" class="px-4 py-2 text-right text-slate-600">{{ formatNumber(row.counts[ch]) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
