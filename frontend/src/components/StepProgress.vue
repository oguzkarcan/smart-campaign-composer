<script setup lang="ts">
import { computed } from 'vue'
import type { CampaignStatus } from '@/api/types'
import { formatNumber } from '@/lib/format'
import { t } from '@/lib/i18n'
import { campaignSteps, type StepKey, type StepState } from '@/lib/steps'

const props = defineProps<{ status: CampaignStatus; matched?: number }>()

const steps = computed(() => campaignSteps(props.status))

// Each step links to its section on the page.
const anchors: Record<StepKey, string> = {
  audience: 'audience-heading',
  messages: 'content-heading',
  delivery: 'delivery-heading',
}

const circle: Record<StepState, string> = {
  done: 'bg-emerald-600 text-white shadow-sm',
  current: 'bg-indigo-600 text-white shadow-sm ring-4 ring-indigo-100',
  upcoming: 'bg-white text-slate-400 ring-1 ring-slate-300',
  failed: 'bg-rose-600 text-white shadow-sm ring-4 ring-rose-100',
}
// The connector after a step fills once the step is done.
const line: Record<StepState, string> = {
  done: 'w-full bg-emerald-500',
  current: 'w-1/2 bg-linear-to-r from-indigo-500 to-indigo-300',
  upcoming: 'w-0',
  failed: 'w-0',
}

function go(key: StepKey) {
  document.getElementById(anchors[key])?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
</script>

<template>
  <nav :aria-label="t('steps.label')">
    <ol class="flex items-center">
      <li v-for="(s, i) in steps" :key="s.key" class="flex flex-1 items-center last:flex-none">
        <button
          type="button"
          class="group flex min-w-0 items-center gap-3 rounded-xl py-1 pr-2 text-left"
          :aria-current="s.state === 'current' ? 'step' : undefined"
          :disabled="s.state === 'upcoming'"
          @click="go(s.key)"
        >
          <span
            class="flex size-9 shrink-0 items-center justify-center rounded-full transition-shadow"
            :class="circle[s.state]"
            aria-hidden="true"
          >
            <svg v-if="s.state === 'done'" viewBox="0 0 20 20" class="size-4.5" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
              <path d="M4.5 10.5l3.5 3.5 7.5-8" />
            </svg>
            <span v-else-if="s.state === 'failed'" class="text-sm font-bold">!</span>
            <svg v-else viewBox="0 0 20 20" class="size-4.5" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
              <template v-if="s.key === 'audience'">
                <circle cx="7.5" cy="7" r="2.75" />
                <path d="M2.5 16c.6-2.7 2.6-4.25 5-4.25s4.4 1.55 5 4.25" />
                <path d="M13 4.6a2.6 2.6 0 0 1 0 4.9M14.75 11.9c1.4.5 2.4 1.9 2.75 4.1" />
              </template>
              <template v-else-if="s.key === 'messages'">
                <path d="M4 4.5h12a1.5 1.5 0 0 1 1.5 1.5v7a1.5 1.5 0 0 1-1.5 1.5H9l-3.5 3v-3H4A1.5 1.5 0 0 1 2.5 13V6A1.5 1.5 0 0 1 4 4.5z" />
                <path d="M6.5 8.5h7M6.5 11h4.5" />
              </template>
              <template v-else>
                <path d="M17.5 2.5l-15 6.25 6 2.25 2.25 6z" />
                <path d="M17.5 2.5L8.5 11" />
              </template>
            </svg>
          </span>
          <span class="min-w-0">
            <span class="block text-[11px] font-semibold tracking-wider text-slate-400 uppercase">
              {{ String(i + 1).padStart(2, '0') }}
            </span>
            <span
              class="block text-sm font-semibold group-enabled:group-hover:text-indigo-700"
              :class="s.state === 'upcoming' ? 'text-slate-400' : 'text-slate-900'"
            >
              {{ t(`steps.${s.key}`) }}
            </span>
            <span class="hidden truncate text-xs text-slate-500 sm:block">
              {{ t(s.detail, { n: formatNumber(matched ?? 0) }) }}
            </span>
          </span>
        </button>
        <span v-if="i < steps.length - 1" class="mx-2 h-1 flex-1 overflow-hidden rounded-full bg-slate-200 sm:mx-4" aria-hidden="true">
          <span class="block h-full rounded-full transition-all duration-500" :class="line[s.state]" />
        </span>
      </li>
    </ol>
  </nav>
</template>
