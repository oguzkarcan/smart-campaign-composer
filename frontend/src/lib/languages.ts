import type { Campaign, Language, Person } from '@/api/types'
import { LANGUAGES } from '@/api/types'

// A typical recipient of each language for the previews, unless the marketer
// picks a real one from the audience.
export const SAMPLE_RECIPIENTS: Record<Language, Pick<Person, 'first_name' | 'city' | 'phone' | 'email'>> = {
  tr: { first_name: 'Ayşe', city: 'İstanbul', phone: '+90 555 000 00 00', email: 'ayse@example.com' },
  en: { first_name: 'Emily', city: 'İzmir', phone: '+90 555 000 00 01', email: 'emily@example.com' },
}

export interface LanguageTab {
  language: Language
  // Messages this language's readers get: per campaign channel, the
  // consented readers in the audience snapshot.
  recipients: number
  // Whether messages exist yet; a language can be needed before it has any
  // (the audience gained readers after the messages were written).
  hasContent: boolean
}

// languageTabs lists the languages a campaign has or needs messages for, in
// display order. Older campaigns without a language list fall back to the
// languages their content is in.
export function languageTabs(c: Campaign): LanguageTab[] {
  const withContent = new Set((c.contents ?? []).map((m) => m.language))
  const listed = c.languages ?? LANGUAGES.filter((l) => withContent.has(l))
  return LANGUAGES.filter((l) => listed.includes(l)).map((language) => {
    const counts = c.audience?.languages?.[language]
    return {
      language,
      recipients: counts ? c.channels.reduce((n, ch) => n + counts[ch], 0) : 0,
      hasContent: withContent.has(language),
    }
  })
}
