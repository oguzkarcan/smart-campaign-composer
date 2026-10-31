// Types mirroring the API's JSON (backend/internal/campaign, segment, httpapi).

export type Channel = 'sms' | 'email'

export const CHANNELS: readonly Channel[] = ['sms', 'email']

// Recipient languages; content is written once per language in the audience.
export type Language = 'tr' | 'en'

export const LANGUAGES: readonly Language[] = ['tr', 'en']

export type CampaignStatus =
  | 'draft'
  | 'audience_ready'
  | 'content_ready'
  | 'approved'
  | 'scheduled'
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

export interface AudienceCounts {
  matched: number
  sms: number
  email: number
}

export interface Audience extends AudienceCounts {
  // The same counts per user language (languages that matched anybody).
  languages?: Partial<Record<Language, AudienceCounts>>
}

export interface Content {
  language: Language
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
  // When a scheduled campaign starts sending.
  scheduled_at?: string
  created_at: string
  updated_at: string
  // Languages the campaign has or needs content for (reachable readers now,
  // plus any with content).
  languages?: Language[]
  // Per language, characters each variable counts as in the worst-case
  // length check, and the maximum worst-case length per path ("sms.body",
  // "email.subject").
  variable_lengths?: Partial<Record<Language, Record<string, number>>>
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
  scheduled_at?: string
  created_at: string
  updated_at: string
}

// A user the campaign's rule matches (GET /campaigns/{id}/recipients).
export interface Person {
  id: number
  first_name: string
  city: string
  language: Language
  phone: string
  email: string
  sms_consent: boolean
  email_consent: boolean
}

export interface People {
  recipients: Person[]
  total: number
  limit: number
  offset: number
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
