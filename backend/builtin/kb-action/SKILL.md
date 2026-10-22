---
name: "知识包 · 动作戏"
description: "动作戏知识：打斗割草技能的用户确认写法与两份 30 秒范例（1v1、割草）、参考视频逐帧拆解模板；词库：特效提示词库 120 条、武学打斗库 108 条（流派、武侠动作、战斗概念、兵器、打斗阶段、阵型、外国流派）、打斗割草第四部分全文按标题切块并建索引（技能库、武器特效、运镜公式、负面词、模板、冲突裁定），IP 包（鸣潮，内部自用）。在 yz-fight、yz-analyze、yz-script 动作场指引时按需读；词库只按 id 和索引查。"
metadata:
  version: "2.2.0"
  author: "柚子"
  owner: "yingce-system"
  skillId: "kb-action"
  tag: drama
  sortWeight: 1956
  source: 3
  createdAt: 1792454400000
  updatedAt: 1792454400000
  initialLikeCount: 0
  initialAddedCount: 0
  extraInfo: ""
---

# 知识包 · 动作戏

这是 yoozee 2.0 skills 的知识包，只有卡片和词库，没有流程。由 yz-* 技能指名读取：引用写成「kb-action › 路径」。读卡的方法见 技能 yz-producer 的 `RUNTIME.md`（用 skill_read_file 读）。

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

