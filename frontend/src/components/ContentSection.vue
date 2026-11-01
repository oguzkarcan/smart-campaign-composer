<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { approveCampaign, generateContent } from '@/api/client'
import type { Campaign, Channel, Content, Language, Person } from '@/api/types'
import { channelLabel, formatDateTime, formatNumber, languageLabel } from '@/lib/format'
import { t } from '@/lib/i18n'
import { languageTabs } from '@/lib/languages'
import { defaultSchedule, MAX_SCHEDULE_DAYS, parseSchedule, toLocalInput } from '@/lib/schedule'
import { extractPartial, writingChannel } from '@/lib/streamExtract'
import BriefPill from './BriefPill.vue'
import ChannelEditor from './ChannelEditor.vue'
import ChannelIcon from './ChannelIcon.vue'
import DevicePreview from './DevicePreview.vue'
import ErrorAlert from './ErrorAlert.vue'
import PreviewPersonSelect from './PreviewPersonSelect.vue'

const props = defineProps<{ campaign: Campaign; previewPerson?: Person | null }>()
const emit = defineEmits<{ update: [campaign: Campaign]; busy: [busy: boolean]; selectPerson: [person: Person | null] }>()

const generating = ref(false)
const attempt = ref(1)
const streamLanguage = ref<Language | null>(null)
const streamed = ref('')
const generateError = ref<unknown>(null)
// The fields of the answer being streamed, for the live editors and previews.
const live = computed(() => extractPartial(streamed.value))
function liveFields(ch: Channel): { subject?: string; body?: string } {
  return ch === 'sms' ? { body: live.value.sms?.body } : { subject: live.value.email?.subject, body: live.value.email?.body }
}
// One channel is on screen at a time. While Claude writes, the view follows the
// channel being written unless the marketer picked one.
const activeChannel = ref<Channel>(props.campaign.channels[0]!)
let channelPicked = false
function pickChannel(ch: Channel) {
  activeChannel.value = ch
  channelPicked = true
}
watch(
  () => writingChannel(live.value),
  (ch) => {
    if (generating.value && ch && !channelPicked && props.campaign.channels.includes(ch)) activeChannel.value = ch
  },
)
// The live editors start from an empty message; only the streamed text shows.
function liveSaved(lang: Language, ch: Channel): Content {
  return { language: lang, channel: ch, subject: '', body: '', human_edited: false, version: 0, updated_at: '' }
}
let controller: AbortController | undefined

const approving = ref(false)
const approveError = ref<unknown>(null)
// "Send now" or "Schedule for later" (a datetime-local value, local time).
const when = ref<'now' | 'later'>('now')
const scheduleInput = ref(defaultSchedule(new Date()))
// The time the schedule is checked against; refreshed when the choice changes
// and on approve, since the page may have been open for a while.
const checkedAt = ref(new Date())
watch([when, scheduleInput], () => (checkedAt.value = new Date()))
const schedule = computed(() => (when.value === 'later' ? parseSchedule(scheduleInput.value, checkedAt.value) : null))
const scheduleMin = computed(() => toLocalInput(new Date()))
const scheduleMax = computed(() => toLocalInput(new Date(Date.now() + MAX_SCHEDULE_DAYS * 86_400_000)))

// Editor state by "language.channel"; every language's editors stay mounted
// (only the active tab is shown), so drafts survive switching tabs.
const editorStates = reactive<Record<string, { dirty: boolean; valid: boolean }>>({})
// Their current text, for the device preview.
const drafts = reactive<Record<string, { subject: string; body: string }>>({})

