import { expect, test } from 'claude-code/testing'

import { jumpFailure } from '../hooks/jump'
import { parseTranscript } from '../hooks/transcript'

// 一个小的会话记录：三轮对话，第二轮后来被回退（Esc Esc），第三轮改了一个文件。
const row = (o: object) => JSON.stringify(o)
const TRANSCRIPT = [
  row({ type: 'user', uuid: 'u1', parentUuid: null, timestamp: '2026-10-30T02:00:00Z', message: { role: 'user', content: '给我设计一个幂等方案' } }),
  row({ type: 'assistant', uuid: 'a1', parentUuid: 'u1', timestamp: '2026-10-30T02:01:00Z', message: { role: 'assistant', content: [{ type: 'thinking', thinking: 'xxxx' }, { type: 'text', text: '推荐三层幂等。' }] } }),
  row({ type: 'user', uuid: 'u2', parentUuid: 'a1', timestamp: '2026-10-30T02:02:00Z', message: { role: 'user', content: '用乐观锁来做' } }),
  row({ type: 'assistant', uuid: 'a2', parentUuid: 'u2', timestamp: '2026-10-30T02:03:00Z', message: { role: 'assistant', content: [{ type: 'text', text: '乐观锁挡不住重复插入。' }] } }),
  row({ type: 'user', uuid: 'u3', parentUuid: 'a1', timestamp: '2026-10-30T02:04:00Z', message: { role: 'user', content: '改用唯一索引实现' } }),
  row({ type: 'assistant', uuid: 'a3', parentUuid: 'u3', timestamp: '2026-10-30T02:05:00Z', message: { role: 'assistant', content: [{ type: 'tool_use', id: 'tu1', name: 'Edit', input: {} }] } }),
  row({ type: 'user', uuid: 'r3', parentUuid: 'a3', timestamp: '2026-10-30T02:05:30Z', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'tu1', content: 'ok' }] }, toolUseResult: { filePath: '/p/internal/order/repo.go', structuredPatch: [{ lines: [' a', '+b', '+c', '-d'] }] } }),
  row({ type: 'assistant', uuid: 'a4', parentUuid: 'r3', timestamp: '2026-10-30T02:06:00Z', message: { role: 'assistant', content: [{ type: 'text', text: '已加唯一索引。' }] } }),
].join('\n')

const PANE = {
  plugin: 'trail',
  component: 'Pane',
  requestId: 'trail',
  props: {
    title: '对话索引',
    isFocused: true,
    bodyColumns: 60,
    placement: 'dock',
    scroll: { offset: 0, bodyRows: 30 },
    view: {},
  },
} as const

test('/trail 列出各轮，回退的分支标出来，点击一轮会展开详情', async ($, on) => {
  on('command.register', () => ({ value: { command: 'trail' } }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 's1' }))
  on('session.root', () => ({ value: '/p' }))
  on('session.cwd', () => ({ value: '/p' }))
  on('env.get', () => ({ value: undefined }))
  on('fs.exists', () => ({ value: true }))
  on('fs.stat', () => ({ value: { kind: 'file', size: TRANSCRIPT.length, mtimeMs: 0, isLink: false } }))
  on('fs.read', () => ({ value: TRANSCRIPT }))
  on('store.get', () => ({ value: undefined }))
  on('clock.after', () => ({ value: undefined }))
  on('store.set', () => ({ value: undefined }))
  on('store.delete', () => ({ value: undefined }))
  on('model.complete', () => ({
    value: {
      isAnswered: true,
      text: '[{"i":1,"kind":"plan","summary":"三层幂等：唯一键 + 预占 + 索引兜底"}]',
      usage: { input_tokens: 1, output_tokens: 1, cache_creation_input_tokens: 0, cache_read_input_tokens: 0 },
    },
  }))
  // 测试工具里没有对话本身，滚动对话这一步只能在真实会话里验证
  on('ui.toast', () => ({ value: undefined }))

  await $.session.start({ cwd: '/p', surface: 'terminal', isInteractive: true })
  const ran = await $.command.run({ command: 'trail', args: '' })
  expect(ran.text).toBe('已打开对话索引（3 轮）')

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ ...PANE, surface })
    expect(await ui.find({ type: 'Text', text: /给我设计一个幂等方案/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /已回退/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /1 文件 \+2 −1/ })).toBeDefined()
    await ui.press({ key: 't:u3' })
    expect(await ui.find({ type: 'Text', text: /repo\.go/ })).toBeDefined()
    await ui.unmount()
  }
})

