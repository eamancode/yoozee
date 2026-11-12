# 短剧新工作流：开发文档

> 产品方案见 `short-drama-new-workflow-v2.md`。本文是落地用的开发文档：每一页显示什么、数据从哪来、复用哪个组件、新写哪些文件、后端改什么、怎么验收。
>
> 分工：Claude 负责前后端开发和本地单元测试、类型检查、构建；Codex 桌面端负责测试、部署和线上模拟真实用户验收（第 12 节）。

## 1. 目标与边界

- 入口「短剧新工作流」，路径 `/drama-flow`，开关 `dramaFlowEnabled`（默认关）。这部分第一版已实现，保留。
- 五步：剧本、定妆、分镜、出片、成片。自动推进，只在要花钱和出问题时叫人，失败局部重试。
- 每一步把这一步的产物细节完整显示：第一层直接在页面上，第二层在右侧详情栏，第三层展开看。
- 和旧「短剧制作」同一批项目、同一套接口；旧入口不动。
- 只保留两类确认：
  - 出视频前的花费确认。
  - 会删掉已有生成产物（分镜图、预演、视频）的操作：服务端返回 `segments_discard_required` 时列出会删掉的段，再确认。
  - 其他确认一律去掉，改成按钮旁写明影响。

## 2. 核对结论：数据从哪来

| 要显示的 | 来源 | 结论 |
| --- | --- | --- |
| 五步状态、停在哪 | `GET /projects/:id/drama/progress` 的 `pipeline` | 已有 |
| 段级进度（脚本、参考、已出、候选、入片、超时长、问题数） | 同上 `segments`、`counts` | 已有 |
| 剧集列表、每集来源与替换影响 | `GET /projects/:id/drama/episodes` | 已有 |
| 每集场次表、色卡齐没齐 | `GET /projects/:id/units/:unitId/drama/scenes` | 已有 |
| 正文、单元字段 | `GET /projects/:id/units`、`/units/:unitId/workspace` | 已有 |
| 风格锚、主基调色卡、色彩脚本 | `GET /projects/:id/drama/bible` | 已有 |
| 需求文档六项 | `GET /projects/:id/drama/brief` | 已有 |
| 资产、出图状态、子卡、色卡资产 | `listProjectAssets` | 已有 |
| 镜头表（草稿 / 已装段、检查问题、小节原文） | `GET /projects/:id/units/:unitId/drama/shotlist` | 已有 |
| 段内镜头、绑定资产、编辑 | `GET/PUT /projects/:id/drama/segments` | 已有 |
| 完整视频提示词、实发参考图、字数 | `POST /projects/:id/shots/:shotId/compile` | 已有 |
| 候选、证据、选定 | workspace 的 `shotArtifacts`，`/drama/evidence`，`/artifacts/:id/pick` | 已有 |
| 出片状态与失败原因 | `GET /projects/:id/video-chains?unitId=` | 已有 |
| 每段实际花费 | workspace 的 `tasks`（`clientContext.shotId` + `billing`） | 已有 |
| 出片预估花费 | `POST /video-chains/preview` 的 `totalMicrocredits` 与每步 `quote` | 已有 |
| 余额 | `getWallet` | 已有 |
| 运行记录 | `listGenerationTasks(limit, { projectId })` | 已有 |
| 出片后改过的段 | 产物 `status = stale` | 已有 |
| 交付包 | `GET /projects/:id/drama/delivery` | 已有；不留导出记录，页面写明「按需生成」 |
| 列表卡片的进度、失败数、进行中数 | 每卡一次 `/drama/progress`，没有失败数和进行中数 | **缺，后端补（第 7 节）** |

另两项核对结果：

- **「重试」实际重做什么**：`RetryProjectVideoChain` 只对失败的链生效；签名没变且已成功的段保留原结果，失败的段改回等待；失败段之后还没出的段会接着出。所以单段出片（`scope=single`）的重试只重做这一段；整场连续出片的重试从失败那一段接着往后出。页面按这个写按钮文案。
- **暂停**：没有「暂停整条流水线」的接口。用现有「生成模式」切到「每步都问我」（`every_step`）实现：当前步做完就停，不再自动往下。页面叫「先停在这一步」，不新增接口。出片的停止仍用取消链。

