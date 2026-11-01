<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { type Health, health } from './api/client'
import { LOCALES, locale, setLocale, t } from './lib/i18n'
import { setTheme, theme, THEMES } from './lib/theme'

type ApiState = 'checking' | 'ok' | 'down'
const apiState = ref<ApiState>('checking')
// Which model writes audiences and messages, from the last health check.
const llm = ref<Pick<Health, 'llm_provider' | 'llm_model'> | null>(null)

const HEALTH_INTERVAL_MS = 15_000
let timer: ReturnType<typeof setInterval> | undefined

async function checkHealth() {
  try {
    const h = await health(AbortSignal.timeout(5_000))
    apiState.value = h.status === 'ok' ? 'ok' : 'down'
    if (h.llm_provider) llm.value = { llm_provider: h.llm_provider, llm_model: h.llm_model }
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
    <header class="sticky top-0 z-30 border-b border-stone-200/80 bg-[#F7F5F0]/85 backdrop-blur-md dark:border-slate-200/80 dark:bg-white/90">
      <div class="mx-auto flex max-w-6xl items-center justify-between gap-4 px-4 py-2.5">
        <nav class="flex min-w-0 items-center gap-6">
          <RouterLink to="/" class="group flex min-w-0 items-center gap-3">
            <span
              class="relative flex size-9 shrink-0 items-center justify-center rounded-xl bg-linear-to-br from-[#4f46e5] to-[#7c3aed] shadow-sm ring-1 ring-[#4f46e5]/30"
              aria-hidden="true"
            >
              <!-- A four-point star with two companions: Sirius and its constellation. -->
              <svg viewBox="0 0 24 24" class="size-5 text-[#fff]">
                <path d="M12 3.5l1.9 5.6 5.6 1.9-5.6 1.9L12 18.5l-1.9-5.6L4.5 11l5.6-1.9z" fill="currentColor" />
                <circle cx="19" cy="4.5" r="1.3" fill="currentColor" opacity=".85" />
                <circle cx="5" cy="19" r="1" fill="currentColor" opacity=".7" />
                <path d="M19 4.5L14.5 8M5 19l4-3.6" stroke="currentColor" stroke-width=".8" opacity=".5" />
              </svg>
            </span>
            <span class="min-w-0 leading-tight">
              <span class="flex items-center gap-2">
                <span class="text-[15px] font-bold tracking-tight whitespace-nowrap text-slate-900">{{ t('app.title') }}</span>
                <span
                  class="hidden rounded-md border border-[#F4462A]/25 bg-[#F4462A]/10 px-1.5 py-px text-[10px] font-bold tracking-[0.12em] text-[#F4462A] sm:inline dark:border-indigo-200 dark:bg-indigo-50 dark:text-indigo-700"
                >
                  INSIDER ONE
                </span>
              </span>
              <span class="hidden truncate text-xs text-slate-500 sm:block">{{ t('app.subtitle') }}</span>
            </span>
          </RouterLink>
          <!-- On phones the pages' own buttons and back links do the navigation. -->
          <div class="hidden items-center gap-1 rounded-xl bg-stone-200/60 p-1 text-sm md:flex dark:bg-slate-100/80">
            <RouterLink
              to="/"
              class="rounded-lg px-3 py-1 font-medium text-slate-600 hover:text-slate-900"
              exact-active-class="bg-white !text-slate-900 shadow-xs"
            >
              {{ t('nav.campaigns') }}
            </RouterLink>
            <RouterLink
              to="/new"
              class="rounded-lg px-3 py-1 font-medium text-slate-600 hover:text-slate-900"
              active-class="bg-white !text-slate-900 shadow-xs"
            >
              {{ t('nav.new') }}
            </RouterLink>
          </div>
        </nav>
        <div class="flex shrink-0 items-center gap-2.5 sm:gap-3">
          <span
            v-if="llm"
            class="flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-semibold"
            :class="
              llm.llm_provider === 'claude'
                ? 'border-emerald-200 bg-emerald-50 text-emerald-800'
                : 'border-amber-200 bg-amber-50 text-amber-800'
            "
            :title="llm.llm_provider === 'claude' ? t('llm.claudeTitle', { model: llm.llm_model ?? '' }) : t('llm.mockTitle')"
          >
            <span class="size-2 rounded-full" :class="llm.llm_provider === 'claude' ? 'bg-emerald-500' : 'bg-amber-500'" />
            <template v-if="llm.llm_provider === 'claude'">
              {{ t('llm.claude') }}
              <span class="hidden font-mono font-normal lg:inline">({{ llm.llm_model }})</span>
            </template>
            <template v-else>{{ t('llm.mock') }}</template>
          </span>
          <span class="flex items-center gap-1.5 text-xs font-medium text-slate-500" role="status" :title="t(stateKeys[apiState])">
            <span class="size-2 rounded-full ring-2 ring-white" :class="stateDot[apiState]" />
            <span class="sr-only lg:not-sr-only">{{ t(stateKeys[apiState]) }}</span>
          </span>
          <div
            role="group"
            :aria-label="t('theme.label')"
            class="flex rounded-lg bg-stone-200/60 p-0.5 text-xs font-semibold dark:bg-slate-100"
          >
            <button
              v-for="th in THEMES"
              :key="th"
              type="button"
              :aria-pressed="theme === th"
              :title="t(`theme.${th}`)"
              class="flex items-center gap-1 rounded-md px-2.5 py-1"
              :class="theme === th ? 'bg-white text-slate-900 shadow-xs' : 'text-slate-500 hover:text-slate-800'"
              @click="setTheme(th)"
            >
              <span aria-hidden="true">{{ th === 'light' ? '☀' : '☾' }}</span>
              <span class="sr-only sm:not-sr-only">{{ t(`theme.${th}`) }}</span>
            </button>
          </div>
          <div
            role="group"
            :aria-label="t('nav.uiLanguage')"
            class="flex rounded-lg bg-stone-200/60 p-0.5 text-xs font-semibold dark:bg-slate-100"
          >
            <button
              v-for="l in LOCALES"
              :key="l"
              type="button"
              :aria-pressed="locale === l"
              :lang="l"
              class="rounded-md px-2.5 py-1"
              :class="locale === l ? 'bg-white text-slate-900 shadow-xs' : 'text-slate-500 hover:text-slate-800'"
              @click="setLocale(l)"
            >
              {{ l.toUpperCase() }}
            </button>
          </div>
        </div>
      </div>
    </header>
    <div v-if="apiState === 'down'" class="border-b border-rose-200 bg-rose-50 text-sm text-rose-900" role="alert">
      <div class="mx-auto flex max-w-6xl flex-wrap items-center justify-between gap-2 px-4 py-2">
        <span>{{ t('api.unreachable') }}</span>
        <button type="button" class="font-medium underline" @click="checkHealth">{{ t('api.checkAgain') }}</button>
      </div>
    </div>
    <main class="mx-auto max-w-6xl px-4 py-5">
      <RouterView />
    </main>
  </div>
</template>
