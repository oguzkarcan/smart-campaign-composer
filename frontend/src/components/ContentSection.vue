<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { approveCampaign, generateContent } from '@/api/client'
import type { Campaign, Content, Language } from '@/api/types'
import { channelLabels, formatDateTime, formatNumber, languageLabels } from '@/lib/format'
import { languageTabs } from '@/lib/languages'
import { defaultSchedule, MAX_SCHEDULE_DAYS, parseSchedule, toLocalInput } from '@/lib/schedule'
import ChannelEditor from './ChannelEditor.vue'
import ErrorAlert from './ErrorAlert.vue'

const props = defineProps<{ campaign: Campaign }>()
const emit = defineEmits<{ update: [campaign: Campaign]; busy: [busy: boolean] }>()

const generating = ref(false)
const attempt = ref(1)
const streamLanguage = ref<Language | null>(null)
const streamed = ref('')
const generateError = ref<unknown>(null)
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

const status = computed(() => props.campaign.status)
const tabs = computed(() => languageTabs(props.campaign))
const activeLanguage = ref<Language>('tr')
watch(
  tabs,
  (t) => {
    if (t.length && !t.some((tab) => tab.language === activeLanguage.value)) activeLanguage.value = t[0]!.language
  },
  { immediate: true },
)
// Messages in display order: by language, then the campaign's channel order.
const contents = computed(() => {
  const byKey = new Map((props.campaign.contents ?? []).map((c) => [`${c.language}.${c.channel}`, c]))
  return tabs.value.flatMap((t) =>
    props.campaign.channels.map((ch) => byKey.get(`${t.language}.${ch}`)).filter((c): c is Content => !!c),
  )
})
const missingLanguages = computed(() => tabs.value.filter((t) => !t.hasContent).map((t) => t.language))
function tabState(lang: Language): 'dirty' | 'invalid' | null {
  const states = contents.value
    .filter((c) => c.language === lang)
    .map((c) => editorStates[`${c.language}.${c.channel}`])
    .filter((s) => !!s)
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
    return `Generate the messages again to include ${missingLanguages.value.map((l) => languageLabels[l]).join(' and ')}.`
  if (anyDirty.value) return 'Save or discard your changes in every language before approving.'
  if (!allValid.value) return 'Fix the problems in every language before approving.'
  return ''
})

async function generate() {
  if (!canGenerate.value || generating.value) return
  if ((anyDirty.value || contents.value.some((c) => c.human_edited)) && !confirm('New messages replace your edits. Continue?')) return
  controller = new AbortController()
  generating.value = true
  emit('busy', true)
  generateError.value = null
  approveError.value = null
  attempt.value = 1
  streamLanguage.value = null
  streamed.value = ''
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
  }
}

function cancel() {
  controller?.abort()
}