test('按类型筛选只留下那一类', async ($, on) => {
  on('command.register', () => ({ value: { command: 'trail' } }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 's1' }))
  on('session.root', () => ({ value: '/p' }))
  on('session.cwd', () => ({ value: '/p' }))
  on('env.get', () => ({ value: undefined }))
  on('fs.exists', () => ({ value: true }))
  on('fs.stat', () => ({ value: { kind: 'file', size: TRANSCRIPT.length, mtimeMs: 0, isLink: false } }))
  on('fs.read', () => ({ value: TRANSCRIPT }))
  on('store.get', () => ({ value: undefined }))
  on('clock.after', () => ({ value: undefined }))
  on('store.set', () => ({ value: undefined }))
  on('store.delete', () => ({ value: undefined }))
  on('model.complete', () => ({
    value: {
      isAnswered: true,
      text: '[{"i":1,"kind":"plan","summary":"三层幂等：唯一键 + 预占 + 索引兜底"}]',
      usage: { input_tokens: 1, output_tokens: 1, cache_creation_input_tokens: 0, cache_read_input_tokens: 0 },
    },
  }))

  await $.session.start({ cwd: '/p', surface: 'terminal', isInteractive: true })
  await $.command.run({ command: 'trail', args: '' })

  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  await ui.press({ key: 'f:change' })
  expect(await ui.find({ type: 'Text', text: /改用唯一索引实现/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /给我设计一个幂等方案/ })).toBeUndefined()
  await ui.unmount()
})

// 与 Go 面板共用的情况（../testdata/claude-commands.jsonl）：拖进终端的图片路径、/compact 和
// 压缩摘要、带参数的斜杠命令、排队记录和 <synthetic> 占位回复。
const REAL_CASES = [
  "{\"type\":\"user\",\"uuid\":\"u1\",\"parentUuid\":null,\"timestamp\":\"2026-10-30T02:00:00Z\",\"cwd\":\"/p\",\"entrypoint\":\"cli\",\"message\":{\"role\":\"user\",\"content\":\"/Users/me/Desktop/shot.png 这边的类型换回文字\"}}",
  "{\"type\":\"assistant\",\"uuid\":\"a1\",\"parentUuid\":\"u1\",\"timestamp\":\"2026-10-30T02:01:00Z\",\"message\":{\"model\":\"claude-x\",\"content\":[{\"type\":\"text\",\"text\":\"改好了。\"}]}}",
  "{\"type\":\"queue-operation\",\"operation\":\"enqueue\",\"timestamp\":\"2026-10-30T05:00:00Z\"}",
  "{\"type\":\"user\",\"uuid\":\"c1\",\"parentUuid\":\"a1\",\"timestamp\":\"2026-10-30T05:00:01Z\",\"message\":{\"role\":\"user\",\"content\":\"/compact\"}}",
  "{\"type\":\"system\",\"subtype\":\"compact_boundary\",\"uuid\":\"b1\",\"parentUuid\":null,\"logicalParentUuid\":\"a1\",\"timestamp\":\"2026-10-30T05:01:00Z\"}",
  "{\"type\":\"user\",\"uuid\":\"s1\",\"parentUuid\":\"b1\",\"isCompactSummary\":true,\"isVisibleInTranscriptOnly\":true,\"timestamp\":\"2026-10-30T05:01:00Z\",\"message\":{\"role\":\"user\",\"content\":\"This session is being continued from a previous conversation that ran out of context.\"}}",
  "{\"type\":\"user\",\"uuid\":\"l1\",\"parentUuid\":\"s1\",\"timestamp\":\"2026-10-30T05:01:01Z\",\"message\":{\"role\":\"user\",\"content\":\"<local-command-stdout>Compacted</local-command-stdout>\"}}",
  "{\"type\":\"user\",\"uuid\":\"g1\",\"parentUuid\":\"l1\",\"timestamp\":\"2026-10-30T05:02:00Z\",\"message\":{\"role\":\"user\",\"content\":\"<command-name>/goal</command-name>\\n            <command-message>goal</command-message>\\n            <command-args>修好所有问题</command-args>\"}}",
  "{\"type\":\"user\",\"uuid\":\"m1\",\"parentUuid\":\"g1\",\"isMeta\":true,\"timestamp\":\"2026-10-30T05:02:00Z\",\"message\":{\"role\":\"user\",\"content\":\"A session-scoped Stop hook is now active.\"}}",
  "{\"type\":\"assistant\",\"uuid\":\"a2\",\"parentUuid\":\"m1\",\"timestamp\":\"2026-10-30T05:03:00Z\",\"message\":{\"model\":\"claude-x\",\"content\":[{\"type\":\"text\",\"text\":\"都修好了。\"}]}}",
  "{\"type\":\"assistant\",\"uuid\":\"y1\",\"parentUuid\":\"a2\",\"timestamp\":\"2026-10-30T09:00:00Z\",\"message\":{\"model\":\"<synthetic>\",\"content\":[{\"type\":\"text\",\"text\":\"No response requested.\"}]}}",
  "{\"type\":\"user\",\"uuid\":\"k1\",\"parentUuid\":\"y1\",\"isMeta\":true,\"timestamp\":\"2026-11-02T08:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"<local-command-caveat>The command below was run directly in Claude Code.</local-command-caveat>\"}}",
  "{\"type\":\"user\",\"uuid\":\"k2\",\"parentUuid\":\"k1\",\"timestamp\":\"2026-11-02T08:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"<command-name>/trail</command-name>\\n            <command-message>trail</command-message>\\n            <command-args></command-args>\"}}",
].join('\n')

