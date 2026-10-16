---
name: "yoozee · 方言编译"
description: "把中性分段编译成项目所选模型（Seedance 2.0 / Seedance 2.5 / MiniMax-H3）的提示词和参数，检查段长、参考数量、字数是否超限，并能输出可直接贴进即梦的粘贴版。提交生成前用；用户只要一段粘贴提示词时也用。"
metadata:
  version: "1.0.0"
  author: "柚子"
  owner: "yingce-system"
  skillId: "yz-dialect"
  tag: drama
  sortWeight: 1955
  source: 3
  createdAt: 1792022400000
  updatedAt: 1792022400000
  initialLikeCount: 0
  initialAddedCount: 0
  extraInfo: ""
---

# 方言编译

技能和分段表不认厂商；这一步才把中性分段翻译成某个模型的「方言」。编译是确定性的：同一段、同一模型，编译结果一样。
能力端口：`segments.read`、`prompt.compile`、`cost.estimate`；调用方法见 技能 yz-producer 的 `RUNTIME.md`（用 skill_read_file 读）。

## 前置

这一段的镜头设计完整（yz-direct）；参考文件都存在（yz-cast、yz-world）。

## 先读

- kb-dialects › models.json：所选模型的 `duration`、`refs`、`frames_exclusive_with_refs`、`prompt_max_chars`。
- kb-dialects › {所选模型}/方言卡.md，必要时读同目录档案.md 的「实测」和「坑」。

## 做法

1. `prompt.compile`（段号 + 模型）。它会：
   - 按方言卡的结构拼提示词（风格块、表演块、本段约束、参考角色说明、分时段镜头、台词与表演状态、声音、约束）；
   - 把参考按角色编号（Seedance 用 @图片N / @视频N / @音频N，MiniMax-H3 用 `<Picture k>` / `<Audio k>`）；
   - 检查：段长在模型范围内、参考数量不超、首尾帧与参考是否互斥、字数不超上限。
2. 有超限：按提示改，不硬交。
   - 段太长：退回 yz-adapt 拆段；
   - 参考太多：近景只挂特写、去掉重复的场景图；
   - 字数超：先压缩风格词和重复的场景词，不删台词和关键动作。
3. 把编译结果给 yz-shoot；用户只要粘贴版时，把提示词原样交给他（附一行：模型、时长、要上传哪几张参考、各自编号）。

## 默认风格块

- 口味为真人剧：Seedance 用中文写实块（「真人剧质感，电影机定焦镜头，场景里真实光源的自然光，皮肤哑光有毛孔，无美颜，细颗粒……」），MiniMax-H3 用英文 video_style 块。
- 口味不是真人剧：按需求文档的口味写风格块，不用写实块。

## 门禁

- 编译报告里无「超限」。
- 台词逐字出现在提示词里，没有被改写或截断。
- 提示词里除参考已有的字外不要求出现可读文字；不写配乐。

## 产出

每段一份编译结果：模型、时长、提示词、参考清单（文件 → 编号 → 角色）、检查报告。
