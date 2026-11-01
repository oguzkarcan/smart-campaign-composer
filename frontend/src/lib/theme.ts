// Light / dark theme. The choice is saved in localStorage; without one the
// default is dark. index.html applies the same rule before the app loads,
// so the page does not flash.
import { ref } from 'vue'

export type Theme = 'light' | 'dark'

export const THEMES: readonly Theme[] = ['light', 'dark']

const STORAGE_KEY = 'scc.theme' // also read by the script in index.html

function saved(): Theme | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return v === 'light' || v === 'dark' ? v : null
  } catch {
    return null // storage blocked: not remembered, still switchable
  }
}

export const theme = ref<Theme>(saved() ?? 'dark')

function apply(t: Theme) {
  document.documentElement.classList.toggle('dark', t === 'dark')
}

export function setTheme(t: Theme) {
  theme.value = t
  try {
    localStorage.setItem(STORAGE_KEY, t)
  } catch {
    // Not remembered, but the switch still works for this visit.
  }
  apply(t)
}

// initTheme applies the current theme (defaulting to dark).
export function initTheme() {
  apply(theme.value)
}