const status = computed(() => props.campaign.status)
const tabs = computed(() => languageTabs(props.campaign))
const activeLanguage = ref<Language>('tr')
watch(
  tabs,
  (list) => {
    if (list.length && !list.some((tab) => tab.language === activeLanguage.value)) activeLanguage.value = list[0]!.language
  },
  { immediate: true },
)
// Messages in display order: by language, then the campaign's channel order.
const contents = computed(() => {
  const byKey = new Map((props.campaign.contents ?? []).map((c) => [`${c.language}.${c.channel}`, c]))
  return tabs.value.flatMap((tab) =>
    props.campaign.channels.map((ch) => byKey.get(`${tab.language}.${ch}`)).filter((c): c is Content => !!c),
  )
})
// Previewing as a person shows their language's messages.
watch(
  () => props.previewPerson,
  (p) => {
    if (p && tabs.value.some((tab) => tab.language === p.language)) activeLanguage.value = p.language
  },
)
// Leaving the previewed person's language goes back to the sample.
function pickLanguage(lang: Language) {
  activeLanguage.value = lang
  if (props.previewPerson && props.previewPerson.language !== lang) emit('selectPerson', null)
}
const streamingNow = computed(() => generating.value && !!streamLanguage.value)
const activeDraft = computed(() => {
  if (streamingNow.value) return { subject: '', body: '', ...liveFields(activeChannel.value) }
  return drafts[`${activeLanguage.value}.${activeChannel.value}`] ?? { subject: '', body: '' }
})
const missingLanguages = computed(() => tabs.value.filter((tab) => !tab.hasContent).map((tab) => tab.language))
function channelState(ch: Channel): 'dirty' | 'invalid' | null {
  return worstState(contents.value.filter((c) => c.language === activeLanguage.value && c.channel === ch))
}
function tabState(lang: Language): 'dirty' | 'invalid' | null {
  return worstState(contents.value.filter((c) => c.language === lang))
}
function worstState(messages: Content[]): 'dirty' | 'invalid' | null {
  const states = messages.map((c) => editorStates[`${c.language}.${c.channel}`]).filter((s) => !!s)
  if (states.some((s) => !s.valid)) return 'invalid'
  if (states.some((s) => s.dirty)) return 'dirty'
  return null
}
const canGenerate = computed(() => status.value === 'audience_ready' || status.value === 'content_ready')
const reachable = computed(() => {
  const a = props.campaign.audience
  return a ? props.campaign.channels.reduce((n, ch) => n + a[ch], 0) : 0
})
const editable = computed(() => status.value === 'content_ready' && !generating.value && !approving.value)
// Only editors of current messages count; a dropped language's state is stale.
const currentStates = computed(() =>
  contents.value.map((c) => editorStates[`${c.language}.${c.channel}`]).filter((s) => !!s),
)
const anyDirty = computed(() => currentStates.value.some((s) => s.dirty))
const allValid = computed(() => currentStates.value.every((s) => s.valid))
const canApprove = computed(
  () =>
    editable.value &&
    contents.value.length > 0 &&
    !missingLanguages.value.length &&
    !anyDirty.value &&
    allValid.value &&
    !schedule.value?.error,
)
const approveHint = computed(() => {
  if (missingLanguages.value.length)
    return t('messages.hintMissing', {
      languages: missingLanguages.value.map((l) => languageLabel(l)).join(` ${t('messages.and')} `),
    })
  if (anyDirty.value) return t('messages.hintDirty')
  if (!allValid.value) return t('messages.hintInvalid')
  return ''
})

async function generate() {
  if (!canGenerate.value || generating.value) return
  if ((anyDirty.value || contents.value.some((c) => c.human_edited)) && !confirm(t('messages.confirmRegenerate'))) return
  controller = new AbortController()
  generating.value = true
  emit('busy', true)
  generateError.value = null
  approveError.value = null
  attempt.value = 1
  streamLanguage.value = null
  streamed.value = ''
  channelPicked = false
  try {
    const c = await generateContent(props.campaign.id, {
      signal: controller.signal,
      onDelta(lang, n, text) {
        if (lang !== streamLanguage.value || n !== attempt.value) {
          // The next language, or a repair round, starts a new answer.
          streamLanguage.value = lang
          attempt.value = n
          streamed.value = ''
          activeLanguage.value = lang
        }
        streamed.value += text
      },
    })
    emit('update', c)
  } catch (e) {
    if (!controller.signal.aborted) generateError.value = e
  } finally {
    generating.value = false
    emit('busy', false)
    if (!channelPicked) activeChannel.value = props.campaign.channels[0]!
  }
}

function cancel() {
  controller?.abort()
}

// Unsaved edits would be lost by leaving the page or closing the tab.
function guardLeave() {
  return !anyDirty.value || confirm(t('messages.leave'))
}
onBeforeRouteLeave(guardLeave)
onBeforeRouteUpdate(guardLeave)
function onBeforeUnload(e: BeforeUnloadEvent) {
  if (anyDirty.value) e.preventDefault()
}
onMounted(() => window.addEventListener('beforeunload', onBeforeUnload))
onUnmounted(() => {
  window.removeEventListener('beforeunload', onBeforeUnload)
  cancel()
})

