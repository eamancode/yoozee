# M5 提示词在线迭代：流程定版之后只改提示词，不再发版

依据：`origin/main` 399e837f（M4.1 第二期已上线：`yz-chapter-shots` 生成带 spec 的分镜行 → 镜头表落库 → Go 检查 → 按 `spec.scene` 装段 → 2.1 编译）。团队 A/B 结论保持字段拼接架构，不改成整段写作。本方案让各阶段的输出质量只靠在线改提示词来迭代；代码只保留结构和解析接口。

`backend/builtin/kb-dialects/models.json` 与 `yoozee-skills/tests/expected/segment-*` 是共用文件，本方案不改。

## 0. 现状：哪些阶段改提示词要发版

| 阶段 | 提示词在哪 | 现在能不能在线改 | 产物记录了版本吗 |
| --- | --- | --- | --- |
| 提取资产、三视图、首帧图 | `backend/internal/prompts` 提示词模板 | 能：`/admin/prompt-templates` 新建、启用版本 | 能：任务记 `TemplateID / TemplateVersion` |
| 生成分段（章节 → 带 spec 的分镜行） | 内置技能 `backend/builtin/yz-chapter-shots`，`//go:embed` 打进程序 | 不能：`EnsureBuiltinSkills` 每次启动按内置包同步，`UpsertBuiltinSkills` 覆盖 `instruction`，哈希一变就新建版本并切过去 | 部分：任务 `metadata.skillVersions` 取的是技能列表里的 `versionId`，不是实际读到文件的那个版本 |
| 装段与视频提示词（镜头表 → 段 → 即梦 / H3 提示词） | 句式写在 `internal/drama/pack.go`（镜头画面句、表情链、灯光七项、续段再锚点）和 `compile.go`（参考声明、台词四元、人物声明、⚠️、约束）；风格块在 `yz-producer/profiles`、`kb-dialects/blocks`；词库在 `kb-*/vocab` | 不能 | 没有 |
| 输出契约（`storyboardRowsOutputContract`、`outputFields`） | 前端代码 | 不改：这是解析器接口，留在代码里 | — |

结论：要做在线迭代的是第二、三行。第一行已经具备，作为本方案的样板（版本号、单一启用、启用中不能改）。

## 1. 内置技能在线覆盖（PR-A）

### 1.1 数据

- `skill_versions` 加三列：
  - `origin`（≤16）：`builtin` 内置包同步出的版本；`online` 后台在线编辑出的版本；空串是其他来源（上传、GitHub、单文件 Markdown），语义不变。
  - `note`（≤500）：这一版改了什么，后台填写。
  - `created_by`（≤36）：在线版本的管理员 ID，内置版本为空。
- `skills` 加一列 `builtin_version_id`（≤36）：最近一次从内置包同步出的版本。只有内置技能有值。
- 运行时仍只读 `skills.current_version_id`（`loadSkillPackageSnapshot`），不加第二条读取路径。在线覆盖生效 ⇔ `current_version_id` 指向 `origin = online` 的版本。
- 迁移 v60 `builtin_skill_online_versions`：加列；回填——内置技能（`source_type = builtin`）当前版本的 `origin` 写 `builtin`，`builtin_version_id` 写当前版本。

### 1.2 版本规则

- **在线编辑生成新版本。** 后台保存时以某个版本（默认当前生效版本）为底，覆盖改动的文本文件（可删除非入口文件），重新规范化打包，新建 `origin = online` 的不可变版本。版本号 `<内置版本号>+online.<n>`，n 为该技能在线版本的序号（如 `2.2.1+online.3`）。保存时可选「保存并启用」或「只保存」（只保存用于评测，不影响用户）。
- **校验。** 必须保留 `SKILL.md`；frontmatter 的 `name`、`description` 必须能解析；`metadata.skillId` 不能改；`metadata.outputFields` 改坏会让前端解析器拿不到 spec，所以保存时按前端同一规则解析，原来声明过的字段名一个都不能少。只接受 UTF-8 文本文件，单文件 ≤512 KB，整包沿用现有上限。
- **启动刷新不覆盖在线版本。** `EnsureBuiltinSkills` 改为比较内置包哈希与 `builtin_version_id` 那一版的哈希：
  - 相同：什么都不做（现在比较的是当前版本哈希，在线版本生效时每次启动都会误判成变化）。
  - 不同：新建 `origin = builtin` 的版本并更新 `builtin_version_id`；当前生效的不是在线版本时同时切到新内置版本、`instruction` 跟着换；在线版本生效时不切，只记日志「内置包已更新到 X，在线版本 Y 仍生效」，后台列表标出「内置包有更新」。
  - `UpsertBuiltinSkills` 不再覆盖 `instruction`（目录信息 `name`、`description`、排序等照旧由内置包决定）；`instruction` 只在切换生效版本时写成该版本的 `SKILL.md`。
