# 建项目改版：一页四段 + 生成模式停靠点

## 一、目标（用户已逐条确认）

把建项目改成「一页四段」：删掉首页启动条（原来有 9 个故事设置下拉）和「创建短剧项目」弹窗，入口只留首页一个作曲区。用户开始输入或拖入文件后，下面原地展开四段；底部是常驻摘要栏和唯一主按钮「开始制作」（Ctrl/⌘+Enter）。同时用三档「生成模式」取代 authMode（step/full），并由服务端执行停靠点。

### 段 1 · 要拍什么（必填）

- 一个大输入框，支持粘贴剧本、小说正文或一句创意，也支持把文件拖到页面任意位置。
- 系统自动识别类型，显示成一个可点「改」的标签：
  - 创意：短于约 300 字、没有场景结构。标签写「识别为 创意 · 将生成 N 集」。
  - 剧本：有场次号、△、「人物：台词」等格式。标签写「识别为 剧本 · 3 集 · 12,480 字」。
  - 小说：长篇连续叙事。标签写「识别为 小说 · 18 章 · 9.6 万字」。
  - 识别函数写成 web/src/lib 下的纯函数，并配 bun test。
- 「导入文件」打开导入弹窗，分三步：上传 → 解析预览（前三章或前三场的切分、总字数）→ 确认是小说还是剧本、章节怎么切。复用现有章节页的小说/剧本导入解析，不要另写第二套。确认后在作曲区显示为文件卡（文件名、字数、[替换] [移除]）。粘贴文本和导入文件二选一，后来的替换前面的，并提示「已替换刚才粘贴的内容」。
- 只有识别为创意时，多出一行「生成 [5▾] 集，每集约 [1 分钟▾]」（用户拍板保留这一行）。原来的叙述视角、叙述结构、基调、角色规模、章节篇幅、单章字数等全部从 UI 删掉，交给 Agent 用默认值。创意建项后走现有的「AI 生成章节」能力。
- 项目名自动生成（来自文件名、剧本标题或首句），旁边放铅笔图标，单击可编辑。「先建一个空项目」降级成文字链接。

### 段 2 · 画面类型（必填，不预选，建项后不可改）

- 四张样张卡：仿真人、写实 CG、3D 动漫、2D 动漫，用 role=radiogroup，支持方向键切换。样张先用风格库里该类型的封面，没有就用中性占位图。
- 没选类型就点「开始制作」时，滚动到这一段并显示「先选画面类型」。
- 先选了风格、后改类型，且风格不属于新类型时：清空风格，原地提示「风格『X』只适用于仿真人，已清空」，并给出 [撤销]。

### 段 3 · 画风从哪来（默认「交给导演分析」）

- **A 交给导演分析**：可选上传参考图（最多 6 张）和一句风格描述，两个都可以不填，文案写「都不填就让导演按故事自己定」。建项后第一个环节就跑 style.lock，参考图和描述作为输入；锁定结果写进平台风格库（标记来源为导演分析、项目名），并锁定为本项目画风。
- **B 从风格中心挑**：原地展开缩略图网格，只显示当前类型；「来自导演分析」的和本人存的排在前面。选中后照 M8 规则跳过全局风格分析，场次色卡照常生成。当前类型的风格库为空时，空态给一个按钮切回 A。
- 原来的「写风格描述」「传参考图」两个独立入口、「项目画风」字段、「选中即锁定」提示全部删掉。

### 段 4 · 怎么做（都有默认值）

- **画幅**：9:16（默认）/ 16:9 / 1:1 / 4:3，分段按钮带比例小图标，标「建项后不可改」。
- **每段时长**（用户拍板就叫这个名字）：15 秒（默认）/ 30 秒，旁边小字换算「约 N 段」。当前模型最长 15 秒时，30 秒仍然可以点，点了在原地提示「X 最长 15 秒，换成 Y？」并给出 [换]。禁止悄悄替用户换模型。
- **视频模型**：默认值标「推荐」；没有渠道的模型置灰，带「去配置渠道」链接。「未校准」不出现在主标签里。
- **生成模式**：三张卡，下面一条流水线（拆剧本 → 导演定画风 → 场次色卡 → 角色场景资产 → 分镜 → 装段 → 出视频 → 剪辑），停靠点高亮。固定写一行「出视频要花钱，任何模式都会在这里停下，带上预估花费等你点。」三档：
  - `until_video` 跑到出视频前（默认）
  - `review_from_assets` 资产我要过目（从资产提取开始，每一步都停）
  - `every_step` 每步都问我
