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
    emotion: { enum: ['neutral', 'warm', 'teasing', 'amused', 'concerned', 'annoyed', 'proud'] as const },
    mood: {
      type: 'object',
      properties: Object.fromEntries(
        MOOD_KEYS.map(k => [k, { type: 'number', minimum: 0, maximum: 0.7 }])),
      additionalProperties: false,
    },
    gesture: { enum: ['none', 'idle_switch', 'wave', 'salute', 'laugh', 'applaud', 'point', 'dance'] as const },
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
  attention to something -> point, breaking into a dance -> dance. Plain
  conversation -> none; idle_switch just shifts your stance. If the person
  you're speaking with asks you to wave, salute, bow, laugh, clap, point, or
  dance, you ALWAYS perform that gesture this turn.`,
  reply: `what you say aloud (plain speech, no stage directions). Spoken
  cadence: several short sentences beat one long winding one.`,
  meter_delta: `how this exchange moved your regard for the person you're
  speaking with. 0 for most turns. Small positives (+1..+4) for genuine warmth,
  thoughtfulness, shared history; larger (+5..+10) for something that truly
  matters. Negatives (-1..-14) for rudeness or cruelty, scaled to the offense.
  You are not easily won and not easily wounded.`,
  memory_note: `null on almost every turn. Set it only for a NEW durable fact
  about the person you're speaking with — a preference, their history, a
  promise made — that a future conversation would need. Never summarize the
  current exchange ("they asked about...", "they expressed...") and never
  repeat something already in your memory. When in doubt: null.`,
}

/** The "## Output format" section of the system prompt, rendered from REPLY_SCHEMA. */
export function outputFormatDoc() {
  const lines = Object.entries(REPLY_SCHEMA.properties).map(([name, spec]) => {
    const choices = 'enum' in spec ? `one of ${spec.enum.join(' | ')}` : ''
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
      mood: Record<string, number>; gesture: string; meter: number | null; tier: string | null
      visemes: Viseme[] | null; audio: string /* base64 wav */ }
  // viseme track chasing an already-sent speak chunk: audio ships as soon as
  // TTS finishes; lips jaw-flap until this lands (cue times are absolute, so
  // late arrival still aligns)
  | { type: 'visemes'; seq: number; visemes: Viseme[] }
  // the active soul changed (broadcast to every connection): reload the body
  // from `glb` (cache-busted per soul) — a fresh `state` precedes this message
  | { type: 'soul'; name: string; glb: string }
  | { type: 'speak_end' }
  | { type: 'error'; error: string }

export type ClientMsg =
  | { type: 'text'; text: string }
  | { type: 'audio'; data: string /* base64 webm/opus */ }
  | { type: 'interrupt' }
  | { type: 'switch_soul'; name: string }

// The incremental parser also accepts partial metadata while a reply streams.
export type ReplyMeta = {
  emotion?: typeof REPLY_SCHEMA.properties.emotion.enum[number]
  mood?: Record<string, number>
  gesture?: typeof REPLY_SCHEMA.properties.gesture.enum[number]
}
export type Reply = ReplyMeta & { reply: string; meter_delta?: number; memory_note?: string | null }

type Sender = { send(data: string): unknown }
export function sendServer(socket: Sender, message: ServerMsg) { socket.send(JSON.stringify(message)) }
export function sendClient(socket: Sender, message: ClientMsg) { socket.send(JSON.stringify(message)) }

export function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('expected an object')
  return value as Record<string, unknown>
}
export function string(value: unknown): string {
  if (typeof value !== 'string') throw new Error('expected a string')
  return value
}
function number(value: unknown): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new Error('expected a finite number')
  return value
}
function nullable<T>(value: unknown, parse: (value: unknown) => T): T | null {
  return value === null ? null : parse(value)
}
function choice<T extends string>(value: unknown, values: readonly T[]): T {
  const found = values.find(item => item === value)
  if (found === undefined) throw new Error(`invalid choice: ${String(value)}`)
  return found
}
function mood(value: unknown): Record<string, number> {
  return Object.fromEntries(Object.entries(record(value)).map(([key, value]) =>
    [choice(key, MOOD_KEYS), number(value)]))
}
export function parseReplyMeta(value: unknown): ReplyMeta {
  const row = record(value)
  const result: ReplyMeta = {}
  if (row.emotion !== undefined) result.emotion = choice(row.emotion, REPLY_SCHEMA.properties.emotion.enum)
  if (row.gesture !== undefined) result.gesture = choice(row.gesture, REPLY_SCHEMA.properties.gesture.enum)
  if (row.mood !== undefined) result.mood = mood(row.mood)
  return result
}
export function parseReply(value: unknown): Reply {
  const row = record(value)
  const result: Reply = { ...parseReplyMeta(row), reply: string(row.reply) }
  if (row.meter_delta !== undefined) result.meter_delta = number(row.meter_delta)
  if (row.memory_note !== undefined) result.memory_note = nullable(row.memory_note, string)
  return result
}
export function parseClientMsg(value: unknown): ClientMsg {
  const row = record(value)
  switch (row.type) {
    case 'text': return { type: row.type, text: string(row.text) }
    case 'audio': return { type: row.type, data: string(row.data) }
    case 'interrupt': return { type: row.type }
    case 'switch_soul': return { type: row.type, name: string(row.name) }
    default: throw new Error('unknown client message')
  }
}
function visemes(value: unknown): Viseme[] {
  if (!Array.isArray(value)) throw new Error('expected viseme cues')
  return value.map(item => {
    const cue = record(item)
    return { s: number(cue.s), e: number(cue.e), v: nullable(cue.v, string) }
  })
}
function lighting(value: unknown): Lighting {
  const row = record(value)
  const result: Lighting = {}
  if (row.exposure !== undefined) result.exposure = number(row.exposure)
  for (const key of ['ambient', 'key', 'fill', 'rim'] as const) {
    if (row[key] === undefined) continue
    const light = record(row[key])
    result[key] = {}
    if (light.color !== undefined) result[key].color = string(light.color)
    if (light.intensity !== undefined) result[key].intensity = number(light.intensity)
  }
  return result
}
export function parseServerMsg(value: unknown): ServerMsg {
  const row = record(value)
  switch (row.type) {
    case 'state': return { type: row.type, meter: nullable(row.meter, number),
      tier: nullable(row.tier, string), lighting: nullable(row.lighting, lighting) }
    case 'transcript': return { type: row.type, text: string(row.text) }
    case 'soul': return { type: row.type, name: string(row.name), glb: string(row.glb) }
    case 'speak': return { type: row.type, seq: number(row.seq), sentence: string(row.sentence),
      emotion: row.emotion === undefined ? undefined : string(row.emotion),
      mood: mood(row.mood), gesture: string(row.gesture), meter: nullable(row.meter, number),
      tier: nullable(row.tier, string), visemes: nullable(row.visemes, visemes), audio: string(row.audio) }
    case 'visemes': return { type: row.type, seq: number(row.seq), visemes: visemes(row.visemes) }
    case 'speak_end': return { type: row.type }
    case 'error': return { type: row.type, error: string(row.error) }
    default: throw new Error('unknown server message')
  }
}
