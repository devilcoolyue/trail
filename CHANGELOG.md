# 更新记录

## v0.1.0 · 2026-10-30

第一个版本。

- Claude Code 插件：`/trail` 打开对话索引，按轮次列出提问和 haiku 一句话总结，可筛选、搜索，按回车把对话滚回那一轮。
  回退掉的分支单独标出；带参数的斜杠命令（`/goal …`、技能）算一轮；几十 MB 的长会话记录流式读取。
- Codex 面板（`trail` 命令）：在 tmux 弹窗里浏览 Codex 和 Claude Code 的会话，跳回 Codex 对话在终端里的原位置
  （需要 `[tui] alternate_screen = "never"`）。
- 非全屏界面（面板在输入框上方）：用 j/k 选择，选中的一轮在面板里展开回答，标题和按键说明始终可见，并提示用 `/tui fullscreen` 切换。
- 已知限制：用 `--resume` 恢复的 Claude Code 会话，最后一次压缩之前的轮次跳不过去（Claude Code 不再显示这些内容），
  面板里仍能看到这些轮次的详情。
