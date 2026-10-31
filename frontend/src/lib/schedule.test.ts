import { describe, expect, it } from 'vitest'
import { defaultSchedule, formatCountdown, parseSchedule, toLocalInput } from './schedule'

// Local times throughout, like the datetime-local input.
const now = new Date(2026, 9, 31, 14, 20, 30)

describe('schedule input', () => {
  it('formats local time to the minute', () => {
    expect(toLocalInput(new Date(2026, 0, 5, 9, 7, 59))).toBe('2026-01-05T09:07')
  })

  it('defaults to the next full hour at least a minute away', () => {
    expect(defaultSchedule(now)).toBe('2026-10-31T15:00')
    expect(defaultSchedule(new Date(2026, 9, 31, 14, 59, 30))).toBe('2026-10-31T16:00')
    expect(defaultSchedule(new Date(2026, 11, 31, 23, 30))).toBe('2027-01-01T00:00')
  })

  it('accepts a time in range and returns it as ISO', () => {
    const r = parseSchedule('2026-11-01T09:00', now)
    expect(r.error).toBeUndefined()
    expect(r.sendAt).toBe(new Date(2026, 10, 1, 9, 0).toISOString())
  })

  it('rejects empty, past, too soon and too far', () => {
    expect(parseSchedule('', now).error).toBe('pick')
    expect(parseSchedule('2026-10-31T14:00', now).error).toBe('tooSoon')
    expect(parseSchedule('2026-10-31T14:21', now).error).toBe('tooSoon') // 30 s ahead
    expect(parseSchedule('2026-10-31T14:22', now).error).toBeUndefined()
    expect(parseSchedule('2026-12-15T10:00', now).error).toBe('tooFar') // more than MAX_SCHEDULE_DAYS
  })
})

describe('formatCountdown', () => {
  it('reads naturally at every scale', () => {
    expect(formatCountdown(-1)).toBe('now')
    expect(formatCountdown(30_000)).toBe('in less than a minute')
    expect(formatCountdown(4 * 60_000 + 59_000)).toBe('in 4 min')
    expect(formatCountdown(65 * 60_000)).toBe('in 1 h 05 min')
    expect(formatCountdown((2 * 1440 + 3 * 60 + 10) * 60_000)).toBe('in 2 d 3 h')
    expect(formatCountdown(2 * 1440 * 60_000)).toBe('in 2 d')
  })

  it('speaks Turkish', () => {
    expect(formatCountdown(65 * 60_000, 'tr')).toBe('1 sa 05 dk sonra')
    expect(formatCountdown((2 * 1440 + 3 * 60) * 60_000, 'tr')).toBe('2 gün 3 sa sonra')
    expect(formatCountdown(0, 'tr')).toBe('şimdi')
  })
})
