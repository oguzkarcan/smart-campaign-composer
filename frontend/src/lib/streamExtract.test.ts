import { describe, expect, it } from 'vitest'
import { extractPartial } from './streamExtract'

const full = JSON.stringify({
  sms: { body: 'Merhaba {{first_name}}, sepetin seni bekliyor!' },
  email: { subject: '{{city}} için %30 indirim', body: 'Selam {{first_name}},\n"Sepetim" sayfana göz at.' },
})

describe('extractPartial', () => {
  it('reads every field of a complete answer', () => {
    expect(extractPartial(full)).toEqual(JSON.parse(full))
  })

  it('shows each field as it grows, at every cut', () => {
    const final = JSON.parse(full)
    let lastSms = ''
    for (let n = 0; n <= full.length; n++) {
      const p = extractPartial(full.slice(0, n))
      const sms = p.sms?.body ?? ''
      // Text only ever grows, and is always a prefix of the final value.
      expect(sms.length).toBeGreaterThanOrEqual(lastSms.length)
      expect(final.sms.body.startsWith(sms)).toBe(true)
      expect(final.email.subject.startsWith(p.email?.subject ?? '')).toBe(true)
      expect(final.email.body.startsWith(p.email?.body ?? '')).toBe(true)
      lastSms = sms
    }
  })

  it('decodes escapes and leaves out one cut in half', () => {
    expect(extractPartial('{"sms": {"body": "a\\nb \\"c\\" \\u00e7\\t\\\\')).toEqual({ sms: { body: 'a\nb "c" ç\t\\' } })
    expect(extractPartial('{"sms": {"body": "line\\')).toEqual({ sms: { body: 'line' } })
    expect(extractPartial('{"sms": {"body": "x\\u00')).toEqual({ sms: { body: 'x' } })
    // A surrogate pair cut between its halves.
    expect(extractPartial('{"sms": {"body": "hi \\ud83d')).toEqual({ sms: { body: 'hi ' } })
    expect(extractPartial('{"sms": {"body": "hi \\ud83d\\ude00"')).toEqual({ sms: { body: 'hi 😀' } })
  })

  it('has nothing before a value starts', () => {
    expect(extractPartial('')).toEqual({})
    expect(extractPartial('{"sms": {"bo')).toEqual({})
    expect(extractPartial('{"sms": {"body":')).toEqual({})
    expect(extractPartial('{"sms": {"body": "')).toEqual({ sms: { body: '' } })
  })

  it('skips text before the JSON and fields it does not show', () => {
    const text = 'Here you go:\n```json\n{"notes": "body: x", "sms": {"tags": ["a", {"body": "no"}], "length": 12, "body": "Hi'
    expect(extractPartial(text)).toEqual({ sms: { body: 'Hi' } })
  })

  it('only takes the fields at their place in the answer', () => {
    expect(extractPartial('{"body": "top", "email": {"subject": "S", "meta": {"body": "deep"}, "body": "B"}}')).toEqual({
      email: { subject: 'S', body: 'B' },
    })
  })

  it('stops at the end of the answer', () => {
    expect(extractPartial('{"sms": {"body": "a"}} {"sms": {"body": "b"}}')).toEqual({ sms: { body: 'a' } })
  })
})
