<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { cancelSchedule, getCampaign, getProgress, sendScheduledNow, watchProgress } from '@/api/client'
import type { Campaign, CampaignStatus, Channel, Progress } from '@/api/types'
import { CHANNELS } from '@/api/types'
import { channelLabel, formatDateTime, formatNumber } from '@/lib/format'
import { locale, t } from '@/lib/i18n'
import { type CountKey, emptyCounts, finished, formatDuration, percentDone, RateMeter, SEGMENTS } from '@/lib/progress'
import { formatCountdown } from '@/lib/schedule'
import CountsBar from './CountsBar.vue'
import ErrorAlert from './ErrorAlert.vue'

const props = defineProps<{
  campaignId: number
  channels: Channel[]
  approvedAt?: string
  scheduledAt?: string
  updatedAt: string
}>()
const emit = defineEmits<{ status: [status: CampaignStatus]; update: [campaign: Campaign] }>()

const progress = ref<Progress | null>(null)
const connection = ref<'connecting' | 'live' | 'reconnecting' | 'closed'>('connecting')
const streamError = ref<unknown>(null)
const rate = ref(0)
const now = ref(Date.now())
const finishedAt = ref<number | null>(null)

let stop: (() => void) | undefined
let meter = new RateMeter()
const clock = setInterval(() => (now.value = Date.now()), 1000)

function connect() {
  stop?.()
  meter = new RateMeter()
  streamError.value = null
  connection.value = 'connecting'
  finishedAt.value = null
  stop = watchProgress(props.campaignId, {
    onProgress(p) {
      // Only a live transition to done marks the finish time; a campaign that
      // was already done when opened falls back to its updated_at.
      if (p.done && progress.value !== null && !progress.value.done) finishedAt.value = Date.now()
      progress.value = p
      connection.value = p.done ? 'closed' : 'live'
      meter.add({ at: Date.now(), finished: finished(p.totals) })
      rate.value = meter.perSecond()
      emit('status', p.status)
    },
    onConnection(state) {
      connection.value = state
    },
    onError(err) {
      streamError.value = err
      connection.value = 'closed'
      // Show the latest counts we can still get.
      getProgress(props.campaignId)
        .then((p) => (progress.value = p))
        .catch(() => {})
    },
  })
}

watch(() => props.campaignId, connect, { immediate: true })
onUnmounted(() => {
  stop?.()
  clearInterval(clock)
})

const totals = computed(() => progress.value?.totals ?? emptyCounts)
const percent = computed(() => percentDone(totals.value))
const channelRows = computed(() =>
  CHANNELS.filter((ch) => props.channels.includes(ch)).map((ch) => ({
    channel: ch,
    counts: progress.value?.channels[ch] ?? emptyCounts,
  })),
)
// On phones the table keeps the final states and totals; the overall bar
// and legend above have the rest.
function columnClass(key: CountKey): string {
  return key === 'sent' || key === 'failed' ? '' : 'hidden sm:table-cell'
}

const failures = computed(() => progress.value?.failures ?? [])
const done = computed(() => progress.value?.done ?? false)

// A scheduled campaign waits with all its messages queued until its time.
const scheduled = computed(() => progress.value?.status === 'scheduled')
const countdown = computed(() => (props.scheduledAt ? formatCountdown(Date.parse(props.scheduledAt) - now.value, locale.value) : ''))

// "Send now" or "Cancel schedule & edit" on a scheduled campaign. Either can
// lose to the worker starting it at its time (a 409); the page then shows
// the campaign as it really is.
const acting = ref<'sendNow' | 'cancel' | null>(null)
const actionError = ref<unknown>(null)

async function act(action: 'sendNow' | 'cancel') {
  if (acting.value || !props.scheduledAt) return
  const question =
    action === 'sendNow'
      ? t('delivery.confirmSendNow', { n: formatNumber(totals.value.total), date: formatDateTime(props.scheduledAt) })
      : t('delivery.confirmCancel')
  if (!confirm(question)) return
  acting.value = action
  actionError.value = null
  try {
    const c = action === 'sendNow' ? await sendScheduledNow(props.campaignId) : await cancelSchedule(props.campaignId)
    if (action === 'sendNow') finishedAt.value = null
    emit('update', c)
  } catch (e) {
    actionError.value = e
    getCampaign(props.campaignId)
      .then((c) => emit('update', c))
      .catch(() => {})
  } finally {
    acting.value = null
  }
}

// Elapsed since sending started (approval, or the scheduled time), frozen
// when the stream reports done. A campaign that was already finished when
// opened ended at its last update.
const elapsed = computed(() => {
  const start = props.scheduledAt ?? props.approvedAt
  if (!start || scheduled.value) return null
  const end = finishedAt.value ?? (done.value ? Date.parse(props.updatedAt) : now.value)
  return formatDuration(end - Date.parse(start), locale.value)
})

const liveLabel = computed(() => {
  if (done.value) return t(progress.value?.status === 'failed' ? 'delivery.finishedFailures' : 'delivery.finished')
  if (scheduled.value && connection.value === 'live') return t('delivery.scheduled')
  return t(`delivery.${connection.value}`)
})
const liveDot = computed(() => {
  if (done.value) return 'bg-slate-400'
  if (scheduled.value && connection.value === 'live') return 'bg-cyan-500'
  return connection.value === 'live' ? 'bg-emerald-500 animate-pulse' : 'bg-amber-400'
})
</script>