- **没有任何视频渠道也允许建项**（用户拍板）。第 4 段顶部显示提醒条「还没有视频渠道，可以先建项目，出视频前要配好」。

### 摘要栏与提交

- 摘要栏用一句话复述全部选择，点其中任一片段就滚动到对应的段。
- 提交失败时保留表单，按 ApiError.reason 把错误落到对应的段，不只弹 toast。
- 草稿按用户 scope 存进 localforage，标注「草稿已保存在本机」。
- 成功后直接进项目页，首屏是同一条流水线，第一个节点亮起。

## 二、改版前：从建项到出视频的每一环

| 环节 | 触发 | 代码 |
| --- | --- | --- |
| 建项（弹窗或首页启动条，类型、画风预设、授权档位、9 个故事设置） | 手点 | `web/src/pages/projects/index.tsx` → `POST /projects` |
| 定风格：风格库条目 | 建项后前端立刻锁定 | `lockDramaStyle(libraryId)` |
| 定风格：写描述 / 传参考图 | 建项后前端起 `style.lock`，出候选，**手点**「锁定此风格」 | `startStyleLock` → `LockDramaStyle(taskId)` |
| 定风格：只选了画风预设 | 建项后前端起 `style.lock`（`lockOnFinish`），收尾自动锁定 | `applyStyleLockTask` |
| 导入小说 / 剧本 | 手点（建项后跳章节页打开导入弹窗） | `chapters.tsx` `ImportNovelModal` |
| 创意 → AI 生成章节 | 首页「AI 生成章节」手点，前端跑大纲任务后导入 | `runBackendGenerationTask` + `importProjectUnits` |
| 剧本优化 `script.adapt` | 手点（章节页） | `RunDramaProjectStep` |
| 场次色卡 `scene.palette` | **自动**：剧本优化收尾接（没锁定风格就不接、写原因） | `chainScenePaletteAfterAdapt` |
| 提取资产 `assets.extract` | **自动**：场次色卡收尾按集依次接（对话框可关 `chainAssets`） | `chainAssetsAfterScenePalette`、`chainAfterAssetsExtract` |
| 新资产出图 | **自动**：提取收尾按模板排队，出完一张接着排 | `queueAssetsExtractImages`、`continueDramaImages` |
| 生成分镜 `chapter.shots` | 手点（章节页每集） | `RunDramaStep` |
| 装段 | **自动**：分镜收尾检查通过即装；有 error 只存草稿，手点「重新装段」 | `WriteDramaShotlist` |
| 出视频 | 手点（制作台，带估价） | 前端 `CreateTask(video)` |
| 剪辑、门禁、交付 | 手点 | 交付页 |

## 三、改版后

### 后端合同

- `projects` 加 `visual_type`、`run_mode`、`style_source`，删 `auth_mode`（迁移 71：`step` → `every_step`，`full` → `until_video`）；风格库 `project_type` 改成和 `visual_type` 同值（`live_action` / `cg_real` / `anime_3d` / `anime_2d`）。
- `POST /projects`：`sourceAs`（`idea` / `novel` / `script` / `blank`）+ `source`（创意的 `text`；小说、剧本的 `units`）+ `ideaPlan{episodes, episodeSeconds}`、`visualType`（必填）、`style`（`{mode: director, brief?, refImageIds?}` 或 `{mode: library, styleId}`）、`aspectRatio`、`segmentSeconds`、`generationModel`、`runMode`。校验按段给 reason：`project_source_invalid` / `project_visual_type_required` / `project_style_invalid` / `project_production_invalid`。
- brief 契约 `auth_mode` → `run_mode`（`drama.BriefShape`、校验、Agent 工具描述、进度「还没定生成模式」、`contracts/brief.schema.json`）。

### 停靠点

统一判定 `drama.ShouldAutoContinue(runMode, next)`：

