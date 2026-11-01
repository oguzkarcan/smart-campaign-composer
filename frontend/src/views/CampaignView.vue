<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { ApiError, generateSegment, getCampaign } from '@/api/client'
import type { Campaign, CampaignStatus, Person } from '@/api/types'
import AudienceCounts from '@/components/AudienceCounts.vue'
import AudiencePeople from '@/components/AudiencePeople.vue'
import ChannelIcon from '@/components/ChannelIcon.vue'
import ContentSection from '@/components/ContentSection.vue'
import DeliveryDashboard from '@/components/DeliveryDashboard.vue'
import ErrorAlert from '@/components/ErrorAlert.vue'
import RuleTree from '@/components/RuleTree.vue'
import SectionShell from '@/components/SectionShell.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import StepProgress from '@/components/StepProgress.vue'
import { channelLabel, editableStatuses, formatDateTime, formatNumber } from '@/lib/format'
import { locale, t } from '@/lib/i18n'
import { describeRule } from '@/lib/segment'

const props = defineProps<{ id: number }>()

const campaign = ref<Campaign | null>(null)
const loadError = ref<unknown>(null)
const segmentError = ref<unknown>(null)
const generating = ref(false)
// Content generation is streaming; the audience must not change meanwhile.
const contentBusy = ref(false)
// "Preview as this person": a matching user whose data fills the previews.
const previewPerson = ref<Person | null>(null)

let controller: AbortController | undefined

async function load(id: number) {
  controller?.abort()
  controller = new AbortController()
  campaign.value = null
  previewPerson.value = null
  loadError.value = null
  segmentError.value = null
  try {
    campaign.value = await getCampaign(id, controller.signal)
  } catch (e) {
    if (!controller.signal.aborted) loadError.value = e
  }
}

watch(() => props.id, load, { immediate: true })
onUnmounted(() => controller?.abort())

const notFound = computed(() => loadError.value instanceof ApiError && loadError.value.status === 404)
const rule = computed(() => (campaign.value?.segment_rule ? describeRule(campaign.value.segment_rule, locale.value) : null))
const delivering = computed(() => !!campaign.value && !editableStatuses.includes(campaign.value.status))

// Approval happens at the bottom of the page; bring the new dashboard into
// view. Cancelling a schedule goes back to the messages.
watch(
  () => [campaign.value?.id, delivering.value] as const,
  async ([id, now], [prevId, before]) => {
    if (id === undefined || id !== prevId || now === before) return // a load, not a change
    await nextTick()
    document.getElementById(now ? 'delivery-heading' : 'content-heading')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  },
)

// The live stream carries status changes (sending → completed).
function onDeliveryStatus(status: CampaignStatus) {
  if (campaign.value && campaign.value.status !== status) campaign.value = { ...campaign.value, status }
}

const editable = computed(() => !!campaign.value && editableStatuses.includes(campaign.value.status))
const createdAt = computed(() => (campaign.value ? formatDateTime(campaign.value.created_at) : ''))

async function regenerate() {
  const c = campaign.value
  if (!c || generating.value) return
  if (c.contents?.length && !confirm(t('audience.confirmRegenerate'))) return
  generating.value = true
  segmentError.value = null
  previewPerson.value = null // a new rule may not match them any more
  try {
    campaign.value = await generateSegment(c.id)
  } catch (e) {
    segmentError.value = e
    // The failure is recorded on the campaign (last_error); show the stored state.
    campaign.value = await getCampaign(c.id).catch(() => c)
  } finally {
    generating.value = false
  }
}
</script>

