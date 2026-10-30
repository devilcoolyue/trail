import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { TrailKind, TrailTurn } from '../types'
import { jumpFailure } from './jump'
import { SYSTEM, buildPrompt, parseAnswer } from './summarize'
import type { Summary } from './summarize'
import { lineSplitter, parseTranscript, slimRow, transcriptCandidates, turnsFromRows } from './transcript'
import type { Row } from './transcript'

const PANE = 'trail'
const BATCH = 12
const KEPT_SESSIONS = 60
// $.fs.read 一次最多读 4 MiB
const MAX_READ = 4 * 1024 * 1024

const turnsAtom = atom({ plugin: 'trail', key: 'turns' } as const, [], { shape: 'turns-2' })
const filterAtom = atom({ plugin: 'trail', key: 'filter' } as const, '')
const queryAtom = atom({ plugin: 'trail', key: 'query' } as const, '')
const selectedAtom = atom({ plugin: 'trail', key: 'selected' } as const, '')
const busyAtom = atom({ plugin: 'trail', key: 'busy' } as const, '')

type $ = EngineInterface

const KINDS: { kind: TrailKind | ''; label: string; color: string }[] = [
  { kind: '', label: '全部', color: 'claude' },
  { kind: 'plan', label: '方案', color: 'permission' },
  { kind: 'change', label: '改动', color: 'success' },
  { kind: 'ask', label: '提问', color: 'suggestion' },
  { kind: 'debug', label: '排错', color: 'warning' },
]
const kindInfo = (k: TrailKind) => KINDS.find(x => x.kind === k) ?? KINDS[0]!

// ───────────────────────── 会话记录 ─────────────────────────

let transcriptPath: string | undefined

async function findTranscript($: $): Promise<string | undefined> {
  if (transcriptPath && (await $.fs.exists(transcriptPath))) return transcriptPath
  const id = await $.session.id()
  const home = (await $.env.get('HOME')) ?? ''
  const config = (await $.env.get('CLAUDE_CONFIG_DIR')) ?? `${home}/.claude`
  const cwds = [await $.session.root(), await $.session.cwd()]
  for (const path of transcriptCandidates(config, cwds, id)) {
    if (await $.fs.exists(path)) return (transcriptPath = path)
  }
  // 路径过长时目录名会被截断加哈希，退回到逐个目录查找
  const projects = `${config}/projects`
  const dirs = await $.fs.list(projects).catch(() => [])
  for (const dir of dirs) {
    if (dir.kind !== 'dir') continue
    const path = `${projects}/${dir.name}/${id}.jsonl`
    if (await $.fs.exists(path)) return (transcriptPath = path)
  }
  return undefined
}

const cacheKey = (sessionId: string) => `summaries:${sessionId}`

async function loadCache($: $, sessionId: string): Promise<Record<string, Summary>> {
  const v = await $.store.get(cacheKey(sessionId))
  return v && typeof v === 'object' ? (v as Record<string, Summary>) : {}
}

async function saveCache($: $, sessionId: string, cache: Record<string, Summary>) {
  await $.store.set(cacheKey(sessionId), cache)
  // 只保留最近的若干个会话，避免超出存储上限
  const recent = ((await $.store.get('recent')) as string[] | undefined) ?? []
  const next = [sessionId, ...recent.filter(id => id !== sessionId)]
  for (const old of next.slice(KEPT_SESSIONS)) await $.store.delete(cacheKey(old))
  await $.store.set('recent', next.slice(0, KEPT_SESSIONS))
}

async function readTurns($: $, path: string): Promise<TrailTurn[]> {
  const { size } = await $.fs.stat(path)
  if (size <= MAX_READ) return parseTranscript(await $.fs.read(path))
  // 长会话的记录有几十 MB，超出 $.fs.read 的上限：用 cat 流式读，每行只留下需要的字段
  const rows: Row[] = []
  const lines = lineSplitter(line => {
    const row = slimRow(line)
    if (row) rows.push(row)
  })
  for await (const { stream, text } of $.process.spawn({ argv: ['cat', path] })) {
    if (stream === 'stdout') lines.push(text)
  }
  lines.end()
  return turnsFromRows(rows)
}

async function refresh($: $): Promise<string | undefined> {
  const path = await findTranscript($)
  if (!path) return '找不到当前会话的记录文件'
  const turns = await readTurns($, path)
  const cache = await loadCache($, await $.session.id())
  for (const t of turns) {
    const c = cache[t.id]
    if (c) Object.assign(t, c, { isSummarized: true })
  }
  await update($, turnsAtom, () => turns)
  return undefined
}