- **启用与回退。** 后台可以把任一版本（在线或历史内置）设为生效；「回退到内置版本」= 生效版本改回 `builtin_version_id`。版本不可删除，任务记录里的版本 ID 始终能查到原文。
- **删除内置技能**沿用墓碑表，与在线版本无关。

### 1.3 实际使用的版本

- `GET /api/skills/:id/file` 的返回加 `versionId`、`version`（这次读到的文件属于哪个版本）。
- 前端 `skill-runtime.ts` 的 `skillVersions` 改用 `SKILL.md` 那次读取返回的 `versionId / version`，不再用技能列表里可能过期的值。任务 `metadata.skillVersions` 因此记的是实际用的版本；在线版本的版本号带 `+online.n`，一眼能分出来。
- 同一次准备里 `SKILL.md` 与引用文件分几次读，中途恰好切换版本的极小窗口不处理（文件 `sha256` 已经记在 `skillFiles` 里，可事后核对）。

### 1.4 接口（都要管理员）

| 方法与路径 | 作用 |
| --- | --- |
| `GET /api/admin/builtin-skills` | 内置技能列表：生效版本、内置版本、是否在线覆盖、内置包是否比生效版本新、在线版本数 |
| `GET /api/admin/builtin-skills/:id/versions` | 版本历史：`origin`、版本号、哈希、文件数、说明、作者、时间、是否生效、是否为当前内置版本 |
| `GET /api/admin/builtin-skills/:id/versions/:versionId/files` | 某版本的文件清单 |
| `GET /api/admin/builtin-skills/:id/versions/:versionId/file?path=` | 某版本某文件的文本内容 |
| `POST /api/admin/builtin-skills/:id/versions` | `{ baseVersionId?, files: [{path, content}], deletePaths?, note, activate }` 新建在线版本 |
| `POST /api/admin/builtin-skills/:id/activate` | `{ versionId }` 设为生效 |
| `POST /api/admin/builtin-skills/:id/revert` | 回退到内置版本 |

### 1.5 后台页面

`/admin/builtin-skills`「内置技能」：列表（搜索、只看在线覆盖）→ 抽屉里左侧版本历史（启用、回退到内置），右侧选文件编辑（沿用 `PromptCodeEditor`），底部填说明，「保存为新版本」「保存并启用」。

## 2. 编译档案数据化（PR-B）

### 2.1 抽什么、留什么

抽成「编译档案」（`drama.CompileProfile`，JSON）的是影响质量的文字：

- **镜头画面句**（`pack.go` `shotText`）：动作模式、表情、核心词、表演节拍、此刻状态、光源句、动作单元（发起 / 打向 / 对方 / 接触结果 / 结束在）、流派、特效（范围 / 持续）。
- **表情链**（`ExpandArc`）：时段写法、「台词落在这一段」、藏七露三收尾。
- **灯光七项**：八个标签（主光、辅光、轮廓光、光比、色温、阴影、曝光、光线变化）与分隔符。
- **段末与续段**：「本段结束时：」「视线：」、续段再锚点（有 / 无上一段结尾两种）。
- **人物声明**：`人物·{name}：{lock}`、取锁定卡条数（现为 6）。
- **台词四元**：音色、语速、语调、情感标签，画外音写法，三元不全时的 delivery 写法。
- **Seedance 编译**：参考声明（首帧、尾帧、人物、场景、机位背景、道具、构图、运镜、音色）、上一段末帧 / 末尾运镜说明、开头句「生成{dur}秒视频。整体风格：」、中段再锚点、声音句（共用声床、无声时的兜底、不要配乐）、登记文字与群像 ⚠️、约束句（人物一致的两种说法、可读文字的两种说法、非中文台词）。
- **H3 编译**：英文参考声明、首尾帧、音色说明、默认声床。
- **⚠️ 风险文案**：17 个 `risk_tags` 的文字。
- **风格块、表演块**：按方言（Seedance / H3）和按风格档案 ID（如 `live-realism`）覆盖；不写时取原来源（`kb-dialects/blocks`、`yz-producer/profiles` 的 `style_block / acting_block`）。
- **机位与运镜词表**（`cameraText`）：X/Y 角度、运镜、构图词、景深词。

