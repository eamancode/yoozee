---
name: kb-action
title: 知识包 · 动作戏
description: 动作戏知识：打斗割草技能的用户确认写法与两份 30 秒范例（1v1、割草）、参考视频逐帧拆解模板；词库：特效提示词库 120 条、武学打斗库 108 条（流派、武侠动作、战斗概念、兵器、打斗阶段、阵型、外国流派）、打斗割草第四部分全文按标题切块并建索引（技能库、武器特效、运镜公式、负面词、模板、冲突裁定），IP 包（鸣潮，内部自用）。在 yz-fight、yz-analyze、yz-script 动作场指引时按需读；词库只按 id 和索引查。
yozi:
  sortWeight: 1956
---

# 知识包 · 动作戏

这是 yoozee 2.0 skills 的知识包，只有卡片和词库，没有流程。由 yz-* 技能指名读取：引用写成「kb-action › 路径」。读卡的方法见 {{RUNTIME}}。

## 词库怎么用

- `vocab/vfx.jsonl`（特效 120 条）、`vocab/martial.jsonl`（武学 108 条）：一条一行，字段 `id / name / en / section / 定义 / AI技术执行 / 情绪功能 / 适用场景`。先看 `vocab/vfx-索引.md`、`vocab/martial-索引.md` 找 id，再读那一行。镜头表 `vfx[].id`、`fight.martial_ids` 引用这些 id，验证器会查存在。
- `vocab/epic-fight/index.md`：打斗割草第四部分 150 多块的索引（标题、原文行、字数）。需要某类技能库、武器特效、运镜公式、负面词、分镜模板时先查索引，再读对应块；不整读。
- `vocab/packs/<ip>/`：IP 包（鸣潮 `wuthering.jsonl` 127 条 + 索引），仅在 game-ip 档案或用户点名 IP 时用；对外包不含。

## 路由

| 情况 | 读 |
| --- | --- |
| 用 action-epic 档案出打斗 / 割草 | `cards/打斗割草-00-使用与优先级.md`、`cards/打斗割草-01-用户确认写法.md`；想看成品颗粒度 `cards/打斗割草-02-30秒打斗完整范例.md` 或 `-03-30秒割草完整范例.md` |
| 打斗阶段怎么分 | `vocab/martial-索引.md` 的「打斗阶段与结构」一节（martial-076～085） |
| 选流派、兵器、阵型 | `vocab/martial-索引.md` → 对应 id |
| 选特效 | `vocab/vfx-索引.md` → 对应 id；配比与大招间隔按档案 vfx_policy |
| 物理规则、打击感五件套、瞬移、慢镜头许可 | `vocab/epic-fight/index.md` 里「第3章追加铁律」块 |
| 拆参考视频 | `cards/参考视频逐帧拆解模板.md` |
| 鸣潮等 IP | `vocab/packs/wuthering-waves/wuthering-索引.md` |

{{CARDS}}
