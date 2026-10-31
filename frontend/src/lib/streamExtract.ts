// Reads the message fields out of a content answer while it is still
// streaming, so the editors and previews can fill in as Claude writes. The
// answer is JSON ({"sms": {"body"}, "email": {"subject", "body"}}) cut off
// anywhere; this is a forgiving scanner, not a parser: it follows object keys
// to know where it is, decodes string values (escapes included) up to where
// the text ends, and ignores everything else. Text before the first "{"
// (prose, a code fence) is skipped. The server's strict parse and validation
// stay the only judge of the final answer.

export interface PartialContent {
  sms?: { body: string }
  email?: { subject?: string; body?: string }
}

// A container being scanned: an object (with its current key) or an array.
type Frame = { kind: 'object'; key: string | null; expectKey: boolean } | { kind: 'array' }

const ESCAPES: Record<string, string> = { '"': '"', '\\': '\\', '/': '/', b: '\b', f: '\f', n: '\n', r: '\r', t: '\t' }

// readString decodes the JSON string starting after the opening quote at
// start. complete is false if the text ends first; an escape cut in half is
// left out rather than shown raw.
function readString(text: string, start: number): { value: string; end: number; complete: boolean } {
  let value = ''
  let i = start
  while (i < text.length) {
    const c = text[i]!
    if (c === '"') return { value, end: i + 1, complete: true }
    if (c !== '\\') {
      value += c
      i++
      continue
    }
    const e = text[i + 1]
    if (e === undefined) break
    if (e === 'u') {
      const hex = text.slice(i + 2, i + 6)
      if (hex.length < 4) break
      if (/^[0-9a-fA-F]{4}$/.test(hex)) value += String.fromCharCode(parseInt(hex, 16))
      i += 6
      continue
    }
    value += ESCAPES[e] ?? e
    i += 2
  }
  // A surrogate pair split by the cut shows as nothing until its other half.
  if (/[\uD800-\uDBFF]$/.test(value)) value = value.slice(0, -1)
  return { value, end: text.length, complete: false }
}

const FIELDS = ['sms.body', 'email.subject', 'email.body'] as const
type Field = (typeof FIELDS)[number]

function assign(out: PartialContent, field: Field, value: string) {
  switch (field) {
    case 'sms.body':
      out.sms = { body: value }
      break
    case 'email.subject':
      out.email = { ...out.email, subject: value }
      break
    case 'email.body':
      out.email = { ...out.email, body: value }
      break
  }
}

// extractPartial returns the message fields found so far in a streamed
// answer, the last one possibly unfinished. Fields not started are absent.
export function extractPartial(text: string): PartialContent {
  const out: PartialContent = {}
  const stack: Frame[] = []
  let i = text.indexOf('{')
  if (i < 0) return out

  while (i < text.length) {
    const c = text[i]!
    const top = stack[stack.length - 1]
    switch (c) {
      case '{':
        stack.push({ kind: 'object', key: null, expectKey: true })
        i++
        continue
      case '[':
        stack.push({ kind: 'array' })
        i++
        continue
      case '}':
      case ']':
        stack.pop()
        if (stack.length === 0) return out // the answer is complete
        i++
        continue
      case ',':
        if (top?.kind === 'object') {
          top.key = null
          top.expectKey = true
        }
        i++
        continue
      case ':':
        if (top?.kind === 'object') top.expectKey = false
        i++
        continue
      case '"': {
        const s = readString(text, i + 1)
        if (top?.kind === 'object' && top.expectKey) {
          if (!s.complete) return out
          top.key = s.value
        } else if (stack.every((f) => f.kind === 'object' && f.key !== null)) {
          const path = stack.map((f) => (f.kind === 'object' ? f.key : '')).join('.')
          if ((FIELDS as readonly string[]).includes(path)) assign(out, path as Field, s.value)
        }
        if (!s.complete) return out
        i = s.end
        continue
      }
      default:
        i++ // whitespace, numbers, true/false/null
    }
  }
  return out
}