| 文件 | 何时读 | 字数 |
| --- | --- | --- |
| `cards/参考视频逐帧拆解模板.md` | yz-analyze 拆解参考视频时按此模板执行并写入契约 analysis；命中需两项证据，不确定标置信度 | 6641 |
| `cards/打斗割草-00-使用与优先级.md` | 用 action-epic 档案出打斗 / 割草提示词前读：优先级、资产使用边界 | 1313 |
| `cards/打斗割草-01-用户确认写法.md` | action-epic 档案的 style_block、景别与运镜代码、流体/粒子/水尘配比、单发慢镜头的来源 | 3486 |
| `cards/打斗割草-02-30秒打斗完整范例.md` | 想看 1v1 30 秒成品写法时读；人物关系、配色、故事不套用 | 5703 |
| `cards/打斗割草-03-30秒割草完整范例.md` | 想看 1vN 割草 30 秒成品写法时读 | 7955 |
| `vocab/epic-fight/001-第四部分开头.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 935 |
| `vocab/epic-fight/002-B-1v1-双人对决最终输出格式(强制).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1055 |
| `vocab/epic-fight/003-C-1vN-割草最终输出格式(强制).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1355 |
| `vocab/epic-fight/004-D-最终格式选择规则.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 730 |
| `vocab/epic-fight/005-F-与后文原技能的关系.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 375 |
| `vocab/epic-fight/006-原-v4-19-完整技能正文(保留).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 844 |
| `vocab/epic-fight/007-一-v4-19相对v4-18的新增与修订(差啥补啥-对照表).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 872 |
| `vocab/epic-fight/008-二-第3章追加铁律(3-14-3-20).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 4866 |
| `vocab/epic-fight/009-三-模式-B-1v1对决专项铁律(v4-19修订版-四点修改).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1235 |
| `vocab/epic-fight/010-四-第8章追加-1v1无魔物-小技能-三维30秒模板(v4-19修订版.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 2596 |
| `vocab/epic-fight/011-五-第9章追加-全武器小技能库(v4-19-13-武器-4-52个).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1313 |
| `vocab/epic-fight/012-六-第9章追加-仙法小技能库(v4-19-24个-0-帧瞬发小仙法).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 687 |
| `vocab/epic-fight/013-七-第11章负面词修订(v4-19).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 489 |
| `vocab/epic-fight/014-八-第12章追加-二十四-v4-19自检(10项-必查).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 744 |
| `vocab/epic-fight/015-九-触发关键词与description更新(v4-19).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 575 |
| `vocab/epic-fight/016-十-冲突裁定表(v4-19与旧条款冲突时以此为准).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 2541 |
| `vocab/epic-fight/017-一-变身觉醒专项铁.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 3562 |
| `vocab/epic-fight/018-八-1v1-对决质检-与-1vN-割草并列.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 5907 |
| `vocab/epic-fight/019-十-v4-12-去对话-去蓄力-1v1-高频贴身自检-(6-项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 4864 |
| `vocab/epic-fight/020-一-v4-13-1vN-大招清屏流自检-(6-项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 2987 |
| `vocab/epic-fight/021-一-画质规格(UE5-4-顶级实时渲染-60fps高帧版).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 681 |
| `vocab/epic-fight/022-二-特效满屏铁律(v3-5-核心升级).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 927 |
| `vocab/epic-fight/023-三-暴力规避铁律(绝对安全规约).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 452 |
| `vocab/epic-fight/024-四-景别限制与循环.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 531 |
| `vocab/epic-fight/025-五-动作频率铁律(v3-5-三级制).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 596 |
| `vocab/epic-fight/026-六-伪一镜到底与场景破坏.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 481 |
| `vocab/epic-fight/027-七-魔物来回穿梭与剧烈反击铁律(v4-0-强化-v4-18升级为全模式强.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1050 |
| `vocab/epic-fight/028-八-战斗节奏铁律(v3-5-去顿帧版-关键变更).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 752 |
| `vocab/epic-fight/029-九-纯动作流叙事铁律.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 436 |
| `vocab/epic-fight/030-十-1v1对决专项铁律(模式-B-与1vN割草并列).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 789 |
| `vocab/epic-fight/031-十一-变身觉醒专项铁律(模式C-形态切换-暴走-可叠加A-B).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1226 |
| `vocab/epic-fight/032-一-打斗底层逻辑(为什么这么打).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1111 |
| `vocab/epic-fight/033-二-战斗节奏设计(按什么节奏打).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 916 |
| `vocab/epic-fight/034-三-技能目标逻辑-打谁-怎么克制(每个技能打谁).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1495 |
| `vocab/epic-fight/035-一-镜头逻辑-伪一镜到底(禁生硬跳切).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 452 |
| `vocab/epic-fight/036-二-保持势均力敌(禁碾压段).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 382 |
| `vocab/epic-fight/037-三-攻击后必须续招(禁空帧-回气-摆姿势).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 353 |
| `vocab/epic-fight/038-四-运镜必须高速(禁缓推缓拉缓摇).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 367 |
| `vocab/epic-fight/039-五-尾帧收束-悬念版或全灭版(禁胜利收刀-禁空场景).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 10962 |
| `vocab/epic-fight/040-一-景别详解(ShotSize).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 552 |
| `vocab/epic-fight/041-二-机位高度(CameraHeight).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 436 |
| `vocab/epic-fight/042-三-运镜运动方式(CameraMovement).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 825 |
| `vocab/epic-fight/043-四-运镜组合公式(v3-5-满屏割草版-10条).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 755 |
| `vocab/epic-fight/044-五-位置锚点规范(PositionAnchoring).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 677 |
| `vocab/epic-fight/045-六-动作描写规范(ActionWriting).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 856 |
| `vocab/epic-fight/046-七-运镜与景别匹配表(v3-5-满屏割草版).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1153 |
| `vocab/epic-fight/047-八-无敌割草-景别与运镜快节奏速查(v4-8-配3-8).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 624 |
| `vocab/epic-fight/048-九-流光割草-运镜九字诀与运镜服务技能(v4-15-配3-13).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1134 |
| `vocab/epic-fight/049-一-主角模板(7种完整人设).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1715 |
| `vocab/epic-fight/050-二-魔物模板(5种完整类型).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1935 |
| `vocab/epic-fight/051-三-角色关系设定(3种完整).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 632 |
| `vocab/epic-fight/052-四-装备破损累积系统(战损可视化).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 480 |
| `vocab/epic-fight/053-一-配色方案库(7套).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1056 |
| `vocab/epic-fight/054-二-武器特效库(13种完整).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 2101 |
| `vocab/epic-fight/055-三-武器断裂与空手搏杀(经典桥段).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 333 |
| `vocab/epic-fight/056-四-武器吸回设定(远程回收).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 3194 |
| `vocab/epic-fight/057-一-10秒模板(10镜头)-v4-18-1-秒分镜版-6秒穿梭连打-4秒.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1240 |
| `vocab/epic-fight/058-二-15秒模板(15镜头)-v4-18-1-秒分镜版-满屏割草-魔物升级.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1638 |
| `vocab/epic-fight/059-三-30秒模板(30镜头)-v4-18-1-秒分镜版-战况升级-满屏仙法.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 3019 |
| `vocab/epic-fight/060-四-双剑客对决30秒模板(A绝杀B-灵动暴打留人版-v4-18-1-秒分.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 2537 |
| `vocab/epic-fight/061-五-技能释放完整链(满屏版).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 367 |
| `vocab/epic-fight/062-六-尾帧设计(3种模式任选).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 278 |
| `vocab/epic-fight/063-七-1v1灵动对决30秒模板(势均力敌-攻防拉锯版-v4-18-1-秒分.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 2562 |
| `vocab/epic-fight/064-八-变身觉醒30秒模板(v4-18-1-秒分镜版-绝境反杀-形态切换-满.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 2655 |
| `vocab/epic-fight/065-九-无敌割草30秒模板(v4-18-1-秒分镜版-三技能连招-主角全程躲.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 2672 |
| `vocab/epic-fight/066-十一-1v1高频贴身互殴30秒模板(v4-18-1-秒分镜版-去对话-去.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 3590 |
| `vocab/epic-fight/067-十二-1vN割草模板库(v4-18-1-秒分镜版-多可选六型-流光割草.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 3524 |
| `vocab/epic-fight/068-十-SSS级一句话总合成系统(v4-9-位置-动作-特效-运镜全合成一句.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1359 |
| `vocab/epic-fight/069-一-五大史诗级特效(经典保留-满屏升级).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1234 |
| `vocab/epic-fight/070-二-速度感特效.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 465 |
| `vocab/epic-fight/071-三-特殊演出特效.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 751 |
| `vocab/epic-fight/072-四-魔物被击败特效(安全版).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 415 |
| `vocab/epic-fight/073-五-装备破损承压特效(主角专用).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 391 |
| `vocab/epic-fight/074-六-灵力仙法专属技能库(v3-5-新增20个-满屏特效版).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 3033 |
| `vocab/epic-fight/075-七-灵力仙法技能组合链.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 337 |
| `vocab/epic-fight/076-八-割草满屏连招(1vN专用-v4-0-差啥补啥新增).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 554 |
| `vocab/epic-fight/077-九-招式组合链大全(直接套用的连招配方-按模式分类).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1396 |
| `vocab/epic-fight/078-十-无敌割草三技能连招链(v4-8-模式-A无敌版).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 915 |
| `vocab/epic-fight/079-十一-古今神话大招库-西方电影魔法技能库-网文玄幻修仙技能库(1-100.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 31322 |
| `vocab/epic-fight/080-二-古今神话-世界神话大招库(201-340-1s-一技-0-帧瞬发-占.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 21607 |
| `vocab/epic-fight/081-三-西方电影魔法-哈利波特技能库(341-440-1s-一技-0-帧瞬发.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 15422 |
| `vocab/epic-fight/082-四-西方电影魔法-中土传奇技能库(441-540-1s-一技-0-帧瞬发.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 15455 |
| `vocab/epic-fight/083-五-西方电影魔法-漫威与奇异博士等电影法术库(541-670-1s-一技.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 20096 |
| `vocab/epic-fight/084-六-网文爆火玄幻修仙技能库(671-1000-1s-一技-0-帧瞬发-占.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 50359 |
| `vocab/epic-fight/085-一-台词使用原则.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 464 |
| `vocab/epic-fight/086-二-台词库(按战斗阶段分类).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 454 |
| `vocab/epic-fight/087-三-口型与字幕规范.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 824 |
| `vocab/epic-fight/088-五-情绪变化曲线.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 439 |
| `vocab/epic-fight/089-六-拟音与音效提示(写进提示词增强画面感).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 554 |
| `vocab/epic-fight/090-七-微表情系统(v4-4-升级-把-神态-做成可驱动-可查表的表演模块).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1343 |
| `vocab/epic-fight/091-八-对话系统(v4-4-升级-把-台词-做成推动动作与叙事的触发器).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1314 |
| `vocab/epic-fight/092-九-剧本转提示词系统(v4-4-升级-核心新能力-把剧情文本自动变为可生.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1819 |
| `vocab/epic-fight/093-十-打斗痕迹系统(v4-5-升级-把-打过-可视化-可累积-可叙事).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1963 |
| `vocab/epic-fight/094-十一-技能释放参考(v4-6-升级-经典动漫演出大杂烩).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 2064 |
| `vocab/epic-fight/095-十二-技能释放参考-扩编(v4-7-更多爆火动漫技能速查).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1531 |
| `vocab/epic-fight/096-十三-身体特效与人物特效系统(v4-7-升级-附着在人物身上的特效).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1369 |
| `vocab/epic-fight/097-十四-受伤系统(v4-9-战损可视化-能量伤优先).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1236 |
| `vocab/epic-fight/098-十五-断肢系统(v4-9-魔物能量化断肢-禁写实血肉).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1238 |
| `vocab/epic-fight/099-十六-残骸保留系统(v4-10-尸体地标-血雾-战场破坏永久化).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 2311 |
| `vocab/epic-fight/100-一-基础版(必带-所有场景通用).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 568 |
| `vocab/epic-fight/101-二-高速战斗模式补充.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 315 |
| `vocab/epic-fight/102-三-1vN群战-割草模式补充(v3-5-强化).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 326 |
| `vocab/epic-fight/103-四-巨兽-BOSS战模式补充.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 351 |
| `vocab/epic-fight/104-六-景别与画质补充.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 325 |
| `vocab/epic-fight/105-七-特效完整性补充(v3-5-强化).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 308 |
| `vocab/epic-fight/106-八-灵力仙法专属补充(v3-5-新增).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 410 |
| `vocab/epic-fight/107-九-打击感补充(v3-5-去顿帧版).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 283 |
| `vocab/epic-fight/108-十-叙事补充.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 426 |
| `vocab/epic-fight/109-十二-快速组合模板.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1544 |
| `vocab/epic-fight/110-一-结构完整性(5项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 315 |
| `vocab/epic-fight/111-二-全局铁律合规(10项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 577 |
| `vocab/epic-fight/112-三-去顿帧关键合规(3项-强制).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 445 |
| `vocab/epic-fight/113-五-特效与画面(8项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 390 |
| `vocab/epic-fight/114-六-魔物密度与反击(3项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 441 |
| `vocab/epic-fight/115-八-1v1对决合规(模式B-必查).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 336 |
| `vocab/epic-fight/116-九-变身觉醒合规(模式C-必查).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 392 |
| `vocab/epic-fight/117-十-打斗逻辑自检(v4-2-逻辑升级必查).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 408 |
| `vocab/epic-fight/118-十一-v4-3-连贯高速战斗自检(5项-必查).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 363 |
| `vocab/epic-fight/119-十二-v4-4-表演与剧本转提示词自检(6项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 498 |
| `vocab/epic-fight/120-十三-v4-5-打斗痕迹自检(6项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 432 |
| `vocab/epic-fight/121-十四-v4-6-技能释放参考自检(5项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 386 |
| `vocab/epic-fight/122-十五-v4-7-快节奏与身体特效自检(6项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 425 |
| `vocab/epic-fight/123-十六-v4-8-无敌割草自检(6项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 646 |
| `vocab/epic-fight/124-十七-v4-9-受伤-断肢-全灭-总合成自检(6项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 735 |
| `vocab/epic-fight/125-十八-v4-10残骸保留与血雾自检(6项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 633 |
| `vocab/epic-fight/126-十九-v4-11千招库-悬浮割草自检(6项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 595 |
| `vocab/epic-fight/127-二十-v4-12去对话-去蓄力-1v1高频贴身自检(6项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 609 |
| `vocab/epic-fight/128-二十一-v4-131vN大招清屏流自检(6项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 714 |
| `vocab/epic-fight/129-二十二-v4-17千招连招-三源换源-禁黑洞镜像-流光割草自检(10项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1132 |
| `vocab/epic-fight/130-二十三-v4-181秒分镜-全模板魔物穿梭反击-特写克制自检(6项).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 35746 |
| `vocab/epic-fight/131-一-主流平台差异.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 582 |
| `vocab/epic-fight/132-二-常见崩坏原因与修复(v3-5-版).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 1068 |
| `vocab/epic-fight/133-三-生成前最后检查(30秒).md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 4457 |
| `vocab/epic-fight/134-一-攻击动词(按力度)劈-砍-斩-削-切-抹-撩-挑-崩-点-扎-刺-戳.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 415 |
| `vocab/epic-fight/135-三-武器与招式名剑-刀-枪-棍-戟-鞭-锏-锤-双刀-双剑-长剑-短刃.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 270 |
| `vocab/epic-fight/136-四-特效与能量词流体-粒子-星火-火星-金辉-灵辉-灵气-仙气-灵纹-法.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 290 |
| `vocab/epic-fight/137-五-神态与情绪睥睨-冷笑-狰狞-怒意-决然-坚毅-倔强-淡漠-肃杀-凛冽.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 415 |
| `vocab/epic-fight/138-七-四字成语(战况-气势)-势如破竹-摧枯拉朽-排山倒海-雷霆万钧-惊天.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 491 |
| `vocab/epic-fight/139-八-满屏与密度描写词满屏-铺天盖地-密密麻麻-成千上万-不计其数-无穷无.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 307 |
| `vocab/epic-fight/140-九-打击感与去顿帧描写词亮度骤增-镜头急推-冲击波放大-受击方猛飞-能量.md` | 按索引 kb-action/vocab/epic-fight/index.md 查到再读这一块 | 577 |
| `vocab/epic-fight/index.md` | yz-fight 需要某类技能、武器、运镜公式、负面词、模板时先查这张索引，再读对应块；不整读 | 7776 |
| `vocab/martial-索引.md` | yz-fight 选流派 / 兵器 / 阵型 / 打斗阶段时按 id 引用（先在这张索引里按名称或场景找 id，再读 martial.jsonl 里对应的那一行） | 8007 |
| `vocab/martial.jsonl` | 词库，一条一行；先看同名索引找 id 再读那一行 | 25547 |
| `vocab/packs/wuthering-waves/README.md` |  | 151 |
| `vocab/packs/wuthering-waves/wuthering-索引.md` | game-ip 档案 + 鸣潮 IP 时按 id 引用；对外包不含（先在这张索引里按名称或场景找 id，再读 packs/wuthering-waves/wuthering.jsonl 里对应的那一行） | 9638 |
| `vocab/packs/wuthering-waves/wuthering.jsonl` | 词库，一条一行；先看同名索引找 id 再读那一行 | 40605 |
| `vocab/vfx-索引.md` | yz-fight 写 vfx[] 时按 id 引用（先在这张索引里按名称或场景找 id，再读 vfx.jsonl 里对应的那一行） | 8060 |
| `vocab/vfx.jsonl` | 词库，一条一行；先看同名索引找 id 再读那一行 | 29162 |
