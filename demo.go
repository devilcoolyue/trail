package main

import "time"

// demoSessions 返回演示用的会话数据，结构和真实解析结果一致。
func demoSessions() []Session {
	now := time.Now()
	day := func(offset int, hh, mm int) time.Time {
		d := now.AddDate(0, 0, -offset)
		return time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, d.Location())
	}
	at := func(base time.Time, min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

	t0 := day(0, 10, 2)
	shop := Session{
		ID: "a3f9c1e2", Source: SourceClaude, Project: "~/code/shop-api", GitBranch: "feat/idempotency",
		Model: "opus-5.5", Title: "订单服务幂等与重构", Start: t0, Live: true,
		Turns: []Turn{
			{Time: at(t0, 0), Duration: 2 * time.Minute, Kind: KindAsk,
				Prompt:  "先读一下 internal/order 目录，说说现在下单流程是怎么走的，有哪些地方可能重复扣库存？",
				Summary: "梳理下单链路，定位 3 处重复扣减风险",
				Answer: "下单链路：`CreateOrder → reserveStock → createPayment → publishEvent`。\n\n" +
					"重复扣库存风险点：\n" +
					"1. 网关超时重试时 `reserveStock` 没有幂等键\n" +
					"2. MQ 消费者 `onPaid` 至少一次投递，未去重\n" +
					"3. 前端双击提交，两次请求几乎同时到达",
				ThinkTok: 1240, Thinking: "先找入口 handler，再顺着 service 往下看事务边界……",
				Tools: []ToolUse{{"Read", 7}, {"Grep", 3}}},
			{Time: at(t0, 4), Duration: 4 * time.Minute, Kind: KindPlan, Pinned: true,
				Prompt:  "给我设计一个幂等方案，要求网关重试、MQ 重投、用户双击都能扛住，先别写代码",
				Summary: "三层幂等：业务唯一键 + Redis 预占 + DB 唯一索引兜底",
				Answer: "推荐 **三层幂等** 方案：\n\n" +
					"1. **请求层**：客户端生成 `Idempotency-Key`，网关透传\n" +
					"2. **预占层**：`SET idem:{key} PENDING NX EX 300`，抢不到直接返回处理中\n" +
					"3. **兜底层**：`orders(user_id, idem_key)` 唯一索引，冲突即视为成功\n\n" +
					"MQ 消费侧用 `event_id` 走同一套预占逻辑。\n\n" +
					"```go\nok, _ := rdb.SetNX(ctx, \"idem:\"+key, \"PENDING\", 5*time.Minute).Result()\nif !ok {\n    return ErrInProgress\n}\n```",
				ThinkTok: 3820, Thinking: "对比了 token 表、分布式锁、状态机三种做法。token 表需要额外一次往返；分布式锁在 Redis 主从切换时可能失效，所以 DB 唯一索引必须保留作为最终兜底……",
				Tools: []ToolUse{{"Read", 2}}},
			{Time: at(t0, 9), Duration: 3 * time.Minute, Kind: KindAsk,
				Prompt:   "Redis 挂了怎么办？会不会整个下单不可用",
				Summary:  "Redis 故障时降级为仅 DB 唯一索引，不阻断下单",
				Answer:   "不会。预占层失败时 **降级跳过**，只依赖 DB 唯一索引；代价是并发重复请求会多打一次 DB，但结果仍然正确。建议加 `idem_redis_degraded` 指标告警。",
				ThinkTok: 860},
			{Time: at(t0, 13), Duration: 6 * time.Minute, Kind: KindChange,
				Prompt:   "按方案实现吧，先做 Redis 预占这层",
				Summary:  "新增 idem 包与中间件，接入 CreateOrder",
				Answer:   "已实现：\n- 新增 `internal/idem` 包，封装 `Acquire / Commit / Release`\n- `CreateOrder` 入口接入中间件\n- 单测覆盖并发双击场景（100 goroutine）",
				ThinkTok: 2100,
				Tools:    []ToolUse{{"Write", 2}, {"Edit", 3}, {"Bash", 2}},
				Files: []FileChange{
					{"A", "internal/idem/redis.go", 86, 0},
					{"A", "internal/idem/redis_test.go", 64, 0},
					{"M", "internal/order/handler.go", 12, 3},
				}},
			// 下面两轮后来被回退
			{Time: at(t0, 20), Duration: 5 * time.Minute, Kind: KindChange, Rewound: true,
				Prompt:   "DB 兜底用乐观锁 version 字段来做",
				Summary:  "尝试乐观锁兜底（已回退）",
				Answer:   "已给 `orders` 加 `version` 列，并在更新时带 `WHERE version = ?`……",
				ThinkTok: 1500,
				Tools:    []ToolUse{{"Edit", 4}, {"Bash", 1}},
				Files:    []FileChange{{"M", "internal/order/repo.go", 31, 9}, {"A", "migrations/0042_order_version.sql", 4, 0}}},
			{Time: at(t0, 26), Duration: 2 * time.Minute, Kind: KindDebug, Rewound: true,
				Prompt:   "测试挂了：duplicate key 还是能插进来",
				Summary:  "乐观锁无法阻止重复插入，放弃此分支",
				Answer:   "乐观锁只保护 **更新**，挡不住两次 **插入**。这条路走不通，建议回到唯一索引方案。",
				ThinkTok: 940, Tools: []ToolUse{{"Bash", 2}}},
			{Time: at(t0, 31), Duration: 4 * time.Minute, Kind: KindChange,
				Prompt:   "DB 兜底改用唯一索引 (user_id, idem_key)，冲突时查出已有订单返回",
				Summary:  "唯一索引兜底 + 冲突回查，迁移脚本 0042",
				Answer:   "已完成：\n- 迁移 `0042_order_idem_key.sql` 添加唯一索引\n- `repo.Insert` 捕获 `23505`，回查已有订单并返回\n- 测试全部通过（38 passed）",
				ThinkTok: 1720,
				Tools:    []ToolUse{{"Edit", 3}, {"Write", 1}, {"Bash", 3}},
				Files: []FileChange{
					{"A", "migrations/0042_order_idem_key.sql", 6, 0},
					{"M", "internal/order/repo.go", 24, 5},
					{"M", "internal/order/repo_test.go", 41, 0},
				}},
			{Time: at(t0, 38), Duration: 3 * time.Minute, Kind: KindPlan, Pinned: true,
				Prompt:   "MQ 消费侧怎么接？onPaid 里还有发券逻辑",
				Summary:  "消费侧以 event_id 预占，发券拆为独立幂等步骤",
				Answer:   "消费侧方案：\n1. `onPaid` 入口用 `event_id` 预占\n2. 发券拆成 `grantCoupon(order_id)`，自身也做幂等\n3. 失败只重试失败的步骤，不整体重放",
				ThinkTok: 2480, Tools: []ToolUse{{"Read", 3}}},
			{Time: at(t0, 43), Duration: 7 * time.Minute, Kind: KindChange,
				Prompt:   "实现消费侧，顺便把发券拆出来",
				Summary:  "onPaid 接入幂等，拆出 coupon.Grant",
				Answer:   "已实现，`onPaid` 现在分三步，每步独立幂等。新增 `coupon.Grant` 及测试。",
				ThinkTok: 1980,
				Tools:    []ToolUse{{"Read", 2}, {"Edit", 5}, {"Write", 1}, {"Bash", 2}},
				Files: []FileChange{
					{"M", "internal/consumer/paid.go", 58, 34},
					{"A", "internal/coupon/grant.go", 47, 0},
					{"M", "internal/coupon/grant_test.go", 39, 2},
				}},
			{Time: at(t0, 52), Duration: 3 * time.Minute, Kind: KindDebug,
				Prompt:   "CI 上 TestOnPaid_Concurrent 偶发失败，帮我看下",
				Summary:  "测试共享 Redis key 前缀导致串扰，改为随机前缀",
				Answer:   "原因：并行测试共用 `idem:` 前缀，互相抢占。改为每个测试用 `t.Name()` 作前缀后连续跑 200 次无失败。",
				ThinkTok: 1310,
				Tools:    []ToolUse{{"Bash", 4}, {"Edit", 1}},
				Files:    []FileChange{{"M", "internal/consumer/paid_test.go", 6, 2}}},
			{Time: at(t0, 57), Duration: 2 * time.Minute, Kind: KindAsk,
				Prompt:   "总结一下这次改动，写个 PR 描述",
				Summary:  "生成 PR 描述：三层幂等 + 消费侧拆分",
				Answer:   "## 订单幂等\n- 请求层 Idempotency-Key\n- Redis 预占（可降级）\n- DB 唯一索引兜底\n\n## 消费侧\n- event_id 预占，发券独立幂等",
				ThinkTok: 520},
		},
	}

	t1 := day(0, 8, 40)
	pay := Session{
		ID: "019a2b7c", Source: SourceCodex, Project: "~/code/shop-api", GitBranch: "fix/pay-callback",
		Model: "gpt-5.5-codex", Title: "支付回调验签失败排查", Start: t1,
		Turns: []Turn{
			{Time: at(t1, 0), Duration: 3 * time.Minute, Kind: KindDebug,
				Prompt:   "微信支付回调一直报验签失败，日志在 logs/pay.log",
				Summary:  "回调 body 被中间件提前读取，验签用到空 body",
				Answer:   "`RequestLogger` 中间件读取了 `r.Body` 但没有回填，验签时 body 为空。",
				ThinkTok: 1900, Tools: []ToolUse{{"shell", 5}}},
			{Time: at(t1, 5), Duration: 2 * time.Minute, Kind: KindChange,
				Prompt:  "修一下，日志里别打完整 body",
				Summary: "回填 body，并截断日志到 512B",
				Answer:  "已用 `io.TeeReader` 回填 body，日志只记录前 512 字节。",
				Tools:   []ToolUse{{"apply_patch", 2}, {"shell", 2}},
				Files:   []FileChange{{"M", "internal/middleware/logger.go", 14, 4}}},
			{Time: at(t1, 9), Duration: 1 * time.Minute, Kind: KindAsk,
				Prompt:  "还有别的中间件有同样问题吗",
				Summary: "另有 AuditLog 存在同样问题，已列出",
				Answer:  "`AuditLog` 也会读取 body，建议统一抽 `peekBody()` 工具函数。"},
		},
	}

	t2 := day(1, 21, 15)
	blog := Session{
		ID: "7d01e9aa", Source: SourceClaude, Project: "~/code/blog", GitBranch: "main",
		Model: "sonnet-5.5", Title: "Hexo 迁移到 Astro", Start: t2,
		Turns: []Turn{
			{Time: at(t2, 0), Kind: KindPlan, Duration: 3 * time.Minute, Pinned: true,
				Prompt: "我想把 Hexo 博客迁到 Astro，保留原有 URL，给个迁移计划", Summary: "四步迁移计划，用 redirects 保留旧链接",
				Answer: "1. 内容迁移：front-matter 字段映射\n2. 路由：`/:year/:month/:slug/` 保持一致\n3. 旧链接 `astro.config` redirects 兜底\n4. RSS / sitemap 对齐"},
			{Time: at(t2, 6), Kind: KindChange, Duration: 8 * time.Minute,
				Prompt: "写个脚本批量转换 front-matter", Summary: "新增 scripts/migrate-fm.ts，转换 214 篇",
				Answer: "脚本已完成，转换 214 篇，3 篇日期格式异常已列出。",
				Tools:  []ToolUse{{"Write", 1}, {"Bash", 3}}, Files: []FileChange{{"A", "scripts/migrate-fm.ts", 92, 0}}},
		},
	}

	t3 := day(3, 15, 30)
	infra := Session{
		ID: "019a11f0", Source: SourceCodex, Project: "~/code/infra", GitBranch: "main",
		Model: "gpt-5.5-codex", Title: "K8s HPA 扩缩容抖动", Start: t3,
		Turns: []Turn{
			{Time: at(t3, 0), Kind: KindDebug, Duration: 4 * time.Minute,
				Prompt: "order-svc 的 HPA 一直在 3 和 8 之间来回跳", Summary: "CPU 指标受 GC 尖刺影响，缺少缩容稳定窗口",
				Answer: "建议设置 `behavior.scaleDown.stabilizationWindowSeconds: 300`，并把目标利用率从 50% 调到 65%。"},
			{Time: at(t3, 7), Kind: KindChange, Duration: 2 * time.Minute,
				Prompt: "改一下 values.yaml", Summary: "调整 HPA behavior 与目标利用率",
				Tools: []ToolUse{{"apply_patch", 1}}, Files: []FileChange{{"M", "charts/order-svc/values.yaml", 9, 1}},
				Answer: "已修改，`helm diff` 输出如上。"},
		},
	}

	all := []Session{shop, pay, blog, infra}
	for si := range all {
		for ti := range all[si].Turns {
			t := &all[si].Turns[ti]
			t.Index = ti + 1
			t.Anchor = firstRunes(t.Prompt, 24)
		}
	}
	return all
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
