import { readSSE } from './sse'
import type {
  Campaign,
  CampaignSummary,
  Channel,
  ContentEdit,
  Language,
  People,
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

export const UNREACHABLE = 'Cannot reach the API. Check that the backend is running, then try again.'
export const TIMED_OUT = 'The API did not answer in time. Try again.'

// Timeouts: reads are quick; calls that wait for the LLM (with up to two
// repair rounds) can take much longer.
const READ_TIMEOUT_MS = 15_000
const WRITE_TIMEOUT_MS = 30_000
const LLM_TIMEOUT_MS = 180_000

// Gateway answers from the proxy, not the API, when the API is down.
const gatewayStatuses = [502, 503, 504]

async function errorFrom(res: Response): Promise<ApiError> {
  let body: ErrorBody = { error: `${res.status} ${res.statusText}`.trim() }
  try {
    const parsed = (await res.json()) as Partial<ErrorBody>
    if (typeof parsed.error === 'string') body = { ...body, ...parsed }
    return new ApiError(res.status, body)
  } catch {
    // Not JSON: a proxy error page rather than the API.
  }
  if (gatewayStatuses.includes(res.status)) body = { error: UNREACHABLE }
  return new ApiError(res.status, body)
}

// send wraps fetch so that network failures and timeouts become ApiErrors
// with a message for the marketer. A caller's own abort is rethrown as is.
async function send(path: string, init: RequestInit, timeoutMs?: number): Promise<Response> {
  const signals = [init.signal, timeoutMs ? AbortSignal.timeout(timeoutMs) : undefined].filter(
    (s): s is AbortSignal => !!s,
  )
  try {
    return await fetch(BASE + path, { ...init, signal: signals.length ? AbortSignal.any(signals) : undefined })
  } catch (e) {
    if (init.signal?.aborted) throw e
    if (e instanceof DOMException && e.name === 'TimeoutError') throw new ApiError(0, { error: TIMED_OUT })
    throw new ApiError(0, { error: UNREACHABLE })
  }
}

async function request<T>(
  method: string,
  path: string,
  opts: { body?: unknown; signal?: AbortSignal; timeoutMs?: number } = {},
): Promise<T> {
  const { body, signal, timeoutMs = method === 'GET' ? READ_TIMEOUT_MS : WRITE_TIMEOUT_MS } = opts
  const res = await send(
    path,
    {
      method,
      signal,
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    },
    timeoutMs,
  )
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
  return request('GET', `/campaigns${qs ? `?${qs}` : ''}`, { signal })
}

export function getCampaign(id: number, signal?: AbortSignal): Promise<Campaign> {
  return request('GET', `/campaigns/${id}`, { signal })
}

// createCampaign stores the campaign and generates its segment. If generation
// fails the campaign is still returned, as a draft with last_error set.
export function createCampaign(brief: string, channels: Channel[]): Promise<Campaign> {
  return request('POST', '/campaigns', { body: { brief, channels }, timeoutMs: LLM_TIMEOUT_MS })
}

export function generateSegment(id: number): Promise<Campaign> {
  return request('POST', `/campaigns/${id}/segment`, { timeoutMs: LLM_TIMEOUT_MS })
}

export function editContent(id: number, language: Language, channel: Channel, edit: ContentEdit): Promise<Campaign> {
  return request('PUT', `/campaigns/${id}/content/${language}/${channel}`, { body: edit })
}

// approveCampaign sends now, or at sendAt (an ISO time) if given.
export function approveCampaign(id: number, sendAt?: string): Promise<Campaign> {
  return request('POST', `/campaigns/${id}/approve`, { body: sendAt ? { send_at: sendAt } : undefined })
}

// getRecipients pages through the users the campaign's rule matches now.
export function getRecipients(
  id: number,
  page: { limit?: number; offset?: number } = {},
  signal?: AbortSignal,
): Promise<People> {
  const q = new URLSearchParams()
  if (page.limit) q.set('limit', String(page.limit))
  if (page.offset) q.set('offset', String(page.offset))
  const qs = q.toString()
  return request('GET', `/campaigns/${id}/recipients${qs ? `?${qs}` : ''}`, { signal })
}

export function getProgress(id: number, signal?: AbortSignal): Promise<Progress> {
  return request('GET', `/campaigns/${id}/progress`, { signal })
}

export interface ContentStreamHandlers {
  // onDelta receives streamed model output. Languages are written one after
  // another; a new language or a higher attempt number (a repair round)
  // starts a new answer, so the preview should be cleared.
  onDelta?: (language: Language, attempt: number, text: string) => void
  signal?: AbortSignal
}

// generateContent streams content generation and resolves with the campaign
// once the output is validated and saved. It rejects with an ApiError for a
// plain error response or an error event.
export async function generateContent(id: number, handlers: ContentStreamHandlers = {}): Promise<Campaign> {
  // No overall timeout: the stream shows progress, and the user can cancel.
  const res = await send(`/campaigns/${id}/content`, {
    method: 'POST',
    signal: handlers.signal,
    headers: { Accept: 'text/event-stream' },
  })
  if (!res.ok) throw await errorFrom(res)
  if (!res.body) throw new ApiError(res.status, { error: 'empty response stream' })

  for await (const msg of readSSE(res.body)) {
    switch (msg.event) {
      case 'delta': {
        const d = JSON.parse(msg.data) as { language: Language; attempt: number; text: string }
        handlers.onDelta?.(d.language, d.attempt, d.text)
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
  // onConnection reports whether the stream is live or EventSource is
  // reconnecting after a network drop.
  onConnection?: (state: 'live' | 'reconnecting') => void
}

// watchProgress follows a campaign's live progress over SSE until it is done
// (completed or failed), then closes the stream so EventSource does not
// reconnect. Network drops are retried by EventSource itself. It returns a
// function that stops watching.
export function watchProgress(id: number, handlers: ProgressHandlers): () => void {
  const source = new EventSource(`${BASE}/campaigns/${id}/events`)
  source.addEventListener('open', () => handlers.onConnection?.('live'))
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
    } else {
      handlers.onConnection?.('reconnecting')
    }
  })
  return () => source.close()
}

export function health(signal?: AbortSignal): Promise<{ status: string; checks: Record<string, string> }> {
  return request('GET', '/health', { signal })
}
