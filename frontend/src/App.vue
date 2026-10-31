<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { health } from './api/client'

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
      <div class="mx-auto flex max-w-5xl items-center justify-between px-4 py-3">
        <nav class="flex items-center gap-6">
          <RouterLink to="/" class="font-semibold tracking-tight">Smart Campaign Composer</RouterLink>
          <RouterLink to="/" class="text-sm text-slate-600 hover:text-slate-900" exact-active-class="!text-indigo-600 font-medium">
            Campaigns
          </RouterLink>
          <RouterLink to="/new" class="text-sm text-slate-600 hover:text-slate-900" active-class="!text-indigo-600 font-medium">
            New campaign
          </RouterLink>
        </nav>
        <span class="flex items-center gap-2 text-sm text-slate-500" role="status">
          <span class="size-2 rounded-full" :class="stateDot[apiState]" />
          {{ stateLabel[apiState] }}
        </span>
      </div>
    </header>
    <main class="mx-auto max-w-5xl px-4 py-8">
      <RouterView />
    </main>
  </div>
</template>
