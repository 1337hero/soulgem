// Single source of truth for the three contracts shared by server + client:
// the LLM reply schema (+ its persona-prompt docs), the morph-name vocabulary,
// and the WS message shapes.
//
// Imported by server/companion.js (Bun runs TS natively) and by main.js
// (bun build bundles it). Nothing here may import browser or Bun APIs.

export const MOOD_KEYS = ['MoodHappy', 'MoodSad', 'MoodAnger', 'MoodFear',
                          'MoodSurprise', 'MoodPuzzled', 'MoodDisgusted'] as const

// Rhubarb mouth shapes (Preston Blair + GHX) -> TRI viseme morphs on the head.
export const SHAPE_VISEME: Record<string, string | null> = {
  A: 'BMP', B: 'Eee', C: 'Eh', D: 'BigAah', E: 'Oh',
  F: 'OohQ', G: 'FV', H: 'Th', X: null,
}

export const VISEME_KEYS = Object.values(SHAPE_VISEME).filter(Boolean) as string[]

// Property order matters: llama.cpp's json_schema grammar generates fields in
// declaration order, so all metadata streams BEFORE the reply text — letting
// TTS start on the first sentence while the model is still writing.
export const REPLY_SCHEMA = {
  type: 'object',
  properties: {
    emotion: { enum: ['neutral', 'warm', 'teasing', 'amused', 'concerned', 'annoyed', 'proud'] },
    mood: {
      type: 'object',
      properties: Object.fromEntries(
        MOOD_KEYS.map(k => [k, { type: 'number', minimum: 0, maximum: 0.7 }])),
      additionalProperties: false,
    },
    gesture: { enum: ['none', 'idle_switch'] },
    reply: { type: 'string' },
    meter_delta: { type: 'integer', minimum: -14, maximum: 10 },
    memory_note: { type: ['string', 'null'] },
  },
  required: ['emotion', 'reply'],
  additionalProperties: false,
}

// Fallback face when the model leaves `mood` empty (it usually does).
export const EMOTION_MOOD: Record<string, Record<string, number>> = {
  neutral: {},
  warm: { MoodHappy: 0.35 },
  teasing: { MoodHappy: 0.5 },
  amused: { MoodHappy: 0.55, MoodSurprise: 0.15 },
  concerned: { MoodSad: 0.3, MoodPuzzled: 0.25 },
  annoyed: { MoodAnger: 0.4 },
  proud: { MoodHappy: 0.3 },
}

// Per-field guidance shown to the model. Rendered into the system prompt by
// outputFormatDoc() so persona/lydia.md never restates the schema.
const FIELD_DOCS: Record<string, string> = {
  emotion: '',
  mood: `object with optional keys ${MOOD_KEYS.join(', ')} — values 0.0-0.7\n  (subtle facial expression while speaking; usually just one key, often none)`,
  gesture: 'idle_switch shifts her stance; more gestures come later',
  reply: 'what you say aloud (plain speech, no stage directions)',
  meter_delta: `how this exchange moved your regard for the Thane. 0 for most
  turns. Small positives (+1..+4) for genuine warmth, thoughtfulness, shared
  history; larger (+5..+10) for something that truly matters. Negatives
  (-1..-14) for rudeness or cruelty, scaled to the offense. You are not easily
  won and not easily wounded.`,
  memory_note: `a short fact about the Thane worth remembering long-term,
  or null. Only durable facts (their preferences, history, promises made) —
  not small talk.`,
}

/** The "## Output format" section of the system prompt, rendered from REPLY_SCHEMA. */
export function outputFormatDoc() {
  const lines = Object.entries(REPLY_SCHEMA.properties).map(([name, spec]) => {
    const choices = (spec as any).enum ? `one of ${(spec as any).enum.join(' | ')}` : ''
    return `- ${name}: ${[choices, FIELD_DOCS[name]].filter(Boolean).join(' — ')}`
  })
  return '## Output format\nRespond with JSON only, matching this schema (fields in this order):\n'
    + lines.join('\n')
}

// ---- WS message shapes ----
export type Viseme = { s: number; e: number; v: string | null }

export type ServerMsg =
  | { type: 'transcript'; text: string }
  | { type: 'speak'; seq: number; sentence: string; emotion?: string
      mood: Record<string, number>; gesture: string; meter: number
      visemes: Viseme[] | null; audio: string /* base64 wav */ }
  | { type: 'speak_end' }
  | { type: 'error'; error: string }

export type ClientMsg =
  | { type: 'text'; text: string }
  | { type: 'audio'; data: string /* base64 webm/opus */ }
  | { type: 'interrupt' }