test('斜杠命令、压缩摘要和占位回复按真实会话处理', async ($, on) => {
  on('command.register', () => ({ value: { command: 'trail' } }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 's2' }))
  on('session.root', () => ({ value: '/p' }))
  on('session.cwd', () => ({ value: '/p' }))
  on('env.get', () => ({ value: undefined }))
  on('fs.exists', () => ({ value: true }))
  on('fs.stat', () => ({ value: { kind: 'file', size: REAL_CASES.length, mtimeMs: 0, isLink: false } }))
  on('fs.read', () => ({ value: REAL_CASES }))
  on('store.get', () => ({ value: undefined }))
  on('clock.after', () => ({ value: undefined }))
  on('store.set', () => ({ value: undefined }))
  on('store.delete', () => ({ value: undefined }))
  on('model.complete', () => ({ value: { isAnswered: false } }))

  await $.session.start({ cwd: '/p', surface: 'terminal', isInteractive: true })
  const ran = await $.command.run({ command: 'trail', args: '' })
  expect(ran.text).toBe('已打开对话索引（2 轮）')

  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await ui.find({ type: 'Text', text: /\[图片\] 这边的类型换回文字/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /\/goal 修好所有问题/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /\/compact|continued from/ })).toBeUndefined()
  // 从第一轮开始到 /goal 那一轮答完，后面几天才写的本地命令说明不算
  expect(await ui.find({ type: 'Text', text: /2 轮 · 3h03m/ })).toBeDefined()
  await ui.unmount()
})

test('超过 4 MiB 的记录改用 cat 流式读取', async ($, on) => {
  let isRead = false
  let argv: readonly string[] = []
  on('command.register', () => ({ value: { command: 'trail' } }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 's3' }))
  on('session.root', () => ({ value: '/p' }))
  on('session.cwd', () => ({ value: '/p' }))
  on('env.get', () => ({ value: undefined }))
  on('fs.exists', () => ({ value: true }))
  on('fs.stat', () => ({ value: { kind: 'file', size: 30 * 1024 * 1024, mtimeMs: 0, isLink: false } }))
  on('fs.read', () => {
    isRead = true
    return { value: '' }
  })
  // 按任意位置切开的片段，一行可能跨两片
  on('process.spawn', async function* (_, e) {
    argv = e.argv
    for (let i = 0; i < TRANSCRIPT.length; i += 97) {
      yield { stream: 'stdout' as const, text: TRANSCRIPT.slice(i, i + 97) }
    }
    return { value: { code: 0, signal: null } }
  })
  on('store.get', () => ({ value: undefined }))
  on('clock.after', () => ({ value: undefined }))
  on('store.set', () => ({ value: undefined }))
  on('store.delete', () => ({ value: undefined }))
  on('model.complete', () => ({ value: { isAnswered: false } }))

  await $.session.start({ cwd: '/p', surface: 'terminal', isInteractive: true })
  const ran = await $.command.run({ command: 'trail', args: '' })
  expect(ran.text).toBe('已打开对话索引（3 轮）')
  expect(isRead).toBe(false)
  expect(argv).toEqual(['cat', '/.claude/projects/-p/s3.jsonl'])
})

