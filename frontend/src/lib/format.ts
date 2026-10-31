import type { CampaignStatus, Channel, Language } from '@/api/types'
import { type Locale, locale, t } from './i18n'

// Labels follow the UI language; call them from templates or computeds so
// they update when it changes.
export const channelLabel = (ch: Channel) => t(`channel.${ch}`)
export const languageLabel = (l: Language) => t(`language.${l}`)
export const statusLabel = (s: CampaignStatus) => t(`status.${s}`)

// Statuses whose segment and content can still change.
export const editableStatuses: readonly CampaignStatus[] = ['draft', 'audience_ready', 'content_ready']

const intlLocales: Record<Locale, string> = { en: 'en-US', tr: 'tr-TR' }
const numberFormats = new Map<Locale, Intl.NumberFormat>()
const dateTimeFormats = new Map<Locale, Intl.DateTimeFormat>()

export function formatNumber(n: number, l: Locale = locale.value): string {
  let f = numberFormats.get(l)
  if (!f) numberFormats.set(l, (f = new Intl.NumberFormat(intlLocales[l])))
  return f.format(n)
}

// formatDateTime shows a time in the browser's time zone, in the UI language.
export function formatDateTime(iso: string, l: Locale = locale.value): string {
  let f = dateTimeFormats.get(l)
  if (!f) dateTimeFormats.set(l, (f = new Intl.DateTimeFormat(intlLocales[l], { dateStyle: 'medium', timeStyle: 'short' })))
  return f.format(new Date(iso))
}
