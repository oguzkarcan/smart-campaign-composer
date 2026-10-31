import { describe, expect, it } from 'vitest'
import { readSSE, type SSEMessage } from './sse'

function streamOf(...chunks: string[]): ReadableStream<Uint8Array> {
  const enc = new TextEncoder()
  return new ReadableStream({
    start(c) {
      for (const chunk of chunks) c.enqueue(enc.encode(chunk))
      c.close()
    },
  })
}

async function collect(body: ReadableStream<Uint8Array>): Promise<SSEMessage[]> {
  const out: SSEMessage[] = []
  for await (const m of readSSE(body)) out.push(m)
  return out
}

describe('readSSE', () => {
  it('parses named events', async () => {
    const got = await collect(
      streamOf('event: delta\ndata: {"attempt":1,"text":"Hi"}\n\nevent: done\ndata: {"id":1}\n\n'),
    )
    expect(got).toEqual([
      { event: 'delta', data: '{"attempt":1,"text":"Hi"}' },
      { event: 'done', data: '{"id":1}' },
    ])
  })

  it('handles events split across chunks, including a split \\r\\n', async () => {
    const got = await collect(streamOf('event: del', 'ta\r', '\ndata: {"a"', ':1}\r\n\r', '\n'))
    expect(got).toEqual([{ event: 'delta', data: '{"a":1}' }])
  })

  it('ignores comments and unknown fields, joins data lines', async () => {
    const got = await collect(streamOf(': keep-alive\n\nid: 7\ndata: one\ndata: two\n\n'))
    expect(got).toEqual([{ event: 'message', data: 'one\ntwo' }])
  })

  it('drops an incomplete trailing event', async () => {
    const got = await collect(streamOf('event: done\ndata: {}\n'))
    expect(got).toEqual([])
  })
})
