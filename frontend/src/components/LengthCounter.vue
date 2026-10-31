<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ count: number; limit: number; variables: Record<string, number> }>()

const tone = computed(() => {
  if (props.count > props.limit) return 'text-rose-600 font-medium'
  if (props.count > props.limit * 0.9) return 'text-amber-600'
  return 'text-slate-500'
})
const title = computed(
  () =>
    'Worst case after personalization: ' +
    Object.entries(props.variables)
      .map(([name, n]) => `{{${name}}} counts as ${n}`)
      .join(', '),
)
</script>

<template>
  <span class="text-xs tabular-nums" :class="tone" :title="title">{{ count }} / {{ limit }} worst case</span>
</template>
