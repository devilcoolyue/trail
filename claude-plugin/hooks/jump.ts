import type { TrailTurn } from '../types'

/** What to tell the person when `$.ui.scroll` could not reach the turn. */
export function jumpFailure(t: TrailTurn, deny: string): string {
  // 恢复的会话只显示最后一次压缩之后的内容，压缩前的轮次在对话里找不到了
  if (t.isCompacted && /nothing drawn/.test(deny)) {
    return '这一轮在上次压缩（/compact）之前。恢复的会话只显示压缩之后的内容，跳不过去了；面板里已展开这一轮的详情'
  }
  if (/none scrolls|no-surface/.test(deny)) {
    return '没能跳转：当前界面不支持滚动对话。全屏模式下才能跳转（tmux 里启动前设置 CLAUDE_CODE_NO_FLICKER=1）'
  }
  if (/not person-initiated/.test(deny)) return '没能跳转：只能由你的点击或按键触发跳转'
  return `没能跳转：${deny}`
}
