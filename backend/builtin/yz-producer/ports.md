# 能力端口

技能只说「要做什么」，不说「调哪个函数」。每个端口在两种运行时里各有一个绑定：本机（`bindings/local/RUNTIME.md`）和柚子（`bindings/yozi/RUNTIME.md`）。换运行时不改技能。

| 端口 | 做什么 | 读 / 写 | 花钱 |
| --- | --- | --- | --- |
| `progress.read` | 读进度表：每段六格（剧本、参考、生成、过审、入片、验收）和八个终态 | 读 | 否 |
| `brief.read` / `brief.write` | 读写需求文档（契约 brief） | 读 / 写 | 否 |
| `segments.read` / `segments.write` | 读写分段表（契约 segment）；写时按所选模型校验段长 | 读 / 写 | 否 |
| `bible.read` / `bible.write` | 读写连戏表（契约 bible） | 读 / 写 | 否 |
| `assets.check` | 按连戏表核对素材库：缺什么、哪项没过门禁 | 读 | 否 |
| `image.generate` | 出图（定妆三视图、空景、道具、构图静帧） | 写 | 是 |
| `image.crop_sheet` | 三视图裁成单图，过窄的补灰到模型允许的宽高比 | 写 | 否 |
| `voice.design` / `voice.speak` | 设计固定音色、念参考音频 | 写 | 是 |
| `prompt.compile` | 中性分段 → 某个模型的提示词与参数（方言编译），并报字数是否超限 | 读 | 否 |
| `cost.estimate` | 估价：段数 × 秒 × 单价 × 候选数，加参考附加费 | 读 | 否 |
| `spend.approve` | 超出预算或授权档位时请人批准 | 交互 | 否 |
| `video.generate` | 提交一段生成（可多条候选），能续跑，已存在的跳过 | 写 | 是 |
| `media.frames` | 抽帧拼图（按画面动静自动定密度） | 读 | 否 |
| `media.faces` | 人脸表：这一段的脸和定妆照、同场其他段并排 | 读 | 否 |
| `media.transcribe` | 听写并按期望台词算字错率 | 读 | 否 |
| `media.levels` | 逐秒响度、无声洞、爆音、整合响度、高频占比 | 读 | 否 |
| `media.voice` | 台词量数：音高、起伏、语速、死寂、人声高出环境多少 | 读 | 否 |
| `evidence.write` | 把一条证据（契约 evidence）记到那一段 | 写 | 否 |
| `take.pick` | 选中一条候选，写一句理由 | 写 | 否 |
| `cut.assemble` | 按剪辑表总装成片 | 写 | 否 |
| `sound.music` / `sound.sfx` | 配乐、音效、房间声元素 | 写 | 是 |
| `gate.check` | 成片门禁：同步、无声洞、爆音、响度、真峰值、产物干净 | 读 | 否 |
| `delivery.package` | 打交付包：成片、字幕、分段表、资产清单、生成参数、自审 | 写 | 否 |
| `feedback.read` | 读带时间码的看片反馈 | 读 | 否 |
| `ask.user` | 问人（每轮 3～5 个问题，能出小样就出小样） | 交互 | 否 |
