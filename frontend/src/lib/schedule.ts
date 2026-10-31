// Helpers for "Schedule for later": the browser picks a local date and time,
// the API gets an absolute RFC 3339 time (send_at).
import { type Locale, translate } from './i18n'

// campaign.MaxScheduleAhead on the server.
export const MAX_SCHEDULE_DAYS = 30
// The form asks for at least a minute ahead, so a time picked "now" is not
// already in the past when the request arrives.
export const MIN_LEAD_MS = 60_000

const DAY_MS = 24 * 60 * 60 * 1000

const pad = (n: number) => String(n).padStart(2, '0')

// toLocalInput formats d for <input type="datetime-local"> in local time,
// to the minute.
export function toLocalInput(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// defaultSchedule is the next full hour that is at least MIN_LEAD_MS away.
export function defaultSchedule(now: Date): string {
  const d = new Date(now.getTime() + MIN_LEAD_MS)
  d.setMinutes(0, 0, 0)
  d.setHours(d.getHours() + 1)
  return toLocalInput(d)
}

// ScheduleError names what is wrong with a picked time; the UI translates it
// (schedule.<error>).
export type ScheduleError = 'pick' | 'tooSoon' | 'tooFar'

export type ScheduleResult = { sendAt: string; error?: undefined } | { sendAt?: undefined; error: ScheduleError }

// parseSchedule checks a datetime-local value against the same range the
// server accepts and returns it as an ISO time.
export function parseSchedule(value: string, now: Date): ScheduleResult {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/.test(value)) return { error: 'pick' }
  const d = new Date(value) // no offset: local time
  if (Number.isNaN(d.getTime())) return { error: 'pick' }
  if (d.getTime() < now.getTime() + MIN_LEAD_MS) return { error: 'tooSoon' }
  if (d.getTime() > now.getTime() + MAX_SCHEDULE_DAYS * DAY_MS) return { error: 'tooFar' }
  return { sendAt: d.toISOString() }
}

// formatCountdown says how long until a time: "in 2 d 3 h", "in 1 h 05 min",
// "in 4 min", "in less than a minute", or "now" once it has passed.
export function formatCountdown(ms: number, locale: Locale = 'en'): string {
  if (ms <= 0) return translate(locale, 'time.now')
  const min = Math.floor(ms / 60_000)
  if (min < 1) return translate(locale, 'time.underMinute')
  const d = Math.floor(min / 1440)
  const h = Math.floor((min % 1440) / 60)
  const m = min % 60
  if (d > 0) return h ? translate(locale, 'time.daysHours', { d, h }) : translate(locale, 'time.days', { d })
  if (h > 0) return translate(locale, 'time.hours', { h, m: pad(m) })
  return translate(locale, 'time.minutes', { m })
}