## 3. 页面与路由

| 路由 | 页面 | 文件 |
| --- | --- | --- |
| `/drama-flow` | 项目展示页 | `web/src/pages/drama-flow/index.tsx` |
| `/drama-flow/new` | 新建页 | `create.tsx` |
| `/drama-flow/:projectId/:stage?` | 主页面；`stage` 为 `script / look / shots / video / cut`；查询参数 `ep`（集）、`item`（选中项）、`tab`（小节）、`panel`（`runs` / `info`） | `detail.tsx` |

`item` 选中后右侧详情栏打开；`tab` 记住每步的小节。刷新、分享链接都能回到同一处。

## 4. 主页面框架

```text
detail.tsx
├─ FlowHeader        项目名、画面类型、画风、生成模式、待办、运行记录、项目信息、更多
├─ StageStrip        五步 + 每步小步骤和数字
├─ main (grid)
│  ├─ EpisodeRail    每集：段数、已出、失败、待办；搜索；矩阵视图切换
│  ├─ StageXxx       当前步工作区（小节 tabs + 汇总行 + 产物）
│  └─ DetailPane     右侧详情栏：选中项的完整内容，可收起；窄屏改底部抽屉
├─ ActionBar         一句话 + 一个主按钮（沿用第一版规则）
├─ InboxPanel        待办（沿用第一版规则）
├─ RunsPanel         运行记录（AppDrawer）
└─ InfoPanel         项目信息（AppDrawer）
```

数据装配集中在 `detail.tsx`，按旧工作台 `projects/workbench/detail.tsx` 的方式拼出 `ProjectDetail`（`core + units + 当前集 workspace`），这样旧的 `ScriptAdaptControl`、`StyleLockControl`、`ScenePaletteControl`、`AssetsExtractControl` 可以直接用。

新增文件：

| 文件 | 内容 |
| --- | --- |
| `parts/detail-pane.tsx` | 详情栏外壳：标题、关闭、内容区、窄屏抽屉 |
| `parts/summary-row.tsx` | 汇总行：一组「标签 + 数字」，可带警示色 |
| `parts/stage-tabs.tsx` | 每步小节切换，写 `tab` 参数，`aria-pressed` |
| `parts/runs-panel.tsx` | 运行记录 |
| `parts/info-panel.tsx` | 项目信息：需求文档、生成模式、时长、模型、预算 |
| `parts/look-*.tsx` | 定妆五个小节 |
| `parts/shots-*.tsx` | 分镜的镜头表状态条、段卡外壳 |
| `parts/video-*.tsx` | 出片段卡、详情、队列条 |
| `model/stage-facts.ts` | 阶段条小步骤和数字、集列表数字的纯函数 |
| `model/costs.ts` | 每段实际花费、本集合计、积分格式化 |
| `model/runs.ts` | 任务 → 运行记录行（步骤名、范围、用时、积分、结果） |
| `hooks/use-flow-data.ts` | 主页面的查询与刷新（从 `detail.tsx` 拆出） |
| `hooks/use-storyboard-run.ts` | 生成分镜、只重写问题小节、重新装段（从旧章节页的同一套规则搬出） |

`model/*.ts` 都写 `bun test` 单元测试。

## 5. 各页实现

### 5.1 阶段条（`stage-strip.tsx` + `model/stage-facts.ts`）

每步下面一行小字：

| 步 | 小字 | 数据 |
| --- | --- | --- |
| 剧本 | `N 集` | episodes |
| 定妆 | `画风 ✓ · 色卡 a/b · 资产 x/y` | bible 风格锚、scene 色卡资产、资产出图状态 |
| 分镜 | `N 段 · 问题 k` | progress.counts |
| 出片 | `已出 x/y · 失败 k` | progress + video steps |
| 成片 | `入片 x/y` | progress.counts.inCut |

