---
name: "yoozee · 审片尺子"
description: "只读的审片：给一段候选或整部成片，抽帧拼图、截脸拼表、听写算字错率、量响度和台词，按八项（人物、台词、崩坏、空间、道具、文字、声音、肤质）返回带时间码和证据的问题清单，并把证据记到那一段上。每条候选出来后、成片自审时、返修后核实改动点时用。"
metadata:
  version: "1.0.0"
  author: "柚子"
  owner: "yingce-system"
  skillId: "yz-qc"
  tag: drama
  sortWeight: 1945
  source: 3
  createdAt: 1792022400000
  updatedAt: 1792022400000
  initialLikeCount: 0
  initialAddedCount: 0
  extraInfo: ""
---

# 审片尺子

你听不见、也不信印象：每条结论要么有时间码和证据文件，要么不写。不改文件、不重出、不剪。
能力端口：`media.frames`、`media.faces`、`media.transcribe`、`media.levels`、`media.voice`、`evidence.write`、`bible.read`、`segments.read`；调用方法见 技能 yz-producer 的 `RUNTIME.md`（用 skill_read_file 读）。

## 输入

文件（一段候选或成片）、连戏表、这段的期望台词。

## 量什么

| 尺子 | 出什么 | 判什么 |
| --- | --- | --- |
| `media.frames` | 抽帧拼图，按画面动静自动定密度（静 2、动 4、猛动和切镜 8 帧/秒） | 手指、五官、虚影、闪烁、穿模；大动作窗口要密抽，每秒 2 帧会漏 |
| `media.faces` | 这一段的脸和定妆照、同场其他段并排 | 是不是同一个人；小特征在不在 |
| `media.transcribe` | 听写（带时间戳）和对期望台词的字错率 | 台词一字不差、听得懂、落在哪一秒；忽略静音上的幻听 |
| `media.levels` | 逐秒响度、无声洞、爆音、整合响度、2～8kHz 与 150Hz 以下占比 | 声音问题 |
| `media.voice` | 音高、起伏、语速、死寂格数、人声高出环境多少 | 只报数：念得平不平、剪辑要补什么 |

## 按八项核（kb-continuity › cards/每段必查与空间图.md）

人物、台词、崩坏、空间、道具、文字、声音、肤质。空间对照连戏表的空间图：位置、身后、视线方向。

## 输出

一张表：时间码、哪一项、看到或量到什么、证据文件、建议（重出 / 剪辑修 / 可留）。同一根因合并。分不清是问题还是设计如此的标「不确定」。没问题就说没问题，不硬凑。

每把尺子的结果用 `evidence.write` 记到那一段那一条候选上（契约 evidence）：
- transcript：`metrics.cer`（字错率），passed = 字错率 ≤ 门槛（默认 0.05；方言、喊叫段可在需求文档里放宽并写明）；
- faces：passed = 同一个人（人看人脸表下结论，写一句依据）；
- frames：passed = 无崩坏；
- levels、voice：passed 为 null（只报数）。

## 返修核实

只核改动点和它前后两秒，不重查全片。

## 门禁

三项必备证据（faces、transcript、frames）都已记到候选上，才可以交 yz-shoot 选中。
