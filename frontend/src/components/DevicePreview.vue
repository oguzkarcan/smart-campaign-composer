<script setup lang="ts">
import { computed } from 'vue'
import type { Channel, Language, Person } from '@/api/types'
import { render } from '@/lib/content'
import { channelLabel } from '@/lib/format'
import { t } from '@/lib/i18n'
import { SAMPLE_RECIPIENTS } from '@/lib/languages'
import EmailPreview from './EmailPreview.vue'
import SmsPreview from './SmsPreview.vue'

// The active message on its device, filled in for the previewed person.
const props = defineProps<{
  language: Language
  channel: Channel
  subject: string
  body: string
  // A real matching user to preview as; otherwise a typical sample.
  person?: Person | null
  // Claude is still writing this text.
  streaming?: boolean
}>()

const sample = computed(() => props.person ?? SAMPLE_RECIPIENTS[props.language])
// Only the allowed variables are filled in, like the worker does.
const values = computed(() => ({ first_name: sample.value.first_name, city: sample.value.city }))
// The previewed person did not consent to this channel, so they are skipped.
const skipped = computed(
  () => !!props.person && !(props.channel === 'sms' ? props.person.sms_consent : props.person.email_consent),
)

// While streaming, a variable still being written ("{{first_") is held back
// instead of shown raw.
function preview(text: string): string {
  const shown = props.streaming ? text.replace(/\{\{[^}]*\}?$/, '') : text
  return render(shown.trim(), values.value)
}
</script>

<template>
  <div class="flex flex-col items-center justify-center gap-2">
    <p v-if="skipped" class="shrink-0 rounded-md bg-amber-50 px-2 py-1 text-xs text-amber-800">
      {{ t('preview.noConsent', { name: sample.first_name, channel: channelLabel(channel) }) }}
    </p>
    <EmailPreview
      v-if="channel === 'email'"
      class="w-full lg:max-h-[36rem] lg:min-h-0 lg:flex-1"
      :subject="preview(subject)"
      :body="preview(body)"
      :recipient="`${sample.first_name} <${sample.email}>`"
      :class="{ 'opacity-60': skipped }"
    />
    <SmsPreview
      v-else
      class="h-[28rem] lg:h-auto lg:max-h-[36rem] lg:min-h-0 lg:flex-1"
      :text="preview(body)"
      :recipient="sample.phone"
      :class="{ 'opacity-60': skipped }"
    />
  </div>
</template>
