<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ApiError, editContent } from '@/api/client'
import type { Campaign, Channel, Content, Language, Person } from '@/api/types'
import { checkField, render, token, VARIABLES } from '@/lib/content'
import { channelLabel } from '@/lib/format'
import { languageLabel } from '@/lib/format'
import { t } from '@/lib/i18n'
import ChannelIcon from './ChannelIcon.vue'
import EmailPreview from './EmailPreview.vue'
import ErrorAlert from './ErrorAlert.vue'
import LengthCounter from './LengthCounter.vue'
import SmsPreview from './SmsPreview.vue'

const props = defineProps<{
  campaignId: number
  language: Language
  channel: Channel
  saved: Content
  variableLengths: Record<string, number>
  limits: Record<string, number>
  readonly: boolean
  // A real matching user to preview as; otherwise a typical sample.
  person?: Person | null
  // Text Claude is writing right now; shown read-only in place of the draft.
  streaming?: { subject?: string; body?: string } | null
}>()

const emit = defineEmits<{
  saved: [campaign: Campaign]
  state: [state: { dirty: boolean; valid: boolean }]
}>()

// A typical recipient of this language for the preview, unless the marketer
// picked a real one from the audience.
const samples: Record<Language, { first_name: string; city: string; phone: string; email: string }> = {
  tr: { first_name: 'Ayşe', city: 'İstanbul', phone: '+90 555 000 00 00', email: 'ayse@example.com' },
  en: { first_name: 'Emily', city: 'İzmir', phone: '+90 555 000 00 01', email: 'emily@example.com' },
}
const sample = computed(() => props.person ?? samples[props.language])
// Only the allowed variables are filled in, like the worker does.
const values = computed(() => ({ first_name: sample.value.first_name, city: sample.value.city }))
// The previewed person did not consent to this channel, so they are skipped.
const skipped = computed(
  () => !!props.person && !(props.channel === 'sms' ? props.person.sms_consent : props.person.email_consent),
)

const subject = ref('')
const body = ref('')
const saving = ref(false)
const saveError = ref<unknown>(null)

// A new version from the server (generation, save) replaces the draft.
watch(
  () => [props.saved.version, props.saved.updated_at],
  () => {
    subject.value = props.saved.subject ?? ''
    body.value = props.saved.body
    saveError.value = null
  },
  { immediate: true },
)

// While streaming, the fields follow the text as it arrives.
watch(
  () => props.streaming,
  (s) => {
    if (!s) return
    subject.value = s.subject ?? ''
    body.value = s.body ?? ''
  },
  { immediate: true, deep: true },
)

// The preview fills in variables; while streaming, a variable still being
// written ("{{first_") is held back instead of shown raw.
function preview(text: string): string {
  const shown = props.streaming ? text.replace(/\{\{[^}]*\}?$/, '') : text
  return render(shown.trim(), values.value)
}

const isEmail = computed(() => props.channel === 'email')
const subjectCheck = computed(() =>
  checkField(subject.value, props.limits['email.subject'] ?? 80, props.variableLengths, { singleLine: true }),
)
const bodyCheck = computed(() =>
  checkField(body.value, props.limits[`${props.channel}.body`] ?? 160, props.variableLengths),
)
const dirty = computed(() => body.value !== props.saved.body || (isEmail.value && subject.value !== (props.saved.subject ?? '')))
const valid = computed(() => bodyCheck.value.problems.length === 0 && (!isEmail.value || subjectCheck.value.problems.length === 0))

watch([dirty, valid], () => emit('state', { dirty: dirty.value, valid: valid.value }), { immediate: true })

// Server problems arrive with paths like "email.subject"; show them under the field.
const serverProblems = computed(() => (saveError.value instanceof ApiError ? saveError.value.problems : []))
// Anything not tied to one of this editor's fields goes in the error box.
const showSaveAlert = computed(() => {
  const fields = [`${props.channel}.body`, `${props.channel}.subject`]
  return !!saveError.value && (serverProblems.value.length === 0 || serverProblems.value.some((p) => !fields.includes(p.path)))
})
function problemsFor(field: 'subject' | 'body', local: string[]): string[] {
  // Half-written text (a variable cut in half) is checked once it is done.
  if (props.streaming) return []
  const path = `${props.channel}.${field}`
  return [...local, ...serverProblems.value.filter((p) => p.path === path).map((p) => p.message)]
}

const bodyField = ref<HTMLTextAreaElement>()

function insertVariable(name: string) {
  const el = bodyField.value
  const t = token(name)
  if (!el) {
    body.value += t
    return
  }
  const { selectionStart: start, selectionEnd: end } = el
  body.value = body.value.slice(0, start) + t + body.value.slice(end)
  requestAnimationFrame(() => {
    el.focus()
    el.setSelectionRange(start + t.length, start + t.length)
  })
}

function reset() {
  subject.value = props.saved.subject ?? ''
  body.value = props.saved.body
  saveError.value = null
}

async function save() {
  if (!dirty.value || !valid.value || saving.value) return
  saving.value = true
  saveError.value = null
  try {
    const edit = isEmail.value ? { subject: subject.value, body: body.value } : { body: body.value }
    emit('saved', await editContent(props.campaignId, props.language, props.channel, edit))
  } catch (e) {
    saveError.value = e
  } finally {
    saving.value = false
  }
}

const inputClass =
  'w-full rounded-lg border px-3 py-2 focus:ring-2 focus:outline-none disabled:bg-slate-50 disabled:text-slate-700'
