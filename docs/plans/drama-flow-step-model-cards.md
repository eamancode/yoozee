# 需求说明：短剧新入口各步骤的模型卡

状态：已确认（2026-11-15：Q1、Q2 按默认；Q3 允许跨族；Q4 成片也加），已实现待测试 ｜ 规划与编码：Claude ｜ 测试：Codex 桌面端

## 1. 需求复述

在新入口 `/drama-flow` 里，剧本、定妆（画风与资产）、分镜、出片、成片五步页面顶部都有一排统计卡。用户希望在这排卡片的末尾加上「模型卡」：

1. 直接看到这一步要用哪个模型：剧本和分镜用文本模型，定妆用文本模型（定画风、色卡）和图片模型（资产出图），出片用视频模型。
2. 点卡片就能就地改模型，不必跑到设置或信息面板。
3. 默认是「上一次用的模型」，打开页面时不用每次重选。
4. 出片页已经有一张「视频模型 Seedance 2.5」卡，但只能看、不能改。这次把它做成可改，其余三步按同样的样式补上。

## 2. 现状（已核实代码）

| 项 | 事实 | 位置 |
|---|---|---|
| 统计卡组件 | `SummaryRow` 只接 `label / value / tone`，纯展示 | `web/src/pages/drama-flow/parts/layout.tsx:62` |
| 剧本 | 6 张卡，没有模型卡 | `parts/stage-script.tsx:86` |
| 定妆·画风 | 4 张卡（状态、来源、主基调色、场次色彩脚本），没有模型卡 | `parts/stage-look.tsx:131` |
| 分镜 | 7 张卡，没有模型卡 | `parts/stage-shots.tsx:134` |
| 出片 | 第 9 张是「视频模型」，只读，用 `useModelNames` 显示名字 | `parts/stage-video.tsx:149` |
| 「上一次模型」记录 | 已有 `useDramaModelPreferencesStore`：按 text / image / video 各记一个，按用户隔离存在 localStorage；短剧任务被受理时由 `rememberAcceptedDramaTask` 写入 | `web/src/stores/use-drama-model-preferences-store.ts` |
| 文本模型取值 | `useDramaModelConfig` → `preferredDramaModel`：上次 > gpt-6.1-sol > 全局默认 > 第一个可用 | `web/src/lib/drama-model-preferences.ts` |
| 文本模型使用点 | 「继续」按钮（剧本 → 定画风 → 色卡 → 资产提取的链）与分镜导演都取 `effectiveConfig.textModel`；后端链式步骤沿用上一步任务的文本模型 | `drama-flow/detail.tsx:227`、`hooks/use-storyboard-run.tsx:101`、`backend/internal/app/project_pipeline.go:594/657` |
| 图片模型取值 | `project.defaultImageModel` 优先，其次全局，**没用上次记录**，和文本、视频不一致 | `drama-flow/detail.tsx:188/415` |
| 视频模型取值 | `projectVideoModel`：上次（且与项目生成模型同一族）> 项目默认 > 同族第一个 > 全局 | `pages/projects/workbench/drama-model.tsx:126` |
| 现成下拉 | `ModelPicker preferenceScope="short-drama"`，旧工作台各步骤已在用；资产出图确认面板 `AssetImageRecovery` 内也有一个图片模型下拉 | `components/model-picker.tsx`、`workbench/detail/asset-image-recovery.tsx:39` |
| 信息面板 | 「制作参数」里只读显示视频模型、图片模型 | `parts/info-panel.tsx:76` |

结论：底层的「上次模型」记录和下拉组件都已具备，缺的是步骤页上的入口，以及图片模型的默认规则统一。

## 3. 功能需求

### FR1 模型卡组件

- 在 `SummaryRow` 末尾增加一种可交互的卡：标签写「文本模型」「图片模型」或「视频模型」，值写模型展示名（沿用 `useModelNames` 的状态文案：没配、正在读目录、目录没读到、已不可用）。
- 卡片上有铅笔图标，单击整张卡就能打开下拉，键盘可以聚焦并用 Enter 或空格打开，保留 `:focus-visible`。下拉复用 `ModelPicker`（`preferenceScope="short-drama"`，显示各选项报价）。
- 不可用的模型置灰并写明原因，规则与旧工作台同一步骤的下拉一致，例如个人渠道不能用于服务端无人值守执行、视频画幅给不出。
- 模型已不可用或没配时，卡片标红，点开即可重选。

