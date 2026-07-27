// Incremental parser for the LLM's streaming JSON reply (REPLY_SCHEMA).
//
// Feed it raw content deltas (the accumulating JSON text, not SSE lines); it
// emits metadata as soon as the `reply` field opens, then each sentence of the
// reply as it completes, then the fully parsed object.
//
// Relies on llama.cpp's json_schema grammar emitting fields in declaration
// order, so everything before `reply` is complete metadata.

export type Event =
  | { type: 'meta'; meta: any }
  | { type: 'sentence'; text: string }
  | { type: 'done'; thought: any }

const SENTENCE_RE = /[^.!?…]+[.!?…]+["')\]]*|[^.!?…]+$/g
const MIN_LEN = 12  // fragments shorter than this aren't worth a TTS round-trip
const UNESCAPE: Record<string, string> = { n: '\n', t: ' ', r: '', '"': '"', '\\': '\\', '/': '/' }

/**
 * Split `buf` into sentences, merging fragments shorter than MIN_LEN with a
 * neighbour. Returns [sentences, rest]; while streaming the trailing piece is
 * held back in `rest` because more text may still extend it.
 */
function drain(buf: string, final: boolean): [string[], string] {
  const parts = buf.match(SENTENCE_RE) ?? []
  const rest = final ? [] : parts.splice(-1)  // kept verbatim: it may still grow
  const out: string[] = []
  for (const raw of parts) {
    const p = raw.trim()
    if (!p) continue
    if (out.length && (p.length < MIN_LEN || out[out.length - 1].length < MIN_LEN)) out[out.length - 1] += ' ' + p
    else out.push(p)
  }
  // a still-short last sentence waits for more text rather than going out alone
  if (!final && out.length && out[out.length - 1].length < MIN_LEN) rest.unshift(out.pop()!)
  return [out, rest.join(' ')]
}

export function replyParser() {
  let content = ''      // raw JSON text so far
  let metaSent = false
  let scanned = 0       // how far into content the unescaper has read
  let esc = false
  let replyDone = false
  let buf = ''          // unescaped reply text not yet emitted
  const spoken: string[] = []  // every sentence emitted, for truncation salvage
  let meta: any = null

  return {
    push(delta: string): Event[] {
      content += delta
      const events: Event[] = []

      if (!metaSent) {
        const m = content.match(/"reply"\s*:\s*"/)
        if (!m) return events
        metaSent = true
        scanned = m.index! + m[0].length
        meta = JSON.parse(content.slice(0, m.index).replace(/,\s*$/, '') + '}')
        events.push({ type: 'meta', meta })
      }

      if (!replyDone) {
        // ponytail: \uXXXX escapes pass through as literal "uXXXX" — the model
        // emits raw UTF-8; add a hex decoder here if that ever changes.
        for (; scanned < content.length; scanned++) {
          const c = content[scanned]
          if (esc) { buf += UNESCAPE[c] ?? c; esc = false }
          else if (c === '\\') esc = true
          else if (c === '"') { replyDone = true; break }
          else buf += c
        }
        const [sentences, rest] = drain(buf, false)
        buf = rest
        spoken.push(...sentences)
        for (const text of sentences) events.push({ type: 'sentence', text })
      }
      return events
    },

    finish(): Event[] {
      const [sentences] = drain(buf, true)
      buf = ''
      spoken.push(...sentences)
      const events: Event[] = sentences.map(text => ({ type: 'sentence', text }))
      let thought: any
      try {
        thought = JSON.parse(content)
      } catch {
        // max_tokens truncation leaves unterminated JSON; salvage what streamed
        // rather than erroring a turn the user already partly heard.
        thought = { emotion: 'neutral', ...meta, reply: spoken.join(' ') }
      }
      events.push({ type: 'done', thought })
      return events
    },
  }
}
