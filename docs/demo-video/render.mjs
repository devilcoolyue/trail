// 把演示页面逐帧渲染成视频，配上配音和背景音乐。
//
// 准备（只需一次）：
//   mkdir -p /tmp/video-tools && cd /tmp/video-tools && npm init -y
//   npm i playwright @ffmpeg-installer/ffmpeg msedge-tts @fontsource/jetbrains-mono @fontsource/inter \
//     @fontsource/noto-sans-symbols @fontsource/noto-sans-symbols-2
//   PLAYWRIGHT_BROWSERS_PATH=/tmp/video-tools/browsers npx playwright install chromium-headless-shell
// 系统里还需要 Noto Sans CJK 字体（显示中文）。配音用微软 Edge 的在线语音（只发送配音文字），
// 生成后缓存在 /tmp/video-tools/narration/。
//
// 用法：
//   node render.mjs claude.html preview 0 12 27.5    # 截这几个时刻的图到 preview/
//   node render.mjs claude.html video trail-claude.mp4
//   node render.mjs codex.html video trail-codex.mp4
//   node render.mjs codex.html audio trail-codex.mp4   # 画面不变，只重做音轨（要先有 trail-codex.video.mp4）
//
// 页面约定：window.render(t) 画出第 t 秒；window.DURATION 是总长；
// window.NARRATION 是 [开始秒数, 配音文字] 列表；window.setNarrationDurations(秒数列表) 用来对齐字幕。
import { spawn, spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const TOOLS = process.env.TRAIL_VIDEO_TOOLS || '/tmp/video-tools'
process.env.PLAYWRIGHT_BROWSERS_PATH ||= join(TOOLS, 'browsers')
const { chromium } = await import(join(TOOLS, 'node_modules/playwright/index.mjs'))
const ffmpeg = join(TOOLS, 'node_modules/@ffmpeg-installer/linux-x64/ffmpeg')
const VOICE = 'zh-CN-XiaoxiaoNeural'
const RATE = '+6%'

const here = dirname(fileURLToPath(import.meta.url))
const font = (family, pkg, file, weight = 400) =>
  `@font-face{font-family:${family};src:url(file://${TOOLS}/node_modules/@fontsource/${pkg}/files/${file});font-weight:${weight}}`
writeFileSync(join(here, 'fonts.css'), [
  font('JBM', 'jetbrains-mono', 'jetbrains-mono-latin-400-normal.woff2'),
  font('JBM', 'jetbrains-mono', 'jetbrains-mono-latin-700-normal.woff2', 700),
  font('JBMx', 'jetbrains-mono', 'jetbrains-mono-latin-ext-400-normal.woff2'),
  font('Sym2', 'noto-sans-symbols-2', 'noto-sans-symbols-2-symbols-400-normal.woff2'),
  font('Sym2b', 'noto-sans-symbols-2', 'noto-sans-symbols-2-math-400-normal.woff2'),
  font('Sym', 'noto-sans-symbols', 'noto-sans-symbols-symbols-400-normal.woff2'),
  ...[400, 500, 600, 700, 800].map(w => font('Inter', 'inter', `inter-latin-${w === 800 ? 700 : w}-normal.woff2`, w)),
].join('\n') + '\n')

const [pageFile = 'claude.html', mode = 'preview', ...args] = process.argv.slice(2)
const FPS = 30

// ───────── 配音 ─────────

async function narrationFile(text) {
  const dir = join(TOOLS, 'narration')
  const id = createHash('sha1').update(`${VOICE} ${RATE} ${text}`).digest('hex').slice(0, 12)
  const out = join(dir, `${id}.mp3`)
  if (existsSync(out)) return out
  const { MsEdgeTTS, OUTPUT_FORMAT } = await import(join(TOOLS, 'node_modules/msedge-tts/dist/index.js'))
  const tts = new MsEdgeTTS()
  await tts.setMetadata(VOICE, OUTPUT_FORMAT.AUDIO_24KHZ_96KBITRATE_MONO_MP3)
  const tmp = join(dir, id)
  mkdirSync(tmp, { recursive: true })
  const { audioFilePath } = await tts.toFile(tmp, text, { rate: RATE })
  tts.close()
  spawnSync('mv', [audioFilePath, out])
  rmSync(tmp, { recursive: true, force: true })
  return out
}

function duration(file) {
  const info = spawnSync(ffmpeg, ['-i', file], { encoding: 'utf8' }).stderr
  const [, h, m, s] = info.match(/Duration: (\d+):(\d+):([\d.]+)/)
  return Number(h) * 3600 + Number(m) * 60 + Number(s)
}

// ───────── 背景音乐：慢速的大七和弦铺底，加一点钢片琴式的分解和弦 ─────────

function music(seconds, SR) {
  const n = Math.ceil(seconds * SR)
  const buf = new Float32Array(n)
  const hz = m => 440 * Math.pow(2, (m - 69) / 12)
  // C maj7 → A m7 → F maj7 → G6，每个和弦 4 秒
  const CHORDS = [[48, 60, 64, 67, 71], [45, 57, 60, 64, 67], [41, 57, 60, 64, 69], [43, 59, 62, 67, 64]]
  const BAR = 4
  for (let i = 0; i < n; i++) {
    const t = i / SR
    const bar = Math.floor(t / BAR)
    const local = t - bar * BAR
    let v = 0
    for (const b of [bar, bar - 1]) {
      // 和弦之间 0.8 秒交叉淡化
      const chord = CHORDS[((b % 4) + 4) % 4]
      const into = b === bar ? Math.min(1, local / 0.8) : Math.max(0, 1 - local / 0.8)
      if (into <= 0) continue
      const env = b === bar ? 0.5 - 0.5 * Math.cos(Math.PI * into) : 0.5 + 0.5 * Math.cos(Math.PI * (1 - into))
      chord.forEach((m, k) => {
        const f = hz(m)
        const amp = k === 0 ? 0.32 : 0.14
        v += env * amp * (Math.sin(2 * Math.PI * f * t) + 0.6 * Math.sin(2 * Math.PI * f * 1.003 * t + k))
      })
    }
    // 分解和弦：每半拍一个音，快速起音、慢慢衰减
    const step = Math.floor(t / 0.5)
    const since = t - step * 0.5
    const chord = CHORDS[Math.floor((step * 0.5) / BAR) % 4]
    const note = chord[1 + (step % 4)] + 12
    v += 0.11 * Math.exp(-since * 5) * Math.min(1, since * 200) * Math.sin(2 * Math.PI * hz(note) * t)
    buf[i] = v
  }
  // 简单低通，去掉毛刺
  for (let i = 1; i < n; i++) buf[i] = buf[i - 1] + 0.35 * (buf[i] - buf[i - 1])
  let peak = 0
  for (const x of buf) peak = Math.max(peak, Math.abs(x))
  for (let i = 0; i < n; i++) buf[i] = (buf[i] / peak) * Math.min(1, i / SR / 1.5, (n - i) / SR / 2.5)
  return buf
}

// ───────── 混音：配音放在各自的开始时间，说话时把背景音乐压低 ─────────
// 机器上的 ffmpeg 是 2018 年的版本，amix 没有 normalize，输入结束时还会改音量，所以在这里自己混

function decode(file, SR) {
  const r = spawnSync(ffmpeg, ['-loglevel', 'error', '-i', file, '-f', 's16le', '-ac', '1', '-ar', String(SR), '-'], { maxBuffer: 1 << 28 })
  const pcm = new Int16Array(r.stdout.buffer, r.stdout.byteOffset, r.stdout.length / 2)
  return Float32Array.from(pcm, x => x / 32768)
}

function mixdown(seconds, voices, out) {
  const SR = 44100
  const bg = music(seconds, SR)
  const n = bg.length
  const voice = new Float32Array(n)
  const duck = new Float32Array(n).fill(1)
  for (const v of voices) {
    const at = Math.round(v.start * SR)
    decode(v.file, SR).forEach((x, i) => { if (at + i < n) voice[at + i] += x })
    // 说话时音乐降到 40%，前后 0.3 秒渐变
    const a = v.start, b = v.start + v.length
    for (let i = Math.max(0, Math.round((a - 0.3) * SR)); i < Math.min(n, Math.round((b + 0.3) * SR)); i++) {
      const t = i / SR
      const k = Math.min(1, (t - (a - 0.3)) / 0.3, ((b + 0.3) - t) / 0.3)
      duck[i] = Math.min(duck[i], 1 - 0.6 * Math.max(0, k))
    }
  }
  const pcm = Buffer.alloc(44 + n * 2)
  pcm.write('RIFF', 0); pcm.writeUInt32LE(36 + n * 2, 4); pcm.write('WAVEfmt ', 8)
  pcm.writeUInt32LE(16, 16); pcm.writeUInt16LE(1, 20); pcm.writeUInt16LE(1, 22)
  pcm.writeUInt32LE(SR, 24); pcm.writeUInt32LE(SR * 2, 28); pcm.writeUInt16LE(2, 32); pcm.writeUInt16LE(16, 34)
  pcm.write('data', 36); pcm.writeUInt32LE(n * 2, 40)
  for (let i = 0; i < n; i++) {
    const x = Math.tanh(voice[i] * 1.3 + bg[i] * 0.14 * duck[i])  // tanh 防止削波
    pcm.writeInt16LE(Math.round(x * 32000), 44 + i * 2)
  }
  writeFileSync(out, pcm)
}

// ───────── 画面 ─────────

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1920, height: 1080 } })
await page.goto('file://' + join(here, pageFile))
await page.evaluate(() => document.fonts.ready)

