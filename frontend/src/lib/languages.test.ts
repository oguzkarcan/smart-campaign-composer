import { describe, expect, it } from 'vitest'
import type { Campaign, Content } from '@/api/types'
import { languageTabs } from './languages'

const content = (language: 'tr' | 'en', channel: 'sms' | 'email'): Content => ({
  language,
  channel,
  body: 'x',
  human_edited: false,
  version: 1,
  updated_at: '',
})

const base: Campaign = {
  id: 1,
  brief: 'b',
  status: 'content_ready',
  channels: ['sms', 'email'],
  segment_rule: null,
  audience: {
    matched: 230,
    sms: 120,
    email: 160,
    languages: { tr: { matched: 180, sms: 100, email: 130 }, en: { matched: 50, sms: 20, email: 30 } },
  },
  contents: [content('tr', 'sms'), content('tr', 'email'), content('en', 'sms'), content('en', 'email')],
  languages: ['tr', 'en'],
  created_at: '',
  updated_at: '',
  limits: {},
}

describe('languageTabs', () => {
  it('counts recipients over the campaign channels, in display order', () => {
    expect(languageTabs({ ...base, languages: ['en', 'tr'] })).toEqual([
      { language: 'tr', recipients: 230, hasContent: true },
      { language: 'en', recipients: 50, hasContent: true },
    ])
    expect(languageTabs({ ...base, channels: ['sms'] }).map((t) => t.recipients)).toEqual([100, 20])
  })

  it('flags a needed language without messages', () => {
    const tabs = languageTabs({ ...base, contents: base.contents!.slice(0, 2) })
    expect(tabs[1]).toEqual({ language: 'en', recipients: 50, hasContent: false })
  })

  it('falls back to the content languages without a language list', () => {
    const old = { ...base, languages: undefined, audience: { matched: 1, sms: 1, email: 0 }, contents: [content('tr', 'sms')] }
    expect(languageTabs(old)).toEqual([{ language: 'tr', recipients: 0, hasContent: true }])
  })
})
