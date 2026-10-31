<script setup lang="ts">
import { t } from '@/lib/i18n'

// How the SMS looks on a phone, filled in for a sample recipient.
defineProps<{ text: string; recipient: string }>()
</script>

<template>
  <figure class="mx-auto w-full max-w-xs rounded-[1.75rem] border-4 border-slate-800 bg-slate-800 shadow-sm dark:border-black dark:bg-black">
    <div class="rounded-[1.4rem] bg-white px-3 pt-2 pb-4">
      <div class="mx-auto mb-3 h-1.5 w-12 rounded-full bg-slate-200" aria-hidden="true" />
      <figcaption class="mb-3 text-center">
        <span class="mx-auto flex size-8 items-center justify-center rounded-full bg-indigo-100 text-xs font-semibold text-indigo-700">
          {{ t('preview.sender').charAt(0) }}
        </span>
        <span class="mt-1 block text-xs font-medium text-slate-700">{{ t('preview.sender') }}</span>
        <span class="block text-[11px] text-slate-400">→ {{ recipient }}</span>
      </figcaption>
      <p
        class="mr-8 rounded-2xl rounded-bl-sm bg-slate-100 px-3 py-2 text-sm whitespace-pre-line text-slate-900 [overflow-wrap:anywhere]"
      >
        {{ text || '…' }}
      </p>
      <p class="mt-1 text-[11px] text-slate-400">{{ t('preview.now') }}</p>
    </div>
  </figure>
</template>
