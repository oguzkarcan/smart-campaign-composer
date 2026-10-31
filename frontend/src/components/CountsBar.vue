<script setup lang="ts">
import { computed } from 'vue'
import type { Counts } from '@/api/types'
import { t } from '@/lib/i18n'
import { SEGMENTS, widths } from '@/lib/progress'

const props = withDefaults(defineProps<{ counts: Counts; size?: 'sm' | 'md' }>(), { size: 'md' })
const w = computed(() => widths(props.counts))
</script>

<template>
  <div
    class="flex w-full overflow-hidden rounded-full bg-slate-100"
    :class="size === 'sm' ? 'h-1.5' : 'h-3'"
    role="img"
    :aria-label="SEGMENTS.map((s) => `${counts[s.key]} ${t(s.label).toLowerCase()}`).join(', ')"
  >
    <div
      v-for="s in SEGMENTS"
      :key="s.key"
      :class="s.bar"
      class="h-full transition-[width] duration-700 ease-out"
      :style="{ width: `${w[s.key]}%` }"
    />
  </div>
</template>
