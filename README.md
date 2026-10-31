# trail · AI CLI 对话索引

给 Claude Code 和 Codex 的对话做一个索引面板：每一轮列出提问和一句话总结（由 haiku 生成），
可以按方案、改动、提问、排错筛选，也可以搜索。选中某一轮，终端会跳回这一轮在对话里的原始位置。

分成两部分：

| | Claude Code | Codex |
|---|---|---|
| 形式 | 原生插件，`claude-plugin/` | 独立终端面板 `trail`（Go） |
| 打开方式 | 输入 `/trail [搜索词]` | 用 `trail codex` 启动，按 `Ctrl-]` 弹出 |
| 跳转 | Claude Code 自己把对话滚到那一轮 | tmux copy-mode 搜索提问文字 |
| 总结 | 插件里调用 haiku | 调用本机的 `claude -p --model haiku` |

运行环境：先支持 macOS，Linux 也能用。

- Claude Code 插件：Claude Code 2.1.275 及以上可以一步安装；已在 2.1.312 上测试。
- Codex 面板：tmux 3.2 及以上（需要 `display-popup`，安装脚本会自动装）；已在 Codex 0.165 上测试。
  自己编译需要 Go 1.24 及以上。

## Claude Code 插件

### 安装

在 Claude Code 里输入：

```
/plugin install trail --marketplace devilcoolyue/trail
```

也可以在终端里分两步：

```bash
claude plugin marketplace add devilcoolyue/trail
claude plugin install trail@trail
```

### 本地开发

```bash
# 只对这一次启动生效
claude --plugin-dir /path/to/trail/claude-plugin
```

改了代码以后在 Claude Code 里执行 `/reload-plugins` 就能生效。

### 使用

- `/trail` 打开面板。终端宽度至少 110 列时，面板停靠在对话右侧，否则显示在输入框上方。
- `/trail 关键词` 打开面板并搜索。
- 点击某一轮，或者用 Tab 选中后按 Enter，对话会滚到这一轮的提问处，这一轮也会展开显示用了哪些工具、改了哪些文件。
- `1`–`5` 按类型筛选；`Esc` 回到输入框。
- 面板开着的时候，每轮对话结束后索引会自动刷新，并总结新的一轮。
- 回退掉的分支（Esc Esc 或编辑后重发）显示为灰色加删除线，这些轮次已不在当前对话里，无法跳转。
- 斜杠命令里交给模型处理的（`/goal …`、自定义命令、技能）也算一轮，显示为 `/命令 参数`；`/clear`、`/compact` 这类不算。

跳转需要 Claude Code 的全屏界面：面板停靠在对话右侧，可以点击，回车能把对话滚回那一轮。
不是全屏时（比如在 tmux 里默认就不是），面板显示在输入框上方，终端不会把点击交给插件，Claude Code
也滚不动对话，所以：

- 用 `j` / `k` 上下选择，`1`–`5` 筛选；按 `Esc` 回到输入框后，`ctrl+x tab` 再进入面板。
- 选中的一轮在面板里展开总结和回答正文，代替跳转。
- 在 Claude Code 里输入 `/tui fullscreen` 切换到全屏（会重启并恢复当前会话），或者启动前设置 `CLAUDE_CODE_NO_FLICKER=1`。

压缩过的会话：同一次运行里压缩（`/compact` 或自动压缩）之后，前面的轮次照样能跳。但用 `--resume` / `--continue`
恢复的会话，Claude Code 只显示最后一次压缩之后的内容，`ctrl+o` 也只能看到压缩摘要，所以压缩前的轮次跳不过去。
这时按回车会提示原因，面板里照样展开这一轮的详情（提问、总结、工具、改动的文件）。

长会话的记录文件常有几十 MB，超过插件一次读文件的上限（4 MiB），这时插件改用 `cat` 流式读取。

### 设置

在 `/config` 里可以把「每轮总结」改为 `off`，这样只用规则提取，不调用模型。
总结结果按会话缓存，同一轮只总结一次。

## Codex 面板（也能浏览 Claude Code 会话）

Codex 的插件只能提供技能、MCP 和 hooks，没法往界面里加面板，所以 trail 借助 tmux：
面板是盖在 Codex 上的弹窗，跳转靠 tmux 在历史里搜索提问文字。tmux 由 `trail codex` 自动配置，不用自己学。

### 安装

一行命令（macOS / Linux），会下载对应系统的 trail、校验 SHA256 并放进 `PATH`；没有 tmux 时用 Homebrew 装上：

```bash
curl -fsSL https://raw.githubusercontent.com/devilcoolyue/trail/main/install.sh | sh
```