| next | until_video | review_from_assets | every_step |
| --- | --- | --- | --- |
| 拆剧本 / 导演定画风 / 场次色卡 | 接 | 接 | 停 |
| 角色场景资产 | 接 | 停 | 停 |
| 分镜（含装段） | 接 | 停 | 停 |
| 出视频 / 剪辑 | **停（写死）** | **停（写死）** | **停（写死）** |

链式触发点（都问它）：剧本优化收尾 → 导演定画风（交给导演分析且没锁定）或场次色卡；流水线跑的导演定画风收尾（直接锁定、存风格库）→ 场次色卡；场次色卡收尾 → 按集提取资产；各集都提取过、资产都出好图、没有分镜在跑 → 按集生成分镜（最后一个收尾的提取任务或出图任务问一次；文本模型配置放在出图模板的 `next` 上随出图任务带下去）。同一环节里的队列（按集依次提取、按集依次生成分镜）不看生成模式。改生成模式只影响之后的接续，已经停住的环节不追补（只有任务收尾时才问）。

进度里的 `pipeline` 写明停在哪、等你做什么；`POST /projects/:id/drama/pipeline/next` 是「继续」（建项后由页面调一次开始第一步）。

## 四、实现时的决定（方案没写到、不扩大范围的取舍）

1. **source 用前端切好的 units，不传资源 ID**：服务端没有小说 / 剧本切分，为了不写第二套解析，小说、剧本由前端用章节页导入的同一套 `splitTextIntoChapters` 切好，随建项一起写入；创意传原话。
2. **分镜和装段是同一步**：装段在生成分镜的收尾里由代码完成（不花钱），所以停靠点停在分镜前，不在装段前单独停；装段检查有 error 时只存草稿、流水线停在装段并写明哪一集有几个问题。
3. **导入剧本跳过拆剧本**：剧本导入时已经是剧集，「拆剧本」这一格算做完，流水线从导演定画风（或场次色卡）开始；要调节奏仍可在章节页手点剧本优化。
4. **导演定画风排在拆剧本之后**：都不填时要按故事定，需要剧本。提示词带剧本节选（剧集正文，没有剧集时取原文章节，6000 字），流水线跑的收尾直接锁定，锁定后存进风格库（名字取项目名，重名加序号；来源 `director`，说明「来自导演分析 · 项目「X」」）。
5. **「每步都问我」也会开始第一步**：用户点「开始制作」就是点了第一步；之后每一步都停。
6. **样张图**：取风格中心里该类型第一条有封面的主色卡图，没有就用中性占位。
7. **项目设置**：删掉「授权档位」「项目画风」，画幅、画面类型、内容来源只读，生成模式可切；分镜生成的门槛从「选了画风预设」改成「锁定了项目风格」（改版前带画风原文的老项目照旧可用）。画布里的「项目画风」节点是画布功能，不在这次范围。
8. **章节页对话框**：场次色卡对话框删掉「写完后按集依次提取资产」开关（由生成模式决定），保留「提取后出图」的图片模型；「分析项目风格」不再选类型（取项目的画面类型）。

## 五、改了哪些技能文件（只改字段名和取值，不做提示词优化）

`auth_mode` → `run_mode`，`step` / `full` → `every_step` / `until_video`，「授权档位」→「生成模式」，「每步确认」→「每步都问我」，「全权」→「跑到出视频前」：

- `yoozee-skills/contracts/brief.schema.json`、`backend/builtin/yz-producer/contracts/brief.schema.json`
- `yoozee-skills/rules/bible.json`、`backend/builtin/yz-producer/rules/bible.json`
- `yoozee-skills/ports.md`、`backend/builtin/yz-producer/ports.md`
- `yoozee-skills/bindings/local/PROJECT.md`、`yoozee-skills/bindings/yozi/RUNTIME.md`、`backend/builtin/yz-producer/RUNTIME.md`
- `yz-producer`、`yz-brief`（含 `templates/需求文档.md`）、`yz-adapt`、`yz-calibrate`、`yz-cast`、`yz-deliver`、`yz-shoot` 的 `SKILL.md`（`yoozee-skills/skills/` 与 `backend/builtin/` 两份）
- `yoozee-skills/tests/sample/需求.json`、`需求文档.md`
