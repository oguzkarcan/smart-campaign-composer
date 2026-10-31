<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { getProgress, watchProgress } from '@/api/client'
import type { CampaignStatus, Channel, Progress } from '@/api/types'
import { CHANNELS } from '@/api/types'
import { channelLabels, formatDateTime, formatNumber } from '@/lib/format'
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
const emit = defineEmits<{ status: [status: CampaignStatus] }>()

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
const countdown = computed(() => (props.scheduledAt ? formatCountdown(Date.parse(props.scheduledAt) - now.value) : ''))

// Elapsed since sending started (approval, or the scheduled time), frozen
// when the stream reports done. A campaign that was already finished when
// opened ended at its last update.
const elapsed = computed(() => {
  const start = props.scheduledAt ?? props.approvedAt
  if (!start || scheduled.value) return null
  const end = finishedAt.value ?? (done.value ? Date.parse(props.updatedAt) : now.value)
  return formatDuration(end - Date.parse(start))
})

const liveLabel = computed(() => {
  if (done.value) return progress.value?.status === 'failed' ? 'Finished with failures' : 'Delivery finished'
  if (scheduled.value && connection.value === 'live') return 'Scheduled'
  return {
    connecting: 'Connecting…',
    live: 'Live',
    reconnecting: 'Reconnecting…',
    closed: 'Not live',
  }[connection.value]
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
      <h2 id="delivery-heading" class="text-lg font-semibold">Delivery</h2>
      <span class="flex items-center gap-2 text-sm text-slate-500" role="status">
        <span class="size-2 rounded-full" :class="liveDot" />
        {{ liveLabel }}
      </span>
    </div>

    <ErrorAlert v-if="streamError" :error="streamError" title="Live updates stopped">
      <button type="button" class="font-medium underline" @click="connect">Reconnect</button>
    </ErrorAlert>

    <div v-if="!progress && !streamError" class="h-40 animate-pulse rounded-xl bg-slate-200" aria-busy="true" />

    <template v-if="progress">
      <div
        v-if="scheduled && scheduledAt"
        class="flex flex-wrap items-baseline justify-between gap-2 rounded-xl border border-cyan-200 bg-cyan-50 p-4 text-cyan-900"
      >
        <p>
          <span class="font-medium">Scheduled for {{ formatDateTime(scheduledAt) }}</span>
          <span class="text-cyan-800"> · sending starts {{ countdown }}</span>
        </p>
        <p class="text-sm text-cyan-800">{{ formatNumber(totals.total) }} messages are queued and waiting.</p>
      </div>
      <div class="space-y-4 rounded-xl border border-slate-200 bg-white p-5">
        <div class="flex flex-wrap items-end justify-between gap-4">
          <div>
            <p class="text-3xl font-semibold tabular-nums">{{ percent }}%</p>
            <p class="text-sm text-slate-500">
              {{ formatNumber(finished(totals)) }} of {{ formatNumber(totals.total) }} messages finished
            </p>
          </div>
          <dl class="flex gap-6 text-sm">
            <div v-if="!done && !scheduled">
              <dt class="text-slate-500">Throughput</dt>
              <dd class="font-medium tabular-nums">{{ formatNumber(Math.round(rate)) }} msg/s</dd>
            </div>
            <div v-if="elapsed">
              <dt class="text-slate-500">{{ done ? 'Took' : 'Elapsed' }}</dt>
              <dd class="font-medium tabular-nums">{{ elapsed }}</dd>
            </div>
          </dl>
        </div>
        <CountsBar :counts="totals" />
        <ul class="flex flex-wrap gap-x-5 gap-y-1 text-sm">
          <li v-for="s in SEGMENTS" :key="s.key" class="flex items-center gap-1.5">
            <span class="size-2.5 rounded-sm" :class="s.dot" />
            <span class="text-slate-600">{{ s.label }}</span>
            <span class="font-medium tabular-nums">{{ formatNumber(totals[s.key]) }}</span>
          </li>
        </ul>
      </div>

      <div class="overflow-x-auto rounded-xl border border-slate-200 bg-white">
        <table class="w-full text-sm">
          <thead class="border-b border-slate-200 text-left text-xs tracking-wide text-slate-500 uppercase">
            <tr>
              <th class="px-4 py-2 font-medium">Channel</th>
              <th class="hidden w-1/3 px-4 py-2 font-medium sm:table-cell">Progress</th>
              <th v-for="s in SEGMENTS" :key="s.key" class="px-3 py-2 text-right font-medium" :class="columnClass(s.key)">
                {{ s.label }}
              </th>
              <th class="px-4 py-2 text-right font-medium">Total</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100">
            <tr v-for="row in channelRows" :key="row.channel">
              <td class="px-4 py-3 font-medium">{{ channelLabels[row.channel] }}</td>
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
        <h3 class="border-b border-rose-100 px-4 py-2 text-sm font-semibold text-rose-900">Failure reasons</h3>
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
