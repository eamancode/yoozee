# M4.1 线上修复方案：让 yz-chapter-shots 在柚子线上真正生效

依据：2026-10-22 线上实测（`111.229.198.225`，后端 v1.6.6，提交 `15d981a9`），报告在 `.local/yozi-ab/验证报告.md`。本方案只修实测暴露的问题，按依赖排成三期，每期单独发版、单独线上复测。`DESIGN-M4-柚子接入.md` 里的故事 / 剧本页、一次到底状态机、批量投递不在本方案内。

## 0. 问题与根因（都能回溯到代码）

| # | 线上现象 | 根因 |
| --- | --- | --- |
| 1 | 项目选了画风，提示词里却没有画风、角色外观、资产清单 | 线上是 `origin/main`。注入这三段的 `21631944`、`793dc6e8`、`4d4e7f02` 只在 `fix/shot-skill-context-budget` 分支 |
| 2 | 29 个镜头各成一段（S01～S29，4～5 秒），出视频会是 29 条短片 | 按场装段的 `dc2ec388`（`chapter-segments.ts`）也只在分支上。线上前端把每个分镜行直接存成一个镜头 |
| 3 | 镜头表字段（情绪弧、灯光七项、站位、衔接）全部丢失 | 保存链路只认平铺字段：`storyboardRowsToProjectShots` 只取剧情、台词、景别、机位等；分支上的 TS 装段 `storyboardRowsToSegments` 也只把景别、机位、正文、台词、声音、结尾状态写进段 |
| 4 | 即使把字段塞进段也存不进去、用不上 | Go `drama.Segment` / `SubShot` / `Line` 是 1.0 形状，`ParseSegment` 开了 `DisallowUnknownFields`，2.1 字段会被整段拒绝；出视频时 `drama.Compile` 也是 1.0 编译器 |
| 5 | 门禁不在线上 | 镜头表检查只有本机 `check.py`；模型写错（如空情绪弧）会被原样保存 |
| 6 | 片长偏长、锚点靠猜 | 提示词里没有每集目标时长；场景资产没有锚点字段 |
| 7 | 截断不可见 | 文本任务不记录 `finish_reason`，截断后只表现为「分镜任务没有返回镜头行」 |
| 8 | 用户要自己上传技能 | `yz-chapter-shots` 还没进内置技能和短剧预设，选择器也不会默认选中 |

实测里没出问题的部分不改：技能注入（四个文件全进）、deepseek-v4.1-flash 一次写完 5.7 万字符、模型输出过门禁 0 error。

## 1. 第一期：发版追平（S，1～2 天）

目标：线上有画风 / 角色 / 资产注入，按场装段，技能内置可选。字段丢失本期不修。

1. **分支落到 main。** 分支上的 1.0 提交（`6b2eb6b0`、`311dbf2a`）在 main 上已有压缩版（`1929d1a5`、`640cb6c1`、`640dc9aa`、`bb6125b7`），`git cherry` 显示 patch 不等价。做法：从 `origin/main` 新开分支，先对比 `036599e4` 是否已含在压缩版里，再按顺序 cherry-pick 从 `9493e246` 到 `4d4e7f02` 的 10 个提交，冲突以分支版本为准。本地未提交的 yoozee 2.x 技能包、`yz-chapter-shots` 和文档改动同期整理成提交。搬完跑 `go test ./internal/drama/... ./internal/handler/...` 和 `bun run build`、`bun test test/*drama* test/*segment*`。
2. **内置技能。** `backend/builtin/yz-chapter-shots/` 随本期提交（含三张卡）；`backend/internal/skills/seed/presets.json` 的 `short-drama-starter`、`short-drama-pilot` 加上它；`chapters.tsx` 生成分镜的选择器在短剧项目里默认选中它（沿用 `useRememberedSkillSelection`，用户改过就记用户的）。
3. **截断可见。** `provider_text.go` 各协议解析时取 `finish_reason` / `stop_reason`，等于 `length` / `max_tokens` 时任务失败，`reason` 写 `text_output_truncated`，`error-codes.ts` 与 `http-api.mdx` 同步。前端提示「输出超过模型上限，换长输出模型或按场生成」。
4. **发版。** 走 `publish-images.yml` 出镜像，`docker-compose.deploy.yml` 按 digest 更新 backend 与 web。

