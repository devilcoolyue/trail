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
const focusAtom = atom({ plugin: 'trail', key: 'focus' } as const, '')
const topAtom = atom({ plugin: 'trail', key: 'top' } as const, -1)
const revealAtom = atom({ plugin: 'trail', key: 'reveal' } as const, '')

// 全屏时列表由插件自己滚动，标题、筛选和按键说明固定不动。记下最近一次画出来的位置，滚轮从这里接着算
const listWindow = { isOwn: false, top: 0, maxTop: 0 }

type $ = EngineInterface

const KINDS: { kind: TrailKind | ''; label: string; color: string }[] = [
  { kind: '', label: '全部', color: 'claude' },
  { kind: 'plan', label: '方案', color: 'permission' },
  { kind: 'change', label: '改动', color: 'success' },
  { kind: 'ask', label: '提问', color: 'suggestion' },
  { kind: 'debug', label: '排错', color: 'warning' },
]
const kindInfo = (k: TrailKind) => KINDS.find(x => x.kind === k) ?? KINDS[0]!

// 当前这一轮的底色。主题里没有背景色可用，按亮暗主题各取一个，主题从 ~/.claude.json 读一次。
let selBackground: string | undefined

async function loadSelBackground($: $): Promise<string> {
  if (selBackground) return selBackground
  let theme = ''
  try {
    const home = (await $.env.get('HOME')) ?? ''
    const dir = (await $.env.get('CLAUDE_CONFIG_DIR')) ?? home
    const path = `${dir}/.claude.json`
    if ((await $.fs.stat(path)).size <= MAX_READ) theme = String(JSON.parse(await $.fs.read(path)).theme ?? '')
  } catch {
    // 读不到就按暗色主题
  }
  return (selBackground = theme.startsWith('light') ? '#dadada' : '#444444')
}

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

