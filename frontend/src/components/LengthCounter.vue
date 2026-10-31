<script setup lang="ts">
import { computed } from 'vue'
import { t } from '@/lib/i18n'

const props = defineProps<{ count: number; limit: number; variables: Record<string, number> }>()

const tone = computed(() => {
  if (props.count > props.limit) return 'text-rose-600 font-medium'
  if (props.count > props.limit * 0.9) return 'text-amber-600'
  return 'text-slate-500'
})
const title = computed(() =>
  t('editor.worstCaseTitle', {
    vars: Object.entries(props.variables)
      .map(([name, n]) => t('editor.countsAs', { name: `{{${name}}}`, n }))
      .join(', '),
  }),
)
</script>

<template>
  <span class="text-xs tabular-nums" :class="tone" :title="title">{{ t('editor.worstCase', { count, limit }) }}</span>
</template>