留在代码里的是结构：分秒计算与 `secText`、字段先后顺序、参考编号（`@图片N / @视频N / @音频N / <Picture N>`）与上限截断、提交时长取整、字数预算与超限判定、装段切点算法、风险标签的去重与顺序、检查规则。

模板写法：`{name}` 占位符，代码用固定的变量表替换；不支持表达式。每个模板允许的占位符是固定的，出现未知占位符保存时就拒绝。可选片段（如光源方向）拆成单独的模板，由代码决定是否拼接，所以结构不会被文案改坏。

### 2.2 内置默认

`internal/drama/compile_profile_default.json`（`//go:embed`，版本号 0）逐字等于现在代码里的文字。`tests/expected` 和 `yoozee-skills` 黄金在默认档案下逐字不变，由现有 `TestCompileMatchesSkillPackageGoldens`、`TestCompile21MatchesSkillPackageGoldens`、`TestPackMatchesPythonSample`、`TestOnlineOutputEndToEnd` 保证；另加一条用例：默认档案序列化 → 反序列化 → 编译，结果与不带档案时逐字一致。

### 2.3 数据

- 新表 `drama_compile_profiles`：`id`、`version`（唯一，从 1 递增）、`name`、`content_json`、`note`、`enabled`（同一时刻最多一个）、`created_by`、时间戳。
- `content_json` 只需写要改的项：读取时先取内置默认，再把存的 JSON 叠上去（对象逐键合并）。以后代码新增的文字项，旧版本自动取默认，不用迁移数据。
- `drama_shotlists` 加两列：`rows_json`（这次写入的分镜行原文，评测重放用）、`compile_profile_version`（装段用的档案版本，0 = 内置默认）。
- 迁移 v61 `drama_compile_profiles`。

### 2.4 版本规则与回退

- 生效档案 = `enabled = true` 的那一版；没有就用内置默认（版本 0）。
- 新建版本默认不启用；启用一版会停用其他版本（一个事务）。启用中的版本不能删除；历史版本可删除（已被镜头表或编译结果引用的也只是记录了版本号，不靠外键）。
- 「回退到内置默认」= 停用全部版本。
- 装段（`PUT .../drama/shotlist`、Agent `drama_shotlist_write`）和编译（`POST .../shots/:id/compile`）都取生效档案；装段把版本号写进 `drama_shotlists.compile_profile_version`，编译结果返回 `compileProfile: { version, name }`。段 JSON（2.1 契约）不加字段，避免动黄金。
- 已装好的段里镜头画面句是装段时写死的：改了镜头画面句类的文字后，要「重新装段」（用已存镜头表重装，不调模型）才会进入段；编译期的文字（参考声明、约束、⚠️、风格块）下次编译就生效。后台在档案页说明这一点，重装入口见第 3 节。

### 2.5 接口（都要管理员）

| 方法与路径 | 作用 |
| --- | --- |
| `GET /api/admin/drama/compile-profiles` | 全部版本、生效版本号、内置默认全文（风格块已按来源文件填好，便于「基于默认新建」） |
| `POST /api/admin/drama/compile-profiles` | `{ name, content, note, enabled }` 新建版本（校验未知字段、未知占位符） |
| `POST /api/admin/drama/compile-profiles/:id/activate` | 启用这一版 |
| `POST /api/admin/drama/compile-profiles/revert` | 回退到内置默认 |
| `DELETE /api/admin/drama/compile-profiles/:id` | 删除历史版本 |

后台页面 `/admin/drama-compile-profiles`「编译档案」：版本列表（启用、删除、基于此版本新建）+ JSON 编辑抽屉。

## 3. 评测重跑（PR-C）

### 3.1 评测集与阶段

- 评测集：一组（项目, 章节）。先用「yoozee 实测·七床」第一章。后台直接填项目 ID，章节默认取有镜头表的第一章，也可多选。离线 CLI 用夹具目录（`rows.json`、`assets.json`、`chapter.md`），仓库里已有 `testdata/online-qichuang-on3`。
- 选「阶段 + 候选版本」，与基线版本（默认当前生效）各跑一遍，上游产物复用已存结果，只重跑该阶段和下游：

