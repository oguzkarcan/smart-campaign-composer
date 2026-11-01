<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { t } from '@/lib/i18n'

// The marketer's original brief on one line; opens over the page, so checking
// the copy against it takes no vertical space.
defineProps<{ brief: string }>()

const open = ref(false)
const root = ref<HTMLElement>()

function onDocumentClick(e: MouseEvent) {
  if (!root.value?.contains(e.target as Node)) open.value = false
}
onMounted(() => document.addEventListener('click', onDocumentClick))
onUnmounted(() => document.removeEventListener('click', onDocumentClick))
</script>

<template>
  <div ref="root" class="relative min-w-56 flex-1" @keydown.esc="open = false">
    <button
      type="button"
      class="flex w-full items-center gap-2 rounded-full border border-amber-200 bg-amber-50 px-3 py-1.5 text-left text-sm hover:border-amber-300"
      :aria-expanded="open"
      @click="open = !open"
    >
      <span class="flex shrink-0 items-center gap-1.5 text-xs font-bold tracking-wider text-amber-800 uppercase">
        <span aria-hidden="true">✦</span> {{ t('new.brief') }}
      </span>
      <span class="min-w-0 flex-1 truncate text-slate-700 italic">{{ brief }}</span>
      <svg viewBox="0 0 20 20" class="size-4 shrink-0 text-amber-800 transition-transform" :class="{ 'rotate-180': open }" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <path d="M5 8l5 5 5-5" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>
    <blockquote
      v-if="open"
      class="absolute inset-x-0 top-full z-20 mt-1.5 max-h-72 overflow-y-auto rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm leading-relaxed whitespace-pre-line text-slate-800 shadow-lg"
    >{{ brief }}</blockquote>
  </div>
</template>
