import type { TrailKind, TrailTurn } from '../types'

// One model call summarizes and classifies a batch of turns. The answer is a
// JSON array; anything that does not parse leaves those turns on the rules.

export type Summary = { kind: TrailKind; summary: string }

const KINDS: readonly TrailKind[] = ['plan', 'change', 'ask', 'debug']

const clip = (s: string, n: number) => (s.length > n ? s.slice(0, n) + '…' : s)

export const SYSTEM = [
  'You index a coding conversation between a developer and an AI assistant.',
  'For each turn you get the developer\'s prompt, the assistant\'s final reply, the tools it ran and the files it changed.',
  'Answer with a JSON array only, one object per turn, in the order given: {"i": <turn number>, "kind": "plan"|"change"|"ask"|"debug", "summary": "..."}.',
  'kind: plan = a design, plan or decision was worked out; change = code or files were changed; debug = an error or failure was diagnosed or fixed; ask = a question was answered or something was explained.',
  "summary: what was decided, found or done, in at most 30 Chinese characters or 18 English words. Write it in the language of that turn's Prompt: a Chinese prompt gets a Chinese summary. State the outcome, not the request. No trailing period.",
].join('\n')

export function buildPrompt(turns: readonly TrailTurn[]): string {
  return turns
    .map(t => {
      const tools = Object.entries(t.tools).map(([name, n]) => `${name}×${n}`).join(', ')
      const files = t.files.map(f => `${f.op} ${f.path} +${f.add} -${f.del}`).join('; ')
      return [
        `### Turn ${t.index}`,
        `Prompt: ${clip(t.prompt, 600)}`,
        `Reply: ${clip(t.answer || '(no text reply)', 1200)}`,
        tools && `Tools: ${tools}`,
        files && `Files: ${files}`,
      ]
        .filter(Boolean)
        .join('\n')
    })
    .join('\n\n')
}

export function parseAnswer(text: string, turns: readonly TrailTurn[]): Map<string, Summary> {
  const out = new Map<string, Summary>()
  const start = text.indexOf('[')
  const end = text.lastIndexOf(']')
  if (start < 0 || end <= start) return out
  let items: unknown
  try {
    items = JSON.parse(text.slice(start, end + 1))
  } catch {
    return out
  }
  if (!Array.isArray(items)) return out
  const byIndex = new Map(turns.map(t => [t.index, t]))
  for (const item of items) {
    const { i, kind, summary } = (item ?? {}) as { i?: unknown; kind?: unknown; summary?: unknown }
    const turn = typeof i === 'number' ? byIndex.get(i) : undefined
    if (!turn || typeof summary !== 'string' || !summary.trim()) continue
    const k = KINDS.includes(kind as TrailKind) ? (kind as TrailKind) : turn.kind
    out.set(turn.id, { kind: k, summary: clip(summary.trim(), 80) })
  }
  return out
}