function fieldClass(problems: string[]) {
  return problems.length
    ? `${inputClass} border-rose-400 focus:border-rose-500 focus:ring-rose-100`
    : `${inputClass} border-slate-300 focus:border-indigo-500 focus:ring-indigo-200`
}
</script>

<template>
  <article class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-xs">
    <header class="flex items-center justify-between gap-2 border-b border-slate-100 px-5 py-3">
      <h3 class="flex items-center gap-2 font-semibold text-slate-900">
        <span class="flex size-7 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600 ring-1 ring-indigo-100">
          <ChannelIcon :channel="channel" class="size-4" />
        </span>
        {{ channelLabel(channel) }}
        <span class="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] font-medium text-slate-500" :title="languageLabel(language)">
          {{ language.toUpperCase() }}
        </span>
      </h3>
      <span v-if="streaming" class="flex items-center gap-1.5 text-xs font-medium text-indigo-700">
        <span class="size-1.5 animate-pulse rounded-full bg-indigo-500" />{{ t('editor.writing') }}
      </span>
      <span v-else class="text-xs text-slate-500">
        {{ t('editor.version', { n: saved.version }) }}<template v-if="saved.human_edited"> · {{ t('editor.edited') }}</template>
        <template v-if="dirty"> · <span class="font-medium text-amber-600">{{ t('editor.unsaved') }}</span></template>
      </span>
    </header>

    <div class="grid lg:grid-cols-12">
      <div class="space-y-4 p-5 lg:col-span-7">
        <div v-if="isEmail">
          <div class="flex items-baseline justify-between">
            <label :for="`${language}-${channel}-subject`" class="text-sm font-medium">{{ t('editor.subject') }}</label>
            <LengthCounter
              :count="subjectCheck.worstCase"
              :limit="subjectCheck.limit"
              :variables="variableLengths"
            />
          </div>
          <input
            :id="`${language}-${channel}-subject`"
            v-model="subject"
            type="text"
            :disabled="readonly || saving || !!streaming"
            :class="fieldClass(problemsFor('subject', subjectCheck.problems))"
            class="mt-1"
          />
          <ul class="mt-1 space-y-0.5 text-xs text-rose-600">
            <li v-for="p in problemsFor('subject', subjectCheck.problems)" :key="p">{{ t('editor.subject') }}: {{ p }}</li>
          </ul>
        </div>

        <div>
          <div class="flex items-baseline justify-between">
            <label :for="`${language}-${channel}-body`" class="text-sm font-medium">{{ isEmail ? t('editor.body') : t('editor.message') }}</label>
            <LengthCounter :count="bodyCheck.worstCase" :limit="bodyCheck.limit" :variables="variableLengths" />
          </div>
          <textarea
            :id="`${language}-${channel}-body`"
            ref="bodyField"
            v-model="body"
            :rows="isEmail ? 8 : 3"
            :disabled="readonly || saving || !!streaming"
            :class="fieldClass(problemsFor('body', bodyCheck.problems))"
            class="mt-1"
          />
          <ul class="mt-1 space-y-0.5 text-xs text-rose-600">
            <li v-for="p in problemsFor('body', bodyCheck.problems)" :key="p">{{ isEmail ? t('editor.body') : t('editor.message') }}: {{ p }}</li>
          </ul>
          <div v-if="!readonly && !streaming" class="mt-2 flex flex-wrap items-center gap-2 text-xs text-slate-500">
            <span>{{ t('editor.insert') }}</span>
            <button
              v-for="v in VARIABLES"
              :key="v"
              type="button"
              :disabled="saving"
              class="rounded-md border border-indigo-200 bg-indigo-50 px-1.5 py-0.5 font-mono text-indigo-700 hover:bg-indigo-100"
              @click="insertVariable(v)"
            >
              {{ token(v) }}
            </button>
          </div>
        </div>

        <ErrorAlert v-if="showSaveAlert" :error="saveError" :title="t('editor.saveError')" />

        <footer v-if="!readonly && !streaming && dirty" class="flex gap-2 border-t border-slate-100 pt-4">
          <button
            type="button"
            :disabled="!valid || saving"
            class="rounded-lg bg-indigo-600 px-3 py-1.5 text-sm font-semibold text-white shadow-sm hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50"
            @click="save"
          >
            {{ saving ? t('editor.saving') : t('editor.save') }}
          </button>
          <button
            type="button"
            :disabled="saving"
            class="rounded-lg border border-slate-300 px-3 py-1.5 text-sm font-medium hover:bg-slate-50"
            @click="reset"
          >
            {{ t('editor.discard') }}
          </button>
        </footer>
      </div>
      <div class="preview-stage border-t border-slate-100 p-5 lg:col-span-5 lg:border-t-0 lg:border-l">
        <div class="space-y-3 lg:sticky lg:top-24">
          <p class="text-[11px] font-semibold tracking-wider text-slate-500 uppercase">
            {{ t('preview.title', { name: sample.first_name, city: sample.city }) }}
          </p>
          <p v-if="skipped" class="rounded-md bg-amber-50 px-2 py-1 text-xs text-amber-800">
            {{ t('preview.noConsent', { name: sample.first_name, channel: channelLabel(channel) }) }}
          </p>
          <EmailPreview
            v-if="isEmail"
            :subject="preview(subject)"
            :body="preview(body)"
            :recipient="`${sample.first_name} <${sample.email}>`"
            :class="{ 'opacity-60': skipped }"
          />
          <SmsPreview v-else :text="preview(body)" :recipient="sample.phone" :class="{ 'opacity-60': skipped }" />
        </div>
      </div>
    </div>
  </article>
</template>
