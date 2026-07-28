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
    gesture: { enum: ['none', 'idle_switch', 'wave', 'salute', 'laugh', 'applaud', 'point'] },
    reply: { type: 'string' },
    meter_delta: { type: 'integer', minimum: -14, maximum: 10 },
    memory_note: { type: ['string', 'null'] },
  },
  // ALL fields required: llama.cpp's grammar only pins the order of required
  // properties — optional ones float, and a gesture emitted after `reply`
  // never reaches the streamed metadata. Required = strict declaration order.
  required: ['emotion', 'mood', 'gesture', 'reply', 'meter_delta', 'memory_note'],
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
  gesture: `a one-shot emote played while you speak. Pick one whenever your
  words act it out: greeting or farewell -> wave, accepting an order or duty ->
  salute, genuine laughter -> laugh, impressed by a feat -> applaud, drawing
  attention to something -> point. Plain conversation -> none; idle_switch
  just shifts your stance. If the user asks you to wave, salute, bow, laugh,
  clap, or point, you ALWAYS perform that gesture this turn.`,
  reply: `what you say aloud (plain speech, no stage directions). Spoken
  cadence: several short sentences beat one long winding one.`,
  meter_delta: `how this exchange moved your regard for the user. 0 for most
  turns. Small positives (+1..+4) for genuine warmth, thoughtfulness, shared
  history; larger (+5..+10) for something that truly matters. Negatives
  (-1..-14) for rudeness or cruelty, scaled to the offense. You are not easily
  won and not easily wounded.`,
  memory_note: `null on almost every turn. Set it only for a NEW durable fact
  about the user themself — a preference, their history, a promise made —
  that a future conversation would need. Never summarize the current exchange
  ("they asked about...", "they expressed...") and never repeat
  something already in your memory. When in doubt: null.`,
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

// per-soul override of the viewer's stock light rig; omitted fields keep stock
export type Lighting = {
  exposure?: number
  ambient?: { color?: string; intensity?: number }
  key?: { color?: string; intensity?: number }
  fill?: { color?: string; intensity?: number }
  rim?: { color?: string; intensity?: number }
}

export type ServerMsg =
  // sent once on connect so a fresh page shows current state before she speaks
  | { type: 'state'; meter: number | null; tier: string | null
      lighting: Lighting | null }
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
