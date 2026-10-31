<script setup lang="ts">
import type { RuleLine } from '@/lib/segment'

defineProps<{ line: RuleLine }>()
</script>

<template>
  <div>
    <p :class="line.children ? 'text-xs font-semibold uppercase tracking-wide text-slate-500' : 'text-slate-800'">
      {{ line.text }}
    </p>
    <ul v-if="line.children" class="mt-1 space-y-1 border-l-2 border-indigo-100 pl-4">
      <li v-for="(child, i) in line.children" :key="i">
        <RuleTree :line="child" />
      </li>
    </ul>
  </div>
</template>
