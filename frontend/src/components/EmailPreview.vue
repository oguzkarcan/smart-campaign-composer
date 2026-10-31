<script setup lang="ts">
import { t } from '@/lib/i18n'

// How the email looks in an inbox, filled in for a sample recipient.
defineProps<{ subject: string; body: string; recipient: string }>()
</script>

<template>
  <figure class="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-lg shadow-slate-900/10">
    <figcaption class="space-y-0.5 border-b border-slate-100 bg-slate-50 px-4 py-3 text-xs text-slate-500">
      <p>
        <span class="inline-block w-12">{{ t('preview.from') }}</span>
        <span class="font-medium text-slate-700">{{ t('preview.sender') }}</span>
        &lt;campaigns@example.com&gt;
      </p>
      <p><span class="inline-block w-12">{{ t('preview.to') }}</span>{{ recipient }}</p>
    </figcaption>
    <div class="space-y-3 px-4 py-4 [overflow-wrap:anywhere]">
      <p class="text-base font-semibold text-slate-900">{{ subject || t('preview.noSubject') }}</p>
      <p class="text-sm leading-relaxed whitespace-pre-line text-slate-700">{{ body || '…' }}</p>
    </div>
  </figure>
</template>
