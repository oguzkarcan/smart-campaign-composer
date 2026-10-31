import { describe, expect, it } from 'vitest'
import { describeRule } from './segment'

describe('describeRule', () => {
  it('describes an empty rule as everyone', () => {
    expect(describeRule({ match: 'all', conditions: [] })).toEqual({ text: 'All users' })
  })

  it('unwraps a single condition', () => {
    expect(describeRule({ match: 'all', conditions: [{ field: 'city', op: 'eq', value: ['Ankara'] }] })).toEqual({
      text: 'City is Ankara',
    })
  })

  it('describes fields, events and nested groups', () => {
    const got = describeRule({
      match: 'all',
      conditions: [
        { field: 'city', op: 'in', value: ['İstanbul', 'Ankara', 'İzmir'] },
        { field: 'language', op: 'neq', value: ['tr'] },
        {
          match: 'any',
          conditions: [
            { event: 'cart_add', within_days: 7, exists: true, min_count: 2 },
            { event: 'page_view', within_days: 1, exists: true },
          ],
        },
        { event: 'purchase', within_days: 30, exists: false },
      ],
    })
    expect(got).toEqual({
      text: 'All of these',
      children: [
        { text: 'City is İstanbul, Ankara or İzmir' },
        { text: 'Language is not Turkish' },
        {
          text: 'Any of these',
          children: [{ text: 'Added to cart at least 2 times in the last 7 days' }, { text: 'Viewed a page in the last day' }],
        },
        { text: 'Did not make a purchase in the last 30 days' },
      ],
    })
  })

  it('falls back to raw names for unknown fields and events', () => {
    const got = describeRule({
      match: 'any',
      conditions: [
        { field: 'tier', op: 'not_in', value: ['gold', 'silver'] },
        { event: 'signup', within_days: 3, exists: true },
      ],
    })
    expect(got.children).toEqual([{ text: 'tier is not gold or silver' }, { text: 'Did signup in the last 3 days' }])
  })
})
