<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { getRecipients } from '@/api/client'
import type { Channel, Person } from '@/api/types'
import { channelLabel, formatNumber } from '@/lib/format'
import { t } from '@/lib/i18n'
import ErrorAlert from './ErrorAlert.vue'

// Read-only list of the people the segment rule matches, with whether each
// campaign channel reaches them. Selecting one previews the messages as that
// person; membership itself only changes through the rule.
const props = defineProps<{
  campaignId: number
  channels: Channel[]
  matched: number
  // Changes when the segment is regenerated, so the list reloads.
  ruleKey: string
  selected: Person | null
}>()
const emit = defineEmits<{ select: [person: Person | null] }>()

const PAGE = 15

const open = ref(false)
const people = ref<Person[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref<unknown>(null)
let controller: AbortController | undefined

async function load(more: boolean) {
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  error.value = null
  try {
    const page = await getRecipients(props.campaignId, { limit: PAGE, offset: more ? people.value.length : 0 }, controller.signal)
    people.value = more ? [...people.value, ...page.recipients] : page.recipients
    total.value = page.total
  } catch (e) {
    if (!controller.signal.aborted) error.value = e
  } finally {
    loading.value = false
  }
}

function toggle() {
  open.value = !open.value
  if (open.value && !people.value.length) load(false)
}

// A new rule means different people; start over.
watch(
  () => props.ruleKey,
  () => {
    people.value = []
    total.value = 0
    if (open.value) load(false)
  },
)
onUnmounted(() => controller?.abort())

const hasMore = computed(() => people.value.length < total.value)

function consent(p: Person, ch: Channel): boolean {
  return ch === 'sms' ? p.sms_consent : p.email_consent
}

function choose(p: Person) {
  emit('select', props.selected?.id === p.id ? null : p)
}
</script>

<template>
  <div class="rounded-lg border border-slate-200 bg-white">
    <button
      type="button"
      class="flex w-full items-center justify-between gap-2 px-4 py-3 text-left text-sm font-medium text-slate-700 hover:bg-slate-50"
      :aria-expanded="open"
      @click="toggle"
    >
      <span>{{ open ? t('people.hide') : t('people.show', { n: formatNumber(matched) }) }}</span>
      <span aria-hidden="true" class="text-slate-400">{{ open ? '▴' : '▾' }}</span>
    </button>

    <div v-if="open" class="space-y-3 border-t border-slate-100 px-4 pt-3 pb-4">
      <p class="text-xs text-slate-500">{{ t('people.readOnly') }}</p>

      <ErrorAlert v-if="error" :error="error" :title="t('people.loadError')">
        <button type="button" class="font-medium underline" @click="load(people.length > 0)">
          {{ t('common.retry') }}
        </button>
      </ErrorAlert>

      <div v-if="loading && !people.length" class="h-24 animate-pulse rounded bg-slate-100" aria-busy="true" />
      <p v-else-if="!error && !people.length" class="text-sm text-slate-500">{{ t('people.empty') }}</p>

      <div v-if="people.length" class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="text-left text-xs text-slate-500">
            <tr>
              <th class="py-2 pr-3 font-medium">{{ t('people.name') }}</th>
              <th class="py-2 pr-3 font-medium">{{ t('people.city') }}</th>
              <th class="py-2 pr-3 font-medium">{{ t('audience.language') }}</th>
              <th v-for="ch in channels" :key="ch" class="py-2 pr-3 font-medium">{{ channelLabel(ch) }}</th>
              <th class="py-2 font-medium"><span class="sr-only">{{ t('people.preview') }}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100">
            <tr
              v-for="p in people"
              :key="p.id"
              class="cursor-pointer hover:bg-slate-50"
              :class="{ 'bg-indigo-50 hover:bg-indigo-50': selected?.id === p.id }"
              @click="choose(p)"
            >
              <td class="py-2 pr-3 font-medium whitespace-nowrap text-slate-900">{{ p.first_name }}</td>
              <td class="py-2 pr-3 whitespace-nowrap text-slate-700">{{ p.city }}</td>
              <td class="py-2 pr-3">
                <span class="rounded bg-slate-100 px-1.5 py-0.5 text-xs font-semibold text-slate-700">
                  {{ p.language.toUpperCase() }}
                </span>
              </td>
              <td v-for="ch in channels" :key="ch" class="py-2 pr-3 whitespace-nowrap">
                <span
                  v-if="consent(p, ch)"
                  class="rounded-full bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-800"
                >
                  ✓ {{ t('people.receives') }}
                </span>
                <span v-else class="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500">
                  {{ t('people.skipped') }}
                </span>
              </td>
              <td class="py-2 text-right">
                <button
                  type="button"
                  class="rounded-md border px-2 py-0.5 text-xs font-medium whitespace-nowrap"
                  :class="
                    selected?.id === p.id
                      ? 'border-indigo-600 bg-indigo-600 text-white'
                      : 'border-slate-300 text-slate-700 hover:bg-slate-100'
                  "
                  :aria-pressed="selected?.id === p.id"
                  :title="t('people.previewTitle', { name: p.first_name })"
                  @click.stop="choose(p)"
                >
                  {{ selected?.id === p.id ? t('people.previewing') : t('people.preview') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="people.length" class="flex flex-wrap items-center justify-between gap-2 text-xs text-slate-500">
        <span class="tabular-nums">
          {{ t('people.showing', { shown: formatNumber(people.length), total: formatNumber(total) }) }}
        </span>
        <button
          v-if="hasMore"
          type="button"
          :disabled="loading"
          class="rounded-lg border border-slate-300 bg-white px-3 py-1 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-50"
          @click="load(true)"
        >
          {{ loading ? t('common.loading') : t('common.loadMore') }}
        </button>
      </div>
    </div>
  </div>
</template>
