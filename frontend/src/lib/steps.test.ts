import { describe, expect, it } from 'vitest'
import { campaignSteps, defaultStep } from './steps'

const states = (s: Parameters<typeof campaignSteps>[0]) => campaignSteps(s).map((x) => x.state)

describe('campaignSteps', () => {
  it('moves through Audience → Messages → Delivery', () => {
    expect(states('draft')).toEqual(['current', 'upcoming', 'upcoming'])
    expect(states('audience_ready')).toEqual(['done', 'current', 'upcoming'])
    expect(states('content_ready')).toEqual(['done', 'current', 'upcoming'])
    expect(states('scheduled')).toEqual(['done', 'done', 'current'])
    expect(states('sending')).toEqual(['done', 'done', 'current'])
    expect(states('completed')).toEqual(['done', 'done', 'done'])
    expect(states('failed')).toEqual(['done', 'done', 'failed'])
  })

  it('says what is happening in the delivery step', () => {
    expect(campaignSteps('scheduled')[2]!.detail).toBe('steps.delivery.scheduled')
    expect(campaignSteps('sending')[2]!.detail).toBe('steps.delivery.sending')
  })
})

describe('defaultStep', () => {
  it('opens where the next action is', () => {
    expect(defaultStep('draft')).toBe(1)
    expect(defaultStep('audience_ready')).toBe(1)
    expect(defaultStep('content_ready')).toBe(2)
  })

  it('opens the dashboard once approved', () => {
    for (const s of ['scheduled', 'approved', 'sending', 'completed', 'failed'] as const) expect(defaultStep(s)).toBe(3)
  })
})