| 阶段 | 候选版本是 | 复用的上游 | 重跑 | 本期实现 |
| --- | --- | --- | --- | --- |
| 视频提示词 | 编译档案版本 | 已存镜头表（`rows_json`；老数据没有时用镜头表 + 段参考反推绑定） | 装段 → 编译，不调模型 | ✅ 后台 + CLI |
| 生成分段 | `yz-chapter-shots` 的某个版本 | 章节正文、资产 | 调文本模型 → 前端解析 → 检查 → 装段 → 编译 | 设计：需要在浏览器里跑解析器，放在后台「评测」页用候选版本发文本任务（技能上下文按版本 ID 读取），任务结束后把分镜行交给同一个评测接口 |
| 提取资产 / 三视图 / 首帧 | 提示词模板版本 | 章节正文 | 调模型 | 不做：图片质量没有自动指标 |

### 3.2 指标表

每个（版本, 章节）一行：

| 指标 | 来源 |
| --- | --- |
| 检查 error / warn | 镜头表检查（视频提示词阶段与版本无关，照抄；生成分段阶段重算） |
| spec 写出率 | 分镜行带 spec 的比例（`stats.rows - stats.no_spec`）/ `stats.rows` |
| 段数、总时长、段时长最短 / 最长 | 装段结果 |
| 字数：合计、平均、最长 | 编译后提示词字符数 |
| 截断 | 超过模型字数上限的段数（error）、超过软预算的段数（warn）；生成分段阶段另记文本任务 `text_output_truncated` |
| 编译 warn | 编译 issues 里的 warn 数（时长步长取整等） |

另附每段的候选 / 基线提示词全文，方便人工对比。

### 3.3 接口与 CLI

- `POST /api/admin/drama/eval` `{ stage: "compile", projectId, unitIds?, candidateVersion, baselineVersion?, model? }` → `{ run }`，`run.rows` 是指标表，`run.markdown` 是同一张表的 Markdown，`run.segments` 是逐段提示词。结果写入 `drama_eval_runs`（`id`、`stage`、`candidate`、`baseline`、`set_json`、`result_json`、`created_by`、`created_at`），`GET /api/admin/drama/eval-runs` 看历史。管理员可以评测任何项目（只读，不写段、不改镜头表）。
- CLI：`go run ./cmd/drama-eval -fixture internal/drama/testdata/online-qichuang-on3 -chapter internal/drama/testdata/online-qichuang/chapter.md -model seedance-2.5 -candidate profile.json [-baseline base.json]`，输出同一张 Markdown 表。
- 「重新装段」：评测满意、启用新档案后，章节页「用已存镜头表重新装段」（`POST /api/projects/:id/units/:unitId/drama/shotlist/repack`），不调模型。

## 4. 文档同步

- `backend-database.mdx`：`skills.builtin_version_id`，`skill_versions.origin / note / created_by`（v60）；`drama_compile_profiles`、`drama_eval_runs`，`drama_shotlists.rows_json / compile_profile_version`（v61）。
- `http-api.mdx`：上面三组管理员接口；`GET /skills/:id/file` 新字段；编译返回的 `compileProfile`；重新装段接口。
- `pending-test.mdx`：每个 PR 的自动化覆盖和待线上验收项。

## 5. 分 PR

| PR | 内容 | 基于 |
| --- | --- | --- |
| A | 本设计文档；内置技能在线覆盖（迁移 v60、技能域、管理员接口、后台页面、前端实际版本） | `origin/main` |
| B | 编译档案（默认 JSON、`pack.go / compile.go` 改读档案、迁移 v61、管理员接口、后台页面、装段 / 编译记版本） | A |
| C | 评测重跑（视频提示词阶段：后台接口、CLI、指标表）与重新装段 | B |

迁移版本号按 A → B 的顺序递增，所以 B、C 叠在前一个 PR 上开。都不部署。

## 6. 风险

- 在线版本把 `outputFields` 改坏会让整条链路拿不到 spec：保存时按前端同一规则校验，字段名只能增不能减。
- 编译档案改坏的后果是提示词变差而不是报错：上线前先用评测跑一遍，评测表和逐段提示词对比后再启用；回退是一键的。
- 内置包升级与在线版本并存时，在线版本会「压住」新内置版本：后台列表标「内置包有更新」，由人决定合并还是回退。
