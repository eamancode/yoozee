# 规则表（规则即代码）

每条规则有 `id`、`level`（error 阻断 / warn 提示）、`desc`、`source`、`impl`（验证器里的函数名）。技能正文只引用 id，不复述规则；验证器两份实现（本机 `tools/check.py`，柚子 `internal/drama/check*.go`）共享 `tests/fixtures/check-*.json`。

| 文件 | 验证器 | 对象 |
| --- | --- | --- |
| `brief.json` | `check.brief` | 需求文档 |
| `story.json` | `check.story` | 故事档 |
| `script.json` | `check.script` | 剧本 |
| `bible.json` | `check.bible` | 资产圣经 |
| `shots.json` | `check.shots` | 镜头表（含档案配额） |
| `segments.json` | `check.segments` | 段（含 2.5 一致性桥） |
| `compile.json` | 编译器 | 编译稿 |
| `analysis.json` | `check.analysis` | 参考视频拆解 |

全局规则（不进档案）：光源动机、180° 轴线、30° 规则、相邻景别禁忌、视线匹配、伏笔优先级、禁 BGM、禁可读文字、场景禁人物、最多双轴运动、一句台词只落一个镜头。档案规则（进 `profiles/*.json`）：配比、禁令、站桩秒数、密度、光线政策。
