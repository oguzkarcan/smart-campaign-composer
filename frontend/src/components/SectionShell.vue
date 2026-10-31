<script setup lang="ts">
// A numbered step section of the campaign page: Audience (01), Messages
// (02), Delivery (03). headingId is what the step bar scrolls to.
defineProps<{ step: number; headingId: string; title: string; subtitle?: string }>()
</script>

<template>
  <section
    class="scroll-mt-20 rounded-2xl border border-slate-200 bg-white shadow-sm"
    :aria-labelledby="headingId"
  >
    <header class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 px-5 py-4 sm:px-6">
      <div class="flex min-w-0 items-center gap-3">
        <span
          class="flex size-8 shrink-0 items-center justify-center rounded-lg bg-indigo-50 font-mono text-xs font-semibold text-indigo-700 ring-1 ring-indigo-100"
          aria-hidden="true"
        >
          {{ String(step).padStart(2, '0') }}
        </span>
        <div class="min-w-0">
          <h2 :id="headingId" class="scroll-mt-24 text-base font-semibold tracking-tight text-slate-900">{{ title }}</h2>
          <p v-if="subtitle" class="truncate text-xs text-slate-500">{{ subtitle }}</p>
        </div>
      </div>
      <div v-if="$slots.actions" class="flex flex-wrap items-center gap-2"><slot name="actions" /></div>
    </header>
    <div class="space-y-5 p-5 sm:p-6"><slot /></div>
  </section>
</template>
