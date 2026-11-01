<script setup lang="ts">
import { t } from '@/lib/i18n'

// How the email looks in an inbox, filled in for a sample recipient. A long
// body scrolls inside the card, so the card keeps the height it is given.
defineProps<{ subject: string; body: string; recipient: string }>()
</script>

<template>
  <figure class="flex flex-col overflow-hidden rounded-xl border border-slate-200 bg-white shadow-lg shadow-slate-900/10">
    <p class="shrink-0 border-b border-slate-100 px-4 pt-3 pb-2.5 text-base font-semibold text-slate-900 [overflow-wrap:anywhere]">
      {{ subject || t('preview.noSubject') }}
    </p>
    <figcaption class="flex shrink-0 items-center gap-3 border-b border-slate-100 bg-slate-50 px-4 py-2 text-xs text-slate-500">
      <span class="flex size-8 shrink-0 items-center justify-center rounded-full bg-indigo-100 font-semibold text-indigo-700" aria-hidden="true">
        {{ t('preview.sender').charAt(0) }}
      </span>
      <span class="min-w-0 space-y-0.5">
        <span class="block truncate">
          <span class="font-medium text-slate-700">{{ t('preview.sender') }}</span>
          &lt;campaigns@example.com&gt;
        </span>
        <span class="block truncate">{{ t('preview.to') }}: {{ recipient }}</span>
      </span>
    </figcaption>
    <p class="min-h-0 flex-1 overflow-y-auto px-4 py-3 text-sm leading-relaxed whitespace-pre-line text-slate-700 [overflow-wrap:anywhere]">{{ body || '…' }}</p>
  </figure>
</template>
