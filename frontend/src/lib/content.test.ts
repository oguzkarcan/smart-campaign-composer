import { describe, expect, it } from 'vitest'
import { canonicalize, charCount, checkField, render, worstCase } from './content'

const lens = { first_name: 14, city: 13 }

describe('canonicalize', () => {
  it('normalizes spacing inside variables', () => {
    expect(canonicalize('Hi {{ first_name }}, {{city}}')).toEqual({ text: 'Hi {{first_name}}, {{city}}', problems: [] })
  })

  it('rejects unknown variables, single braces and stray braces', () => {
    expect(canonicalize('Hi {{last_name}}').problems[0]).toMatch(/unknown variable "\{\{last_name\}\}"/)
    expect(canonicalize('Hi {first_name}').problems[0]).toMatch(/single braces/)
    expect(canonicalize('50% off }').problems[0]).toMatch(/stray/)
  })
})

describe('worstCase', () => {
  it('counts code points, not UTF-16 units', () => {
    expect(charCount('İ😀')).toBe(2)
  })

  it('replaces each variable with its counted length', () => {
    // "Hi " (3) + 14 + ", " (2) + 13 = 32
    expect(worstCase('Hi {{first_name}}, {{city}}', lens)).toBe(32)
  })

  it('ignores spacing and counts unknown variables as written', () => {
    expect(worstCase('{{ city }}', lens)).toBe(13)
    expect(worstCase('{{last_name}}', lens)).toBe(13)
  })

  it('never counts a variable shorter than its placeholder, and uses a fallback when unmeasured', () => {
    expect(worstCase('{{city}}', { city: 2 })).toBe(8)
    expect(worstCase('{{city}}', {})).toBe(40)
  })
})

describe('checkField', () => {
  it('accepts text within the limit after trimming', () => {
    expect(checkField('  Hi {{ first_name }}  ', 160, lens)).toEqual({ worstCase: 17, limit: 160, problems: [] })
  })

  it('reports the worst-case overflow like the server', () => {
    const got = checkField(`${'a'.repeat(150)} {{first_name}}`, 160, lens)
    expect(got.worstCase).toBe(165)
    expect(got.problems).toEqual(['is 165 characters in the worst case, max 160; shorten it by at least 5'])
  })

  it('measures and reports length even when a variable is broken', () => {
    const got = checkField(`${'a'.repeat(160)} {last_name}`, 160, lens)
    expect(got.worstCase).toBe(172)
    expect(got.problems).toHaveLength(2)
  })

  it('rejects empty and multi-line subjects', () => {
    expect(checkField('   ', 80, lens).problems).toEqual(['must not be empty'])
    expect(checkField('Hello\nthere', 80, lens, { singleLine: true }).problems).toEqual(['must be a single line'])
  })
})

describe('render', () => {
  it('fills variables with sample values', () => {
    expect(render('Hi {{ first_name }} from {{city}}', { first_name: 'Ayşe', city: 'İzmir' })).toBe('Hi Ayşe from İzmir')
  })
})