### 5.2 集列表（`episode-rail.tsx`）

- 每集：标题、`段 n · 出 x · 败 k`，有必须处理的事时标警示点。
- 顶部搜索（按标题）；切换「列表 / 矩阵」：矩阵每格一集，颜色按出片完成度，悬停写数字。
- 剧本、分镜、出片三步显示集列表；定妆和成片是全项目视角，不显示。

### 5.3 剧本（`stage-script.tsx`）

小节：剧集 | 原文对照 | 检查

- 汇总行：集数、总字数、预计时长（按需求文档每集时长）、来源章节数、有分镜的集数。
- 剧集小节：
  - 集卡列表（当前集高亮）：集号标题、字数、场次数、来源章节、是否已有分镜。
  - 当前集：场次表（`scenes`：场号、场次头、色卡是否齐），正文按段显示；铅笔就地改，保存走 `updateProjectUnit`。
  - 保存后若这一集已有分镜，正文上方提示「分镜对应改动前的剧本」，给「重做这一集分镜」（跳到分镜步并触发生成，走 5.5 的规则）。
- 原文对照：左剧本、右来源章节正文（`episodes.sources` 指向的章节单元）。
- 检查：剧本优化任务的检查提醒（任务 `resultJson` 的 outcome），列出并可定位到集。
- 操作区：复用 `ScriptAdaptControl`（改编 / 只调节奏、流式侧栏、续写、替换影响列表都在里面）。

### 5.4 定妆

小节：画风 | 色卡 | 角色 | 场景 | 道具 | 清单核对

- 画风（`look-style.tsx`）
  - 风格锚：`StyleBibleEditor`，改动先进草稿，「保存」走 `putDramaBible`。
  - 风格封面、来源、是否锁定：读 bible 与 `projectLockedStyle`。
  - 操作：`StyleLockControl`（分析、锁定、封面、保存到我的风格都在里面）。
- 色卡（`look-palettes.tsx`）
  - 主基调色卡：`ProjectPaletteCard`（master）。
  - 场次色卡：按集分组、每场一张 `ProjectPaletteCard`；场次已不存在、没合成上的组件里已标。
  - 色彩脚本：bible `color_script` 只读列表。
  - 操作：重新合成（`resynthesizeDramaPalettes`）、`ScenePaletteControl`。
- 角色 / 场景 / 道具（`look-assets.tsx`）
  - 小节头：共几个、已出、出图中、失败、没出；「重试全部失败的」（一次 `queueDramaAssetImages` 带全部失败 id）。
  - 卡片：`ProjectVisualAssetCard`；角色卡下面挂 `ProjectCharacterVariants`。
  - 卡片上补两行：失败原因、被几段引用（progress 段引用的资产统计）。
  - 点卡片：详情栏显示完整外观描述、别名、原文依据、出图提示词、版本、引用它的段（可跳分镜）。数据来自资产对象本身（`description`、`metadata`、`versions`）。
  - 提取：`AssetsExtractControl`（按当前集）。
- 清单核对：`DramaAssetChecklist`。

### 5.5 分镜

- 镜头表状态条（`shots-shotlist-bar.tsx`）：读 `getDramaShotlist`
  - 显示：已装段 / 草稿、小节数、检查 error 和 warn 数、技能与版本。
  - 草稿时：「只重写问题小节」「带着问题先装段」「整集重新生成」。
  - 已装段时：「重新装段」「整集重新生成」。
  - 动作都走 `hooks/use-storyboard-run.ts`：
    - 生成调 `generateChapterStoryboard`，装段调 `repackDramaShotlist`。
    - 服务端返回 `segments_discard_required` 时列出会删的段确认一次。
    - 整集重新生成且会删产物时，同样列出确认。
    - 进度用 `ChapterStoryboardRunPanel` 显示在抽屉里。
