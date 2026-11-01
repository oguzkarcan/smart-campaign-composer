<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { ApiError, generateSegment, getCampaign } from '@/api/client'
import type { Campaign, CampaignStatus, Person } from '@/api/types'
import AudienceCounts from '@/components/AudienceCounts.vue'
import AudiencePeople from '@/components/AudiencePeople.vue'
import BriefPill from '@/components/BriefPill.vue'
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
import { defaultStep, type StepNumber } from '@/lib/steps'

const props = defineProps<{ id: number }>()

const campaign = ref<Campaign | null>(null)
const loadError = ref<unknown>(null)
const segmentError = ref<unknown>(null)
const generating = ref(false)
// Content generation is streaming; the audience must not change meanwhile.
const contentBusy = ref(false)
// "Preview as this person": a matching user whose data fills the previews.
const previewPerson = ref<Person | null>(null)
const activeStep = ref<StepNumber>(1)

let controller: AbortController | undefined

async function load(id: number) {
  controller?.abort()
  controller = new AbortController()
  campaign.value = null
  previewPerson.value = null
  loadError.value = null
  segmentError.value = null
  try {
    const c = await getCampaign(id, controller.signal)
    activeStep.value = defaultStep(c.status)
    campaign.value = c
  } catch (e) {
    if (!controller.signal.aborted) loadError.value = e
  }
}

watch(() => props.id, load, { immediate: true })
onUnmounted(() => controller?.abort())

const notFound = computed(() => loadError.value instanceof ApiError && loadError.value.status === 404)
const rule = computed(() => (campaign.value?.segment_rule ? describeRule(campaign.value.segment_rule, locale.value) : null))
const delivering = computed(() => !!campaign.value && !editableStatuses.includes(campaign.value.status))

// Approving opens the live dashboard; cancelling a schedule goes back to the
// messages.
watch(
  () => [campaign.value?.id, delivering.value] as const,
  ([id, now], [prevId, before]) => {
    if (id === undefined || id !== prevId || now === before) return // a load, not a change
    activeStep.value = now ? 3 : 2
  },
)
// The Audience step can be taller than the screen; each step starts at the top.
watch(activeStep, () => window.scrollTo({ top: 0 }))

// Previewing as someone is only visible next to the messages.
function selectPerson(person: Person | null) {
  previewPerson.value = person
  if (person && campaign.value?.contents?.length) activeStep.value = 2
}

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
  <div class="space-y-3">
    <RouterLink v-if="!campaign" to="/" class="inline-flex items-center gap-1 text-sm font-medium text-slate-500 hover:text-indigo-600">
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
      <header class="rounded-2xl border border-slate-200 bg-white shadow-sm">
        <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 px-4 py-2">
          <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            <RouterLink to="/" class="text-sm font-medium text-slate-500 hover:text-indigo-600">{{ t('nav.back') }}</RouterLink>
            <h1 class="text-lg font-bold tracking-tight text-slate-900">{{ t('campaign.title', { id: campaign.id }) }}</h1>
            <StatusBadge :status="campaign.status" />
            <span class="text-xs text-slate-500">{{ createdAt }}</span>
          </div>
          <div class="flex items-center gap-1.5 text-xs">
            <span class="sr-only">{{ t('campaign.channels') }}</span>
            <span
              v-for="ch in campaign.channels"
              :key="ch"
              class="inline-flex items-center gap-1 rounded-full border border-slate-200 bg-white px-2.5 py-0.5 font-medium text-slate-700 shadow-xs"
            >
              <ChannelIcon :channel="ch" class="size-3.5 text-indigo-600" />
              {{ channelLabel(ch) }}
            </span>
          </div>
        </div>
        <div class="border-t border-slate-100 p-2">
          <StepProgress :status="campaign.status" :matched="campaign.audience?.matched" :active="activeStep" @select="activeStep = $event" />
        </div>
      </header>

      <!-- Hidden steps stay mounted: unsaved drafts, a running generation and the
           live delivery stream all live in them. -->
      <div
        v-if="delivering"
        v-show="activeStep === 3"
        id="step-panel-delivery"
        role="tabpanel"
        aria-labelledby="step-tab-delivery"
        class="space-y-3"
      >
        <BriefPill :brief="campaign.brief" />
        <DeliveryDashboard
          :campaign-id="campaign.id"
          :channels="campaign.channels"
          :approved-at="campaign.approved_at"
          :scheduled-at="campaign.scheduled_at"
          :updated-at="campaign.updated_at"
          @status="onDeliveryStatus"
          @update="campaign = $event"
        />
      </div>

      <div v-show="activeStep === 1" id="step-panel-audience" role="tabpanel" aria-labelledby="step-tab-audience" class="space-y-3">
        <figure class="rounded-xl border border-amber-200/70 bg-amber-50/70 px-4 py-3">
          <p class="text-xs font-bold tracking-wider uppercase text-amber-800 mb-1 flex items-center gap-1.5"><span aria-hidden="true">✦</span> {{ t('new.brief') }}</p>
          <blockquote class="text-[15px] leading-relaxed whitespace-pre-line text-slate-800">{{ campaign.brief }}</blockquote>
        </figure>

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
            @select="selectPerson"
          />
        </div>
      </SectionShell>

      </div>

      <div v-if="campaign.segment_rule" v-show="activeStep === 2" id="step-panel-messages" role="tabpanel" aria-labelledby="step-tab-messages">
        <ContentSection
          :campaign="campaign"
          :preview-person="previewPerson"
          @update="campaign = $event"
          @select-person="selectPerson"
          @busy="contentBusy = $event"
        />
      </div>
    </template>
  </div>
</template>
