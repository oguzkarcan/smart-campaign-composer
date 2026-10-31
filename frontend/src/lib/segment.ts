import type { Condition, EventCondition, FieldCondition, GroupCondition, MatchMode, SegmentRule } from '@/api/types'

// A readable line of a segment rule; groups have children.
export interface RuleLine {
  text: string
  children?: RuleLine[]
}

const fieldLabels: Record<string, string> = { city: 'City', language: 'Language' }

const languageNames: Record<string, string> = { en: 'English', tr: 'Turkish' }

// Event phrases: [did it, did not do it].
const eventPhrases: Record<string, [string, string]> = {
  page_view: ['Viewed a page', 'Did not view any page'],
  cart_add: ['Added to cart', 'Did not add to cart'],
  purchase: ['Made a purchase', 'Did not make a purchase'],
}

function isGroup(c: Condition): c is GroupCondition {
  return 'conditions' in c
}

function isEvent(c: Condition): c is EventCondition {
  return 'event' in c
}

function matchText(match: MatchMode): string {
  return match === 'any' ? 'Any of these' : 'All of these'
}

function listValues(field: string, values: string[]): string {
  const shown = field === 'language' ? values.map((v) => languageNames[v] ?? v) : values
  if (shown.length <= 1) return shown.join('')
  return `${shown.slice(0, -1).join(', ')} or ${shown.at(-1)}`
}

function describeField(c: FieldCondition): string {
  const label = fieldLabels[c.field] ?? c.field
  const values = listValues(c.field, c.value)
  switch (c.op) {
    case 'eq':
    case 'in':
      return `${label} is ${values}`
    case 'neq':
    case 'not_in':
      return `${label} is not ${values}`
  }
}

function describeEvent(c: EventCondition): string {
  const [did, didNot] = eventPhrases[c.event] ?? [`Did ${c.event}`, `Did not do ${c.event}`]
  const window = c.within_days === 1 ? 'in the last day' : `in the last ${c.within_days} days`
  if (c.exists === false) return `${didNot} ${window}`
  const times = c.min_count && c.min_count > 1 ? ` at least ${c.min_count} times` : ''
  return `${did}${times} ${window}`
}

function describeCondition(c: Condition): RuleLine {
  if (isGroup(c)) return { text: matchText(c.match), children: c.conditions.map(describeCondition) }
  if (isEvent(c)) return { text: describeEvent(c) }
  return { text: describeField(c) }
}

// describeRule turns a segment rule into readable lines. A root with one
// condition is shown without the "All of these" wrapper.
export function describeRule(rule: SegmentRule): RuleLine {
  if (rule.conditions.length === 0) return { text: 'All users' }
  if (rule.conditions.length === 1) return describeCondition(rule.conditions[0]!)
  return { text: matchText(rule.match), children: rule.conditions.map(describeCondition) }
}