验收（线上，同一项目新建一章）：提示词里有【项目画风】【已确认角色外观】【项目已确认资产】；分镜制作页是 5～6 段而不是 29 段；技能在选择器里默认出现；人为把 `maxOutputTokens` 调小能看到截断提示。

## 2. 第二期：镜头表落库 + Go 装段 / 检查 / 编译 2.1（L，2～3 周）

目标：模型写的镜头表原样存下来，后端过门禁、装段、编译成 2.1 提示词。这是让 yoozee 在线上有效果的核心。

### 2.1 数据

- **每章一份镜头表。** 新表 `drama_shotlists`：`project_id`、`unit_id`（唯一）、`version`、`shotlist_json`（契约 shotlist）、`script_json`（由章节正文转出的台词表）、`check_json`（最近一次检查结果）、`created_by`、时间戳。镜头表是段的来源，段是派生物；用户改镜头表就重装段。
- **段继续用 `Shot` + `ShotRevision.SpecJSON`**，写 2.1 段（`contracts/segment.schema.json`），`shot_ids` 回指镜头表。旧项目没有镜头表的章节照旧走平铺字段，不做兼容层。
- `backend-database.mdx` 同步新表。

### 2.2 后端 Go（`backend/internal/drama/`，与 Python 共用夹具做黄金对照）

| 新文件 | 移植自 | 范围 | 黄金 |
| --- | --- | --- | --- |
| `vocab.go` | `common.vocab` | 从内置包读情绪弧 / 表情 / 动作模式 / 武术 / 特效 JSONL | 计数 32 / 101 / 22 / 120 / 108 |
| `script.go` | `script.py from-md` | 章节正文 → 场次与台词 id（`E01S01L01`），说话人 + 场号消歧同文台词 | 七床 E01 → 3 场 16 句 |
| `rows.go` | `.local/yozi-ab/run/rows_to_shotlist.py` | 分镜行 + `spec` → 镜头表；台词按原文对回 id；无 `spec` 的行用平铺字段兜底 | 线上实测输出 → 29 镜、16 句全对上 |
| `check.go` | `check.py` 的 shots 部分 | R-SHOT-001～030 与 R-SCHEMA（error 级全做，warn 级先做 007/008/009/012/013/021/024/027） | `tests/fixtures/check-bad-*.json` 命中规则；`tests/sample` 与七床 0 error |
| `pack.go` | `pack.py` | 场分组、估时、切点、桥、弧展开、动作模式、灯光七项、站位 / 锚点 / 运动方向句、cast、screen_text、jimeng 块 | `tests/sample/剧本/分段.json` 逐字一致 |
| `segment.go`（扩） | `segment.schema.json` 2.1 | `Segment` 加 cast、screen_text、jimeng、shot_ids、profile、reanchor、density、handles、warnings、continuity_bridge 等；`SubShot` 加 expression、positions、anchors、motion、light_full、transition、start_state 等；`Line` 加 line_id、timbre、pace、tone、emotion、placement。仍然 `DisallowUnknownFields`，只是认识的字段变全 | 现有 `TestCompileMatchesSkillPackageGoldens` 不变 |
| `compile.go`（扩） | `compile.py` 2.1 | 台词四元、人物声明、登记文字 ⚠️、群像 ⚠️、风险警告、声床按条、软预算 | `tests/expected/seg25-*` |

`pack.go` 和 `check.go` 最大（Python 合计约 1,700 行），先做，二者都是纯函数，不碰路由。

### 2.3 接口

`PUT /api/projects/:id/units/:unitId/drama/shotlist`，请求体是分镜行数组（带或不带 `spec`）和段长规则。服务端依次：

