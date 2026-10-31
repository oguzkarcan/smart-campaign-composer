export interface SSEMessage {
  event: string
  data: string
}

// readSSE parses a text/event-stream body into messages. It is used for
// streams EventSource cannot open (POST requests). Comments (": ...") and
// fields other than event/data are ignored; multiple data lines are joined
// with "\n" as the spec says.
export async function* readSSE(body: ReadableStream<Uint8Array>): AsyncGenerator<SSEMessage> {
  const reader = body.pipeThrough(new TextDecoderStream()).getReader()
  let buffer = ''
  let event = ''
  let data: string[] = []
  try {
    for (;;) {
      const { value, done } = await reader.read()
      if (done) break
      buffer += value
      let nl: number
      while ((nl = buffer.search(/\r\n|\r|\n/)) >= 0) {
        if (nl === buffer.length - 1 && buffer[nl] === '\r') break // may be half of \r\n
        const line = buffer.slice(0, nl)
        buffer = buffer.slice(nl + (buffer.startsWith('\r\n', nl) ? 2 : 1))
        if (line === '') {
          if (data.length > 0) yield { event: event || 'message', data: data.join('\n') }
          event = ''
          data = []
          continue
        }
        if (line.startsWith(':')) continue
        const colon = line.indexOf(':')
        const field = colon < 0 ? line : line.slice(0, colon)
        let value = colon < 0 ? '' : line.slice(colon + 1)
        if (value.startsWith(' ')) value = value.slice(1)
        if (field === 'event') event = value
        else if (field === 'data') data.push(value)
      }
    }
  } finally {
    reader.releaseLock()
  }
}
