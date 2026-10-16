# yoozee 1.0 skills

一套把剧本做成短剧成片的技能。它同时有两种用法：

- **独立使用**：放进 Claude Code 或 Codex 的项目里，在本机生成、质检、剪辑、交付；
- **嵌入柚子**：作为柚子的内置技能，由短剧 Agent 调用柚子的后端工具完成同样的事。

两种用法共用同一份源文件（本目录），由 `build.py` 打成不同的包。

## 它解决什么

片子要满足八个终态才算做完：需求问透、剧本定了、参考齐了、每段过审、成片过门禁、交了、甲方验收、能复用。
技能不按「传统流程」往下推，而是每次只做「依赖都已成立、自己还没成立」的那一件事；每一步的结果都要有证据（听写、抽帧、人脸表、量数），没有证据就不算做完。

## 目录

| 路径 | 是什么 |
| --- | --- |
| `skills/yz-*` | 12 个技能，每个只管一个环节 |
| `kb/kb-*` | 5 个知识包，只有卡片，技能按需读取 |
| `kb/kb-dialects/models.json` | 可选的生成模型和它们的边界（段长上限、参考数量、提示词上限），柚子后端也读它 |
| `contracts/*.schema.json` | 四份数据契约：需求、分段、连戏表、证据 |
| `ports.md` | 能力端口清单：技能只说「要做什么」，具体调用由运行时绑定 |
| `bindings/local` | 本机运行时：Python 尺子脚本、适配规格、审片员和探路员、密钥守门 |
| `bindings/yozi` | 柚子运行时：端口到柚子后端工具的映射 |
| `build.py` | 打包脚本 |

## 生成模型与段长

建项目时先选生成模型，一段（一次生成）的最长时长跟着模型走：

| 模型 | 一段时长 | 依据 |
| --- | --- | --- |
| Seedance 2.0 | 4～15 秒 | 官方接口文档 |
| MiniMax-H3 | 4～15 秒 | 实测档案 |
| Seedance 2.5 | 4～30 秒 | 官方接口文档 |

完整边界见 `kb/kb-dialects/models.json`；每个模型的脾气和提示词写法见同目录下的档案和方言卡。

## 打包

```bash
python3 yoozee-skills/build.py            # 打出四种包到 yoozee-skills/dist/，并同步柚子内置技能
python3 yoozee-skills/build.py --check    # 只做自检：字数上限、引用的卡片存在、契约是合法 JSON Schema
```

产物：

| 产物 | 用法 |
| --- | --- |
| `dist/claude-code/` | 整个拷进项目根目录：`.claude/skills`、`.claude/agents`、`.claude/yoozee`（运行时） |
| `dist/codex/` | 整个拷进项目根目录：`.agents/skills`、`yoozee/`（运行时）、`AGENTS.md` 片段 |
| `dist/standalone/yoozee-skills-<版本>.zip` | 对外包：上面两种 + README + 来源说明，不含柚子绑定和任何密钥 |
| `backend/builtin/yz-*`、`backend/builtin/kb-*` | 柚子内置技能（由 build.py 写入仓库） |

## 本机运行需要

ffmpeg / ffprobe、Python 3 加 numpy 和 Pillow、本地 `whisper` 命令（听写）、一种中文字体。生成模型的密钥只放项目根目录的 `.env`，不进命令行、日志和提示词。

## 来源

见 `SOURCES.md`。