### FR2 各步骤放哪些卡

| 步骤 | 模型卡 | 作用于 |
|---|---|---|
| 剧本 | 文本模型 | 拆剧本，以及从这里点「继续」发起的链 |
| 定妆·画风 | 文本模型、图片模型 | 文本：导演定画风、场次色卡；图片：资产出图、重新出图、出图确认面板的默认值 |
| 分镜 | 文本模型 | 分镜导演（SSS、本体论等）、返修 |
| 出片 | 视频模型（现有卡改为可改） | 出片、重新出、花费预览 |
| 成片 | 视频模型（与出片同一个值） | 时间线上点灰块去补出缺的段 |

定妆的色卡小节放文本模型卡；角色、场景、道具小节放图片模型卡，与画风小节的卡指向同一个值。

### FR3 默认值与记忆

- 默认就是「上一次用的模型」：
  - 文本、视频：沿用现有 `preferredDramaModel` / `projectVideoModel` 的顺序。
  - 图片：改为「上次 > 项目默认图片模型 > 全局默认 > 第一个可用」，与另外两类一致。
- 在卡上选中即写入上次记录（`remember`），不必等任务提交成功。任务受理时仍按现有逻辑再记一次。
- 记录按用户隔离，跨项目共用，与现状一致。不写回项目设置，也不改全局设置。
- 文本模型是三步共用一个，还是按步骤分开记，见第 7 节 Q1。

### FR4 生效范围

- 只影响之后的提交，已在排队或运行中的任务不变，续跑沿用原任务的模型。
- 改完后，同页的确认面板（资产出图确认、出片花费预览）默认值跟着变；信息面板「制作参数」同步显示。
- 视频模型可以换到项目「生成模型」以外的族（Q3）。换族后卡片标黄，出片页写明：所选模型属于哪一族、分镜是按原族规则装的段、这个模型一段最长几秒、超时长的段要回分镜重新装段。所选渠道时长与分段上限冲突时，同时显示冲突原因。上次用的视频模型只要还能用就直接作为默认，不再要求同族；旧工作台不变。

## 4. 非功能要求

- 样式沿用 `drama-flow-summary` 的卡片尺寸和 token，不新增全局 `.ant-*` 覆盖，明暗主题都要验证。
- 模型目录读不到时不能把模型当成不可用（沿用 `modelNameOf` 的三态）。
- 只改前端；FR3 图片默认规则的改动只影响新入口，旧工作台 `/projects` 的行为不变。如果第 7 节 Q1 选了按步骤分开记，后端链式续跑也要改（见 Q1）。

## 5. 验收标准（Codex 桌面端）

1. 五个步骤（含定妆的色卡和角色、场景、道具小节）的页面都出现对应模型卡，显示名称与实际提交时用的模型一致（以请求里的 `model` / `logicalModelId` 为准）。
2. 在卡上改模型后刷新页面，默认值仍是刚选的；换个项目打开，默认值也一样；换账号不串。
3. 改图片模型后，打开资产出图确认面板，默认值跟着变；改视频模型后，花费预览中的模型与报价跟着变。
4. 模型不可用或没配时卡片标红，点开重选后恢复正常；模型目录读失败时显示「没法确认」，不是「不可用」。
5. 运行中的任务不受影响；续跑沿用原模型。
6. 键盘可以操作（Tab 聚焦，Enter 打开），明暗主题都正常，窄屏下统计行换行不溢出。
7. `cd web && bun run build`、`bun run lint`，以及相关专项 `bun test` 都通过；旧工作台模型下拉的相关测试无回归。
8. 不产生视频花费：出片只验证到花费预览为止。

## 6. 分工

Claude：在功能分支实现并补单元测试，不提交。Codex 桌面端：测试、浏览器验收、回报问题清单。部署另行确认。

## 7. 待确认事项

