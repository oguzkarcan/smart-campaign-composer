<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { getRecipients } from '@/api/client'
import type { Language, Person } from '@/api/types'
import { t } from '@/lib/i18n'
import { SAMPLE_RECIPIENTS } from '@/lib/languages'

// Who the previews are filled in for: the language's sample or one of the first
// matching people. The full list stays in the Audience step.
const props = defineProps<{
  campaignId: number
  language: Language
  // Changes when the segment is regenerated, so the list reloads.
  ruleKey: string
  selected: Person | null
  disabled?: boolean
}>()
const emit = defineEmits<{ select: [person: Person | null] }>()

const LIMIT = 30

const people = ref<Person[]>([])
let controller: AbortController | undefined

async function load() {
  controller?.abort()
  controller = new AbortController()
  try {
    people.value = (await getRecipients(props.campaignId, { limit: LIMIT }, controller.signal)).recipients
  } catch {
    // The sample still works, and the Audience step reports the list's errors.
    people.value = []
  }
}
watch(() => props.ruleKey, load, { immediate: true })
onUnmounted(() => controller?.abort())

// Someone picked further down the Audience list is not among the first ones.
const options = computed(() => {
  const s = props.selected
  return s && !people.value.some((p) => p.id === s.id) ? [s, ...people.value] : people.value
})
const sample = computed(() => SAMPLE_RECIPIENTS[props.language])

function onChange(e: Event) {
  const id = Number((e.target as HTMLSelectElement).value)
  emit('select', options.value.find((p) => p.id === id) ?? null)
}
</script>

<template>
  <label class="flex max-w-full items-center gap-2 self-center rounded-full border border-slate-200 bg-white py-1 pr-1.5 pl-3 text-xs font-semibold text-slate-500 shadow-xs">
    <span class="shrink-0">{{ t('preview.asLabel') }}</span>
    <select
      :value="selected?.id ?? ''"
      :disabled="disabled"
      class="min-w-0 cursor-pointer rounded-full bg-indigo-50 py-0.5 pr-1 pl-2 font-semibold text-indigo-700 focus:ring-2 focus:ring-indigo-200 focus:outline-none disabled:cursor-not-allowed disabled:opacity-60"
      @change="onChange"
    >
      <option value="">{{ t('preview.sample', { name: sample.first_name, city: sample.city }) }}</option>
      <option v-for="p in options" :key="p.id" :value="p.id">{{ p.first_name }} · {{ p.city }} · {{ p.language.toUpperCase() }}</option>
    </select>
  </label>
</template>
