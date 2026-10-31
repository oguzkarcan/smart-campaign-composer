<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { health, UNREACHABLE } from './api/client'

type ApiState = 'checking' | 'ok' | 'down'
const apiState = ref<ApiState>('checking')

const HEALTH_INTERVAL_MS = 15_000
let timer: ReturnType<typeof setInterval> | undefined

async function checkHealth() {
  try {
    const h = await health(AbortSignal.timeout(5_000))
    apiState.value = h.status === 'ok' ? 'ok' : 'down'
  } catch {
    apiState.value = 'down'
  }
}

onMounted(() => {
  checkHealth()
  timer = setInterval(checkHealth, HEALTH_INTERVAL_MS)
})
onUnmounted(() => clearInterval(timer))

const stateLabel: Record<ApiState, string> = { checking: 'Checking API…', ok: 'API online', down: 'API unreachable' }
const stateDot: Record<ApiState, string> = { checking: 'bg-slate-300', ok: 'bg-emerald-500', down: 'bg-rose-500' }
</script>

<template>
  <div class="min-h-screen">
    <header class="border-b border-slate-200 bg-white">
      <div class="mx-auto flex max-w-5xl items-center justify-between gap-4 px-4 py-3">
        <nav class="flex items-center gap-6">
          <RouterLink to="/" class="font-semibold tracking-tight whitespace-nowrap">Smart Campaign Composer</RouterLink>
          <!-- On phones the pages' own buttons and back links do the navigation. -->
          <RouterLink
            to="/"
            class="hidden text-sm text-slate-600 hover:text-slate-900 sm:inline"
            exact-active-class="!text-indigo-600 font-medium"
          >
            Campaigns
          </RouterLink>
          <RouterLink
            to="/new"
            class="hidden text-sm text-slate-600 hover:text-slate-900 sm:inline"
            active-class="!text-indigo-600 font-medium"
          >
            New campaign
          </RouterLink>
        </nav>
        <span class="flex shrink-0 items-center gap-2 text-sm text-slate-500" role="status" :title="stateLabel[apiState]">
          <span class="size-2 rounded-full" :class="stateDot[apiState]" />
          <span class="sr-only sm:not-sr-only">{{ stateLabel[apiState] }}</span>
        </span>
      </div>
    </header>
    <div v-if="apiState === 'down'" class="border-b border-rose-200 bg-rose-50 text-sm text-rose-900" role="alert">
      <div class="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-2 px-4 py-2">
        <span>{{ UNREACHABLE }}</span>
        <button type="button" class="font-medium underline" @click="checkHealth">Check again</button>
      </div>
    </div>
    <main class="mx-auto max-w-5xl px-4 py-8">
      <RouterView />
    </main>
  </div>
</template>
