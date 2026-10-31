import { readSSE } from './sse'
import type {
  Campaign,
  CampaignSummary,
  Channel,
  ContentEdit,
  ErrorBody,
  Problem,
  Progress,
} from './types'

// All requests go to same-origin /api, proxied to the API by Vite or nginx.
const BASE = '/api'

export class ApiError extends Error {
  readonly status: number
  readonly problems: Problem[]

  constructor(status: number, body: ErrorBody) {
    super(body.error || `request failed with status ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.problems = body.problems ?? []
  }
}

async function errorFrom(res: Response): Promise<ApiError> {
  let body: ErrorBody = { error: `${res.status} ${res.statusText}`.trim() }
  try {
    const parsed = (await res.json()) as Partial<ErrorBody>
    if (typeof parsed.error === 'string') body = { ...body, ...parsed }
  } catch {
    // not JSON (e.g. a proxy error page); keep the status text
  }
  return new ApiError(res.status, body)
}

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const res = await fetch(BASE + path, {
    method,
    signal,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw await errorFrom(res)
  return (await res.json()) as T
}

export interface ListParams {
  limit?: number
  before?: number // id of the last campaign of the previous page
}

export function listCampaigns(params: ListParams = {}, signal?: AbortSignal): Promise<CampaignSummary[]> {
  const q = new URLSearchParams()
  if (params.limit) q.set('limit', String(params.limit))
  if (params.before) q.set('before', String(params.before))
  const qs = q.toString()
  return request('GET', `/campaigns${qs ? `?${qs}` : ''}`, undefined, signal)
}

export function getCampaign(id: number, signal?: AbortSignal): Promise<Campaign> {
  return request('GET', `/campaigns/${id}`, undefined, signal)
}

// createCampaign stores the campaign and generates its segment. If generation
// fails the campaign is still returned, as a draft with last_error set.
export function createCampaign(brief: string, channels: Channel[]): Promise<Campaign> {
  return request('POST', '/campaigns', { brief, channels })
}

export function generateSegment(id: number): Promise<Campaign> {
  return request('POST', `/campaigns/${id}/segment`)
}

export function editContent(id: number, channel: Channel, edit: ContentEdit): Promise<Campaign> {
  return request('PUT', `/campaigns/${id}/content/${channel}`, edit)
}

export function approveCampaign(id: number): Promise<Campaign> {
  return request('POST', `/campaigns/${id}/approve`)
}

export function getProgress(id: number, signal?: AbortSignal): Promise<Progress> {
  return request('GET', `/campaigns/${id}/progress`, undefined, signal)
}

export interface ContentStreamHandlers {
  // onDelta receives streamed model output. A higher attempt number starts a
  // repair round, so the preview should be cleared.
  onDelta?: (attempt: number, text: string) => void
  signal?: AbortSignal
}

// generateContent streams content generation and resolves with the campaign
// once the output is validated and saved. It rejects with an ApiError for a
// plain error response or an error event.
export async function generateContent(id: number, handlers: ContentStreamHandlers = {}): Promise<Campaign> {
  const res = await fetch(`${BASE}/campaigns/${id}/content`, {
    method: 'POST',
    signal: handlers.signal,
    headers: { Accept: 'text/event-stream' },
  })
  if (!res.ok) throw await errorFrom(res)
  if (!res.body) throw new ApiError(res.status, { error: 'empty response stream' })

  for await (const msg of readSSE(res.body)) {
    switch (msg.event) {
      case 'delta': {
        const d = JSON.parse(msg.data) as { attempt: number; text: string }
        handlers.onDelta?.(d.attempt, d.text)
        break
      }
      case 'done':
        return JSON.parse(msg.data) as Campaign
      case 'error': {
        const body = JSON.parse(msg.data) as ErrorBody
        throw new ApiError(body.status ?? 500, body)
      }
    }
  }
  throw new ApiError(502, { error: 'the stream ended before generation finished' })
}

export interface ProgressHandlers {
  onProgress: (p: Progress) => void
  onError?: (err: Error) => void
}

// watchProgress follows a campaign's live progress over SSE until it is done
// (completed or failed), then closes the stream so EventSource does not
// reconnect. Network drops are retried by EventSource itself. It returns a
// function that stops watching.
export function watchProgress(id: number, handlers: ProgressHandlers): () => void {
  const source = new EventSource(`${BASE}/campaigns/${id}/events`)
  source.addEventListener('progress', (e) => {
    const p = JSON.parse((e as MessageEvent<string>).data) as Progress
    handlers.onProgress(p)
    if (p.done) source.close()
  })
  // The server's own error event (e.g. a database failure mid-stream).
  source.addEventListener('error', (e) => {
    if (e instanceof MessageEvent && typeof e.data === 'string') {
      const body = JSON.parse(e.data) as ErrorBody
      handlers.onError?.(new ApiError(body.status ?? 500, body))
      source.close()
    } else if (source.readyState === EventSource.CLOSED) {
      // A non-200 answer (e.g. 404): EventSource gives up without a reason.
      handlers.onError?.(new Error('live updates are unavailable'))
    }
  })
  return () => source.close()
}

export function health(signal?: AbortSignal): Promise<{ status: string; checks: Record<string, string> }> {
  return request('GET', '/health', undefined, signal)
}