let isSummarizing = false

// 后台的刷新和总结出错时不打扰对话，只记到调试日志
function logError($: $, err: unknown) {
  $.ui.log(`trail: ${err instanceof Error ? err.message : String(err)}`, { to: 'debug' })
}

async function summarizeMissing($: $, mode: string) {
  if (mode === 'off' || isSummarizing) return
  isSummarizing = true
  try {
    const sessionId = await $.session.id()
    const todo = (await read($, turnsAtom)).filter(t => !t.isSummarized && !t.isRewound)
    for (let i = 0; i < todo.length; i += BATCH) {
      const batch = todo.slice(i, i + BATCH)
      await update($, busyAtom, () => `正在用 haiku 总结 ${i + 1}–${i + batch.length} / ${todo.length} 轮…`)
      const r = await $.model.complete({
        model: 'haiku',
        system: SYSTEM,
        prompt: buildPrompt(batch),
        maxTokens: 2000,
        timeoutMs: 90_000,
      })
      if (!r.isAnswered) {
        $.ui.log(`trail: 总结失败（${r.reason}），先显示规则提取的结果`, { to: 'debug' })
        break
      }
      const found = parseAnswer(r.text, batch)
      if (found.size === 0) continue
      const cache = await loadCache($, sessionId)
      for (const [id, s] of found) cache[id] = s
      await saveCache($, sessionId, cache)
      await update($, turnsAtom, turns =>
        turns.map(t => {
          const s = found.get(t.id)
          return s ? { ...t, ...s, isSummarized: true } : t
        }),
      )
    }
  } finally {
    isSummarizing = false
    await update($, busyAtom, () => '')
  }
}

async function openPane($: $, query: string, mode: string): Promise<string> {
  await update($, queryAtom, () => query)
  const problem = await refresh($)
  const opened = await $.ui.open({ id: PANE, title: '对话索引', focus: true })
  $.clock.after(1, () => summarizeMissing($, mode).catch(err => logError($, err)))
  if (problem) return problem
  const n = (await read($, turnsAtom)).length
  return opened.isPlaced ? `已打开对话索引（${n} 轮）` : `对话索引暂未显示：${opened.reason}`
}

// ───────────────────────── 文字排版 ─────────────────────────

/** 终端里一个字符占几格：中日韩、全角符号占两格。 */
function cellWidth(cp: number): number {
  if (
    (cp >= 0x1100 && cp <= 0x115f) ||
    (cp >= 0x2e80 && cp <= 0xa4cf && cp !== 0x303f) ||
    (cp >= 0xac00 && cp <= 0xd7a3) ||
    (cp >= 0xf900 && cp <= 0xfaff) ||
    (cp >= 0xfe30 && cp <= 0xfe4f) ||
    (cp >= 0xff00 && cp <= 0xff60) ||
    (cp >= 0xffe0 && cp <= 0xffe6) ||
    (cp >= 0x1f300 && cp <= 0x1faff) ||
    (cp >= 0x20000 && cp <= 0x3fffd)
  )
    return 2
  return 1
}

function width(s: string): number {
  let w = 0
  for (const ch of s) w += cellWidth(ch.codePointAt(0) ?? 0)
  return w
}

function truncate(s: string, max: number): string {
  if (max <= 0) return ''
  if (width(s) <= max) return s
  let out = ''
  let w = 0
  for (const ch of s) {
    const cw = cellWidth(ch.codePointAt(0) ?? 0)
    if (w + cw > max - 1) break
    out += ch
    w += cw
  }
  return out + '…'
}

const pad = (n: number) => String(n).padStart(2, '0')

