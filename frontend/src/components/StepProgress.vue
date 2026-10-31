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
  done: 'bg-emerald-600 text-white',
  current: 'bg-indigo-600 text-white ring-4 ring-indigo-100',
  upcoming: 'bg-white text-slate-400 border border-slate-300',
  failed: 'bg-rose-600 text-white',
}
const line: Record<StepState, string> = {
  done: 'bg-emerald-600',
  current: 'bg-slate-200',
  upcoming: 'bg-slate-200',
  failed: 'bg-slate-200',
}

function go(key: StepKey) {
  document.getElementById(anchors[key])?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
</script>

<template>
  <nav :aria-label="t('steps.label')">
    <ol class="flex items-start">
      <li v-for="(s, i) in steps" :key="s.key" class="flex flex-1 items-start last:flex-none">
        <button
          type="button"
          class="group flex min-w-0 items-center gap-3 text-left"
          :aria-current="s.state === 'current' ? 'step' : undefined"
          :disabled="s.state === 'upcoming'"
          @click="go(s.key)"
        >
          <span
            class="flex size-8 shrink-0 items-center justify-center rounded-full text-sm font-semibold"
            :class="circle[s.state]"
            aria-hidden="true"
          >
            <template v-if="s.state === 'done'">✓</template>
            <template v-else-if="s.state === 'failed'">!</template>
            <template v-else>{{ i + 1 }}</template>
          </span>
          <span class="min-w-0">
            <span
              class="block text-sm font-semibold group-enabled:group-hover:underline"
              :class="s.state === 'upcoming' ? 'text-slate-400' : 'text-slate-900'"
            >
              {{ t(`steps.${s.key}`) }}
            </span>
            <span class="hidden truncate text-xs text-slate-500 sm:block">
              {{ t(s.detail, { n: formatNumber(matched ?? 0) }) }}
            </span>
          </span>
        </button>
        <span v-if="i < steps.length - 1" class="mx-3 mt-4 h-0.5 flex-1 rounded" :class="line[s.state]" aria-hidden="true" />
      </li>
    </ol>
  </nav>
</template>