- 汇总行：段数、镜头数、总时长、单段上限、超时长段、没参考的段、问题数。
- 段卡：沿用 `DramaSegmentCard`（镜头、台词、绑定资产、拆合、移镜、逐张改都在里面），外面加段头标记：
  - 超时长、没参考、有问题、已出片、出片后改过（产物 stale）。
- 密度：完整（默认，展开全部段）/ 紧凑（只展开选中段）。选择存 `localStorage` 小配置。
- 进行中：状态条显示正在写，点开看流式。

### 5.6 出片

- 汇总行：段数、已出、生成中、排队、失败、没出；本集已花积分（tasks billing 合计）；余额（`getWallet`）；默认视频模型和画幅。
- 段卡（`video-segment-card.tsx`），一行三栏：
  - 左：选定候选的视频；没有时显示状态或失败原因全文。
  - 中：段编号标题、时长、镜头前两行摘要、参考图缩略图（段绑定资产）、首帧来源、等上一段的提示。
  - 右：候选数与是否选定、这一段实际花费；按钮：出这一段、重试、停止、看片。
- 详情栏（`video-detail.tsx`），选中一段时：
  - 完整提示词：调 `compileProjectShot`，显示提示词、字数 / 上限、实发参考图（`files`）、警告和问题。
  - 候选：`DramaTakeCompare` + `DramaTakeEvidencePanel`，可选定。
  - 记录：这一段的任务（状态、模型、时间、用时、积分、错误），可复制错误。
- 队列条（`video-queue-bar.tsx`）：本集排队、生成中、失败、完成，点一项定位。
- 批量：「重试全部失败的」（按链去重）、「停止出片」。
- 出片入口：仍是 `VideoChainPanel`（预览范围、参考图数、费用后提交），它就是唯一的花费确认。

### 5.7 成片

- 汇总行：段数、已入片、缺片、总时长。
- 时间线（`cut-timeline.tsx`）：每段一块，段编号、时长、是否入片、选定候选；缺的块可点去出片。
- 交付：`DramaDeliveryPanel`（检查、导出、下载交付包）。
- 看片反馈：把旧 `DramaFeedbackForm` 导出复用（只加 `export`，不改行为）。
- 精剪：「在旧版剪辑页打开」链接到 `/projects/:id/editor`。

### 5.8 运行记录（`runs-panel.tsx` + `model/runs.ts`）

- `listGenerationTasks(100, { projectId })`，有进行中的任务时 5 秒刷新。
- 每行：步骤名（按 `clientContext.chapterOperation`、`type`、`metadata.source` 映射成剧本优化 / 定画风 / 场次色卡 / 提取资产 / 生成分镜 / 出图 / 出视频）、范围（集或段）、模型、开始时间、用时、积分、状态。
- 失败行展开原因；进行中可取消（`cancelGenerationTask`）。

### 5.9 项目信息（`info-panel.tsx`）

- 需求文档六项及确认状态（`getDramaBrief`），「编辑」打开 `DramaBriefModal`。
- 生成模式：复用 `ProjectPipelinePanel` 的切换；切到「每步都问我」就是「先停在这一步」。
- 画面类型、画幅、每集时长、单段时长、文本 / 图片 / 视频模型（project 字段 + `useDramaModels`）。
- 已用积分：运行记录里已结算的合计。

### 5.10 项目展示页

- 卡片：封面、名称、简介、画面类型、画风、生成模式、五步状态条、集数、`出片 x/y`、失败数、待办数、卡在哪一句话、更新时间、归档标记。
- 进度改用批量接口（第 7 节）：进入视野的卡片 id 攒 150ms 一批请求，每批最多 50 个。
- 筛选：全部、进行中、等我处理、已完成、已归档。

### 5.11 新建页

- 沿用 `ProjectComposer flow`。贴入正文后显示：识别出几章、每章标题与字数、预计集数（`novel-parse`）。
- 画风来源、生成模式、每集时长、单段时长、画幅、各步模型全部显示（带默认值）。没配好的模型标出并给设置链接。
- 底部一句预告：「将改编成约 N 集，自动做到出视频前，出视频前会先给你看花费」。

## 6. 交互规则