1. 章节正文转台词表（`script.go`）；
2. 分镜行转镜头表（`rows.go`）；
3. 检查（`check.go`）。有 error 不落库，返回 `reason: shotlist_check_failed` 和规则项；warn 随结果返回；
4. 装段（`pack.go`），按 `PlanSegmentOrder` 的语义替换本章的段；
5. 写 `drama_shotlists` 与段版本，返回段表、检查结果、装段说明。

`GET` 同路径取镜头表和检查结果。`http-api.mdx` 与 OpenAPI 同步。Agent 工具加 `drama_shotlist_read/write`，同一套服务端逻辑。

### 2.4 前端

- `chapters.tsx` 的 `storeGeneratedStoryboard`：分镜行带 `spec` 时调新接口，不再走 TS 装段；不带 `spec` 的（用户选了别的分镜技能）继续走现有 TS 装段。
- 检查不过时，弹窗列出规则项和对应镜头，给「重新生成」与「仍按平铺字段保存」两个按钮。
- 分镜制作页段详情加只读区：表情链、站位、灯光、衔接（直接读段 `spec`）。

验收（线上）：同一章重新生成后，段数与本机 `pack.py` 一致；段版本的 `specJson` 带 2.1 字段；在分镜制作页点生成时提交的视频提示词与本机 `compile.py --paste` 逐字一致；故意改坏一镜（空情绪弧 id）保存被拒并指出镜头。

## 3. 第三期：提示词补全与交付（M，1 周）

1. **每集时长。** 项目设置加「每集时长（秒）」，存进 `brief_json.duration`；生成分镜提示词加一句「本集目标 N 秒，镜头 est 合计不超过 N×1.1」；`check.go` 的 R-SEG-008 读它。
2. **场景锚点。** 场景资产的定义加 `anchors`（字符串数组），资产页可编辑，提取场景时由模型填；`chapterStoryboardAssetPrompt` 在场景行后带「锚点：A、B、C」；`check.go` 的 R-SHOT-012 用它当空间图。
3. **风格档案。** 柚子画风 → yoozee 档案的对照表放在 `kb-dialects` 旁（悬疑犯罪 / 都市 / 古装真人 → live-realism，3D 系 → anime-3d…），检查和编译按档案取配额与光线政策；项目设置可手动改。
4. **分镜脚本下载。** `storyboard.go` 移植 `storyboard_md.py`，分镜制作页加「下载即梦分镜脚本」，黄金 `tests/expected/storyboard-sample-E01.md`。
5. **按场生成（兜底）。** 截断或章节正文超过阈值时，前端按场景标题切开，每场一个任务，合并分镜行后走同一接口。阈值先按实测定：deepseek-v4.1-flash 一章 3 场 5.7 万字符没截断。

## 4. 风险与取舍

- **分支与 main 分叉。** 第一期 cherry-pick 冲突主要在 `project_drama.go` 和项目页，1.0 部分以 main 为底、后续提交为准。
- **Go 移植量大。** 约 2,600 行 Python，黄金夹具已齐。先 error 级规则 + 装段 + 编译，warn 级规则可分批。
- **排队约 3 分钟。** 实测生成前排队 3 分钟（`startedAt` 比创建晚 3 分钟），不属于 yoozee，但影响体验，建议另查 worker 并发与队列积压。
- **只测了 deepseek-v4.1-flash。** gpt-6-sol、claude-opus-5-5 各跑一次，记录输出长度与截断。

## 5. 每期都要做的事

- 文档：`backend-database.mdx`、`http-api.mdx`、`code-map.mdx`、`features.mdx`，已实现未确认的写 `pending-test.mdx`。
- 线上复测：沿用「yoozee 实测·七床」项目新建章节，按 `.local/yozi-ab/run/` 的脚本拿任务结果跑 `check.py` 对照。
- 测试数据：线上账号里的私有技能「yoozee · 章节分镜（镜头表）」在第一期内置后删除，避免选择器里出现两个同名技能。
