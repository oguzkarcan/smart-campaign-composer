import type { Counts } from '@/api/types'
import { type Locale, type MessageKey, translate } from './i18n'

export type CountKey = Exclude<keyof Counts, 'total'>

// Bar segments in display order, finished states first; label is a
// message key.
export const SEGMENTS: readonly { key: CountKey; label: MessageKey; bar: string; dot: string }[] = [
  { key: 'sent', label: 'count.sent', bar: 'bg-emerald-500', dot: 'bg-emerald-500' },
  { key: 'failed', label: 'count.failed', bar: 'bg-rose-500', dot: 'bg-rose-500' },
  { key: 'processing', label: 'count.processing', bar: 'bg-indigo-400', dot: 'bg-indigo-400' },
  { key: 'retry', label: 'count.retry', bar: 'bg-amber-400', dot: 'bg-amber-400' },
  { key: 'queued', label: 'count.queued', bar: 'bg-slate-300', dot: 'bg-slate-300' },
]

export const emptyCounts: Counts = { total: 0, queued: 0, processing: 0, retry: 0, sent: 0, failed: 0 }

// finished counts messages in a final state (sent or failed).
export function finished(c: Counts): number {
  return c.sent + c.failed
}

// percentDone is the share of messages in a final state, 0-100, floored so
// it only shows 100 when everything is done.
export function percentDone(c: Counts): number {
  if (c.total === 0) return 0
  return Math.floor((finished(c) / c.total) * 100)
}

// widths returns each segment's share of the bar in percent.
export function widths(c: Counts): Record<CountKey, number> {
  const out = { sent: 0, failed: 0, processing: 0, retry: 0, queued: 0 }
  if (c.total === 0) return out
  for (const s of SEGMENTS) out[s.key] = (c[s.key] / c.total) * 100
  return out
}

export interface Sample {
  at: number // ms
  finished: number
}

// RateMeter estimates messages finished per second over a sliding window of
// progress samples, smoothing the 1 s ticks of the event stream.
export class RateMeter {
  private samples: Sample[] = []

  constructor(private readonly windowMs = 5_000) {}

  add(s: Sample): void {
    this.samples.push(s)
    while (this.samples.length > 2 && s.at - this.samples[0]!.at > this.windowMs) this.samples.shift()
  }

  perSecond(): number {
    if (this.samples.length < 2) return 0
    const first = this.samples[0]!
    const last = this.samples.at(-1)!
    const seconds = (last.at - first.at) / 1000
    return seconds > 0 ? Math.max(0, (last.finished - first.finished) / seconds) : 0
  }
}

// formatDuration renders a duration as "42s" or "3m 05s".
export function formatDuration(ms: number, locale: Locale = 'en'): string {
  const total = Math.max(0, Math.round(ms / 1000))
  const m = Math.floor(total / 60)
  const s = total % 60
  return m > 0
    ? translate(locale, 'time.duration', { m, s: String(s).padStart(2, '0') })
    : translate(locale, 'time.seconds', { s })
}