const narration = await page.evaluate(() => window.NARRATION || [])
const voices = []
for (const [start, text] of narration) {
  const file = await narrationFile(text)
  voices.push({ start, file, length: duration(file) })
}
await page.evaluate(d => window.setNarrationDurations && window.setNarrationDurations(d), voices.map(v => v.length))

if (mode === 'preview') {
  mkdirSync(join(here, 'preview'), { recursive: true })
  const name = pageFile.replace(/\.html$/, '')
  for (const t of args.map(Number)) {
    await page.evaluate(t => window.render(t), t)
    await page.screenshot({ path: join(here, 'preview', `${name}-t${String(t).padStart(5, '0')}.png`) })
  }
} else {
  const out = args[0] || join(here, pageFile.replace(/\.html$/, '.mp4'))
  const total = await page.evaluate(() => window.DURATION)
  const silent = out.replace(/\.mp4$/, '.video.mp4')
  const enc = spawn(ffmpeg, [mode === 'audio' ? '-n' : '-y', '-loglevel', 'error', '-f', 'image2pipe', '-framerate', String(FPS), '-c:v', 'mjpeg', '-i', '-',
    '-c:v', 'libx264', '-preset', 'slow', '-crf', '18', '-pix_fmt', 'yuv420p', silent], { stdio: ['pipe', 'inherit', 'inherit'] })
  const frames = mode === 'audio' ? 0 : Math.round(total * FPS)  // audio：画面已经渲染过，只重做音轨
  if (mode === 'audio') enc.kill()
  for (let i = 0; i < frames; i++) {
    await page.evaluate(t => window.render(t), i / FPS)
    const jpg = await page.screenshot({ type: 'jpeg', quality: 92 })
    if (!enc.stdin.write(jpg)) await new Promise(r => enc.stdin.once('drain', r))
    if (i % 150 === 0) console.log(`frame ${i}/${frames}`)
  }
  if (mode !== 'audio') {
    enc.stdin.end()
    await new Promise((res, rej) => enc.on('close', code => (code === 0 ? res() : rej(new Error('ffmpeg exit ' + code)))))
  }

  // 音轨：配音 + 背景音乐，混好后和画面合在一起
  const wav = out.replace(/\.mp4$/, '.audio.wav')
  mixdown(total, voices, wav)
  const mux = spawnSync(ffmpeg, ['-y', '-loglevel', 'error', '-i', silent, '-i', wav,
    '-map', '0:v', '-map', '1:a', '-c:v', 'copy', '-c:a', 'aac', '-b:a', '160k', '-t', String(total), '-movflags', '+faststart', out], { stdio: 'inherit' })
  if (mux.status !== 0) throw new Error('ffmpeg mux failed')
  rmSync(wav)  // 画面（.video.mp4）留着，改配音时用 audio 模式不用重新渲染
  console.log('wrote', out)
}
await browser.close()
