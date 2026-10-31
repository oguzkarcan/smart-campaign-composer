<script setup lang="ts">
import { computed } from 'vue'
import { ApiError, TIMED_OUT, UNREACHABLE } from '@/api/client'
import { t } from '@/lib/i18n'

const props = defineProps<{ error: unknown; title?: string }>()

// The client's own transport messages are translated; the API's messages are
// shown as the server wrote them.
const message = computed(() => {
  const text = props.error instanceof Error ? props.error.message : String(props.error)
  if (text === UNREACHABLE) return t('api.unreachable')
  if (text === TIMED_OUT) return t('api.timedOut')
  return text
})
const problems = computed(() => (props.error instanceof ApiError ? props.error.problems : []))
</script>

<template>
  <div role="alert" class="rounded-xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-900">
    <p v-if="title" class="font-medium">{{ title }}</p>
    <p :class="{ 'mt-1': title }">{{ message }}</p>
    <ul v-if="problems.length" class="mt-2 list-disc space-y-0.5 pl-5">
      <li v-for="p in problems" :key="p.path + p.message">
        <code v-if="p.path" class="text-xs">{{ p.path }}</code> {{ p.message }}
      </li>
    </ul>
    <div v-if="$slots.default" class="mt-3"><slot /></div>
  </div>
</template>