<template>
  <section class="space-y-4" aria-labelledby="delivery-heading">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 id="delivery-heading" class="text-lg font-semibold">{{ t('delivery.title') }}</h2>
      <span class="flex items-center gap-2 text-sm text-slate-500" role="status">
        <span class="size-2 rounded-full" :class="liveDot" />
        {{ liveLabel }}
      </span>
    </div>

    <ErrorAlert v-if="streamError" :error="streamError" :title="t('delivery.stopped')">
      <button type="button" class="font-medium underline" @click="connect">{{ t('delivery.reconnect') }}</button>
    </ErrorAlert>

    <div v-if="!progress && !streamError" class="h-40 animate-pulse rounded-xl bg-slate-200" aria-busy="true" />

    <template v-if="progress">
      <ErrorAlert v-if="actionError" :error="actionError" :title="t('delivery.actionError')" />
      <div
        v-if="scheduled && scheduledAt"
        class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-cyan-200 bg-cyan-50 p-4 text-cyan-900"
      >
        <div class="space-y-1">
          <p>
            <span class="font-medium">{{ t('delivery.scheduledFor', { date: formatDateTime(scheduledAt) }) }}</span>
            <span class="text-cyan-800"> · {{ t('delivery.startsIn', { countdown }) }}</span>
          </p>
          <p class="text-sm text-cyan-800">{{ t('delivery.waiting', { n: formatNumber(totals.total) }) }}</p>
        </div>
        <div class="flex flex-wrap gap-2">
          <button
            type="button"
            :disabled="!!acting"
            class="rounded-lg bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-700 disabled:opacity-50"
            @click="act('sendNow')"
          >
            {{ acting === 'sendNow' ? t('delivery.sending') : t('delivery.sendNow') }}
          </button>
          <button
            type="button"
            :disabled="!!acting"
            class="rounded-lg border border-cyan-300 bg-white px-3 py-1.5 text-sm font-medium text-cyan-900 hover:bg-cyan-100 disabled:opacity-50"
            @click="act('cancel')"
          >
            {{ acting === 'cancel' ? t('delivery.cancelling') : t('delivery.cancelSchedule') }}
          </button>
        </div>
      </div>
      <div class="space-y-4 rounded-xl border border-slate-200 bg-white p-5">
        <div class="flex flex-wrap items-end justify-between gap-4">
          <div>
            <p class="text-3xl font-semibold tabular-nums">{{ percent }}%</p>
            <p class="text-sm text-slate-500">
              {{ t('delivery.finishedOf', { done: formatNumber(finished(totals)), total: formatNumber(totals.total) }) }}
            </p>
          </div>
          <dl class="flex gap-6 text-sm">
            <div v-if="!done && !scheduled">
              <dt class="text-slate-500">{{ t('delivery.throughput') }}</dt>
              <dd class="font-medium tabular-nums">{{ t('delivery.perSecond', { n: formatNumber(Math.round(rate)) }) }}</dd>
            </div>
            <div v-if="elapsed">
              <dt class="text-slate-500">{{ done ? t('delivery.took') : t('delivery.elapsed') }}</dt>
              <dd class="font-medium tabular-nums">{{ elapsed }}</dd>
            </div>
          </dl>
        </div>
        <CountsBar :counts="totals" />
        <ul class="flex flex-wrap gap-x-5 gap-y-1 text-sm">
          <li v-for="s in SEGMENTS" :key="s.key" class="flex items-center gap-1.5">
            <span class="size-2.5 rounded-sm" :class="s.dot" />
            <span class="text-slate-600">{{ t(s.label) }}</span>
            <span class="font-medium tabular-nums">{{ formatNumber(totals[s.key]) }}</span>
          </li>
        </ul>
      </div>

      <div class="overflow-x-auto rounded-xl border border-slate-200 bg-white">
        <table class="w-full text-sm">
          <thead class="border-b border-slate-200 text-left text-xs tracking-wide text-slate-500 uppercase">
            <tr>
              <th class="px-4 py-2 font-medium">{{ t('delivery.channel') }}</th>
              <th class="hidden w-1/3 px-4 py-2 font-medium sm:table-cell">{{ t('delivery.progress') }}</th>
              <th v-for="s in SEGMENTS" :key="s.key" class="px-3 py-2 text-right font-medium" :class="columnClass(s.key)">
                {{ t(s.label) }}
              </th>
              <th class="px-4 py-2 text-right font-medium">{{ t('delivery.total') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100">
            <tr v-for="row in channelRows" :key="row.channel">
              <td class="px-4 py-3 font-medium whitespace-nowrap">{{ channelLabel(row.channel) }}</td>
              <td class="hidden px-4 py-3 sm:table-cell">
                <div class="flex items-center gap-2">
                  <CountsBar :counts="row.counts" size="sm" />
                  <span class="w-10 text-right text-xs tabular-nums text-slate-500">{{ percentDone(row.counts) }}%</span>
                </div>
              </td>
              <td
                v-for="s in SEGMENTS"
                :key="s.key"
                class="px-3 py-3 text-right tabular-nums"
                :class="[columnClass(s.key), { 'text-rose-600 font-medium': s.key === 'failed' && row.counts.failed > 0 }]"
              >
                {{ formatNumber(row.counts[s.key]) }}
              </td>
              <td class="px-4 py-3 text-right font-medium tabular-nums">{{ formatNumber(row.counts.total) }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="failures.length" class="rounded-xl border border-rose-200 bg-white">
        <h3 class="border-b border-rose-100 px-4 py-2 text-sm font-semibold text-rose-900">{{ t('delivery.failures') }}</h3>
        <ul class="divide-y divide-slate-100 text-sm">
          <li v-for="f in failures" :key="f.error" class="flex items-start justify-between gap-4 px-4 py-2">
            <span class="break-all text-slate-700">{{ f.error }}</span>
            <span class="shrink-0 font-medium tabular-nums text-rose-600">{{ formatNumber(f.count) }}</span>
          </li>
        </ul>
      </div>
    </template>
  </section>
</template>
