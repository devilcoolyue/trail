# trail（Claude Code 插件）

输入 `/trail` 打开对话索引面板：按轮次列出这个会话里的每一次提问和 haiku 生成的一句话总结，
可以按方案、改动、提问、排错筛选，也可以用 `/trail 关键词` 搜索。点击某一轮（或 Tab 选中后按 Enter），
Claude Code 会把对话滚到这一轮的提问处，并展开显示这一轮用了哪些工具、改了哪些文件。

安装（Claude Code 2.1.275 及以上）：

```
/plugin install trail --marketplace devilcoolyue/trail
```

试用：`claude --plugin-dir <这个目录>`。更多说明见上一级目录的 README.md。

## English

trail adds a `/trail` command that opens an index of the current conversation beside the chat. Each
round is listed with the prompt you typed and a one-line summary written by Claude Haiku, so long
sessions can be scanned without wading through thinking, tool output and diffs. Filter rounds by kind
(plan, change, question, debugging) or search with `/trail <words>`. Pressing a round scrolls the
conversation back to that prompt and expands what it ran and which files it changed. Summaries are
cached per session; set the plugin's `summaries` option to `off` to use rule-based summaries and make
no model calls. Jumping needs the fullscreen interface, the default in recent Claude Code versions.