1. 选中即详情：点任意产物，右侧详情栏显示；`Esc` 收起；URL 带 `item`。
2. 就地编辑：铅笔进入编辑；保存失败时改动留着，可以重试或放弃。
3. 失败原位：失败项在卡片上显示原因和重试，同时进待办。
4. 只两类确认：花费确认、会删掉产物的确认（见第 1 节）。
5. 不猜数据：读失败显示「没读到」和「再读一次」；不显示成 0。
6. 键盘：在段列表里 `J/K` 或上下键切换、`Enter` 打开详情、`Esc` 收起。
7. 明暗主题、`prefers-reduced-motion`、`:focus-visible` 遵循设计系统 token。

## 7. 后端改动

### 7.1 批量进度摘要

`POST /api/drama/project-summaries`

请求：

```json
{ "ids": ["p1", "p2"] }
```

- `ids` 去重后最多 50 个，空数组返回空结果。
- 每个 id 都按当前用户校验归属；不属于当前用户或不存在的 id 不报错，放进 `missing`。

响应 `data`：

```json
{
  "summaries": {
    "p1": {
      "pipeline": { "runMode": "until_video", "stages": [], "current": "video", "stop": null },
      "counts": { "segments": 12, "generated": 8, "inCut": 6, "overLimit": 0, "issues": 1 },
      "line": "……",
      "episodes": 3,
      "chapters": 10,
      "failedImages": 1,
      "failedVideos": 1,
      "runningVideos": 2
    }
  },
  "missing": []
}
```

实现：

- handler：`backend/internal/handler/project_drama_routes.go` 加路由，只解析 body 和调 service。
- service 别名：`internal/service/aliases_*.go` 导出新类型。
- app：`internal/app/project_drama_summary.go`
  - `DramaSummaries(userID, ids)`：逐个调用 `DramaProgress`。
  - 资产失败数：用 `dramaImageView` 统计 `failed`。
  - 出片数：读项目的连续生成链任务（`repo.ProjectVideoChains(userID, projectID, "")`），解码状态，每段只看最近一次（与前端 `latestVideoSteps` 同一口径），统计失败和进行中。
- 测试：`internal/app/project_drama_summary_test.go`（归属、上限、口径），`internal/handler/project_drama_routes_test.go` 加路由鉴权表项。
- 文档：`docs/content/docs/backend/http-api.mdx` 补这个接口；不改表。

### 7.2 不做的

- 不新增暂停接口（用生成模式）。
- 不新增导出记录（交付包按需生成）。

## 8. 前端 API 改动

- `web/src/services/api/project-drama.ts` 加 `getDramaSummaries(ids, signal)` 和类型 `DramaProjectSummary`。
- `drama-panels.tsx`：`DramaFeedbackForm` 加 `export`，不改行为。

## 9. 测试

本地由 Claude 跑：

- `cd web && bun test src/pages/drama-flow`（model 单测）
- `cd web && bunx tsc --noEmit -p .`、`bun run lint`、`bunx vite build`
- `cd backend && go test ./internal/app/ -run 'DramaSummar|Feature' && go test ./internal/handler/`

Codex 负责的测试、部署、验收见第 12 节。

## 10. 实施顺序

1. 后端批量摘要接口和测试；前端 API。
2. 主页面框架：`use-flow-data`、详情栏、小节切换、汇总行、阶段条和集列表数字。
3. 剧本、定妆。
4. 分镜（含 `use-storyboard-run`）、出片。
5. 成片、运行记录、项目信息、列表卡片、新建页。
6. 单测、类型检查、lint、构建；更新 `pending-test.mdx`。
7. 写 Codex 任务书。

### 实施记录

- 1–7 已完成。和上文的出入：
  - 分镜段卡的第一层用新页面自己的只读镜头表（镜头、台词、绑定资产），点开后详情栏里才挂原 `DramaSegmentCard`。原因是每张原卡打开时都会调一次提示词编译，整集全部展开会一次发出 N 个请求。
  - 定妆的资产卡是新页面自己画的，没有复用 `ProjectVisualAssetCard`。原因是那张卡依赖资产页的文件夹、移动、预览弹窗；换图用的是同一个 `AssetImageLibraryPicker`。
  - 新建页没有新增「识别出几章」的列表，沿用编辑框下方原有的识别结果一行，加上底部预告句。
