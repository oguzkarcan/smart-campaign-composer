// Client-side mirror of backend/internal/content validation, for inline
// feedback while the marketer edits. The server re-validates on save and on
// approval, so this only needs to agree with it, not replace it.

export const VARIABLES = ['first_name', 'city'] as const

// Fallback when the API sent no measurement (content.UnknownLength).
const UNKNOWN_LENGTH = 40

const varToken = /\{\{([^{}]*)\}\}/g
const singleBrace = new RegExp(`\\{\\s*(${VARIABLES.join('|')})\\s*\\}`)

export function token(name: string): string {
  return `{{${name}}}`
}

// Characters, counted as Go counts runes (code points, not UTF-16 units).
export function charCount(s: string): number {
  let n = 0
  for (const _ of s) n++
  return n
}

function allowed(name: string): boolean {
  return (VARIABLES as readonly string[]).includes(name)
}

// canonicalize rewrites "{{ first_name }}" to "{{first_name}}" and reports
// unknown or malformed variables, like the server.
export function canonicalize(s: string): { text: string; problems: string[] } {
  const problems: string[] = []
  const text = s.replace(varToken, (m: string, inner: string) => {
    const name = inner.trim()
    if (!allowed(name)) {
      problems.push(`unknown variable "${m}", allowed: ${VARIABLES.map(token).join(', ')}`)
      return m
    }
    return token(name)
  })
  if (problems.length > 0) return { text: s, problems }
  const rest = text.replace(varToken, '')
  const single = rest.match(singleBrace)
  if (single) {
    problems.push(`"${single[0]}" uses single braces; write variables like ${token('first_name')}`)
  } else if (/[{}]/.test(rest)) {
    problems.push(`has stray "{" or "}"; braces are only allowed in variables like ${token('first_name')}`)
  }
  return { text, problems }
}

// countedLength is how many characters a variable counts as in the worst
// case: its measured length, never shorter than its placeholder.
function countedLength(name: string, lengths: Record<string, number>): number {
  const n = lengths[name] ?? 0
  return Math.max(n > 0 ? n : UNKNOWN_LENGTH, charCount(token(name)))
}

// worstCase returns the length of a template with every variable replaced by
// its longest possible value. Spacing inside braces is ignored; unknown
// variables count as written, so a broken draft still gets a useful count.
export function worstCase(tmpl: string, lengths: Record<string, number>): number {
  let n = charCount(tmpl)
  for (const m of tmpl.matchAll(varToken)) {
    const name = m[1]!.trim()
    if (allowed(name)) n += countedLength(name, lengths) - charCount(m[0])
  }
  return n
}

export interface FieldCheck {
  worstCase: number // of the trimmed text; 0 when empty
  limit: number
  problems: string[]
}

// checkField validates one template the way the server does.
export function checkField(
  s: string,
  limit: number,
  lengths: Record<string, number>,
  opts: { singleLine?: boolean } = {},
): FieldCheck {
  const trimmed = s.trim()
  if (trimmed === '') return { worstCase: 0, limit, problems: ['must not be empty'] }
  // Unlike the server, keep checking after a variable problem, so the
  // marketer sees every problem and a meaningful count at once.
  const { problems } = canonicalize(trimmed)
  const n = worstCase(trimmed, lengths)
  if (n > limit) {
    problems.push(`is ${n} characters in the worst case, max ${limit}; shorten it by at least ${n - limit}`)
  }
  if (opts.singleLine && /[\r\n]/.test(trimmed)) problems.push('must be a single line')
  return { worstCase: n, limit, problems }
}

// render fills a template with sample values for a preview.
export function render(tmpl: string, values: Record<string, string>): string {
  return tmpl.replace(varToken, (m: string, inner: string) => values[inner.trim()] ?? m)
}
