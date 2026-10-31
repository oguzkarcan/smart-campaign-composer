<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { listCampaigns } from '@/api/client'
import type { CampaignSummary } from '@/api/types'
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
    <div class="flex flex-wrap items-center justify-between gap-4">
      <h1 class="text-2xl font-semibold tracking-tight">{{ t('list.title') }}</h1>
      <RouterLink
        to="/new"
        class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700"
      >
        {{ t('nav.new') }}
      </RouterLink>
    </div>

    <ErrorAlert v-if="error" :error="error" :title="t('list.loadError')">
      <button type="button" class="font-medium underline" @click="refresh">{{ t('common.retry') }}</button>
    </ErrorAlert>

    <div v-if="loading" class="space-y-2" aria-busy="true">
      <div v-for="i in 4" :key="i" class="h-20 animate-pulse rounded-xl bg-slate-200" />
    </div>

    <div
      v-else-if="!campaigns.length && !error"
      class="rounded-xl border border-dashed border-slate-300 bg-white p-10 text-center"
    >
      <p class="font-medium">{{ t('list.empty') }}</p>
      <p class="mt-1 text-sm text-slate-500">{{ t('list.emptyHint') }}</p>
      <RouterLink to="/new" class="mt-3 inline-block text-sm font-medium text-indigo-600 hover:underline">
        {{ t('list.createFirst') }}
      </RouterLink>
    </div>

    <ul v-else class="divide-y divide-slate-100 overflow-hidden rounded-xl border border-slate-200 bg-white">
      <li v-for="c in campaigns" :key="c.id">
        <RouterLink
          :to="{ name: 'campaign', params: { id: c.id } }"
          class="grid gap-x-6 gap-y-2 px-4 py-3 hover:bg-slate-50 sm:grid-cols-[1fr_14rem]"
        >
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-medium text-slate-500">#{{ c.id }}</span>
              <StatusBadge :status="c.status" />
              <span class="text-xs text-slate-500">
                {{ c.channels.map((ch) => channelLabel(ch)).join(' + ') }} ·
                {{ formatDateTime(c.created_at) }}
              </span>
            </div>
            <p class="mt-1 truncate text-slate-800" :title="c.brief">{{ c.brief }}</p>
            <p v-if="c.status === 'scheduled' && c.scheduled_at" class="mt-0.5 text-xs text-cyan-800">
              {{ t('list.sends', { date: formatDateTime(c.scheduled_at) }) }}
            </p>
            <p v-if="c.last_error" class="mt-0.5 truncate text-xs text-rose-600" :title="c.last_error">
              {{ c.last_error }}
            </p>
          </div>
          <div v-if="c.messages.total > 0" class="self-center">
            <CountsBar :counts="c.messages" size="sm" />
            <p class="mt-1 text-xs text-slate-500 tabular-nums">
              {{ t('list.sent', { sent: formatNumber(c.messages.sent), total: formatNumber(c.messages.total) }) }}
              <span v-if="c.messages.failed" class="text-rose-600">{{ t('list.failed', { n: formatNumber(c.messages.failed) }) }}</span>
            </p>
          </div>
        </RouterLink>
      </li>
    </ul>

    <div v-if="hasMore && !loading" class="text-center">
      <button
        type="button"
        :disabled="loadingMore"
        class="rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm font-medium hover:bg-slate-50 disabled:opacity-50"
        @click="loadMore"
      >
        {{ loadingMore ? t('common.loading') : t('common.loadMore') }}
      </button>
    </div>
  </section>
</template>