- 列表批量摘要由 `model/summary-batcher.ts` 攒批，150ms 一批、每批最多 50 个。
- 读取状态统一由 `model/read-state.ts` 判断（读到 / 在读 / 没读到，带旧缓存的刷新失败也算没读到）。出片的生成中、排队、失败、还没出要进度和这一集的出片链都读到才写数字，在读写「…」，没读到写「没读到」并给重试；这时段卡不写「还没出」，「出这一段」先禁用，免得把正在出的段再提交一次。别的集的链没读到只影响那一集在集列表里的那一行。
- 列表「加载更多」用 `hooks/use-load-more-sentinel.ts` 的回调 ref：筛选切换时结果区会先换成加载态，哨兵重新挂载是新节点，节点一换就重新观察。
- 第四轮（线上验收后修复）：
  - 滚动定位的 effect 一律用带大括号的函数体。Chrome 157 起 `scrollIntoView` 返回 Promise，箭头函数隐式返回会被 React 当清理函数调用而崩溃；`test/effect-implicit-return.test.ts` 扫源码防回退。
  - 画风锁定状态由 `model/style-lock.ts` 判断，口径取 `projectLockedStyle().locked`（与旧工作台、后端「风格定了」一致），分「在读 / 没读到 / 没锁 / 只有风格锚 / 已锁定」五态。「生成分镜」的拦截改用共享的 `projectStyleMissing()`（旧章节页同一函数）。
  - 花费由 `costSummary` / `costSummaryText` 统一：先看读取状态，再区分空列表、全部有账单、全部没账单、部分缺账单。
  - 模型展示名由 `hooks/use-model-names.ts` 解析：打开时读一次模型目录，系统模型以目录为准，个人渠道看本地配置；只改显示，不改存的 ID。
  - 详情栏滚动区底部留出柚智浮层默认占位（240px），默认位置下栏内按钮不被盖住。
  - 真实路由回归：`test/fixtures/drama-flow-lifecycle.harness.tsx`（happy-dom，真实路由页）和 `scripts/drama-flow-chrome-e2e.mjs`（Chrome + CDP 拦接口），共用无模型夹具 `test/fixtures/drama-flow-api-fixture.ts`。

## 11. 风险

- 复用的旧组件依赖 `ProjectDetail`，只装了当前集的 workspace；跨集的信息（如资产被哪些段引用）改从 progress 读。
- 旧组件里的弹窗确认（如风格重新锁定）保留原样；本期不改旧组件行为。
- 列表批量接口对每个项目都算完整进度，项目多时有开销；靠「只请求进入视野的卡片」控制。

## 12. 交给 Codex：测试、部署、线上验收

任务书放 `.local/codex-tasks/drama-flow/brief.md`（仓库外），内容要点：

1. 拉取分支，跑第 9 节命令和 `go test ./...`、`bun run build`，失败只报告不改业务代码。
2. 部署前向用户确认；按现有发布流程部署，部署后确认 Schema 版本和健康检查。
3. 管理后台打开「短剧新工作流」开关（只对测试账号可见时按现有能力处理）。
4. 用户自己登录后，用真实账号走一遍：
   - 列表 → 新建 → 主页面 → 五步各小节；
   - 剧本就地改；定妆单张重试；分镜改一个镜头、拆段；
   - 出片先只预览花费不提交（真实出视频要用户另行批准）；
   - 成片检查与交付包下载；运行记录、项目信息、待办；
   - 明暗主题、1280 和 1024 宽度。
5. 每项截图并记录通过 / 不通过 / 现象，写 `.local/codex-tasks/drama-flow/report.md`。
6. 不读凭据文件，不输入密码，不推 main，不打 tag。
