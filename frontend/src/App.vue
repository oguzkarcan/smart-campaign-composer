<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { health } from './api/client'
import { LOCALES, locale, setLocale, t } from './lib/i18n'

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
  document.documentElement.lang = locale.value
  checkHealth()
  timer = setInterval(checkHealth, HEALTH_INTERVAL_MS)
})
onUnmounted(() => clearInterval(timer))

const stateKeys = { checking: 'api.checking', ok: 'api.ok', down: 'api.down' } as const
const stateDot: Record<ApiState, string> = { checking: 'bg-slate-300', ok: 'bg-emerald-500', down: 'bg-rose-500' }
</script>

<template>
  <div class="min-h-screen">
    <header class="border-b border-slate-200 bg-white">
      <div class="mx-auto flex max-w-5xl items-center justify-between gap-4 px-4 py-3">
        <nav class="flex items-center gap-6">
          <RouterLink to="/" class="font-semibold tracking-tight whitespace-nowrap">{{ t('app.title') }}</RouterLink>
          <!-- On phones the pages' own buttons and back links do the navigation. -->
          <RouterLink
            to="/"
            class="hidden text-sm text-slate-600 hover:text-slate-900 sm:inline"
            exact-active-class="!text-indigo-600 font-medium"
          >
            {{ t('nav.campaigns') }}
          </RouterLink>
          <RouterLink
            to="/new"
            class="hidden text-sm text-slate-600 hover:text-slate-900 sm:inline"
            active-class="!text-indigo-600 font-medium"
          >
            {{ t('nav.new') }}
          </RouterLink>
        </nav>
        <div class="flex shrink-0 items-center gap-4">
          <span class="flex items-center gap-2 text-sm text-slate-500" role="status" :title="t(stateKeys[apiState])">
            <span class="size-2 rounded-full" :class="stateDot[apiState]" />
            <span class="sr-only md:not-sr-only">{{ t(stateKeys[apiState]) }}</span>
          </span>
          <div
            role="group"
            :aria-label="t('nav.uiLanguage')"
            class="flex overflow-hidden rounded-lg border border-slate-300 text-xs font-semibold"
          >
            <button
              v-for="(l, i) in LOCALES"
              :key="l"
              type="button"
              :aria-pressed="locale === l"
              :lang="l"
              class="px-2.5 py-1"
              :class="[
                locale === l ? 'bg-indigo-600 text-white' : 'bg-white text-slate-600 hover:bg-slate-50',
                { 'border-l border-slate-300': i > 0 },
              ]"
              @click="setLocale(l)"
            >
              {{ l.toUpperCase() }}
            </button>
          </div>
        </div>
      </div>
    </header>
    <div v-if="apiState === 'down'" class="border-b border-rose-200 bg-rose-50 text-sm text-rose-900" role="alert">
      <div class="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-2 px-4 py-2">
        <span>{{ t('api.unreachable') }}</span>
        <button type="button" class="font-medium underline" @click="checkHealth">{{ t('api.checkAgain') }}</button>
      </div>
    </div>
    <main class="mx-auto max-w-5xl px-4 py-8">
      <RouterView />
    </main>
  </div>
</template>