也可以从 [Releases](https://github.com/devilcoolyue/trail/releases) 手动下载，或者用 Go 安装：

```bash
go install github.com/devilcoolyue/trail@latest
```

浏览器下载的压缩包，macOS 第一次运行可能会拦截，用 `xattr -d com.apple.quarantine <trail 的路径>` 解除。

### 使用

在项目目录里用 `trail codex` 代替 `codex` 启动，参数照常传给 Codex（比如 `trail codex resume --last`）：

```bash
trail codex
```

对话时按 `Ctrl-]` 打开对话索引。选中一轮按回车，弹窗关闭，终端停在那一轮的提问处，下面紧跟回答；
看完按 `q` 回到 Codex 继续输入。滚轮可以翻看更早的对话。

`trail codex` 做了这些事：

- 在 trail 专用的 tmux（`tmux -L trail`）里运行 Codex，和你自己的 tmux 互不影响；
  会先读你的 `~/.tmux.conf`，prefix 等设置照旧，再加上 trail 需要的设置。
- 以 `--no-alt-screen` 启动 Codex：对话留在终端历史里，跳转才搜得到；历史保留 20 万行。
- 底部状态栏提示快捷键；关掉终端窗口时 Codex 一起结束，和直接运行 `codex` 一样。

已经在 tmux 里运行 `trail codex` 的话，Codex 就在当前 pane 里启动，`Ctrl-]` 绑到当前这个 tmux（不改配置文件）。

### 自己用 tmux

想直接在自己的 tmux 里运行 `codex`，运行一次：

```bash
trail setup
```

它会在 `~/.codex/config.toml` 的 `[tui]` 里加上 `alternate_screen = "never"`，在 `~/.tmux.conf` 末尾加上
`prefix + t` 和 `Ctrl-]` 两个快捷键（以及 `history-limit`），改动前先备份成 `.trail-bak`；tmux 正在运行时会让它重新读配置。
再运行一次不会重复添加。手动配置的话，`~/.tmux.conf` 里加一行：

```tmux
bind-key t display-popup -E -w 92% -h 88% /usr/local/bin/trail
```

`tmux source ~/.tmux.conf` 只能在 tmux 已经运行时用；没启动 tmux 时会报 `error connecting to ...`，直接运行 `tmux` 就会读配置。

在运行 Codex 的 tmux pane 里按快捷键，面板会自动选中这个 pane 里正在进行的会话（标着「● 当前终端」）。

`codex exec` 这类一次性的非交互调用（比如其他程序用它生成标题）不会列出来；Claude Code 的 `claude -p` 等 SDK 调用中，
只有一轮、也没用工具的会话同样不列。

其他会话可以按 `s` 浏览（Codex 和 Claude Code 混在一起，按时间分组），但不能跳转。
对这些会话按回车，会提示用 `codex resume <id>` 或 `claude --resume <id>` 恢复。

### 按键

| 按键 | 作用 |
|---|---|
| `↑↓` / `jk` / 鼠标滚轮 | 选择一轮对话 |
| `⏎` / 再次点击已选中项 | 跳到终端原位置 |
| `/` | 搜索提问、结论、文件路径 |
| `1-5` / `←→` | 按类型筛选：全部 / 方案 / 改动 / 提问 / 排错 |
| `space` | 展开或折叠思考过程 |
| `p` | 标记为关键节点（★） |
| `tab` | 在索引和详情之间切换焦点 |
| `s` | 会话列表与概览 |
| `[` `]` | 切换到上一个 / 下一个会话 |
| `q` / `esc` | 退出 |

### 参数和环境变量

- `-n 40`：最多读取最近几个会话（默认 40）
- `--demo`：使用内置的演示数据
- `TRAIL_SUMMARIES=off`：不调用模型总结。找不到 `claude` 命令时也会自动关闭。
- 总结结果缓存在 `~/Library/Caches/trail/summaries.json`（macOS）或 `~/.cache/trail/summaries.json`（Linux）。

### 演示

`./demo-tmux.sh` 会在一个独立的 tmux 里放一段模拟的对话历史，按 `Ctrl-b t` 打开演示面板，可以体验跳转。

## 代码结构

- `claude-plugin/`：Claude Code 插件
  - `hooks/register.tsx`：`/trail` 命令、面板绘制、跳转、每轮结束后刷新
  - `hooks/transcript.ts`：解析会话记录（.jsonl），区分当前分支和回退掉的分支
  - `hooks/summarize.ts`：haiku 总结的提示词和结果解析
  - `hooks/jump.ts`：跳转失败时给用户看的说明
  - `tests/`：`claude plugin test claude-plugin` 运行
- Go 面板
  - `codex.go` / `claude.go`：解析两种会话记录
  - `sessions.go`：查找最近的会话，识别 tmux pane 里正在运行的会话
  - `summarize.go`：调用 `claude -p --model haiku` 总结并缓存
  - `ui.go`：界面；`jump.go`：tmux 跳转；`demo.go`：演示数据
  - `launch.go`：`trail codex`；`setup.go`：`trail setup`；`install.sh`：一键安装
  - `go test ./...` 运行解析器测试；`TRAIL_REAL_CODEX=<目录>` 或 `TRAIL_REAL_CLAUDE=<目录> go test -run TestDumpReal -v` 打印一批真实会话的解析结果
