<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { listCampaigns } from '@/api/client'
import type { CampaignSummary } from '@/api/types'
import ChannelIcon from '@/components/ChannelIcon.vue'
import CountsBar from '@/components/CountsBar.vue'
import ErrorAlert from '@/components/ErrorAlert.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { channelLabel, formatDateTime, formatNumber } from '@/lib/format'
import { t } from '@/lib/i18n'

const PAGE = 20
const MAX_LIMIT = 200 // campaign.MaxListLimit
const REFRESH_MS = 3_000

const campaigns = ref<CampaignSummary[]>([])
const loading = ref(true)
const loadingMore = ref(false)
const hasMore = ref(false)
const error = ref<unknown>(null)

let controller: AbortController | undefined
let timer: ReturnType<typeof setTimeout> | undefined

// Scheduled campaigns are polled too, so their row turns to sending on time.
const active = computed(() =>
  campaigns.value.some((c) => c.status === 'approved' || c.status === 'scheduled' || c.status === 'sending'),
)

// refresh reloads everything shown so far in one request; while a campaign is
// delivering it repeats, so its row stays live.
async function refresh() {
  clearTimeout(timer)
  controller?.abort()
  controller = new AbortController()
  const limit = Math.min(Math.max(campaigns.value.length, PAGE), MAX_LIMIT)
  try {
    const list = await listCampaigns({ limit }, controller.signal)
    campaigns.value = list
    hasMore.value = list.length === limit
    error.value = null
  } catch (e) {
    if (controller.signal.aborted) return
    error.value = e
  } finally {
    loading.value = false
  }
  if (active.value) timer = setTimeout(refresh, REFRESH_MS)
}

async function loadMore() {
  const last = campaigns.value.at(-1)
  if (!last || loadingMore.value) return
  loadingMore.value = true
  try {
    const page = await listCampaigns({ limit: PAGE, before: last.id })
    campaigns.value = [...campaigns.value, ...page]
    hasMore.value = page.length === PAGE
  } catch (e) {
    error.value = e
  } finally {
    loadingMore.value = false
  }
}

refresh()
onUnmounted(() => {
  clearTimeout(timer)
  controller?.abort()
})
</script>

<template>
  <section class="space-y-6">
    <div class="relative overflow-hidden rounded-2xl border border-stone-200 bg-white p-6 shadow-sm sm:p-8 dark:border-indigo-100">
      <div
        class="pointer-events-none absolute inset-0 bg-linear-to-br from-[#F4462A]/6 via-white to-[#F59E0B]/8 opacity-90 dark:from-indigo-50 dark:to-violet-50"
        aria-hidden="true"
      />
      <div class="pointer-events-none absolute -top-20 -right-10 size-64 rounded-full bg-[#F4462A]/10 blur-3xl dark:bg-violet-200/40" aria-hidden="true" />
      <div class="relative flex flex-wrap items-end justify-between gap-6">
        <div class="max-w-2xl space-y-2">
          <p class="text-xs font-bold tracking-[0.14em] text-indigo-600 uppercase">{{ t('app.subtitle') }}</p>
          <h1 class="text-3xl font-bold tracking-tight text-slate-900">{{ t('list.title') }}</h1>
          <p class="text-slate-600">{{ t('list.intro') }}</p>
        </div>
        <RouterLink
          to="/new"
          class="inline-flex items-center gap-2 rounded-xl bg-linear-to-r from-indigo-600 to-violet-600 px-5 py-2.5 text-sm font-semibold text-white shadow-md shadow-indigo-500/20 hover:from-indigo-700 hover:to-violet-700"
        >
          <span aria-hidden="true">✦</span>
          {{ t('nav.new') }}
        </RouterLink>
      </div>
    </div>

    <ErrorAlert v-if="error" :error="error" :title="t('list.loadError')">
      <button type="button" class="font-medium underline" @click="refresh">{{ t('common.retry') }}</button>
    </ErrorAlert>

    <div v-if="loading" class="space-y-3" aria-busy="true">
      <div v-for="i in 4" :key="i" class="h-24 animate-pulse rounded-2xl bg-slate-200" />
    </div>

    <div
      v-else-if="!campaigns.length && !error"
      class="rounded-2xl border border-dashed border-slate-300 bg-white p-12 text-center"
    >
      <p class="font-semibold">{{ t('list.empty') }}</p>
      <p class="mt-1 text-sm text-slate-500">{{ t('list.emptyHint') }}</p>
      <RouterLink to="/new" class="mt-3 inline-block text-sm font-semibold text-indigo-600 hover:underline">
        {{ t('list.createFirst') }}
      </RouterLink>
    </div>

    <ul v-else class="space-y-3">
      <li v-for="c in campaigns" :key="c.id">
        <RouterLink
          :to="{ name: 'campaign', params: { id: c.id } }"
          class="group grid gap-x-8 gap-y-3 rounded-2xl border border-slate-200 bg-white px-5 py-4 shadow-xs transition hover:-translate-y-px hover:border-indigo-300 hover:shadow-md sm:grid-cols-[1fr_15rem]"
        >
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-mono text-xs font-medium text-slate-400">#{{ c.id }}</span>
              <StatusBadge :status="c.status" />
              <span class="flex items-center gap-1 text-slate-400" :title="c.channels.map((ch) => channelLabel(ch)).join(' + ')">
                <ChannelIcon v-for="ch in c.channels" :key="ch" :channel="ch" class="size-4" />
              </span>
              <span class="text-xs text-slate-500">{{ formatDateTime(c.created_at) }}</span>
            </div>
            <p class="mt-1.5 line-clamp-2 text-[15px] font-medium text-slate-800 group-hover:text-slate-900" :title="c.brief">
              {{ c.brief }}
            </p>
            <p v-if="c.status === 'scheduled' && c.scheduled_at" class="mt-1 text-xs font-medium text-cyan-800">
              {{ t('list.sends', { date: formatDateTime(c.scheduled_at) }) }}
            </p>
            <p v-if="c.last_error" class="mt-1 truncate text-xs text-rose-600" :title="c.last_error">
              {{ c.last_error }}
            </p>
          </div>
          <div v-if="c.messages.total > 0" class="self-center">
            <CountsBar :counts="c.messages" size="sm" />
            <p class="mt-1.5 text-xs text-slate-500 tabular-nums">
              {{ t('list.sent', { sent: formatNumber(c.messages.sent), total: formatNumber(c.messages.total) }) }}
              <span v-if="c.messages.failed" class="text-rose-600">{{ t('list.failed', { n: formatNumber(c.messages.failed) }) }}</span>
            </p>
          </div>
          <div v-else class="hidden items-center justify-end text-slate-300 group-hover:text-indigo-500 sm:flex" aria-hidden="true">
            <svg viewBox="0 0 20 20" class="size-5" fill="none" stroke="currentColor" stroke-width="1.8">
              <path d="M7.5 4.5l5.5 5.5-5.5 5.5" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </div>
        </RouterLink>
      </li>
    </ul>

    <div v-if="hasMore && !loading" class="text-center">
      <button
        type="button"
        :disabled="loadingMore"
        class="rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium shadow-xs hover:bg-slate-50 disabled:opacity-50"
        @click="loadMore"
      >
        {{ loadingMore ? t('common.loading') : t('common.loadMore') }}
      </button>
    </div>
  </section>
</template>
