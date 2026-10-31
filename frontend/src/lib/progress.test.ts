import { describe, expect, it } from 'vitest'
import { emptyCounts, formatDuration, percentDone, RateMeter, widths } from './progress'

const counts = { total: 200, queued: 50, processing: 10, retry: 5, sent: 120, failed: 15 }

describe('progress math', () => {
  it('counts sent and failed as done, floored', () => {
    expect(percentDone(counts)).toBe(67)
    expect(percentDone({ ...counts, sent: 184, queued: 1, processing: 0, retry: 0 })).toBe(99)
    expect(percentDone(emptyCounts)).toBe(0)
  })

  it('splits the bar by status', () => {
    expect(widths(counts)).toEqual({ sent: 60, failed: 7.5, processing: 5, retry: 2.5, queued: 25 })
    expect(Object.values(widths(emptyCounts)).every((w) => w === 0)).toBe(true)
  })
})

describe('RateMeter', () => {
  it('measures over a sliding window', () => {
    const m = new RateMeter(5_000)
    expect(m.perSecond()).toBe(0)
    m.add({ at: 0, finished: 0 })
    m.add({ at: 1_000, finished: 100 })
    expect(m.perSecond()).toBe(100)
    m.add({ at: 10_000, finished: 1_000 }) // the first sample drops out of the window
    expect(m.perSecond()).toBe(100)
  })
})

describe('formatDuration', () => {
  it('formats seconds and minutes', () => {
    expect(formatDuration(42_400)).toBe('42s')
    expect(formatDuration(185_000)).toBe('3m 05s')
  })
})