// Unsaved edits would be lost by leaving the page or closing the tab.
const LEAVE_PROMPT = 'You have unsaved message changes. Leave without saving?'
function guardLeave() {
  return !anyDirty.value || confirm(LEAVE_PROMPT)
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
  const parts = props.campaign.channels.map((ch) => `${formatNumber(a?.[ch] ?? 0)} ${channelLabels[ch]}`)
  checkedAt.value = new Date()
  const s = schedule.value
  if (s?.error) return // shown under the time picker
  const action = s?.sendAt
    ? `Schedule ${parts.join(' + ')} messages for ${formatDateTime(s.sendAt)}?`
    : `Approve and send ${parts.join(' + ')} messages now?`
  if (!confirm(`${action} The content is locked after this.`)) return
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
  <section class="space-y-4" aria-labelledby="content-heading">
    <div class="flex items-center justify-between gap-4">
      <h2 id="content-heading" class="text-lg font-semibold">Messages</h2>
      <button
        v-if="canGenerate && contents.length && !generating"
        type="button"
        :disabled="approving"
        class="rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium hover:bg-slate-50 disabled:opacity-50"
        @click="generate"
      >
        Regenerate messages
      </button>
    </div>

    <ErrorAlert v-if="generateError" :error="generateError" title="Claude could not write valid messages" />

    <!-- Live generation: the model's raw output as it streams in. -->
    <div v-if="generating" class="space-y-2 rounded-xl border border-indigo-200 bg-white p-4" aria-live="polite">
      <div class="flex items-center justify-between gap-2 text-sm">
        <span class="flex items-center gap-2 text-indigo-700">
          <span class="size-2 animate-pulse rounded-full bg-indigo-500" />
          <template v-if="attempt === 1">
            Claude is writing the {{ streamLanguage ? languageLabels[streamLanguage] : '' }} messages…
          </template>
          <template v-else>
            Fixing the {{ streamLanguage ? languageLabels[streamLanguage] : '' }} messages (attempt {{ attempt }})…
          </template>
          <span v-if="tabs.length > 1 && streamLanguage" class="text-slate-500">
            ({{ tabs.findIndex((t) => t.language === streamLanguage) + 1 }} of {{ tabs.length }} languages)
          </span>
        </span>
        <button type="button" class="text-slate-500 hover:text-slate-800" @click="cancel">Cancel</button>
      </div>
      <pre class="max-h-72 overflow-auto rounded-lg bg-slate-900 p-3 font-mono text-xs whitespace-pre-wrap text-slate-100">{{ streamed || ' ' }}<span class="animate-pulse">▍</span></pre>
    </div>

    <template v-else-if="!contents.length">
      <div v-if="canGenerate" class="rounded-xl border border-dashed border-slate-300 bg-white p-6 text-center">
        <template v-if="reachable > 0">
          <p class="text-slate-600">Next, let Claude write the messages for this audience.</p>
          <button
            type="button"
            class="mt-3 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700"
            @click="generate"
          >
            Generate messages
          </button>
        </template>
        <p v-else class="text-slate-600">
          Nobody in this audience can be reached on the chosen channels. Change the brief or the channels.
        </p>
      </div>
      <p v-else class="text-sm text-slate-500">Messages are written once the audience is ready.</p>
    </template>

    <template v-if="contents.length && !generating">
      <div v-if="tabs.length > 1" role="tablist" aria-label="Message language" class="flex flex-wrap gap-2">
        <button
          v-for="t in tabs"
          :key="t.language"
          type="button"
          role="tab"
          :aria-selected="activeLanguage === t.language"
          class="flex items-center gap-2 rounded-lg border px-3 py-1.5 text-sm font-medium"
          :class="
            activeLanguage === t.language
              ? 'border-indigo-600 bg-indigo-50 text-indigo-800'
              : 'border-slate-300 bg-white text-slate-700 hover:bg-slate-50'
          "
          @click="activeLanguage = t.language"
        >
          {{ languageLabels[t.language] }}
          <span class="font-normal text-slate-500 tabular-nums">{{ formatNumber(t.recipients) }} messages</span>
          <span v-if="!t.hasContent" class="size-2 rounded-full bg-rose-500" title="No messages yet" />
          <span v-else-if="tabState(t.language) === 'invalid'" class="size-2 rounded-full bg-rose-500" title="Has problems" />
          <span v-else-if="tabState(t.language) === 'dirty'" class="size-2 rounded-full bg-amber-500" title="Unsaved changes" />
        </button>
      </div>
      <p v-if="tabs.length > 1" class="text-sm text-slate-500">
        Each recipient gets the message in their own language.
      </p>

      <p
        v-if="missingLanguages.includes(activeLanguage)"
        class="rounded-xl border border-dashed border-rose-300 bg-white p-4 text-sm text-slate-600"
      >
        This audience now includes {{ languageLabels[activeLanguage] }} readers, but there are no
        {{ languageLabels[activeLanguage] }} messages yet. Generate the messages again to write them.
      </p>

      <template v-for="t in tabs" :key="t.language">
        <div
          v-show="t.language === activeLanguage"
          class="grid gap-4"
          :class="{ 'lg:grid-cols-2': campaign.channels.length > 1 }"
        >
          <ChannelEditor
            v-for="c in contents.filter((m) => m.language === t.language)"
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
          />
        </div>
      </template>

      <ErrorAlert v-if="approveError" :error="approveError" title="Could not approve" />

      <div v-if="status === 'content_ready'" class="space-y-3 rounded-xl border border-slate-200 bg-white p-4">
        <fieldset class="space-y-2" :disabled="!editable">
          <legend class="text-sm font-medium text-slate-700">When should the messages go out?</legend>
          <div class="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
            <label class="flex items-center gap-2">
              <input v-model="when" type="radio" value="now" class="accent-emerald-600" />
              Send now
            </label>
            <label class="flex items-center gap-2">
              <input v-model="when" type="radio" value="later" class="accent-emerald-600" />
              Schedule for later
            </label>
            <input
              v-if="when === 'later'"
              v-model="scheduleInput"
              type="datetime-local"
              aria-label="Send date and time"
              :min="scheduleMin"
              :max="scheduleMax"
              class="rounded-lg border border-slate-300 px-2 py-1 text-sm"
              :class="{ 'border-rose-400': schedule?.error }"
            />
          </div>
          <p v-if="schedule?.error" class="text-sm text-rose-600">{{ schedule.error }}</p>
          <p v-else-if="schedule?.sendAt" class="text-sm text-slate-500">
            Recipients and content are fixed now; sending starts {{ formatDateTime(schedule.sendAt) }} (your local time).
          </p>
        </fieldset>
        <div class="flex flex-wrap items-center gap-3">
          <button
            type="button"
            :disabled="!canApprove"
            class="rounded-lg bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-700 disabled:cursor-not-allowed disabled:opacity-50"
            @click="approve"
          >
            {{ approving ? 'Approving…' : when === 'later' ? 'Approve and schedule' : 'Approve and send' }}
          </button>
          <span v-if="approveHint" class="text-sm text-slate-500">{{ approveHint }}</span>
        </div>
      </div>
      <p v-else-if="status === 'scheduled' && campaign.scheduled_at" class="text-sm text-slate-500">
        This content was approved and is locked; it will be sent {{ formatDateTime(campaign.scheduled_at) }}.
      </p>
      <p v-else class="text-sm text-slate-500">This content was approved and is locked.</p>
    </template>
  </section>
</template>
