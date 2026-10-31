import type { Condition, EventCondition, FieldCondition, GroupCondition, MatchMode, SegmentRule } from '@/api/types'
import { type Locale, type MessageKey, translate } from './i18n'

// A readable line of a segment rule; groups have children.
export interface RuleLine {
  text: string
  children?: RuleLine[]
}

const fieldKeys: Record<string, MessageKey> = { city: 'rule.field.city', language: 'rule.field.language' }
const languageKeys: Record<string, MessageKey> = { en: 'language.en', tr: 'language.tr' }

// Event phrases: [did it, did not do it].
const eventKeys: Record<string, [MessageKey, MessageKey]> = {
  page_view: ['rule.event.page_view', 'rule.event.page_view.not'],
  cart_add: ['rule.event.cart_add', 'rule.event.cart_add.not'],
  purchase: ['rule.event.purchase', 'rule.event.purchase.not'],
}

function isGroup(c: Condition): c is GroupCondition {
  return 'conditions' in c
}

function isEvent(c: Condition): c is EventCondition {
  return 'event' in c
}

// Describer renders rules in one UI language.
class Describer {
  constructor(private readonly locale: Locale) {}

  private t(key: MessageKey, params?: Record<string, string | number>) {
    return translate(this.locale, key, params)
  }

  match(match: MatchMode): string {
    return this.t(match === 'any' ? 'rule.any' : 'rule.all')
  }

  private values(field: string, values: string[]): string {
    const shown = field === 'language' ? values.map((v) => (languageKeys[v] ? this.t(languageKeys[v]) : v)) : values
    if (shown.length <= 1) return shown.join('')
    return `${shown.slice(0, -1).join(', ')} ${this.t('rule.or')} ${shown.at(-1)}`
  }

  private field(c: FieldCondition): string {
    const field = fieldKeys[c.field] ? this.t(fieldKeys[c.field]!) : c.field
    const values = this.values(c.field, c.value)
    const negated = c.op === 'neq' || c.op === 'not_in'
    return this.t(negated ? 'rule.isNot' : 'rule.is', { field, values })
  }

  private event(c: EventCondition): string {
    const keys = eventKeys[c.event]
    const negated = c.exists === false
    const did = keys
      ? this.t(keys[negated ? 1 : 0])
      : this.t(negated ? 'rule.event.other.not' : 'rule.event.other', { event: c.event })
    const window = c.within_days === 1 ? this.t('rule.lastDay') : this.t('rule.lastDays', { n: c.within_days ?? 0 })
    const times = !negated && c.min_count && c.min_count > 1 ? this.t('rule.atLeast', { n: c.min_count }) : ''
    const line = this.t('rule.eventLine', { did, times, window }).replace(/\s+/g, ' ').trim()
    return line.charAt(0).toLocaleUpperCase(this.locale) + line.slice(1)
  }

  condition(c: Condition): RuleLine {
    if (isGroup(c)) return { text: this.match(c.match), children: c.conditions.map((x) => this.condition(x)) }
    if (isEvent(c)) return { text: this.event(c) }
    return { text: this.field(c) }
  }
}

// describeRule turns a segment rule into readable lines in the UI language.
// A root with one condition is shown without the "All of these" wrapper.
export function describeRule(rule: SegmentRule, locale: Locale = 'en'): RuleLine {
  const d = new Describer(locale)
  if (rule.conditions.length === 0) return { text: translate(locale, 'rule.allUsers') }
  if (rule.conditions.length === 1) return d.condition(rule.conditions[0]!)
  return { text: d.match(rule.match), children: rule.conditions.map((c) => d.condition(c)) }
}
