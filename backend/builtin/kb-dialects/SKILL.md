---
name: "知识包 · 模型档案与方言"
description: "生成模型的边界（models.json：每家一段最长几秒、参考数量、提示词上限）、档案和提示词方言卡：Seedance 2.0、Seedance 2.5、MiniMax-H3。在 yz-producer、yz-adapt、yz-dialect、yz-calibrate 指引时读取。"
metadata:
  version: "1.0.0"
  author: "柚子"
  owner: "yingce-system"
  skillId: "kb-dialects"
  tag: drama
  sortWeight: 1956
  source: 3
  createdAt: 1792022400000
  updatedAt: 1792022400000
  initialLikeCount: 0
  initialAddedCount: 0
  extraInfo: ""
---

# 知识包 · 模型档案与方言

这是 yoozee 1.0 skills 的知识包，只有卡片，没有流程。由 yz-* 技能指名读取：引用写成「kb-dialects › 卡片路径」。读卡的方法见 技能 yz-producer 的 `RUNTIME.md`（用 skill_read_file 读）。
一次只读需要的那一张，不整包读；卡片开头两行写了来源和何时读。

| 卡片 | 何时读 |
| --- | --- |
| `minimax-h3/方言卡.md` | 项目模型是 MiniMax-H3，编译或检查提示词时读 |
| `minimax-h3/档案.md` | 项目选了 MiniMax-H3 时，编译提示词、挑参考、估价之前读 |
| `models.json` | 可选生成模型的边界：一段最长几秒、参考数量、提示词上限 |
| `seedance-2.0/即梦粘贴格式.md` | 编译 Seedance / 即梦提示词时读：双层输出与粘贴版结构 |
| `seedance-2.0/方言卡.md` | 项目模型是 Seedance 2.0 或 Seedance 2.5，编译或检查提示词时读（2.5 的差别见 seedance-2.5/方言卡.md） |
| `seedance-2.0/最终任务指令.md` | 只要即梦粘贴提示词（不走完整制作线）时读 |
| `seedance-2.0/标准输出示例.md` | 想看即梦粘贴版的颗粒度时读 |
| `seedance-2.0/格式硬规则.md` | 编译即梦粘贴版前读 |
| `seedance-2.0/档案.md` | 项目选了 Seedance 2.0 时，写分段表、挑参考、编译提示词、估价之前读 |
| `seedance-2.5/方言卡.md` | 项目模型是 Seedance 2.5 时，和 seedance-2.0/方言卡.md 一起读 |
| `seedance-2.5/档案.md` | 项目选了 Seedance 2.5 时，写分段表、挑参考、编译提示词、估价之前读 |
