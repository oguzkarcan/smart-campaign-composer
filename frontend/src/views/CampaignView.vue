<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { ApiError, generateSegment, getCampaign } from '@/api/client'
import type { Campaign, CampaignStatus, Person } from '@/api/types'
import AudienceCounts from '@/components/AudienceCounts.vue'
import AudiencePeople from '@/components/AudiencePeople.vue'
import ContentSection from '@/components/ContentSection.vue'
import DeliveryDashboard from '@/components/DeliveryDashboard.vue'
import ErrorAlert from '@/components/ErrorAlert.vue'
import RuleTree from '@/components/RuleTree.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import StepProgress from '@/components/StepProgress.vue'
import { channelLabel, editableStatuses, formatDateTime } from '@/lib/format'
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
  <div class="space-y-8">
    <RouterLink to="/" class="text-sm text-indigo-600 hover:underline">{{ t('nav.back') }}</RouterLink>

    <div v-if="notFound" class="rounded-xl border border-slate-200 bg-white p-8 text-center">
      <p class="font-medium">{{ t('campaign.notFound', { id }) }}</p>
      <RouterLink to="/" class="mt-2 inline-block text-sm text-indigo-600 hover:underline">{{ t('campaign.backToList') }}</RouterLink>
    </div>

    <ErrorAlert v-else-if="loadError" :error="loadError" :title="t('campaign.loadError')">
      <button type="button" class="font-medium underline" @click="load(id)">{{ t('common.retry') }}</button>
    </ErrorAlert>

    <div v-else-if="!campaign" class="animate-pulse space-y-4" aria-busy="true">
      <div class="h-7 w-48 rounded bg-slate-200" />
      <div class="h-20 rounded-xl bg-slate-200" />
      <div class="h-40 rounded-xl bg-slate-200" />
    </div>

    <template v-else>
      <header class="space-y-3">
        <div class="flex flex-wrap items-center gap-3">
          <h1 class="text-2xl font-semibold tracking-tight">{{ t('campaign.title', { id: campaign.id }) }}</h1>
          <StatusBadge :status="campaign.status" />
          <span class="text-sm text-slate-500">{{ createdAt }}</span>
        </div>
        <blockquote class="rounded-xl border border-slate-200 bg-white p-4 whitespace-pre-line text-slate-700">
          {{ campaign.brief }}
        </blockquote>
        <div class="flex items-center gap-2 text-sm">
          <span class="text-slate-500">{{ t('campaign.channels') }}</span>
          <span v-for="ch in campaign.channels" :key="ch" class="rounded bg-slate-100 px-2 py-0.5 font-medium">
            {{ channelLabel(ch) }}
          </span>
        </div>
        <StepProgress
          :status="campaign.status"
          :matched="campaign.audience?.matched"
          class="rounded-xl border border-slate-200 bg-white px-4 py-3"
        />
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

      <section class="space-y-4" aria-labelledby="audience-heading">
        <div class="flex items-center justify-between gap-4">
          <h2 id="audience-heading" class="text-lg font-semibold">{{ t('audience.title') }}</h2>
          <button
            v-if="editable && campaign.segment_rule"
            type="button"
            :disabled="generating || contentBusy"
            class="rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium hover:bg-slate-50 disabled:opacity-50"
            @click="regenerate"
          >
            {{ generating ? t('audience.regenerating') : t('audience.regenerate') }}
          </button>
        </div>

        <ErrorAlert v-if="segmentError" :error="segmentError" :title="t('audience.error')" />
        <ErrorAlert v-else-if="campaign.last_error" :error="campaign.last_error" :title="t('audience.lastError')" />

        <div v-if="!campaign.segment_rule" class="rounded-xl border border-dashed border-slate-300 bg-white p-6 text-center">
          <p class="text-slate-600">{{ t('audience.none') }}</p>
          <button
            v-if="editable"
            type="button"
            :disabled="generating"
            class="mt-3 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50"
            @click="regenerate"
          >
            {{ generating ? t('new.submitting') : t('new.submit') }}
          </button>
        </div>

        <template v-else>
          <div class="rounded-xl border border-slate-200 bg-white p-4" :class="{ 'opacity-50': generating }">
            <p class="mb-2 text-sm text-slate-500">{{ t('audience.who') }}</p>
            <RuleTree v-if="rule" :line="rule" />
            <details class="mt-3 text-xs text-slate-500">
              <summary class="cursor-pointer">{{ t('audience.json') }}</summary>
              <pre class="mt-2 overflow-x-auto rounded bg-slate-50 p-3">{{ JSON.stringify(campaign.segment_rule, null, 2) }}</pre>
            </details>
          </div>
          <AudienceCounts v-if="campaign.audience" :audience="campaign.audience" :channels="campaign.channels" />
          <AudiencePeople
            v-if="campaign.audience"
            :campaign-id="campaign.id"
            :channels="campaign.channels"
            :matched="campaign.audience.matched"
            :rule-key="JSON.stringify(campaign.segment_rule)"
            :selected="previewPerson"
            @select="previewPerson = $event"
          />
        </template>
      </section>

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
