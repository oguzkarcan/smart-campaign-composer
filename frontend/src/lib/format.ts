import type { CampaignStatus, Channel } from '@/api/types'

export const channelLabels: Record<Channel, string> = { sms: 'SMS', email: 'Email' }

export const statusLabels: Record<CampaignStatus, string> = {
  draft: 'Draft',
  audience_ready: 'Audience ready',
  content_ready: 'Content ready',
  approved: 'Approved',
  scheduled: 'Scheduled',
  sending: 'Sending',
  completed: 'Completed',
  failed: 'Failed',
}

// Statuses whose segment and content can still change.
export const editableStatuses: readonly CampaignStatus[] = ['draft', 'audience_ready', 'content_ready']

const numberFormat = new Intl.NumberFormat('en-US')

export function formatNumber(n: number): string {
  return numberFormat.format(n)
}

const dateTimeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })

export function formatDateTime(iso: string): string {
  return dateTimeFormat.format(new Date(iso))
}