async function openPane($: $, query: string, mode: string, isFullscreen: boolean): Promise<string> {
  await update($, queryAtom, () => query)
  // 打开时显示最近的几轮
  await update($, topAtom, () => -1)
  await update($, revealAtom, () => '')
  const problem = await refresh($)
  const opened = await $.ui.open({ id: PANE, title: '对话索引', focus: true })
  $.clock.after(1, () => summarizeMissing($, mode).catch(err => logError($, err)))
  if (problem) return problem
  const n = (await read($, turnsAtom)).length
  if (!opened.isPlaced) return `对话索引暂未显示：${opened.reason}`
  return isFullscreen
    ? `已打开对话索引（${n} 轮）`
    : `已打开对话索引（${n} 轮）。当前不是全屏界面，点击和跳回原位置都用不了，选中的一轮会在面板里展开；输入 /tui fullscreen 切换到全屏（会重启并恢复这个会话）`
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

/** 按格宽折行，最多 max 行，放不下时最后一行以 … 结尾。 */
function wrap(text: string, cols: number, max: number): string[] {
  const lines: string[] = []
  for (const para of text.split('\n')) {
    let line = ''
    let w = 0
    for (const ch of para) {
      const cw = cellWidth(ch.codePointAt(0) ?? 0)
      if (w + cw > cols) {
        lines.push(line)
        line = ''
        w = 0
      }
      line += ch
      w += cw
    }
    if (line.trim() !== '') lines.push(line)
  }
  if (lines.length <= max) return lines
  const kept = lines.slice(0, max)
  kept[max - 1] = truncate(kept[max - 1] + '……', cols)
  return kept
}

/** 回答的正文，去掉代码块和 Markdown 符号，留给非全屏时在面板里展开。 */
function plainAnswer(answer: string): string {
  return answer
    .replace(/```[\s\S]*?```/g, '[代码]')
    .replace(/[`*_#>|]/g, '')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .split('\n')
    .map(l => l.trim())
    .filter(l => l !== '' && !/^[-:\s]+$/.test(l))
    .join('\n')
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

  on('command.run', { command: 'trail' }, async ($, e) => ({
    text: await openPane($, e.args.trim(), mode, e.presentation?.isFullscreen !== false),
  }))

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

  // 记下焦点在哪一行：这一行要事先对调颜色，引擎反色后才是想要的样子（见 seg）
  on('ui.focus', async ($, e, next) => {
    const result = await next(e)
    if (e.requestId === PANE && !result.deny) {
      await update($, focusAtom, () => e.element ?? '')
      await update($, revealAtom, () => e.element ?? '')
    }
    return result
  })

  // 滚轮、PgUp/PgDn 只滚中间的列表：引擎的窗口不动（面板内容正好一屏），插件挪自己的起点
  on('ui.scroll', async ($, e, next) => {
    if (e.component !== 'Pane' || e.requestId !== PANE || !listWindow.isOwn || e.origin.kind !== 'person') return next(e)
    if (e.by === 0) return {}
    const step = Math.sign(e.by) * Math.max(1, Math.round(Math.abs(e.by) / 2))
    const top = Math.max(0, listWindow.top + step)
    await update($, revealAtom, () => '')
    await update($, topAtom, () => (top >= listWindow.maxTop ? -1 : top))
    return {}
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const { Box, Text, Button } = $.ui.resolve(e)
    const selBg = await loadSelBackground($)
    const ring = e.props.isFocused ? await read($, focusAtom) : ''
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
            {filter === k.kind ? (
              <Text color="inverseText" backgroundColor={k.color} bold>{` ${k.label} ${counts(k.kind)} `}</Text>
            ) : (
              <Text color={k.color}>{`${k.label} ${counts(k.kind)}`}</Text>
            )}
          </Button>
        ))}
        {query !== '' && (
          <Button key="clear-query" plain onPress={() => update($, queryAtom, () => '')}>
            <Text color="warning">{`“${truncate(query, 16)}” ${shown.length} 条 ✕`}</Text>
          </Button>
        )}
      </Box>
    )

    // 引擎会把有焦点的按钮和鼠标下的按钮整个反色，彩色字会变成一块块色块。
    // 所以这几种情况下事先对调前景和背景，反色后正好是 selBg 底色上的彩色字，和 Codex 面板一样：
    // lit：焦点在这一行；hover：鼠标在这一行（引擎同时应用，不经过插件）；marked：当前这一轮，没有反色，直接画底色
    const seg = (text: string, fg: string, o: { lit?: boolean; marked?: boolean; bold?: boolean; italic?: boolean; strike?: boolean } = {}) => (
      <Text
        color={o.lit ? selBg : fg}
        backgroundColor={o.lit ? fg : o.marked ? selBg : undefined}
        bold={o.bold}
        italic={o.italic}
        strikethrough={o.strike}
        hover={{ color: selBg, backgroundColor: fg }}
      >
        {text}
      </Text>
    )
    const hints = (items: [string, string][]) => (
      <Text>
        {items.map(([key, what], i) => (
          <Text key={`h:${i}`}>
            {i > 0 && <Text color="inactive">{'  '}</Text>}
            <Text color="claude">{key}</Text>
            <Text color="inactive">{` ${what}`}</Text>
          </Text>
        ))}
      </Text>
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
      // 鼠标点击不会移动焦点；挪过来，之后按 ↑↓ 从这一轮接着走
      await $.ui.focus({ requestId: PANE, key: `t:${t.id}` }).catch(() => undefined)
    }

    // 非全屏时面板在输入框上方：收不到点击，也滚不动对话，Tab 移到哪一行插件也不知道。
    // 改由 j/k 选中，只画放得下的几行，标题、提示和按键说明一直看得见；选中的一轮直接展开回答
    if (e.props.placement === 'inline') {
      listWindow.isOwn = false
      const at = shown.findIndex(t => t.id === selected)
      const i = at >= 0 ? at : shown.length - 1
      const sel = shown[i]
      const move = (d: number) => {
        const next = shown[Math.max(0, Math.min(shown.length - 1, i + d))]
        if (next) return update($, selectedAtom, () => next.id)
      }
      const notice = '非全屏：点不了，也跳不回原位置 · 输入 /tui fullscreen 切换'

      // 行数按优先级分：标题、提示、筛选、两条分隔线、按键说明一定要有，再是选中那一轮和它的总结，
      // 剩下的给回答正文和其他轮（放不下时上下各留一行"还有几轮"）
      const fixed = 6 + (busy !== '' ? 1 : 0)
      let avail = Math.max(0, e.props.scroll.bodyRows - fixed - (sel ? 2 : 0))
      const rest = Math.max(0, shown.length - 1)
      const answer = sel ? plainAnswer(sel.answer) : ''
      const wantAnswer = Math.min(8, Math.max(0, avail - Math.min(rest, 2) - (rest > 2 ? 2 : 0)))
      const answerLines = sel && wantAnswer > 0 ? wrap(answer, cols - 7, wantAnswer) : []
      avail -= answerLines.length
      const others = avail >= rest ? rest : Math.max(0, avail - 2)
      let from = Math.max(0, i - Math.floor(others / 2))
      const to = Math.min(shown.length, from + others + 1)
      from = Math.max(0, to - others - 1)
      const visible = shown.slice(from, to)

      const line = (t: TrailTurn) => {
        const info = kindInfo(t.kind)
        const isSel = t === sel
        const head = `${isSel ? '▌' : ' '}${t.isRewound ? '○' : '●'} ${pad(t.index)} `
        const time = clock(t.time)
        const prompt = truncate(t.prompt, cols - width(head) - width(info.label) - 1 - time.length - 2)
        const fill = Math.max(1, cols - width(head) - width(info.label) - 1 - width(prompt) - time.length - 1)
        const dim = t.isRewound
        const o = { lit: ring === `t:${t.id}`, marked: isSel }
        return (
          <Button key={`t:${t.id}`} plain onPress={() => update($, selectedAtom, () => t.id)}>
            {seg(head, dim ? 'inactive' : info.color, { ...o, bold: isSel })}
            {seg(info.label, dim ? 'inactive' : info.color, { ...o, bold: !dim })}
            {seg(' ', 'text', o)}
            {seg(prompt, dim ? 'inactive' : 'text', { ...o, bold: isSel, strike: dim })}
            {seg(' '.repeat(fill), 'text', o)}
            {seg(time, 'inactive', o)}
          </Button>
        )
      }

      return (
        <Box flexDirection="column">
          {header}
          <Text color="warning">{truncate(notice, cols)}</Text>
          {tabs}
          <Text dimColor>{'─'.repeat(cols)}</Text>
          {all.length === 0 && <Text dimColor>这个会话还没有对话。</Text>}
          {all.length > 0 && shown.length === 0 && <Text dimColor>没有匹配的对话。</Text>}
          {from > 0 && <Text dimColor>{`  ↑ 还有 ${from} 轮`}</Text>}
          {visible.map(t => (
            <Box key={`row:${t.id}`} flexDirection="column">
              {line(t)}
              {t === sel && (
                <Box flexDirection="column" paddingLeft={5}>
                  <Text>
                    <Text dimColor>↳ </Text>
                    <Text color="subtle" italic={!t.isSummarized}>{truncate(t.isRewound ? '（已回退）' + t.summary : t.summary, cols - 7)}</Text>
                  </Text>
                  {answerLines.map((l, k) => (
                    <Text key={`a:${k}`} dimColor>{`  ${l}`}</Text>
                  ))}
                </Box>
              )}
            </Box>
          ))}
          {to < shown.length && <Text dimColor>{`  ↓ 还有 ${shown.length - to} 轮`}</Text>}
          <Text dimColor>{'─'.repeat(cols)}</Text>
          <Box flexDirection="row" columnGap={2}>
            <Button key="nav:up" plain hotkey="k" onPress={() => move(-1)}>上一轮</Button>
            <Button key="nav:down" plain hotkey="j" onPress={() => move(1)}>下一轮</Button>
            <Text dimColor>{e.props.isFocused ? '1-5 筛选 · Esc 回到输入框' : 'ctrl+x tab 进入面板后可用按键'}</Text>
          </Box>
        </Box>
      )
    }

    // 每一轮：两行，第一行提问，第二行一句话总结。
    // 当前这一轮（面板有焦点时是焦点所在的一轮，否则是上次跳到的一轮）带底色和左侧色条，下面展开详情
    const ringTurn = ring.startsWith('t:') ? ring.slice(2) : ''
    const current = ringTurn && shown.some(t => t.id === ringTurn) ? ringTurn : selected
    const rows = shown.map(t => {
      const info = kindInfo(t.kind)
      const isCur = t.id === current
      const [g1, g2] = gutter(all, t, isTree)
      const time = clock(t.time)
      const bar = isCur ? '▌' : ' '
      const head = `${g1} ${pad(t.index)} `
      const promptRoom = cols - 1 - width(head) - width(info.label) - 1 - time.length - 2
      const prompt = truncate(t.prompt, promptRoom)
      const fill = Math.max(1, cols - 1 - width(head) - width(info.label) - 1 - width(prompt) - time.length - 1)

      const s = sumFiles(t)
      const meta = t.isRewound ? '↺ 已回退' : t.files.length > 0 ? `${t.files.length} 文件 +${s.add} −${s.del}` : ''
      const lead = `${g2}    ↳ `
      const summary = truncate(t.summary, cols - 1 - width(lead) - width(meta) - 2)
      const gap = Math.max(1, cols - 1 - width(lead) - width(summary) - width(meta) - 1)

      const dim = t.isRewound
      const o = { lit: ring === `t:${t.id}`, marked: isCur }
      return (
        <Box key={`row:${t.id}`} flexDirection="column">
          <Button key={`t:${t.id}`} plain onPress={() => jump(t)} autoFocus={t.id === selected || undefined}>
            {seg(bar, info.color, o)}
            {seg(head, dim ? 'inactive' : info.color, o)}
            {seg(info.label, dim ? 'inactive' : info.color, { ...o, bold: !dim })}
            {seg(' ', 'text', o)}
            {seg(prompt, dim ? 'inactive' : 'text', { ...o, bold: isCur, strike: dim })}
            {seg(' '.repeat(fill), 'text', o)}
            {seg(time, 'inactive', o)}
            <Text>{'\n'}</Text>
            {seg(bar, info.color, o)}
            {seg(lead, 'inactive', o)}
            {seg(summary, dim ? 'inactive' : isCur ? 'text' : 'subtle', { ...o, italic: !t.isSummarized })}
            {seg(' '.repeat(gap), 'text', o)}
            {seg(meta, t.isRewound ? 'inactive' : 'diffAdded', o)}
          </Button>
        </Box>
      )
    })
    const cur = shown.find(t => t.id === current)

    // 列表窗口：面板一屏放得下的行数减去固定的标题、筛选、两条分隔线和按键说明
    const footer = e.props.isFocused
      ? ([['↑↓', '选择'], ['⏎', '跳到原位置'], ['滚轮', '翻页'], ['1-5', '筛选'], ['Esc', '回到输入框']] as [string, string][])
      : ([['点击', '跳到原位置'], ['滚轮', '翻页'], ['ctrl+x tab', '用键盘'], ['/trail 关键词', '搜索']] as [string, string][])
    const footerWidth = footer.reduce((w, [k, v], i) => w + (i > 0 ? 2 : 0) + width(k) + 1 + width(v), 0)
    const tabsWidth =
      KINDS.reduce((w, k) => w + 4 + width(`${k.label} ${counts(k.kind)}`) + (filter === k.kind ? 2 : 0), 0) +
      (query !== '' ? width(`“${truncate(query, 16)}” ${shown.length} 条 ✕`) + 1 : 0)
    const lines = (w: number) => Math.max(1, Math.ceil(w / cols))
    const fixed = 1 + (busy !== '' ? 1 : 0) + lines(tabsWidth) + 2 + lines(footerWidth)
    const listRows = Math.max(2, e.props.scroll.bodyRows - fixed)
    const heights = shown.map(t => 2 + (t === cur ? detailHeight(t, cols) : 0))
    const endFrom = (from: number) => {
      let h = 0
      let i = from
      while (i < shown.length && h + heights[i]! <= listRows) h += heights[i++]!
      return Math.max(i, Math.min(shown.length, from + 1))
    }
    let maxTop = shown.length
    for (let h = 0; maxTop > 0 && h + heights[maxTop - 1]! <= listRows; ) h += heights[--maxTop]!
    const stored = await read($, topAtom)
    let top = stored < 0 ? maxTop : Math.min(stored, maxTop)
    // 焦点移到窗口边上时，带上它前后各一轮，↑↓ 才有下一行可走
    const reveal = await read($, revealAtom)
    const ri = shown.findIndex(t => `t:${t.id}` === reveal)
    if (ri >= 0) {
      if (ri <= top) top = Math.max(0, ri - 1)
      else while (top < ri && endFrom(top) <= Math.min(shown.length - 1, ri + 1)) top++
      top = Math.min(top, maxTop)
    }
    const end = endFrom(top)
    Object.assign(listWindow, { isOwn: true, top, maxTop })
    const range = shown.length > end - top ? ` ${top + 1}–${end} / ${shown.length} ` : ''
    const rule = (label: string) => (
      <Box flexShrink={0}>
        <Text color="inactive">{'─'.repeat(Math.max(0, cols - width(label) - 1)) + label + '─'}</Text>
      </Box>
    )

    return (
      <Box flexDirection="column" height={e.props.scroll.bodyRows}>
        {/* 只有列表能被压缩：别的行估算差一行时，也是列表少一行，标题和按键说明不动 */}
        <Box flexDirection="column" flexShrink={0}>
          {header}
          {tabs}
        </Box>
        {rule(range && top > 0 ? ` ↑${range}` : range)}
        <Box flexDirection="column" height={listRows} flexShrink={1} overflow="hidden">
          {all.length === 0 && <Text color="inactive">这个会话还没有对话。</Text>}
          {all.length > 0 && shown.length === 0 && <Text color="inactive">没有匹配的对话。</Text>}
          {/* 每一轮都画出来，靠上移把窗口外的裁掉：只画窗口里的几行的话，焦点所在行前面一插入新行，
              Claude Code 的 ↑↓ 就会走乱（它按位置记焦点） */}
          <Box flexDirection="column" flexShrink={0} marginTop={-heights.slice(0, top).reduce((a, b) => a + b, 0)}>
            {rows.map((row, k) => (
              <Box key={`wrap:${shown[k]!.id}`} flexDirection="column" flexShrink={0}>
                {row}
                {cur && shown[k] === cur && detail(cur, cols, Box, Text)}
              </Box>
            ))}
          </Box>
        </Box>
        {rule(end < shown.length ? ` ↓ 还有 ${shown.length - end} 轮 ` : '')}
        <Box flexShrink={0}>{hints(footer)}</Box>
      </Box>
    )
  })
}

/** detail() 画出来有几行，算列表窗口用。 */
function detailHeight(t: TrailTurn, cols: number): number {
  return (
    wrap(plainAnswer(t.answer), cols - 13, 4).length +
    (Object.keys(t.tools).length > 0 ? 1 : 0) +
    (t.thinkingChars > 0 ? 1 : 0) +
    Math.min(8, t.files.length) +
    (t.files.length > 8 ? 1 : 0)
  )
}

// 当前这一轮展开：结论、工具、改动文件、思考量
function detail(t: TrailTurn, cols: number, Box: any, Text: any) {
  const tools = Object.entries(t.tools)
    .sort((a, b) => b[1] - a[1])
    .map(([name, n]) => `${name}×${n}`)
    .join(' · ')
  const answer = wrap(plainAnswer(t.answer), cols - 13, 4)
  return (
    <Box flexDirection="column" paddingLeft={7}>
      {answer.map((l, i) => (
        <Text key={`ans:${i}`}>
          <Text color="inactive">{i === 0 ? '结论  ' : '      '}</Text>
          <Text>{l}</Text>
        </Text>
      ))}
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