test('压缩之前的轮次跳不过去时说明原因', async () => {
  const [before, after] = parseTranscript(REAL_CASES)
  expect(before!.isCompacted).toBe(true)
  expect(after!.isCompacted).toBe(false)
  // 恢复的会话里，压缩之前的轮次没有画出来
  expect(jumpFailure(before!, 'nothing drawn under requestId')).toMatch(/上次压缩/)
  expect(jumpFailure(after!, 'nothing drawn under requestId')).toMatch(/^没能跳转/)
})

test('非全屏时面板在输入框上方：标题和按键说明一直在，j/k 选中并展开回答', async ($, on) => {
  on('command.register', () => ({ value: { command: 'trail' } }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 's4' }))
  on('session.root', () => ({ value: '/p' }))
  on('session.cwd', () => ({ value: '/p' }))
  on('env.get', () => ({ value: undefined }))
  on('fs.exists', () => ({ value: true }))
  on('fs.stat', () => ({ value: { kind: 'file', size: TRANSCRIPT.length, mtimeMs: 0, isLink: false } }))
  on('fs.read', () => ({ value: TRANSCRIPT }))
  on('store.get', () => ({ value: undefined }))
  on('clock.after', () => ({ value: undefined }))
  on('store.set', () => ({ value: undefined }))
  on('store.delete', () => ({ value: undefined }))
  on('model.complete', () => ({ value: { isAnswered: false } }))

  await $.session.start({ cwd: '/p', surface: 'terminal', isInteractive: true })
  const ran = await $.command.run({ command: 'trail', args: '', presentation: { isFullscreen: false, columns: 100, rows: 40 } })
  expect(ran.text).toMatch(/\/tui fullscreen/)

  const ui = await $.ui.mount({ ...PANE, surface: 'terminal', props: { ...PANE.props, placement: 'inline', scroll: { offset: 0, bodyRows: 12 } } })
  expect(await ui.find({ type: 'Text', text: /对话索引/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /\/tui fullscreen/ })).toBeDefined()
  // 默认选中最后一轮，回答直接展开
  expect(await ui.find({ type: 'Text', text: /已加唯一索引/ })).toBeDefined()
  await ui.press({ key: 'nav:up' })
  await ui.press({ key: 'nav:up' })
  await ui.press({ key: 'nav:up' })
  expect(await ui.find({ type: 'Text', text: /推荐三层幂等/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /已加唯一索引/ })).toBeUndefined()
  await ui.unmount()
})

test('焦点移到哪一轮就展开哪一轮；这一行事先对调颜色，引擎反色后是底色上的彩色字', async ($, on) => {
  on('command.register', () => ({ value: { command: 'trail' } }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('session.id', () => ({ value: 's1' }))
  on('session.root', () => ({ value: '/p' }))
  on('session.cwd', () => ({ value: '/p' }))
  on('env.get', () => ({ value: undefined }))
  on('fs.exists', () => ({ value: true }))
  on('fs.stat', () => ({ value: { kind: 'file', size: TRANSCRIPT.length, mtimeMs: 0, isLink: false } }))
  on('fs.read', () => ({ value: TRANSCRIPT }))
  on('store.get', () => ({ value: undefined }))
  on('store.set', () => ({ value: undefined }))
  on('store.delete', () => ({ value: undefined }))
  on('clock.after', () => ({ value: undefined }))
  on('ui.focus', () => ({}))
  on('ui.log', () => ({ value: undefined }))

  await $.session.start({ cwd: '/p', surface: 'terminal', isInteractive: true })
  await $.command.run({ command: 'trail', args: '' } as any)
  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await ui.find({ type: 'Text', text: /结论/ })).toBeUndefined()

  // 引擎在用户按 Tab 或方向键时发出的事件
  await $.ui.focus({ component: 'Pane', requestId: 'trail', plugin: 'trail', element: 't:u1', origin: { kind: 'person' } } as any)
  expect(await ui.find({ type: 'Text', text: /结论/ })).toBeDefined()
  const prompt = (await ui.find({ type: 'Text', text: '给我设计一个幂等方案' })) as any
  expect(prompt.props.color).toBe('#444444')
  expect(prompt.props.backgroundColor).toBe('text')
  // 别的行是普通颜色（悬停时的对调由界面自己做，测试工具里看不到 hover）
  const other = (await ui.find({ type: 'Text', text: '改用唯一索引实现' })) as any
  expect(other.props.color).toBe('text')
  expect(other.props.backgroundColor).toBeUndefined()
  await ui.unmount()
})
