// Types mirroring the API's JSON (backend/internal/campaign, segment, httpapi).

export type Channel = 'sms' | 'email'

export const CHANNELS: readonly Channel[] = ['sms', 'email']

export type CampaignStatus =
  | 'draft'
  | 'audience_ready'
  | 'content_ready'
  | 'approved'
  | 'sending'
  | 'completed'
  | 'failed'

export type MatchMode = 'all' | 'any'

// A segment condition is exactly one of three kinds, decided by which key is set.
export interface FieldCondition {
  field: string
  op: 'eq' | 'neq' | 'in' | 'not_in'
  value: string[]
}

export interface EventCondition {
  event: string
  within_days?: number
  exists?: boolean
  min_count?: number
}

export interface GroupCondition {
  match: MatchMode
  conditions: Condition[]
}

export type Condition = FieldCondition | EventCondition | GroupCondition

export interface SegmentRule {
  match: MatchMode
  conditions: Condition[]
}

export interface Audience {
  matched: number
  sms: number
  email: number
}

export interface Content {
  channel: Channel
  subject?: string
  body: string
  human_edited: boolean
  version: number
  updated_at: string
}

export interface Campaign {
  id: number
  brief: string
  status: CampaignStatus
  channels: Channel[]
  segment_rule: SegmentRule | null
  audience: Audience | null
  contents: Content[] | null
  last_error?: string
  approved_at?: string
  created_at: string
  updated_at: string
  // Characters each variable counts as in the worst-case length check, and
  // the maximum worst-case length per path ("sms.body", "email.subject").
  variable_lengths?: Record<string, number>
  limits: Record<string, number>
}

export interface Counts {
  total: number
  queued: number
  processing: number
  retry: number
  sent: number
  failed: number
}

export interface FailureReason {
  error: string
  count: number
}

export interface Progress {
  campaign_id: number
  status: CampaignStatus
  done: boolean
  totals: Counts
  channels: Partial<Record<Channel, Counts>>
  failures: FailureReason[]
}

export interface CampaignSummary {
  id: number
  brief: string
  status: CampaignStatus
  channels: Channel[]
  messages: Counts
  last_error?: string
  approved_at?: string
  created_at: string
  updated_at: string
}

export interface Problem {
  path: string
  message: string
}

export interface ErrorBody {
  status?: number // only in SSE error events
  error: string
  problems?: Problem[]
}

export interface ContentEdit {
  subject?: string
  body: string
}
