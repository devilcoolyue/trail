import type { TrailFile, TrailKind, TrailTurn } from '../types'

// Parses a Claude Code transcript (the session's .jsonl) into turns: one per
// prompt the person typed, holding what the model answered, the tools it ran
// and the files it changed until the next prompt.

/**
 * A transcript row cut down to what the turns need: a long session's
 * transcript runs to tens of MB, mostly tool results and file contents.
 */
export type Row = {
  type?: string
  uuid?: string
  parentUuid?: string | null
  logicalParentUuid?: string | null
  isSidechain?: boolean
  isMeta?: boolean
  isCompactSummary?: boolean
  subtype?: string
  timestamp?: string
  model?: string
  content?: string | Block[]
  /** What an Edit / Write result changed. */
  change?: TrailFile
}

/** A content block: its text, a tool use's name, or how long the thinking ran. */
type Block = { type?: string; text?: string; name?: string; chars?: number }

type RawRow = Omit<Row, 'model' | 'content' | 'change'> & {
  message?: { model?: string; content?: unknown }
  toolUseResult?: unknown
}
type RawBlock = { type?: string; text?: string; thinking?: string; name?: string }

// Rows the engine writes as user rows that are not something the person typed.
const NOT_A_PROMPT = /^\s*(<local-command-|<bash-input>|<bash-stdout>|<task-notification>|\[Request interrupted)/
const COMMAND = /<command-name>\s*(\/?[^<\s]+)\s*<\/command-name>/
const COMMAND_ARGS = /<command-args>([\s\S]*?)<\/command-args>/
// Older versions write a slash command without arguments (/compact) as a plain prompt.
const BARE_COMMAND = /^\/[A-Za-z][\w:-]*$/

function slimBlock(b: RawBlock | null): Block {
  switch (b?.type) {
    case 'text':
      return { type: 'text', text: b.text ?? '' }
    case 'thinking':
      return { type: 'thinking', chars: (b.thinking ?? '').length }
    case 'tool_use':
      return { type: 'tool_use', name: b.name }
    default:
      return { type: b?.type }
  }
}

/**
 * One line of the transcript as a Row; undefined for a line still being
 * written, and for rows outside the conversation (no uuid: queue records,
 * titles, snapshots).
 */
export function slimRow(line: string): Row | undefined {
  let raw: RawRow
  try {
    raw = JSON.parse(line) as RawRow
  } catch {
    return undefined
  }
  if (!raw || typeof raw !== 'object' || !raw.uuid) return undefined
  const content = raw.message?.content
  return {
    type: raw.type,
    uuid: raw.uuid,
    parentUuid: raw.parentUuid,
    logicalParentUuid: raw.logicalParentUuid,
    isSidechain: raw.isSidechain,
    isMeta: raw.isMeta,
    isCompactSummary: raw.isCompactSummary,
    subtype: raw.subtype,
    timestamp: raw.timestamp,
    model: raw.message?.model,
    content:
      typeof content === 'string'
        ? content
        : Array.isArray(content)
          ? (content as (RawBlock | null)[]).map(slimBlock)
          : undefined,
    change: raw.type === 'user' && raw.toolUseResult ? fileChange(raw.toolUseResult) : undefined,
  }
}

/** Splits streamed text into lines: `push` each piece as it comes, then `end`. */
export function lineSplitter(onLine: (line: string) => void) {
  let parts: string[] = []
  return {
    push(text: string) {
      let start = 0
      for (let i = text.indexOf('\n'); i >= 0; i = text.indexOf('\n', start)) {
        parts.push(text.slice(start, i))
        onLine(parts.join(''))
        parts = []
        start = i + 1
      }
      if (start < text.length) parts.push(text.slice(start))
    },
    end() {
      if (parts.length > 0) onLine(parts.join(''))
      parts = []
    },
  }
}

function blocksOf(content: Row['content']): Block[] {
  return Array.isArray(content) ? content : []
}

// 粘贴图片的引用（[图片#1 /path/x.png]、[Image #1]）和拖进终端的图片路径缩成 [图片]
function cleanPrompt(text: string): string {
  return text
    .replace(/\[(?:图片|Image)\s*#\d+[^\]]*\]/g, '[图片]')
    .replace(/(?:\/[^\s/]+)+\.(?:png|jpe?g|gif|webp)\b/gi, '[图片]')
    .replace(/\s+/g, ' ')
    .trim()
}

type Prompt = { text: string; isCommand: boolean }

/**
 * The prompt the person typed, or undefined when the row is none. A slash
 * command reads "/name args" and counts as a turn only when the model answers
 * it (/goal, skills), not for /clear or /compact.
 */
function promptOf(row: Row): Prompt | undefined {
  if (row.type !== 'user' || row.isMeta || row.isSidechain || row.isCompactSummary || !row.uuid) {
    return undefined
  }
  const content = row.content
  let text: string
  if (typeof content === 'string') {
    text = content
  } else {
    const blocks = blocksOf(content)
    if (blocks.length === 0 || blocks.some(b => b.type === 'tool_result')) return undefined
    const texts = blocks.filter(b => b.type === 'text').map(b => b.text ?? '')
    const images = blocks.filter(b => b.type === 'image').length
    text = (images > 0 ? '[图片] ' : '') + texts.join(' ')
  }
  const command = COMMAND.exec(text)
  if (command) {
    const args = COMMAND_ARGS.exec(text)?.[1]?.trim()
    const name = '/' + command[1]!.replace(/^\//, '')
    return { text: cleanPrompt(args ? `${name} ${args}` : name), isCommand: true }
  }
  if (NOT_A_PROMPT.test(text)) return undefined
  const clean = cleanPrompt(text)
  return clean === '' ? undefined : { text: clean, isCommand: BARE_COMMAND.test(clean) }
}

function countLines(s: string): number {
  if (s === '') return 0
  return s.split('\n').length - (s.endsWith('\n') ? 1 : 0)
}

/** What an Edit / Write / MultiEdit result says it changed, from its structured record. */
function fileChange(result: unknown): TrailFile | undefined {
  if (!result || typeof result !== 'object') return undefined
  const r = result as {
    filePath?: string
    type?: string
    content?: string
    structuredPatch?: { lines?: string[] }[]
  }
  if (typeof r.filePath !== 'string') return undefined
  if (r.type === 'create' && typeof r.content === 'string') {
    return { path: r.filePath, op: 'A', add: countLines(r.content), del: 0 }
  }
  if (!Array.isArray(r.structuredPatch)) return undefined
  let add = 0
  let del = 0
  for (const hunk of r.structuredPatch) {
    for (const line of hunk.lines ?? []) {
      if (line.startsWith('+')) add++
      else if (line.startsWith('-')) del++
    }
  }
  return { path: r.filePath, op: 'M', add, del }
}

/** The uuids on the conversation's current branch, walked back from its last row. */
function currentBranch(rows: readonly Row[]): Set<string> {
  const byId = new Map<string, Row>()
  let leaf: Row | undefined
  for (const row of rows) {
    if (!row.uuid || row.isSidechain) continue
    byId.set(row.uuid, row)
    if (row.type === 'user' || row.type === 'assistant') leaf = row
  }
  const onBranch = new Set<string>()
  let at = leaf
  while (at?.uuid && !onBranch.has(at.uuid)) {
    onBranch.add(at.uuid)
    const parent = at.parentUuid ?? at.logicalParentUuid
    at = parent ? byId.get(parent) : undefined
  }
  return onBranch
}

const DEBUG_RE = /报错|错误|失败|异常|崩溃|不生效|不工作|为什么.{0,8}(不|没)|排查|修复|\bbug\b|\berror\b|\bfail|exception|crash|traceback|panic|\bfix\b|doesn'?t work|not working/i
const PLAN_RE = /方案|设计|架构|规划|计划|思路|选型|怎么做|如何实现|建议|对比|\bplan\b|\bdesign\b|architect|approach|propos|trade-?off/i

export function guessKind(prompt: string, files: TrailFile[]): TrailKind {
  if (DEBUG_RE.test(prompt)) return 'debug'
  if (files.length > 0) return 'change'
  if (PLAN_RE.test(prompt)) return 'plan'
  return 'ask'
}

/** A one-line summary without a model: the answer's first sentence, plain. */
export function guessSummary(answer: string, prompt: string): string {
  const plain = answer
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/[`*_#>|]/g, '')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\s+/g, ' ')
    .trim()
  const source = plain || prompt
  const sentence = source.split(/(?<=[。！？!?])\s*|(?<=\.)\s+/)[0] ?? source
  return sentence.length > 80 ? sentence.slice(0, 80) + '…' : sentence
}

export function parseTranscript(text: string): TrailTurn[] {
  const rows: Row[] = []
  for (const line of text.split('\n')) {
    const row = slimRow(line)
    if (row) rows.push(row)
  }
  return turnsFromRows(rows)
}

export function turnsFromRows(rows: readonly Row[]): TrailTurn[] {
  const onBranch = currentBranch(rows)
  const turns: TrailTurn[] = []
  let turn: TrailTurn | undefined
  let lastAnswer = ''
  let isCommand = false
  let answered = false

  const close = () => {
    if (!turn || (isCommand && !answered)) return
    turn.index = turns.length + 1
    turn.answer = lastAnswer
    turn.kind = guessKind(turn.prompt, turn.files)
    turn.summary = guessSummary(turn.answer, turn.prompt)
    turns.push(turn)
  }

  for (const row of rows) {
    if (row.isSidechain) continue
    const prompt = promptOf(row)
    if (prompt !== undefined && row.uuid) {
      close()
      lastAnswer = ''
      isCommand = prompt.isCommand
      answered = false
      const time = row.timestamp ?? ''
      turn = {
        id: row.uuid,
        index: turns.length + 1,
        time,
        endTime: time,
        prompt: prompt.text,
        answer: '',
        thinkingChars: 0,
        tools: {},
        files: [],
        isRewound: !onBranch.has(row.uuid),
        isCompacted: false,
        kind: 'ask',
        summary: '',
        isSummarized: false,
      }
      continue
    }
    if (row.subtype === 'compact_boundary') {
      for (const t of turns) t.isCompacted = true
      if (turn) turn.isCompacted = true
    }
    if (!turn) continue
    // <synthetic> is a placeholder the engine adds itself ("No response requested."), sometimes hours later
    if (row.type === 'assistant' && row.model?.startsWith('<')) continue
    // Only answers and tool results time a turn: queue records, the caveat a
    // later local command writes and the like can come days later
    const isWork = row.type === 'assistant' || blocksOf(row.content).some(b => b.type === 'tool_result')
    if (row.timestamp && isWork) turn.endTime = row.timestamp

    if (row.type === 'assistant') {
      answered = true
      const texts: string[] = []
      for (const block of blocksOf(row.content)) {
        if (block.type === 'text' && block.text?.trim()) texts.push(block.text.trim())
        else if (block.type === 'thinking') turn.thinkingChars += block.chars ?? 0
        else if (block.type === 'tool_use' && block.name) {
          turn.tools[block.name] = (turn.tools[block.name] ?? 0) + 1
        }
      }
      if (texts.length > 0) lastAnswer = texts.join('\n\n')
    } else if (row.type === 'user' && row.change) {
      const change = row.change
      const same = turn.files.find(f => f.path === change.path)
      if (same) {
        same.add += change.add
        same.del += change.del
      } else {
        turn.files.push({ ...change })
      }
    }
  }
  close()
  return turns
}

/**
 * Where the session's transcript is kept: `<config>/projects/<cwd with every
 * character but letters and digits made '-'>/<session id>.jsonl`.
 */
export function transcriptCandidates(configDir: string, cwds: string[], sessionId: string): string[] {
  const dirs = cwds.map(cwd => cwd.replace(/[^a-zA-Z0-9]/g, '-'))
  return [...new Set(dirs)].map(dir => `${configDir}/projects/${dir}/${sessionId}.jsonl`)
}