function clock(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function span(fromIso: string, toIso: string): string {
  const ms = new Date(toIso).getTime() - new Date(fromIso).getTime()
  if (!(ms > 0)) return ''
  const min = Math.round(ms / 60_000)
  if (min < 1) return ''
  return min >= 60 ? `${Math.floor(min / 60)}h${pad(min % 60)}m` : `${min}m`
}

const sumFiles = (t: TrailTurn) =>
  t.files.reduce((acc, f) => ({ add: acc.add + f.add, del: acc.del + f.del }), { add: 0, del: 0 })

function matches(t: TrailTurn, q: string): boolean {
  const needle = q.toLowerCase()
  return [t.prompt, t.summary, t.answer, ...t.files.map(f => f.path)].some(s =>
    s.toLowerCase().includes(needle),
  )
}

/** 对话树的左侧连线：主线 ●，回退掉的分支 ○ 挂在主线右边。 */
function gutter(all: readonly TrailTurn[], t: TrailTurn, isTree: boolean): [string, string] {
  if (!isTree) return [t.isRewound ? '○ ' : '● ', '  ']
  const next = all[all.indexOf(t) + 1]
  if (t.isRewound) return ['│○', next?.isRewound ? '││' : '│ ']
  if (!next) return ['● ', '  ']
  return ['● ', next.isRewound ? '├╮' : '│ ']
}


// ───────────────────────── 钩子 ─────────────────────────

export const register: Register = (on, options) => {
  const mode = String(options.summaries ?? 'haiku')

  on('session.start', async ($, e, next) => {
    await $.command.register({
      name: 'trail',
      description: '对话索引：按轮次浏览、筛选、搜索本会话，点击跳回原位置',
      argumentHint: '[搜索词]',
    })
    return next(e)
  })

  on('command.run', { command: 'trail' }, async ($, e) => ({ text: await openPane($, e.args.trim(), mode) }))

  // 面板开着时，每轮结束后刷新索引并总结新的一轮
  on('turn.complete', async ($, e, next) => {
    const result = await next(e)
    const panes = await $.ui.panes()
    if (panes.some(p => p.id === PANE)) {
      $.clock.after(1, () =>
        refresh($)
          .then(() => summarizeMissing($, mode))
          .catch(err => logError($, err)),
      )
    }
    return result
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const { Box, Text, Button } = $.ui.resolve(e)
    const cols = Math.max(30, e.props.bodyColumns)
    const all = await read($, turnsAtom)
    const filter = await read($, filterAtom)
    const query = await read($, queryAtom)
    const selected = await read($, selectedAtom)
    const busy = await read($, busyAtom)

    const shown = all.filter(t => (!filter || t.kind === filter) && (!query || matches(t, query)))
    const isTree = !filter && !query

    // 标题行
    const live = all.filter(t => !t.isRewound)
    const files = new Set(live.flatMap(t => t.files.map(f => f.path)))
    const totals = live.reduce(
      (acc, t) => {
        const s = sumFiles(t)
        return { add: acc.add + s.add, del: acc.del + s.del }
      },
      { add: 0, del: 0 },
    )
    const first = all[0]
    const last = all[all.length - 1]
    const elapsed = first && last ? span(first.time, last.endTime) : ''

    const header = (
      <Box flexDirection="column">
        <Text>
          <Text color="claude" bold>✻ 对话索引</Text>
          <Text dimColor>
            {`  ${live.length} 轮${elapsed ? ' · ' + elapsed : ''}`}
            {files.size > 0 ? ` · ${files.size} 个文件 ` : ''}
          </Text>
          {files.size > 0 && <Text color="diffAdded">+{totals.add}</Text>}
          {files.size > 0 && <Text color="diffRemoved"> −{totals.del}</Text>}
        </Text>
        {busy !== '' && <Text dimColor italic>◌ {busy}</Text>}
      </Box>
    )

    // 筛选
    const counts = (k: TrailKind | '') => (k ? all.filter(t => t.kind === k).length : all.length)
    const tabs = (
      <Box flexDirection="row" flexWrap="wrap" columnGap={1}>
        {KINDS.map((k, i) => (
          <Button
            key={`f:${k.kind || 'all'}`}
            plain
            hotkey={String(i + 1)}
            onPress={() => update($, filterAtom, () => k.kind)}
          >
            <Text color={k.color} inverse={filter === k.kind} bold={filter === k.kind}>
              {filter === k.kind ? ` ${k.label} ${counts(k.kind)} ` : `${k.label} ${counts(k.kind)}`}
            </Text>
          </Button>
        ))}
        {query !== '' && (
          <Button key="clear-query" plain onPress={() => update($, queryAtom, () => '')}>
            <Text color="warning">{`“${truncate(query, 16)}” ${shown.length} 条 ✕`}</Text>
          </Button>
        )}
      </Box>
    )

    const jump = async (t: TrailTurn) => {
      await update($, selectedAtom, () => t.id)
      if (t.isRewound) {
        $.ui.toast('这一轮后来被回退了，不在当前对话里')
        return
      }
      let deny: string | undefined
      try {
        deny = (await $.ui.scroll({ to: { requestId: t.id }, block: 'start' })).deny
      } catch (err) {
        deny = String(err)
      }
      if (deny) $.ui.toast(jumpFailure(t, deny))
    }

    // 每一轮：两行，第一行提问，第二行一句话总结
    const rows = shown.map(t => {
      const info = kindInfo(t.kind)
      const isSel = t.id === selected
      const [g1, g2] = gutter(all, t, isTree)
      const time = clock(t.time)
      const head = `${g1} ${pad(t.index)} `
      const promptRoom = cols - width(head) - width(info.label) - 1 - time.length - 2
      const prompt = truncate(t.prompt, promptRoom)
      const fill = Math.max(1, cols - width(head) - width(info.label) - 1 - width(prompt) - time.length - 1)

      const s = sumFiles(t)
      const meta = t.isRewound ? '↺ 已回退' : t.files.length > 0 ? `${t.files.length} 文件 +${s.add} −${s.del}` : ''
      const lead = `${g2}    ↳ `
      const summary = truncate(t.summary, cols - width(lead) - width(meta) - 2)
      const gap = Math.max(1, cols - width(lead) - width(summary) - width(meta) - 1)

      const dim = t.isRewound
      return (
        <Box key={`row:${t.id}`} flexDirection="column">
          <Button key={`t:${t.id}`} plain onPress={() => jump(t)} autoFocus={isSel || undefined}>
            <Text color={dim ? 'inactive' : info.color}>{head}</Text>
            <Text color={dim ? 'inactive' : info.color} bold={!dim}>{info.label}</Text>
            <Text> </Text>
            <Text color={dim ? 'inactive' : 'text'} bold={isSel} strikethrough={dim}>{prompt}</Text>
            <Text>{' '.repeat(fill)}</Text>
            <Text dimColor>{time}</Text>
            <Text>{'\n'}</Text>
            <Text dimColor>{lead}</Text>
            <Text color={dim ? 'inactive' : 'subtle'} italic={!t.isSummarized}>{summary}</Text>
            <Text>{' '.repeat(gap)}</Text>
            <Text color={t.isRewound ? 'inactive' : 'diffAdded'} dimColor>{meta}</Text>
          </Button>
          {isSel && detail(t, cols, Box, Text)}
        </Box>
      )
    })

    return (
      <Box flexDirection="column" rowGap={0}>
        {header}
        {tabs}
        <Text dimColor>{'─'.repeat(cols)}</Text>
        {all.length === 0 && <Text dimColor>这个会话还没有对话。</Text>}
        {all.length > 0 && shown.length === 0 && <Text dimColor>没有匹配的对话。</Text>}
        {rows}
        <Text dimColor>{'─'.repeat(cols)}</Text>
        <Text dimColor>
          {e.props.isFocused
            ? 'Tab 选择 · Enter 跳到原位置 · 1-5 筛选 · Esc 回到输入框'
            : '点击某一轮跳到原位置 · /trail 关键词 可搜索'}
        </Text>
      </Box>
    )
  })
}

// 选中的那一轮展开：工具、改动文件、思考量
function detail(t: TrailTurn, cols: number, Box: any, Text: any) {
  const tools = Object.entries(t.tools)
    .sort((a, b) => b[1] - a[1])
    .map(([name, n]) => `${name}×${n}`)
    .join(' · ')
  return (
    <Box flexDirection="column" paddingLeft={7}>
      {tools !== '' && (
        <Text>
          <Text dimColor>工具  </Text>
          <Text color="subtle">{truncate(tools, cols - 13)}</Text>
        </Text>
      )}
      {t.thinkingChars > 0 && (
        <Text dimColor>{`思考  ${t.thinkingChars >= 1000 ? (t.thinkingChars / 1000).toFixed(1) + 'k' : t.thinkingChars} 字`}</Text>
      )}
      {t.files.slice(0, 8).map(f => {
        const stat = f.del > 0 ? `+${f.add} −${f.del}` : `+${f.add}`
        const path = truncate(f.path.replace(/^.*?([^/]+\/[^/]+)$/, '$1'), cols - 13 - stat.length)
        return (
          <Text key={`file:${f.path}`}>
            <Text color={f.op === 'A' ? 'success' : 'warning'}>{f.op} </Text>
            <Text>{path}</Text>
            <Text>{' '.repeat(Math.max(1, cols - 9 - width(path) - stat.length))}</Text>
            <Text color="diffAdded">+{f.add}</Text>
            {f.del > 0 && <Text color="diffRemoved"> −{f.del}</Text>}
          </Text>
        )
      })}
      {t.files.length > 8 && <Text dimColor>{`… 还有 ${t.files.length - 8} 个文件`}</Text>}
    </Box>
  )
}
