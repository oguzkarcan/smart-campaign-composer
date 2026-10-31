<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { getRecipients } from '@/api/client'
import type { Channel, PeopleSort, Person, SortOrder } from '@/api/types'
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

// Open from the start: on wide screens the list sits beside the rule.
const open = ref(true)
const people = ref<Person[]>([])
const total = ref(0)
const page = ref(0) // 0-based
const sort = ref<PeopleSort>('id')
const order = ref<SortOrder>('asc')
const loaded = ref(false)
const loading = ref(false)
const error = ref<unknown>(null)
let controller: AbortController | undefined

const pages = computed(() => Math.max(1, Math.ceil(total.value / PAGE)))
const from = computed(() => (total.value ? page.value * PAGE + 1 : 0))
const to = computed(() => page.value * PAGE + people.value.length)

// The server sorts and pages, so the order holds across all pages.
async function load() {
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  error.value = null
  try {
    const p = await getRecipients(
      props.campaignId,
      { limit: PAGE, offset: page.value * PAGE, sort: sort.value, order: order.value },
      controller.signal,
    )
    people.value = p.recipients
    total.value = p.total
    loaded.value = true
  } catch (e) {
    if (!controller.signal.aborted) error.value = e
  } finally {
    loading.value = false
  }
}

function go(to: number) {
  if (to < 0 || to >= pages.value || to === page.value) return
  page.value = to
  load()
}

// Clicking a column sorts by it A→Z; clicking it again reverses. Sorting
// starts again from the first page.
function sortBy(column: PeopleSort) {
  if (sort.value === column) order.value = order.value === 'asc' ? 'desc' : 'asc'
  else {
    sort.value = column
    order.value = 'asc'
  }
  page.value = 0
  load()
}

function ariaSort(column: PeopleSort) {
  if (sort.value !== column) return 'none'
  return order.value === 'asc' ? 'ascending' : 'descending'
}

function toggle() {
  open.value = !open.value
  if (open.value && !loaded.value) load()
}

// A new rule means different people; start over.
watch(
  () => props.ruleKey,
  () => {
    people.value = []
    total.value = 0
    page.value = 0
    loaded.value = false
    if (open.value) load()
  },
)
onUnmounted(() => controller?.abort())
load()

const columns = computed(() => [
  { key: 'first_name' as const, label: t('people.name') },
  { key: 'city' as const, label: t('people.city') },
  { key: 'language' as const, label: t('audience.language') },
])

function consent(p: Person, ch: Channel): boolean {
  return ch === 'sms' ? p.sms_consent : p.email_consent
}

function choose(p: Person) {
  emit('select', props.selected?.id === p.id ? null : p)
}
</script>

