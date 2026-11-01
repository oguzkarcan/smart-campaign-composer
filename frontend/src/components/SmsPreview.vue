<script setup lang="ts">
import { t } from '@/lib/i18n'

// How the SMS looks on a phone, filled in for a sample recipient. The thread
// scrolls inside the screen, so the phone keeps the height it is given.
defineProps<{ text: string; recipient: string }>()
</script>

<template>
  <figure class="flex w-64 flex-col rounded-[2.5rem] border-[6px] border-slate-800 bg-slate-800 shadow-xl shadow-slate-900/20 dark:border-black dark:bg-black">
    <div class="flex min-h-0 flex-1 flex-col overflow-hidden rounded-[2.1rem] bg-white">
      <div class="relative flex shrink-0 items-center justify-between px-6 pt-2.5 pb-1 text-[11px] font-semibold text-slate-900" aria-hidden="true">
        <span>9:41</span>
        <span class="absolute top-2 left-1/2 h-5 w-20 -translate-x-1/2 rounded-full bg-slate-800 dark:bg-black" />
        <svg viewBox="0 0 24 12" class="h-2.5 w-6" fill="currentColor">
          <rect x="0" y="8" width="3" height="4" rx=".6" />
          <rect x="4.5" y="5.5" width="3" height="6.5" rx=".6" />
          <rect x="9" y="3" width="3" height="9" rx=".6" />
          <rect x="15" y="2" width="9" height="9" rx="2" />
        </svg>
      </div>
      <figcaption class="shrink-0 border-b border-slate-100 px-3 pt-1.5 pb-2 text-center">
        <span class="mx-auto flex size-8 items-center justify-center rounded-full bg-indigo-100 text-xs font-semibold text-indigo-700">
          {{ t('preview.sender').charAt(0) }}
        </span>
        <span class="mt-0.5 block text-xs font-medium text-slate-700">{{ t('preview.sender') }}</span>
        <span class="block text-[11px] text-slate-400">→ {{ recipient }}</span>
      </figcaption>
      <div class="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        <p class="mr-6 rounded-2xl rounded-bl-sm bg-slate-100 px-3 py-2 text-sm whitespace-pre-line text-slate-900 [overflow-wrap:anywhere]">
          {{ text || '…' }}
        </p>
        <p class="mt-1 text-[11px] text-slate-400">{{ t('preview.now') }}</p>
      </div>
      <div class="shrink-0 px-3 pb-1.5" aria-hidden="true">
        <div class="rounded-full border border-slate-200 px-3 py-1 text-xs text-slate-400">{{ t('preview.textMessage') }}</div>
        <div class="mx-auto mt-2 h-1 w-20 rounded-full bg-slate-300" />
      </div>
    </div>
  </figure>
</template>
