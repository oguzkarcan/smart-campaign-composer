import { describe, expect, it } from 'vitest'
import { translate } from './i18n'

describe('translate', () => {
  it('fills placeholders and leaves unknown ones', () => {
    expect(translate('en', 'list.sent', { sent: 5, total: 10 })).toBe('5 / 10 sent')
    expect(translate('tr', 'list.sent', { sent: 5, total: 10 })).toBe('5 / 10 gönderildi')
    expect(translate('en', 'list.sent', { sent: 5 })).toBe('5 / {total} sent')
  })

  it('has every label in both languages', () => {
    expect(translate('tr', 'status.scheduled')).toBe('Zamanlandı')
    expect(translate('tr', 'channel.email')).toBe('E-posta')
  })
})