<template>
  <div class="flex flex-col rounded-xl border border-slate-200 bg-white shadow-xs">
    <div class="flex items-center justify-between gap-2 border-b border-slate-100 px-4 py-3">
      <div class="flex min-w-0 items-center gap-2">
        <h3 class="text-sm font-semibold text-slate-900">{{ t('audience.matching') }}</h3>
        <span class="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-semibold text-slate-600 tabular-nums">
          {{ formatNumber(matched) }}
        </span>
      </div>
      <button
        type="button"
        class="flex size-7 items-center justify-center rounded-lg text-slate-400 hover:bg-slate-100 hover:text-slate-700"
        :aria-expanded="open"
        :aria-label="open ? t('people.hide') : t('people.show', { n: formatNumber(matched) })"
        :title="open ? t('people.hide') : t('people.show', { n: formatNumber(matched) })"
        @click="toggle"
      >
        <svg viewBox="0 0 20 20" class="size-4 transition-transform" :class="{ 'rotate-180': open }" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M5 8l5 5 5-5" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
    </div>

    <div v-if="open" class="flex flex-1 flex-col gap-3 px-4 pt-3 pb-4">
      <p class="text-xs text-slate-500">{{ t('people.readOnly') }}</p>

      <ErrorAlert v-if="error" :error="error" :title="t('people.loadError')">
        <button type="button" class="font-medium underline" @click="load()">
          {{ t('common.retry') }}
        </button>
      </ErrorAlert>

      <div v-if="loading && !loaded" class="space-y-2" aria-busy="true">
        <div v-for="i in 8" :key="i" class="h-9 animate-pulse rounded-lg bg-slate-100" />
      </div>
      <p v-else-if="!error && loaded && !total" class="text-sm text-slate-500">{{ t('people.empty') }}</p>

      <div v-if="people.length" class="-mx-4 overflow-x-auto" :class="{ 'opacity-60': loading }" :aria-busy="loading">
        <table class="w-full text-sm">
          <thead class="border-y border-slate-100 bg-slate-50/70 text-left text-[11px] tracking-wide text-slate-500 uppercase">
            <tr>
              <th
                v-for="(col, i) in columns"
                :key="col.key"
                class="py-2 pr-3 font-semibold"
                :class="{ 'pl-4': i === 0 }"
                :aria-sort="ariaSort(col.key)"
              >
                <button
                  type="button"
                  class="inline-flex items-center gap-1 uppercase hover:text-slate-900"
                  :class="{ 'text-slate-900': sort === col.key }"
                  :title="t('people.sortBy', { column: col.label })"
                  @click="sortBy(col.key)"
                >
                  {{ col.label }}
                  <span aria-hidden="true" class="text-[9px]" :class="sort === col.key ? 'text-indigo-600' : 'text-slate-300'">
                    {{ sort === col.key && order === 'desc' ? '▼' : '▲' }}
                  </span>
                </button>
              </th>
              <th v-for="ch in channels" :key="ch" class="py-2 pr-3 font-semibold">{{ channelLabel(ch) }}</th>
              <th class="py-2 pr-4 font-semibold"><span class="sr-only">{{ t('people.preview') }}</span></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100">
            <tr
              v-for="p in people"
              :key="p.id"
              class="cursor-pointer transition-colors hover:bg-slate-50"
              :class="{ 'bg-indigo-50/80 shadow-[inset_3px_0_0_var(--color-indigo-500)] hover:bg-indigo-50': selected?.id === p.id }"
              @click="choose(p)"
            >
              <td class="py-2 pr-3 pl-4 whitespace-nowrap">
                <span class="flex items-center gap-2.5">
                  <span
                    class="flex size-7 shrink-0 items-center justify-center rounded-full bg-linear-to-br from-indigo-100 to-violet-100 text-xs font-semibold text-indigo-700"
                    aria-hidden="true"
                  >
                    {{ p.first_name.charAt(0) }}
                  </span>
                  <span class="font-medium text-slate-900">{{ p.first_name }}</span>
                </span>
              </td>
              <td class="py-2 pr-3 whitespace-nowrap text-slate-600">{{ p.city }}</td>
              <td class="py-2 pr-3">
                <span class="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] font-medium text-slate-600">
                  {{ p.language.toUpperCase() }}
                </span>
              </td>
              <td v-for="ch in channels" :key="ch" class="py-2 pr-3 whitespace-nowrap">
                <span
                  v-if="consent(p, ch)"
                  class="inline-flex size-6 items-center justify-center rounded-full bg-emerald-50 text-emerald-600 ring-1 ring-emerald-200"
                  :title="t('people.receives')"
                >
                  <svg viewBox="0 0 20 20" class="size-3.5" fill="none" stroke="currentColor" stroke-width="2.6" aria-hidden="true">
                    <path d="M5 10.5l3.2 3.2L15 7" stroke-linecap="round" stroke-linejoin="round" />
                  </svg>
                  <span class="sr-only">{{ t('people.receives') }}</span>
                </span>
                <span
                  v-else
                  class="inline-flex size-6 items-center justify-center rounded-full text-slate-400 ring-1 ring-slate-200"
                  :title="t('people.skipped')"
                >
                  <svg viewBox="0 0 20 20" class="size-3" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                    <path d="M6 6l8 8M14 6l-8 8" stroke-linecap="round" />
                  </svg>
                  <span class="sr-only">{{ t('people.skipped') }}</span>
                </span>
              </td>
              <td class="py-2 pr-4 text-right">
                <button
                  type="button"
                  class="rounded-lg px-2.5 py-1 text-xs font-semibold whitespace-nowrap transition-colors"
                  :class="
                    selected?.id === p.id
                      ? 'bg-indigo-600 text-white shadow-sm'
                      : 'text-indigo-700 ring-1 ring-indigo-200 hover:bg-indigo-50'
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

      <nav v-if="people.length" class="mt-auto flex flex-wrap items-center justify-between gap-2 text-xs text-slate-500">
        <span class="tabular-nums">
          {{ t('people.range', { from: formatNumber(from), to: formatNumber(to), total: formatNumber(total) }) }}
        </span>
        <div class="flex items-center gap-2">
          <button
            type="button"
            :disabled="loading || page === 0"
            class="rounded-lg border border-slate-200 bg-white px-3 py-1 text-sm font-medium text-slate-700 shadow-xs hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-40"
            @click="go(page - 1)"
          >
            ‹ {{ t('people.prev') }}
          </button>
          <span class="tabular-nums" aria-live="polite">{{ t('people.page', { page: page + 1, pages }) }}</span>
          <button
            type="button"
            :disabled="loading || page >= pages - 1"
            class="rounded-lg border border-slate-200 bg-white px-3 py-1 text-sm font-medium text-slate-700 shadow-xs hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-40"
            @click="go(page + 1)"
          >
            {{ t('people.next') }} ›
          </button>
        </div>
      </nav>
    </div>
  </div>
</template>