<template>
  <div class="space-y-6">
    <RouterLink to="/" class="inline-flex items-center gap-1 text-sm font-medium text-slate-500 hover:text-indigo-600">
      {{ t('nav.back') }}
    </RouterLink>

    <div v-if="notFound" class="rounded-2xl border border-slate-200 bg-white p-10 text-center shadow-sm">
      <p class="font-medium">{{ t('campaign.notFound', { id }) }}</p>
      <RouterLink to="/" class="mt-2 inline-block text-sm text-indigo-600 hover:underline">{{ t('campaign.backToList') }}</RouterLink>
    </div>

    <ErrorAlert v-else-if="loadError" :error="loadError" :title="t('campaign.loadError')">
      <button type="button" class="font-medium underline" @click="load(id)">{{ t('common.retry') }}</button>
    </ErrorAlert>

    <div v-else-if="!campaign" class="animate-pulse space-y-4" aria-busy="true">
      <div class="h-44 rounded-2xl bg-slate-200" />
      <div class="h-72 rounded-2xl bg-slate-200" />
    </div>

    <template v-else>
      <header class="relative overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
        <div
          class="pointer-events-none absolute -top-24 -right-16 size-72 rounded-full bg-linear-to-br from-indigo-100 to-violet-100 opacity-70 blur-3xl"
          aria-hidden="true"
        />
        <div class="relative space-y-5 px-5 pt-6 pb-5 sm:px-6">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div class="space-y-1.5">
              <div class="flex flex-wrap items-center gap-3">
                <h1 class="text-2xl font-bold tracking-tight text-slate-900">{{ t('campaign.title', { id: campaign.id }) }}</h1>
                <StatusBadge :status="campaign.status" />
              </div>
              <p class="flex items-center gap-1.5 text-sm text-slate-500">
                <svg viewBox="0 0 20 20" class="size-4" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
                  <circle cx="10" cy="10" r="7.5" />
                  <path d="M10 6v4l2.5 1.5" stroke-linecap="round" />
                </svg>
                {{ createdAt }}
              </p>
            </div>
            <div class="flex items-center gap-2 text-sm">
              <span class="sr-only">{{ t('campaign.channels') }}</span>
              <span
                v-for="ch in campaign.channels"
                :key="ch"
                class="inline-flex items-center gap-1.5 rounded-full border border-slate-200 bg-white px-3 py-1 font-medium text-slate-700 shadow-xs"
              >
                <ChannelIcon :channel="ch" class="size-4 text-indigo-600" />
                {{ channelLabel(ch) }}
              </span>
            </div>
          </div>
          <figure class="relative rounded-xl border border-amber-200/70 bg-amber-50/70 py-4 pr-4 pl-14">
            <span class="absolute inset-y-3 left-0 w-1 rounded-r-full bg-amber-400" aria-hidden="true" />
            <svg
              viewBox="0 0 24 24"
              class="absolute top-4 left-5 size-6 text-amber-500"
              fill="currentColor"
              aria-hidden="true"
            >
              <path d="M9.5 6C6.5 6.6 4.5 9 4.5 12.3V18h5.6v-5.6H7.4c.1-2 1.2-3.3 2.6-3.8L9.5 6zm9.4 0c-3 .6-5 3-5 6.3V18h5.6v-5.6h-2.7c.1-2 1.2-3.3 2.6-3.8L18.9 6z" />
            </svg>
            <p class="text-xs font-bold tracking-wider uppercase text-amber-800 mb-1 flex items-center gap-1.5"><span aria-hidden="true">✦</span> {{ t('new.brief') }}</p>
            <blockquote class="text-[15px] leading-relaxed whitespace-pre-line text-slate-800">{{ campaign.brief }}</blockquote>
          </figure>
        </div>
        <div class="relative border-t border-slate-100 bg-slate-50/70 px-5 py-4 sm:px-6">
          <StepProgress :status="campaign.status" :matched="campaign.audience?.matched" />
        </div>
      </header>

      <DeliveryDashboard
        v-if="delivering"
        :campaign-id="campaign.id"
        :channels="campaign.channels"
        :approved-at="campaign.approved_at"
        :scheduled-at="campaign.scheduled_at"
        :updated-at="campaign.updated_at"
        @status="onDeliveryStatus"
        @update="campaign = $event"
      />

      <SectionShell
        :step="1"
        heading-id="audience-heading"
        :title="t('audience.title')"
        :subtitle="campaign.audience ? t('steps.audience.done', { n: formatNumber(campaign.audience.matched) }) : undefined"
      >
        <template v-if="editable && campaign.segment_rule" #actions>
          <button
            type="button"
            :disabled="generating || contentBusy"
            class="rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 shadow-xs hover:bg-slate-50 disabled:opacity-50"
            @click="regenerate"
          >
            {{ generating ? t('audience.regenerating') : t('audience.regenerate') }}
          </button>
        </template>

        <ErrorAlert v-if="segmentError" :error="segmentError" :title="t('audience.error')" />
        <ErrorAlert v-else-if="campaign.last_error" :error="campaign.last_error" :title="t('audience.lastError')" />

        <div v-if="!campaign.segment_rule" class="rounded-xl border border-dashed border-slate-300 p-8 text-center">
          <p class="text-slate-600">{{ t('audience.none') }}</p>
          <button
            v-if="editable"
            type="button"
            :disabled="generating"
            class="mt-3 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-indigo-700 disabled:opacity-50"
            @click="regenerate"
          >
            {{ generating ? t('new.submitting') : t('new.submit') }}
          </button>
        </div>

        <div v-else class="grid gap-5 lg:grid-cols-12" :class="{ 'opacity-50': generating }">
          <div class="space-y-4 lg:col-span-5">
            <div class="rounded-xl border border-slate-200 bg-slate-50/60 p-4">
              <p class="mb-2 text-xs font-semibold tracking-wide text-slate-500 uppercase">{{ t('audience.who') }}</p>
              <RuleTree v-if="rule" :line="rule" />
              <details class="mt-3 text-xs text-slate-500">
                <summary class="cursor-pointer hover:text-slate-700">{{ t('audience.json') }}</summary>
                <pre class="mt-2 overflow-x-auto rounded-lg bg-white p-3 font-mono ring-1 ring-slate-200">{{ JSON.stringify(campaign.segment_rule, null, 2) }}</pre>
              </details>
            </div>
            <AudienceCounts v-if="campaign.audience" :audience="campaign.audience" :channels="campaign.channels" />
          </div>
          <AudiencePeople
            v-if="campaign.audience"
            class="lg:col-span-7"
            :campaign-id="campaign.id"
            :channels="campaign.channels"
            :matched="campaign.audience.matched"
            :rule-key="JSON.stringify(campaign.segment_rule)"
            :selected="previewPerson"
            @select="previewPerson = $event"
          />
        </div>
      </SectionShell>

      <ContentSection
        v-if="campaign.segment_rule"
        :campaign="campaign"
        :preview-person="previewPerson"
        @update="campaign = $event"
        @clear-preview="previewPerson = null"
        @busy="contentBusy = $event"
      />
    </template>
  </div>
</template>