async function approve() {
  if (!canApprove.value) return
  const a = props.campaign.audience
  const parts = props.campaign.channels.map((ch) => `${formatNumber(a?.[ch] ?? 0)} ${channelLabel(ch)}`).join(' + ')
  checkedAt.value = new Date()
  const s = schedule.value
  if (s?.error) return // shown under the time picker
  const question = s?.sendAt
    ? t('messages.confirmSchedule', { parts, date: formatDateTime(s.sendAt) })
    : t('messages.confirmSend', { parts })
  if (!confirm(question)) return
  approving.value = true
  approveError.value = null
  try {
    emit('update', await approveCampaign(props.campaign.id, s?.sendAt))
  } catch (e) {
    approveError.value = e
  } finally {
    approving.value = false
  }
}
</script>

<template>
  <section
    class="grid rounded-2xl border border-slate-200 bg-white shadow-sm lg:h-[calc(100dvh-14rem)] lg:min-h-[26rem] lg:grid-cols-12 lg:grid-rows-1"
    aria-labelledby="content-heading"
  >
    <h2 id="content-heading" class="sr-only">{{ t('messages.title') }}</h2>

    <!-- Editor and approval -->
    <div class="flex min-h-0 min-w-0 flex-col gap-3 p-4 lg:col-span-7">
      <div class="flex shrink-0 flex-wrap items-center gap-2">
        <BriefPill :brief="campaign.brief" />
        <button
          v-if="canGenerate && contents.length && !generating"
          type="button"
          :disabled="approving"
          class="inline-flex shrink-0 items-center gap-1.5 rounded-full border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 shadow-xs hover:bg-slate-50 disabled:opacity-50"
          @click="generate"
        >
          <span aria-hidden="true" class="text-indigo-500">✦</span>
          {{ t('messages.regenerate') }}
        </button>
      </div>

      <div class="-mx-1 flex min-h-0 flex-1 flex-col gap-3 px-1 py-0.5 lg:overflow-y-auto">
        <ErrorAlert v-if="generateError" :error="generateError" :title="t('messages.error')" />

        <!-- Live generation: progress for the marketer, the model's raw output on demand. -->
        <div
          v-if="generating"
          class="shrink-0 rounded-xl border border-indigo-200 bg-linear-to-r from-indigo-50 to-violet-50 px-3 py-2"
          aria-live="polite"
        >
          <div class="flex items-center justify-between gap-2 text-sm">
            <span class="flex flex-wrap items-center gap-2 text-indigo-700">
              <span class="size-2 animate-pulse rounded-full bg-indigo-500" />
              <template v-if="!streamLanguage">{{ t('messages.writingAny') }}</template>
              <template v-else-if="attempt === 1">{{ t('messages.writing', { language: languageLabel(streamLanguage) }) }}</template>
              <template v-else>{{ t('messages.repairing', { language: languageLabel(streamLanguage), n: attempt }) }}</template>
              <span v-if="tabs.length > 1 && streamLanguage" class="text-slate-500">
                {{ t('messages.languageOf', { i: tabs.findIndex((tab) => tab.language === streamLanguage) + 1, n: tabs.length }) }}
              </span>
            </span>
            <button type="button" class="text-slate-500 hover:text-slate-800" @click="cancel">{{ t('messages.cancel') }}</button>
          </div>
          <details class="mt-1 text-xs text-slate-500">
            <summary class="cursor-pointer">{{ t('messages.raw') }}</summary>
            <pre class="mt-2 max-h-28 overflow-auto rounded-lg bg-slate-900 p-3 font-mono whitespace-pre-wrap text-slate-100 dark:bg-black dark:text-slate-800">{{ streamed || ' ' }}<span class="animate-pulse">▍</span></pre>
          </details>
        </div>

        <div
          v-if="contents.length || streamingNow"
          class="flex shrink-0 flex-wrap items-center justify-between gap-2"
        >
          <div
            v-if="tabs.length > 1"
            role="tablist"
            :aria-label="t('messages.tabs')"
            class="inline-flex flex-wrap gap-1 rounded-full bg-slate-100 p-1"
          >
            <button
              v-for="tab in tabs"
              :key="tab.language"
              type="button"
              role="tab"
              :aria-selected="activeLanguage === tab.language"
              :disabled="generating"
              class="flex items-center gap-2 rounded-full px-3 py-0.5 text-sm font-semibold transition-colors"
              :class="
                activeLanguage === tab.language
                  ? 'bg-white text-indigo-700 shadow-sm'
                  : 'text-slate-600 hover:text-slate-900 disabled:opacity-60 disabled:hover:text-slate-600'
              "
              :title="t('messages.tabCount', { n: formatNumber(tab.recipients) })"
              @click="pickLanguage(tab.language)"
            >
              {{ languageLabel(tab.language) }}
              <span class="font-normal text-slate-500 tabular-nums">{{ formatNumber(tab.recipients) }}</span>
              <template v-if="generating">
                <span v-if="tab.language === streamLanguage" class="size-2 animate-pulse rounded-full bg-indigo-500" />
              </template>
              <span v-else-if="!tab.hasContent" class="size-2 rounded-full bg-rose-500" :title="t('messages.noMessagesYet')" />
              <span
                v-else-if="tabState(tab.language) === 'invalid'"
                class="size-2 rounded-full bg-rose-500"
                :title="t('messages.hasProblems')"
              />
              <span
                v-else-if="tabState(tab.language) === 'dirty'"
                class="size-2 rounded-full bg-amber-500"
                :title="t('messages.unsaved')"
              />
            </button>
          </div>

          <div
            v-if="campaign.channels.length > 1"
            role="tablist"
            :aria-label="t('messages.channelTabs')"
            class="inline-flex gap-1 rounded-full bg-slate-100 p-1"
          >
            <button
              v-for="ch in campaign.channels"
              :key="ch"
              type="button"
              role="tab"
              :aria-selected="activeChannel === ch"
              class="flex items-center gap-1.5 rounded-full px-3 py-0.5 text-sm font-semibold transition-colors"
              :class="activeChannel === ch ? 'bg-white text-indigo-700 shadow-sm' : 'text-slate-600 hover:text-slate-900'"
              @click="pickChannel(ch)"
            >
              <ChannelIcon :channel="ch" class="size-4" />
              {{ channelLabel(ch) }}
              <template v-if="!generating">
                <span v-if="channelState(ch) === 'invalid'" class="size-2 rounded-full bg-rose-500" :title="t('messages.hasProblems')" />
                <span v-else-if="channelState(ch) === 'dirty'" class="size-2 rounded-full bg-amber-500" :title="t('messages.unsaved')" />
              </template>
            </button>
          </div>
        </div>

        <!-- The answer being written, field by field, in the same editors and preview. -->
        <template v-if="generating && streamLanguage">
          <ChannelEditor
            v-for="ch in campaign.channels"
            v-show="ch === activeChannel"
            :key="`live.${streamLanguage}.${ch}`"
            :campaign-id="campaign.id"
            :language="streamLanguage"
            :channel="ch"
            :saved="liveSaved(streamLanguage, ch)"
            :streaming="liveFields(ch)"
            :variable-lengths="campaign.variable_lengths?.[streamLanguage] ?? {}"
            :limits="campaign.limits"
            readonly
          />
        </template>

        <div
          v-if="!generating && !contents.length"
          class="flex flex-1 flex-col items-center justify-center rounded-xl border border-dashed border-indigo-200 bg-indigo-50/40 p-8 text-center"
        >
          <template v-if="canGenerate">
            <template v-if="reachable > 0">
              <p class="text-slate-600">{{ t('messages.next') }}</p>
              <button
                type="button"
                class="mt-3 inline-flex items-center gap-2 rounded-lg bg-linear-to-r from-indigo-600 to-violet-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:from-indigo-700 hover:to-violet-700"
                @click="generate"
              >
                {{ t('messages.generate') }}
              </button>
            </template>
            <p v-else class="text-slate-600">{{ t('messages.unreachable') }}</p>
          </template>
          <p v-else class="text-sm text-slate-500">{{ t('messages.afterAudience') }}</p>
        </div>

        <template v-if="contents.length && !generating">
          <p
            v-if="missingLanguages.includes(activeLanguage)"
            class="rounded-xl border border-dashed border-rose-300 bg-white p-4 text-sm text-slate-600"
          >
            {{ t('messages.missing', { language: languageLabel(activeLanguage) }) }}
          </p>

          <!-- Every editor stays mounted, so drafts survive switching language or channel. -->
          <ChannelEditor
            v-for="c in contents"
            v-show="c.language === activeLanguage && c.channel === activeChannel"
            :key="`${c.language}.${c.channel}`"
            :campaign-id="campaign.id"
            :language="c.language"
            :channel="c.channel"
            :saved="c"
            :variable-lengths="campaign.variable_lengths?.[c.language] ?? {}"
            :limits="campaign.limits"
            :readonly="!editable"
            @saved="emit('update', $event)"
            @state="(s) => (editorStates[`${c.language}.${c.channel}`] = s)"
            @draft="(d) => (drafts[`${c.language}.${c.channel}`] = d)"
          />

          <ErrorAlert v-if="approveError" :error="approveError" :title="t('messages.approveError')" />
        </template>
      </div>

      <!-- Approval, docked under the editor so it never scrolls away. -->
      <template v-if="contents.length && !generating">
        <div v-if="status === 'content_ready'" class="shrink-0 space-y-1.5 rounded-xl border border-emerald-200 bg-emerald-50/40 px-3 py-2">
          <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
            <fieldset class="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm" :disabled="!editable">
              <legend class="sr-only">{{ t('messages.when') }}</legend>
              <label class="flex items-center gap-1.5">
                <input v-model="when" type="radio" value="now" class="accent-emerald-600" />
                {{ t('messages.sendNow') }}
              </label>
              <label class="flex items-center gap-1.5">
                <input v-model="when" type="radio" value="later" class="accent-emerald-600" />
                {{ t('messages.later') }}
              </label>
              <input
                v-if="when === 'later'"
                v-model="scheduleInput"
                type="datetime-local"
                :aria-label="t('messages.sendTime')"
                :min="scheduleMin"
                :max="scheduleMax"
                class="rounded-lg border border-slate-300 px-2 py-0.5 text-sm"
                :class="{ 'border-rose-400': schedule?.error }"
              />
            </fieldset>
            <button
              type="button"
              :disabled="!canApprove"
              class="ml-auto rounded-lg bg-emerald-600 px-4 py-1.5 text-sm font-semibold text-white shadow-sm hover:bg-emerald-700 disabled:cursor-not-allowed disabled:opacity-50"
              @click="approve"
            >
              {{ approving ? t('messages.approving') : when === 'later' ? t('messages.approveSchedule') : t('messages.approveSend') }}
            </button>
          </div>
          <p v-if="schedule?.error" class="text-xs text-rose-600">{{ t(`schedule.${schedule.error}`, { days: MAX_SCHEDULE_DAYS }) }}</p>
          <p v-else-if="approveHint" class="text-xs text-slate-500">{{ approveHint }}</p>
          <p v-else-if="schedule?.sendAt" class="text-xs text-slate-500">
            {{ t('messages.fixedNow', { date: formatDateTime(schedule.sendAt) }) }}
          </p>
        </div>
        <p v-else-if="status === 'scheduled' && campaign.scheduled_at" class="shrink-0 text-sm text-slate-500">
          {{ t('messages.lockedScheduled', { date: formatDateTime(campaign.scheduled_at) }) }}
        </p>
        <p v-else class="shrink-0 text-sm text-slate-500">{{ t('messages.locked') }}</p>
      </template>
    </div>

    <!-- Live device stage -->
    <div class="preview-stage flex min-h-0 min-w-0 flex-col gap-3 rounded-b-2xl border-t border-slate-100 p-4 lg:col-span-5 lg:rounded-l-none lg:rounded-r-2xl lg:border-t-0 lg:border-l">
      <PreviewPersonSelect
        :campaign-id="campaign.id"
        :language="activeLanguage"
        :rule-key="JSON.stringify(campaign.segment_rule)"
        :selected="previewPerson ?? null"
        :disabled="generating"
        @select="emit('selectPerson', $event)"
      />
      <DevicePreview
        class="min-h-0 flex-1"
        :language="activeLanguage"
        :channel="activeChannel"
        :subject="activeDraft.subject"
        :body="activeDraft.body"
        :person="previewPerson?.language === activeLanguage ? previewPerson : null"
        :streaming="streamingNow"
      />
    </div>
  </section>
</template>
