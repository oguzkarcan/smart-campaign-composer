<script setup lang="ts">
import { computed } from 'vue'
import type { CampaignStatus } from '@/api/types'
import { formatNumber } from '@/lib/format'
import { t } from '@/lib/i18n'
import { campaignSteps, type StepNumber, type StepState } from '@/lib/steps'

const props = defineProps<{ status: CampaignStatus; matched?: number; active: StepNumber }>()
const emit = defineEmits<{ select: [step: StepNumber] }>()

// The tabs of the campaign workspace; a step opens once the campaign reached it.
const steps = computed(() => campaignSteps(props.status))

const circle: Record<StepState, string> = {
  done: 'bg-emerald-600 text-white shadow-sm',
  current: 'bg-indigo-600 text-white shadow-sm ring-2 ring-indigo-100',
  upcoming: 'bg-white text-slate-400 ring-1 ring-slate-300',
  failed: 'bg-rose-600 text-white shadow-sm ring-2 ring-rose-100',
}
</script>

<template>
  <div role="tablist" :aria-label="t('steps.label')" class="grid grid-cols-3 gap-1 rounded-xl bg-slate-100 p-1">
    <button
      v-for="(s, i) in steps"
      :id="`step-tab-${s.key}`"
      :key="s.key"
      type="button"
      role="tab"
      class="flex min-w-0 items-center gap-2.5 rounded-lg px-2.5 py-1 text-left transition-colors disabled:cursor-not-allowed"
      :class="active === i + 1 ? 'bg-white shadow-sm' : 'enabled:hover:bg-white/60'"
      :aria-selected="active === i + 1"
      :aria-controls="`step-panel-${s.key}`"
      :aria-current="s.state === 'current' ? 'step' : undefined"
      :disabled="s.state === 'upcoming'"
      @click="emit('select', (i + 1) as StepNumber)"
    >
      <span
            class="hidden size-7 shrink-0 items-center sm:flex justify-center rounded-full transition-shadow"
            :class="circle[s.state]"
            aria-hidden="true"
          >
            <svg v-if="s.state === 'done'" viewBox="0 0 20 20" class="size-4" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
              <path d="M4.5 10.5l3.5 3.5 7.5-8" />
            </svg>
            <span v-else-if="s.state === 'failed'" class="text-xs font-bold">!</span>
            <svg v-else viewBox="0 0 20 20" class="size-4" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
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
      <span class="min-w-0 leading-tight">
        <span class="block truncate text-sm font-semibold" :class="s.state === 'upcoming' ? 'text-slate-400' : active === i + 1 ? 'text-indigo-700' : 'text-slate-800'">
          {{ i + 1 }}. {{ t(`steps.${s.key}`) }}
        </span>
        <span class="hidden truncate text-xs text-slate-500 sm:block">
          {{ t(s.detail, { n: formatNumber(matched ?? 0) }) }}
        </span>
      </span>
    </button>
  </div>
</template>
