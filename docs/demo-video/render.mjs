// 把 demo.html 逐帧渲染成视频。
//
// 准备（只需一次）：
//   mkdir -p /tmp/video-tools && cd /tmp/video-tools && npm init -y
//   npm i playwright @ffmpeg-installer/ffmpeg @fontsource/jetbrains-mono @fontsource/inter \
//     @fontsource/noto-sans-symbols @fontsource/noto-sans-symbols-2
//   PLAYWRIGHT_BROWSERS_PATH=/tmp/video-tools/browsers npx playwright install chromium-headless-shell
// 系统里还需要 Noto Sans CJK 字体（显示中文）。
//
// 用法：
//   node render.mjs preview 0 12 27.5     # 截这几个时刻的图到 preview/
//   node render.mjs video demo.mp4        # 渲染整段视频（30 fps）
import { spawn } from 'node:child_process'
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const TOOLS = process.env.TRAIL_VIDEO_TOOLS || '/tmp/video-tools'
process.env.PLAYWRIGHT_BROWSERS_PATH ||= join(TOOLS, 'browsers')
const { chromium } = await import(join(TOOLS, 'node_modules/playwright/index.mjs'))
const ffmpeg = join(TOOLS, 'node_modules/@ffmpeg-installer/linux-x64/ffmpeg')

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

const [mode = 'preview', ...args] = process.argv.slice(2)
const FPS = 30
const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1920, height: 1080 } })
await page.goto('file://' + join(here, 'demo.html'))
await page.evaluate(() => document.fonts.ready)
const frame = async t => {
  await page.evaluate(t => window.render(t), t)
  return page.screenshot({ type: 'jpeg', quality: 92 })
}

if (mode === 'preview') {
  mkdirSync(join(here, 'preview'), { recursive: true })
  for (const t of args.map(Number)) {
    await page.evaluate(t => window.render(t), t)
    await page.screenshot({ path: join(here, 'preview', `t${String(t).padStart(5, '0')}.png`) })
  }
} else {
  const out = args[0] || join(here, 'demo.mp4')
  const duration = await page.evaluate(() => window.DURATION)
  const enc = spawn(ffmpeg, ['-y', '-loglevel', 'error', '-f', 'image2pipe', '-framerate', String(FPS), '-c:v', 'mjpeg', '-i', '-',
    '-c:v', 'libx264', '-preset', 'slow', '-crf', '18', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', out], { stdio: ['pipe', 'inherit', 'inherit'] })
  const total = Math.round(duration * FPS)
  for (let i = 0; i < total; i++) {
    const jpg = await frame(i / FPS)
    if (!enc.stdin.write(jpg)) await new Promise(r => enc.stdin.once('drain', r))
    if (i % 150 === 0) console.log(`frame ${i}/${total}`)
  }
  enc.stdin.end()
  await new Promise((res, rej) => enc.on('close', code => (code === 0 ? res() : rej(new Error('ffmpeg exit ' + code)))))
  console.log('wrote', out)
}
await browser.close()
