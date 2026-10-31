<script setup lang="ts">
import { computed } from 'vue'
import { ApiError } from '@/api/client'

const props = defineProps<{ error: unknown; title?: string }>()

const message = computed(() => (props.error instanceof Error ? props.error.message : String(props.error)))
const problems = computed(() => (props.error instanceof ApiError ? props.error.problems : []))
</script>

<template>
  <div role="alert" class="rounded-lg border border-rose-200 bg-rose-50 p-4 text-sm text-rose-900">
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