| 编号 | 问题 | 默认方案（不回复即按此执行） |
|---|---|---|
| Q1 | 文本模型是三步（剧本、定画风/色卡、分镜）共用一个，还是按步骤各记各的？ | **共用一个**：任一步改了，三步一起变。只改前端，「继续」链在后端沿用同一个模型，行为可以预期。若要按步骤分开记，需要让后端链式续跑在跨步骤时改用下一步的模型，另计约 1 天工作量 |
| Q2 | 图片模型的默认值改为「上次优先」，覆盖项目设置里的默认图片模型，可以吗？ | 可以，只在新入口生效；项目设置里的值仍作为没有上次记录时的回退 |
| Q3 | 是否允许在出片页跨模型族换视频模型？ | **已定：允许**，换族后提示分段规则与超时长 |
| Q4 | 「成片」以及其他没标红的统计行是否也加？ | **已定：成片也加**（视频模型） |

## 8. 实现记录

| 文件 | 内容 |
|---|---|
| `web/src/pages/drama-flow/model/step-models.ts` | 新增：`flowImageModel`、`flowVideoModel`、`videoFamilyMismatch` |
| `web/src/pages/drama-flow/parts/model-card.tsx` | 新增：`modelSummaryItem`，汇总行里的模型下拉，选中即 `remember` |
| `web/src/pages/drama-flow/parts/layout.tsx` | `SummaryItem` 加 `model` 标记（`data-model`） |
| `web/src/pages/drama-flow/hooks/use-flow-data.ts` | 统一给出 `textModel`、`imageModel`、`videoModel`、`videoOffFamily` |
| `detail.tsx`、`parts/info-panel.tsx` | 出图、重新出图、「继续」、出图确认面板、信息面板改用统一的 `imageModel` |
| `parts/stage-script.tsx`、`stage-look.tsx`、`look-assets.tsx`、`stage-shots.tsx`、`stage-video.tsx`、`stage-cut.tsx` | 各步骤接入模型卡；出片页加跨族与时长冲突提示 |
| `drama-flow.css` | 模型卡的局部样式（components 层，仅限 `.drama-flow-summary` 内） |
| `web/test/drama-flow-step-models.test.ts` | 取值规则单测 |

## 9. 第一轮验收修复（对应 `step-model-cards-review.md`）

| 问题 | 修法 |
|---|---|
| P1-01 没按这一步能不能用来置灰 | 从 `DramaModelPicker` 抽出 `useDramaModelAvailability`（个人渠道、平台短剧目录、视频画幅 / 分辨率），两边共用同一套判断。模型卡下拉用它置灰并写明原因；默认值和提交只取能用的，原选择不能用时回退到能用的，并在卡上写明改用了谁 |
| P1-02 目录状态被覆盖 | `use-flow-data` 按文本、图片、视频分别给出 `StepModel`：原选择 `chosen`、实际提交 `effective`、状态 `status`。状态建在 `modelNameOf` 之上，顺序是：目录确认下架、目录在读或没读到、这一步不能用、改用了别的。信息面板和模型卡读同一份；D-03 原断言不动 |
| P2-01 整卡不可点、没有铅笔、读屏名称失真 | 卡里只有一个触发器：按钮用 `::after` 铺满整张卡，内容是状态文字加铅笔图标，辅助名称为「更换图片模型，当前：…」。不嵌套按钮，保留 Enter、Space、方向键，聚焦时整卡显示焦点环 |

配套改动：
- `ModelPicker` 新增三个可选参数 `triggerContent`、`triggerAriaLabel`、`showPreferenceNotice`，不传时行为不变。
- 「继续」和分镜导演的提交改用与卡片同一个 `data.textModel`。
- 测试夹具新增 `GET /dramas/models`，按夹具自己的系统目录列出可用模型（新依赖登记，不放宽任何断言）。
- 单测补了可用性过滤和状态顺序。

## 10. 发布复验修复

- 新入口的剧本优化、全剧立项、画风、色卡和资产提取共用步骤卡的文本模型与渠道校验。弹窗换模会同步到步骤卡。已有任务的恢复按检查点复核原模型。
- 资产出图确认面板会同步图片选择、展示停用原因，并在入队前复核渠道。纯文本步骤可以在没有可用图片模型时继续，请求中省略图片任务模板。
- 名称目录或短剧能力目录读取失败时，步骤卡提示原因并阻止未经确认的模型进入生成或报价。刷新恢复后重新计算选择。
- 模型卡的标签、名称、铅笔和留白位于同一个原生按钮内。键盘可用 Tab 聚焦、Enter 或空格打开列表、方向键和 Enter 选定模型。
