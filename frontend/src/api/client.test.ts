import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  ApiError,
  cancelSchedule,
  editContent,
  generateContent,
  getCampaign,
  getRecipients,
  sendScheduledNow,
  TIMED_OUT,
  UNREACHABLE,
} from './client'

function stubFetch(impl: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>) {
  vi.stubGlobal('fetch', vi.fn(impl))
}

afterEach(() => {
  vi.unstubAllGlobals()
})

async function errorOf(p: Promise<unknown>): Promise<ApiError> {
  const err = await p.then(
    () => null,
    (e: unknown) => e,
  )
  expect(err).toBeInstanceOf(ApiError)
  return err as ApiError
}

describe('api client errors', () => {
  it('keeps the API error message and problems', async () => {
    stubFetch(async () =>
      Response.json({ error: 'invalid content', problems: [{ path: 'sms.body', message: 'too long' }] }, { status: 422 }),
    )
    const err = await errorOf(getCampaign(1))
    expect(err.status).toBe(422)
    expect(err.message).toBe('invalid content')
    expect(err.problems).toEqual([{ path: 'sms.body', message: 'too long' }])
  })

  it('reports a proxy error page as an unreachable API', async () => {
    stubFetch(async () => new Response('<html>502 Bad Gateway</html>', { status: 502 }))
    const err = await errorOf(getCampaign(1))
    expect(err.status).toBe(502)
    expect(err.message).toBe(UNREACHABLE)
  })

  it('reports a network failure as an unreachable API', async () => {
    stubFetch(async () => {
      throw new TypeError('Failed to fetch')
    })
    expect((await errorOf(getCampaign(1))).message).toBe(UNREACHABLE)
  })

  it('times out a request that never answers', async () => {
    // AbortSignal.timeout ignores fake timers; fire it by hand instead.
    const timeout = new AbortController()
    const spy = vi.spyOn(AbortSignal, 'timeout').mockReturnValue(timeout.signal)
    stubFetch(
      (_input, init) =>
        new Promise((_resolve, reject) => init?.signal?.addEventListener('abort', () => reject(init.signal?.reason))),
    )
    const pending = errorOf(getCampaign(1))
    expect(spy).toHaveBeenCalledWith(15_000)
    timeout.abort(new DOMException('signal timed out', 'TimeoutError'))
    expect((await pending).message).toBe(TIMED_OUT)
    spy.mockRestore()
  })

  it('rethrows the caller’s own abort', async () => {
    stubFetch(
      (_input, init) =>
        new Promise((_resolve, reject) => init?.signal?.addEventListener('abort', () => reject(init.signal?.reason))),
    )
    const ctl = new AbortController()
    const pending = getCampaign(1, ctl.signal).catch((e: unknown) => e)
    ctl.abort()
    const err = await pending
    expect(err).not.toBeInstanceOf(ApiError)
    expect((err as DOMException).name).toBe('AbortError')
  })
})

describe('content endpoints', () => {
  it('edits one language and channel', async () => {
    let url = ''
    stubFetch(async (input) => {
      url = String(input)
      return Response.json({ id: 1 })
    })
    await editContent(1, 'en', 'sms', { body: 'Hi' })
    expect(url).toBe('/api/campaigns/1/content/en/sms')
  })

  it('passes the language of every streamed delta', async () => {
    const sse = [
      'event: delta\ndata: {"language":"tr","attempt":1,"text":"Mer"}\n\n',
      'event: delta\ndata: {"language":"en","attempt":1,"text":"Hi"}\n\n',
      'event: done\ndata: {"id":1}\n\n',
    ].join('')
    stubFetch(async () => new Response(sse, { headers: { 'Content-Type': 'text/event-stream' } }))
    const deltas: string[] = []
    const c = await generateContent(1, { onDelta: (lang, attempt, text) => deltas.push(`${lang}${attempt}:${text}`) })
    expect(deltas).toEqual(['tr1:Mer', 'en1:Hi'])
    expect(c.id).toBe(1)
  })
})

describe('schedule actions', () => {
  it('cancels with DELETE and sends now with POST', async () => {
    const calls: string[] = []
    stubFetch(async (input, init) => {
      calls.push(`${init?.method} ${String(input)}`)
      return Response.json({ id: 7, status: 'content_ready' })
    })
    await cancelSchedule(7)
    await sendScheduledNow(7)
    expect(calls).toEqual(['DELETE /api/campaigns/7/schedule', 'POST /api/campaigns/7/send-now'])
  })

  it('reports a lost race as a 409', async () => {
    stubFetch(async () => Response.json({ error: 'not allowed in the campaign\'s current status (status sending)' }, { status: 409 }))
    const err = await errorOf(sendScheduledNow(7))
    expect(err.status).toBe(409)
    expect(err.message).toMatch(/status sending/)
  })
})

describe('recipients', () => {
  it('pages with limit and offset', async () => {
    const urls: string[] = []
    stubFetch(async (input) => {
      urls.push(String(input))
      return Response.json({ recipients: [], total: 0, limit: 15, offset: 0 })
    })
    await getRecipients(7)
    await getRecipients(7, { limit: 15, offset: 30 })
    await getRecipients(7, { limit: 15, sort: 'city', order: 'desc' })
    expect(urls).toEqual([
      '/api/campaigns/7/recipients',
      '/api/campaigns/7/recipients?limit=15&offset=30',
      '/api/campaigns/7/recipients?limit=15&sort=city&order=desc',
    ])
  })
})
