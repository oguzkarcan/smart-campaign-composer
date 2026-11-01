import type { CampaignStatus } from '@/api/types'
import type { MessageKey } from './i18n'

export type StepKey = 'audience' | 'messages' | 'delivery'
export type StepState = 'done' | 'current' | 'upcoming' | 'failed'
export type StepNumber = 1 | 2 | 3

export interface Step {
  key: StepKey
  state: StepState
  // Short status line under the step name (a message key).
  detail: MessageKey
}

// campaignSteps maps a campaign status to the three steps a marketer goes
// through: Audience → Messages → Delivery.
export function campaignSteps(status: CampaignStatus): Step[] {
  const audienceDone: Step = { key: 'audience', state: 'done', detail: 'steps.audience.done' }
  const messagesDone: Step = { key: 'messages', state: 'done', detail: 'steps.messages.done' }
  switch (status) {
    case 'draft':
      return [
        { key: 'audience', state: 'current', detail: 'steps.audience.current' },
        { key: 'messages', state: 'upcoming', detail: 'steps.messages.upcoming' },
        { key: 'delivery', state: 'upcoming', detail: 'steps.delivery.upcoming' },
      ]
    case 'audience_ready':
    case 'content_ready':
      return [
        audienceDone,
        { key: 'messages', state: 'current', detail: 'steps.messages.current' },
        { key: 'delivery', state: 'upcoming', detail: 'steps.delivery.upcoming' },
      ]
    case 'scheduled':
      return [audienceDone, messagesDone, { key: 'delivery', state: 'current', detail: 'steps.delivery.scheduled' }]
    case 'approved':
    case 'sending':
      return [audienceDone, messagesDone, { key: 'delivery', state: 'current', detail: 'steps.delivery.sending' }]
    case 'completed':
      return [audienceDone, messagesDone, { key: 'delivery', state: 'done', detail: 'steps.delivery.completed' }]
    case 'failed':
      return [audienceDone, messagesDone, { key: 'delivery', state: 'failed', detail: 'steps.delivery.failed' }]
  }
}

// defaultStep is the step a campaign page opens on: a new audience is reviewed
// before anything else, then the messages, then the live dashboard once they
// are approved.
export function defaultStep(status: CampaignStatus): StepNumber {
  switch (status) {
    case 'draft':
    case 'audience_ready':
      return 1
    case 'content_ready':
      return 2
    default:
      return 3
  }
}
