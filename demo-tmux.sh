#!/bin/bash
# 在独立的 tmux 服务器（-L trail-demo）里搭一个完整演示：
# 屏幕上是模拟的 AI CLI 历史输出，按 Ctrl-b 再按 t 弹出 trail 面板。
# 使用独立 socket，不会改动你当前终端所在 tmux 的任何配置；在 tmux 里运行也可以。
DIR="$(cd "$(dirname "$0")" && pwd)"
T="tmux -L ${TRAIL_DEMO_SOCKET:-trail-demo} -f /dev/null"

$T kill-server 2>/dev/null
$T new-session -d -s demo -x "$(tput cols)" -y "$(tput lines)" "$DIR/fake_history.sh"
$T set -g prefix C-b
$T set -g mouse on
$T set -g status-style "bg=#161b22,fg=#9da7b3"
$T set -g status-left " trail 演示 "
$T set -g status-left-length 20
$T set -g status-right "#[fg=#f0a97a,bold] ▶ 点这里或按 Ctrl-b t 打开面板 #[default]· Ctrl-b d 退出 "
$T set -g status-right-length 60
# 不传 --pane：trail 在弹窗里会自动找到下方的 pane（tmux 3.3 的弹窗命令不展开 #{pane_id}）
POPUP="display-popup -E -w 92% -h 88% $DIR/trail --demo"
$T bind-key t $POPUP
$T bind-key -n MouseDown1StatusRight $POPUP

TMUX= $T attach -t demo
$T kill-server 2>/dev/null
