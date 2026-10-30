#!/bin/bash
# 模拟 Claude Code 的回滚输出：每一轮提问之后跟着大量工具调用和思考输出
python3 - <<'PY'
import sys
prompts = ["先读一下 internal/order 目录，说说现在下单流程是怎么走的",
 "给我设计一个幂等方案，要求网关重试、MQ 重投、用户双击都能扛住，先别写代码",
 "Redis 挂了怎么办？会不会整个下单不可用",
 "按方案实现吧，先做 Redis 预占这层"]
for i,p in enumerate(prompts):
    print(f"> {p}")
    for j in range(40):
        print(f"  ⏺ Read(internal/order/file_{i}_{j}.go)  ... noise line {j}")
PY
exec bash
