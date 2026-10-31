<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ApiError, editContent } from '@/api/client'
import type { Campaign, Channel, Content, Language } from '@/api/types'
import { checkField, render, token, VARIABLES } from '@/lib/content'
import { channelLabel } from '@/lib/format'
import { t } from '@/lib/i18n'
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
}>()

const emit = defineEmits<{
  saved: [campaign: Campaign]
  state: [state: { dirty: boolean; valid: boolean }]
}>()

// A typical recipient of this language for the preview.
const samples: Record<Language, { first_name: string; city: string }> = {
  tr: { first_name: 'Ayşe', city: 'İstanbul' },
  en: { first_name: 'Emily', city: 'İzmir' },
}
const sample = computed(() => samples[props.language])

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
  <article class="space-y-4 rounded-xl border border-slate-200 bg-white p-5">
    <header class="flex items-center justify-between gap-2">
      <h3 class="font-semibold">{{ channelLabel(channel) }}</h3>
      <span class="text-xs text-slate-500">
        {{ t('editor.version', { n: saved.version }) }}<template v-if="saved.human_edited"> · {{ t('editor.edited') }}</template>
        <template v-if="dirty"> · <span class="font-medium text-amber-600">{{ t('editor.unsaved') }}</span></template>
      </span>
    </header>

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
        :disabled="readonly || saving"
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
        :disabled="readonly || saving"
        :class="fieldClass(problemsFor('body', bodyCheck.problems))"
        class="mt-1"
      />
      <ul class="mt-1 space-y-0.5 text-xs text-rose-600">
        <li v-for="p in problemsFor('body', bodyCheck.problems)" :key="p">{{ isEmail ? t('editor.body') : t('editor.message') }}: {{ p }}</li>
      </ul>
      <div v-if="!readonly" class="mt-2 flex flex-wrap items-center gap-2 text-xs text-slate-500">
        <span>{{ t('editor.insert') }}</span>
        <button
          v-for="v in VARIABLES"
          :key="v"
          type="button"
          :disabled="saving"
          class="rounded border border-slate-300 px-1.5 py-0.5 font-mono hover:bg-slate-50"
          @click="insertVariable(v)"
        >
          {{ token(v) }}
        </button>
      </div>
    </div>

    <div class="space-y-2 rounded-lg bg-slate-50 p-3">
      <p class="text-xs font-medium tracking-wide text-slate-500 uppercase">
        {{ t('preview.title', { name: sample.first_name, city: sample.city }) }}
      </p>
      <EmailPreview
        v-if="isEmail"
        :subject="render(subject.trim(), sample)"
        :body="render(body.trim(), sample)"
        :recipient="sample.first_name"
      />
      <SmsPreview v-else :text="render(body.trim(), sample)" :recipient="sample.first_name" />
    </div>

    <ErrorAlert v-if="showSaveAlert" :error="saveError" :title="t('editor.saveError')" />

    <footer v-if="!readonly && dirty" class="flex gap-2">
      <button
        type="button"
        :disabled="!valid || saving"
        class="rounded-lg bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50"
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
  </article>
</template>
