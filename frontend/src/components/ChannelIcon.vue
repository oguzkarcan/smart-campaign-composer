<script setup lang="ts">
import type { Channel } from '@/api/types'

// A chat bubble for SMS, an envelope for email.
defineProps<{ channel: Channel }>()
</script>

<template>
  <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <template v-if="channel === 'sms'">
      <path d="M4 4.5h12a1.5 1.5 0 0 1 1.5 1.5v7a1.5 1.5 0 0 1-1.5 1.5H9l-3.5 3v-3H4A1.5 1.5 0 0 1 2.5 13V6A1.5 1.5 0 0 1 4 4.5z" />
      <path d="M6.5 9.5h.01M10 9.5h.01M13.5 9.5h.01" stroke-width="2.2" />
    </template>
    <template v-else>
      <rect x="2.5" y="4.5" width="15" height="11" rx="1.5" />
      <path d="M3 5.5l7 5 7-5" />
    </template>
  </svg>
</template>
