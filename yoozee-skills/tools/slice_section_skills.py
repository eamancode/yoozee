#!/usr/bin/env python3
"""按小节生成（M8 卡 F / 卡 E）的分镜技能包：从资产库原文按卡 0 文档的切片取段，原文一字不改。

  python yoozee-skills/tools/slice_section_skills.py \
      --wenxi15 "E:/AI影视/资产/分镜-文戏15/7.3.json" \
      --wenxi30 "E:/AI影视/资产/分镜-文戏30/即梦剧本-分镜_优化版.txt" \
      --wuxi "E:/AI影视/资产/分镜-武戏/skill-complete-v7.md"

写出 yoozee-skills/packages/yz-shots-wenxi15、yz-shots-wenxi30、yz-shots-wuxi、yz-shots-wuxi-live（只给了哪几个就写哪几个），
并同步到 backend/builtin/（内置种子）。武戏两个包：yz-shots-wuxi 是完整切片（CG / 仙侠 / 动漫 / 游戏项目），
yz-shots-wuxi-live 是仿真人项目的精简切片（去掉 v5.0 万能模板里的 UE5 画质串、仙法、魔物、能量化伤痕和 v7.1 CG 画风），
按风格档案绑定（步骤表 chapter.shots#wuxi 的 ProfileSkills）。
每段原文用「<!-- 原文：文件 第 a～b 行 -->」和「<!-- 原文结束 -->」夹住，中间是源文件那几行的原样文字（7.3.json 按顶层键取整段行，
保留原缩进和逗号）；后端测试（drama/section_skills_test.go）按标记把每段对回源文件逐字比较。标记外面只有卡头的一句来源说明和
SKILL.md 末尾的「柚子流程接入说明」，这些是平台配置，不是技能正文。
"""
import argparse
import hashlib
import json
import pathlib
import re
import shutil

ROOT = pathlib.Path(__file__).resolve().parent.parent
BUILTIN = ROOT.parent / "backend" / "builtin"
PROFILES = ["action-epic", "anime-3d", "duanju-shiga", "game-ip", "live-realism"]
SHA = {
    "wenxi15": "72564e7c041b69d82d7c4425ca61faaf05697681d5be13dca522ce7af07b7fa2",
    "wenxi30": "4dfad8e3029150eb14397ca000358d60025d49b7354769154425c4d1ddada707",
    "wuxi": "0bb7fe6f626a2ae83084b51b5ef12857411e61a5f606a3f116e73f0b0b0c9fb1",
}
# 武戏两个包各自覆盖的风格档案：真人 / 短剧视感用精简切片，其余用完整切片。
WUXI_LIVE_PROFILES = ["duanju-shiga", "live-realism"]
WUXI_FULL_PROFILES = [p for p in PROFILES if p not in WUXI_LIVE_PROFILES]

# 7.3.json：SKILL.md 正文与四张必读卡各取哪些顶层键（卡 0 文档 §3e / §6.1；卡 04 导演镜头随预算放宽到 4 张卡一起带上）。
WENXI15 = {
    "skill": ["purpose", "input_contract"],
    "cards": [
        ("cards/01-输出格式.md", "输出格式", ["output_format_system", "final_output_skeleton", "minimal_output_example", "fixed_output_structure_lock_system", "style_and_emotion_system", "end_shot_station_timing_system"]),
        ("cards/02-时长与台词.md", "时长与台词", ["segment_planning_system", "dialogue_budget_system", "speech_type_classification_system", "non_dialogue_segment_system"]),
        ("cards/03-片段边界.md", "片段边界", ["reference_first_frame_timecode_system", "segment_boundary_pixel_logic_lock_system", "new_subject_and_scene_branch_system"]),
        ("cards/04-导演镜头.md", "导演镜头", ["director_camera_design_system", "dialogue_visual_decoupling_system", "camera_motion_trigger_system", "depth_of_field_decision_system", "character_state_camera_matching", "shot_size_and_lens_system", "composition_and_layering_system", "shot_rhythm_and_variation_system", "cinematography_aesthetic_system"]),
    ],
}

# 即梦剧本-分镜_优化版.txt：按行号取（含章节标题前后的「====」行；卡 0 文档 §4e / §6.1；第 4 张卡是文戏最常用的 15.2 对话戏）。
WENXI30 = {
    "skill": [(1, 60), (118, 217)],
    "cards": [
        ("cards/01-拆分与格式.md", "拆分与格式（三、四、五）", [(218, 410)]),
        ("cards/02-硬规则与任务.md", "硬规则与任务（十七、十九）", [(2852, 2900), (3062, 3108)]),
        ("cards/03-示例与模板.md", "示例与模板（十八小节 1、二十最终粘贴模板）", [(2902, 2993), (3223, 3308)]),
        ("cards/04-对话戏.md", "对话戏（15.2）", [(1046, 1219)]),
    ],
}

NOTE_WENXI15 = """## 柚子流程接入说明（平台配置，不改上面的原文）

这一段是柚子接入时追加的，只说明在柚子里怎么用这份 7.3 分镜规则书，不改上面和卡片里的任何规则。

- 用在哪：柚子「生成分段」的文戏 15 档。项目的视频模型单段上限是 15 秒时，每场戏由平台按本规则书的口径先切成小节（10～15 秒、最多两句完整台词、不在接话中间切），一个小节就是这里的一个「片段」，也是一次视频生成。
- 先读取 `cards/01-输出格式.md`、`cards/02-时长与台词.md`、`cards/03-片段边界.md` 和 `cards/04-导演镜头.md`：四张卡是 7.3.json 里对应规则的原文，按原文执行。
- 每一轮只写平台点名的那一个片段，片段号、场号、本节剧本范围和本节台词表由平台给出。不写整集完成标记、续写锚点和覆盖台账；剧本范围以外的内容不写。
- 输出用本规则书的原生 Markdown 格式（`## 片段 X（时长：XX秒）`、要素提取、当前空间站位、镜头行、最后一镜结束站位、下一片段继承点），不要改写成 JSON 或表格。
- 台词按平台给的本节台词表逐字照抄，每句只写在一个镜头里。
- 画风：项目锁定的风格（【全局风格锚】【项目画风】）优先于本规则书默认的真人写实。栏目名「参考首帧（0s-0.5s）真人写实脸」照原样写，画面内容按项目风格写。
- BGM：要素提取里的「BGM背景音乐」照规则写，保留在原样输出和分镜卡里；柚子编译视频提示词时会去掉。镜头行和 Audio：SFX 里不要写任何音乐。
- 角色、场景、道具写平台给的资产原名（同一人有子卡时写「名·变体」）。
"""

NOTE_WENXI30 = """## 柚子流程接入说明（平台配置，不改上面的原文）

这一段是柚子接入时追加的，只说明在柚子里怎么用这份即梦分镜任务说明书，不改上面和卡片里的任何规则。

- 用在哪：柚子「生成分段」的文戏 30 档。项目的视频模型单段上限超过 15 秒（Seedance 2.5 为 30 秒）时，每场戏由平台按本说明书的口径先切成小节（不超过 30 秒、不在接话中间切），一个小节就是这里的一个「小节」，也是一次视频生成。
- 先读取 `cards/01-拆分与格式.md`、`cards/02-硬规则与任务.md`、`cards/03-示例与模板.md` 和 `cards/04-对话戏.md`：四张卡是说明书对应章节的原文，按原文执行。
- 每一轮只写平台点名的那一个小节，小节号、场号、本节剧本范围和本节台词表由平台给出。参数确认、全局风格锁定、角色音色设定只在第一个小节前写一次；不写台词核对摘要和自检摘要。
- 输出用本说明书的导演分镜字段格式（小节头、每个镜头的全部字段、小节约束、站位标注、连续性总结），不要改写成 JSON 或表格。
- 跳过「即梦粘贴提示词」这一段：柚子按镜头字段自己编译视频提示词，不用它（说明书里要求写粘贴提示词的地方在柚子里都不适用）。
- 台词按平台给的本节台词表逐字照抄，每句只写在一个镜头里。
- 画风：项目锁定的风格（【全局风格锚】【项目画风】）优先于本说明书默认的 3D 动漫，按项目风格写。
- 角色、场景、道具写平台给的资产原名（同一人有子卡时写「名·变体」）。
"""


# skill-complete-v7.md：按行号取（卡 0 文档 §5a / §5e / §6.1）。前 11,645 行是 PDF 转出的文本，表格错位，照原样摘；
# v4.18 / v4.19 的重复铁律、千招库、v4.4「长对话翻译成动作」的剧本转提示词铁律都不带（与模式 D 的台词逐字冲突）。
WUXI_SKILL = [(24, 92, "第 0 部分：v5.0 技能本体与四步出稿"), (11646, 11665, "v6.0 增量补丁包：作者说明与冲突裁定表"), (12088, 12091, "v7.0 最终优先级")]
WUXI_DIRECTOR = [(12093, 12415, "v6.3 电影级打斗强化补丁 + v7.0 电影动作导演系统")]
WUXI_SHORT = [(11666, 11873, "v6.0：3.22 2D 闪帧、四·0 剧本层（模式 D）、四·分镜层 11 字段"), (12043, 12087, "v6.1：3.23 景别+站位全模板强制铁律")]
WUXI = {
    "yz-shots-wuxi": {
        "name": "yoozee · 分镜（武戏，skill-complete-v7）",
        "description": "柚子「生成分段」武戏档（分镜制作里把小节改成武戏后用）：史诗打斗提示词生成器 skill-complete-v7 的原文切片（v5.0 万能模板、v6.0 模式 D 与闪帧、v6.1 站位、v6.3 / v7.0 导演层、v7.1 画风），一个武戏小节写成 N 秒 N 镜的分镜骨架。用于 CG、仙侠、动漫、游戏项目；正文和卡片是原文按行摘出（一字不改），柚子只追加了流程接入说明。",
        "sort": 1972,
        "profiles": WUXI_FULL_PROFILES,
        "intro": "下面是 skill-complete-v7 技能本体（v5.0）的使用流程、v6.0 补丁的冲突裁定表和 v7.0 的最终优先级原文。原文前半是 PDF 转出的文本，个别列表和表格的换行错位，照原样保留。",
        "cards": [
            ("cards/01-万能模板v5.md", "v5.0 万能打斗提示词模板（参数表、全局设定、核心铁律、分镜骨架结构、SSS 一句话、负面词）", [(99, 583, "【一、参数表】～【四、分镜骨架】结构逻辑"), (1163, 1220, "【五、SSS 级一句话总合成】【六、完整负面提示词】")]),
            ("cards/02-导演层v6.3与v7.0.md", "导演层（v6.3 攻击因果与空间连续、v7.0 电影动作导演系统与每镜标准格式）", WUXI_DIRECTOR),
            ("cards/03-短剧模式D与站位闪帧.md", "短剧模式 D、2D 闪帧与景别站位（v6.0 / v6.1）", WUXI_SHORT),
            ("cards/04-画风与成品示例.md", "v7.1 电影画风预设与 v6.0 完整成品示例", [(12418, 12565, "v7.1 电影画风预设补丁"), (11981, 12020, "v6.0 完整成品示例（30 秒·1v1）")]),
        ],
    },
    "yz-shots-wuxi-live": {
        "name": "yoozee · 分镜（武戏·仿真人，skill-complete-v7 精简切片）",
        "description": "柚子「生成分段」武戏档的仿真人版（真人剧、短剧视感项目）：skill-complete-v7 去掉 UE5 画质串、仙法、魔物、能量化伤痕（v5.0 万能模板）和 v7.1 CG 画风后的原文切片，只留 v6.3 / v7.0 导演层、v6.0 模式 D 与闪帧、v6.1 站位和 v5.0 的物理与打击感铁律摘段，一个武戏小节写成 N 秒 N 镜的分镜骨架。正文和卡片是原文按行摘出（一字不改），柚子只追加了流程接入说明。",
        "sort": 1971,
        "profiles": WUXI_LIVE_PROFILES,
        "intro": "下面是 skill-complete-v7 技能本体（v5.0）的使用流程、v6.0 补丁的冲突裁定表和 v7.0 的最终优先级原文。原文前半是 PDF 转出的文本，个别列表和表格的换行错位，照原样保留。",
        "cards": [
            ("cards/01-导演层v6.3与v7.0.md", "导演层（v6.3 攻击因果与空间连续、v7.0 电影动作导演系统与每镜标准格式）", WUXI_DIRECTOR),
            ("cards/02-短剧模式D与站位闪帧.md", "短剧模式 D、2D 闪帧与景别站位（v6.0 / v6.1）", WUXI_SHORT),
            ("cards/03-物理与打击感.md", "v5.0 核心铁律摘段：打击感五件套、命中写法与物理五规、景别铁律", [(327, 328, "打击感五件套"), (386, 399, "命中五件套写法与物理五规"), (561, 568, "CU 特写占比与景别铁律")]),
        ],
    },
}

WUXI_FORMAT = """输出格式（v7.0「4.29 每镜标准格式」，柚子按字段名读，每项一行，字段名照写）：

```
【核心观看体验】{角色关系} + {战斗特点} + {高潮方向}
镜 1｜0-1s
景别 / 焦段 / 运镜：MS / 35mm / TRACK
摄影机目标：…
站位：{人物 A} 画左中景·面向右·同高；{人物 B} 画右中景·面向左·同高；相距 3m
动作链：起手→攻击→防守→变招→结果
力度：P2 中·格挡
受击：R1 上身一晃
战损：L1
闪帧：无
特效：Layer1 …
环境反馈：…
表情：{人物 A} …；{人物 B} …
台词：{人物}：“台词原文”
光影：…
下一镜连接：WHIP MATCH …
镜 2｜1-2s
…
```

- 4.29 的 12 项照写；「表情」「台词」「光影」三行是柚子另加的（分镜卡显示、台词核对和编译用），没有台词的镜写「台词：无」。
- 站位、动作链、表情、台词里的 A / B 一律写人物真名（平台在【参数表】里给了谁是 A、谁是 B）。"""

WUXI_NOTE_COMMON = """- 先读取 {cards}：卡片是 skill-complete-v7 对应章节的原文，按原文执行；冲突时按原文的优先级裁定（v7.0 > v6.3 > v6.2 > v6.1 > v6.0 > v5.0）。
- 模式：一律按「模式 D·短剧分镜」写（v6.0 冲突裁定表：模式 D 启用时零台词铁律豁免）。台词按平台给的本节台词表逐字照抄，不删、不改、不翻译成动作或拟声；一句台词可以跨相邻几个 1 秒镜头说完：写在开口的那一镜，后面几镜的「台词」写「（接上句）」。本节台词表以外的台词、战吼、拟声词不加。
- 时长：本节 N 秒 = N 镜（1 秒 1 镜），N 由平台给出（不超过项目视频模型的单段上限）；30 秒导演节拍图（4.02）按 N/30 的比例缩放，平台在本轮指令里给了缩放后的节拍。不按 10 / 15 / 30 秒模板补齐或截断。
- 参数表、全局设定、核心铁律、SSS 一句话和负面词由柚子按项目数据编译视频提示词时处理：每一轮只写【核心观看体验】一句和本节的分镜骨架，不写这几段，也不写 JSON、代码块、自检清单或完成标记。
- 画风：项目锁定的风格（【全局风格锚】【项目画风】）优先于本技能自带的 CG 画风假设（UE5.4 画质串、CG 国风 / 仙侠动画、v7.1 黄昏暖金逆光等）；两者冲突时按项目风格写光影、材质和特效。
- 不写任何 BGM、配乐、背景音乐。
- 角色、场景、道具写平台给的资产原名（同一人有子卡时写「名·变体」）；站位的地标优先用场景资产的锚点名。

"""

WUXI_NOTE = {
    "yz-shots-wuxi": "## 柚子流程接入说明（平台配置，不改上面的原文）\n\n这一段是柚子接入时追加的，只说明在柚子里怎么用 skill-complete-v7，不改上面和卡片里的任何规则。\n\n"
    "- 用在哪：柚子「生成分段」的武戏档。分镜制作里用户把某个小节改成武戏后，平台按武戏口径把这一节的剧本范围重新切成 1～N 个武戏小节（每节不超过项目视频模型的单段上限），一个小节就是一次视频生成。本包用于 CG、仙侠、动漫、游戏项目；真人剧、短剧视感项目用精简切片 yz-shots-wuxi-live。\n"
    "- 绑定：本包是武戏档的默认包。后台「流程技能」页武戏列里，史诗打斗（action-epic）、3D 动漫（anime-3d）、游戏 IP 战斗（game-ip）以及没有单独绑定的风格档案都用本包；真人剧（live-realism）、短剧视感（duanju-shiga）没有单独绑定时用 yz-shots-wuxi-live。管理员在武戏列改绑后以绑定为准。\n",
    "yz-shots-wuxi-live": "## 柚子流程接入说明（平台配置，不改上面的原文）\n\n这一段是柚子接入时追加的，只说明在柚子里怎么用 skill-complete-v7，不改上面和卡片里的任何规则。\n\n"
    "- 用在哪：柚子「生成分段」的武戏档，仿真人项目（真人剧、短剧视感）专用。分镜制作里用户把某个小节改成武戏后，平台按武戏口径把这一节的剧本范围重新切成 1～N 个武戏小节（每节不超过项目视频模型的单段上限），一个小节就是一次视频生成。\n"
    "- 绑定：真人剧（live-realism）、短剧视感（duanju-shiga）在后台「流程技能」页武戏列没有单独绑定时用本包；其余风格档案（史诗打斗、3D 动漫、游戏 IP 战斗）和没有单独绑定的档案用默认包 yz-shots-wuxi。管理员在武戏列改绑后以绑定为准。\n"
    "- 本包是精简切片：不带 v5.0 万能模板里的 UE5 画质串、仙法、魔物、能量化伤痕和 v7.1 CG 画风。打斗按真人实拍的物理写：没有仙法、剑气和魔物；特效层只写真实的物理反馈（尘土、碎屑、水花、布料），闪帧照 3.22 嵌入。\n",
}


def build_wuxi(path):
    lines = read_source(path, "wuxi")
    src = "skill-complete-v7.md"
    for skill_id, spec in WUXI.items():
        skill = frontmatter(skill_id, spec["name"], spec["description"], spec["sort"])
        body = ["# 分镜（武戏）：skill-complete-v7", "", spec["intro"], ""]
        for a, b, label in WUXI_SKILL:
            body += [excerpt(src, lines, a, b, label), ""]
        cards = "、".join(f"`{rel}`" for rel, _, _ in spec["cards"])
        note = WUXI_NOTE[skill_id] + WUXI_NOTE_COMMON.format(cards=cards) + WUXI_FORMAT + "\n"
        files = {"SKILL.md": skill + "\n" + "\n".join(body) + note}
        for rel, title, spans in spec["cards"]:
            parts = [f"# skill-complete-v7 · {title}", "", "以下是 skill-complete-v7.md 对应章节的原文（按行摘出，一字不改；前半是 PDF 转出的文本，换行错位照原样保留）。", ""]
            for a, b, label in spans:
                parts += [excerpt(src, lines, a, b, label), ""]
            files[rel] = "\n".join(parts)
        manifest = {"id": skill_id, "step": "chapter.shots#wuxi", "contract": "section.wuxi@1", "profiles": spec["profiles"], "version": "1.0.0", "files": sorted(files), "eval": []}
        write_package(skill_id, files, manifest)
        total = sum(len(v) for v in files.values())
        print(f"{skill_id}: {total} 字（SKILL.md + {len(spec['cards'])} 张卡）")


def frontmatter(skill_id, name, description, sort_weight):
    return f"""---
name: "{name}"
description: "{description}"
metadata:
  version: "1.0.0"
  author: "柚子"
  owner: "yingce-system"
  skillId: "{skill_id}"
  tag: drama
  sortWeight: {sort_weight}
  source: 3
  createdAt: 1793059200000
  updatedAt: 1793059200000
  initialLikeCount: 0
  initialAddedCount: 0
  extraInfo: ""
---
"""


def excerpt(source_name, lines, a, b, label=""):
    """源文件第 a～b 行（从 1 数，含两端）的原样文字，夹在标记里。"""
    body = "\n".join(lines[a - 1 : b])
    tail = f"（{label}）" if label else ""
    span = f"{a}" if a == b else f"{a}～{b}"
    return f"<!-- 原文：{source_name} 第 {span} 行{tail} -->\n{body}\n<!-- 原文结束 -->"


def key_ranges(lines):
    """7.3.json 每个顶层键占的行（第一行是「  \"键\": 」起头，到下一个键之前；最后一个键不含收尾的「}」）。"""
    starts = [(i + 1, m.group(1)) for i, l in enumerate(lines) if (m := re.match(r'^  "([a-z0-9_]+)": ', l))]
    out = {}
    for j, (start, key) in enumerate(starts):
        end = starts[j + 1][0] - 1 if j + 1 < len(starts) else len(lines) - 1
        while end > start and lines[end - 1].strip() == "":
            end -= 1
        out[key] = (start, end)
    return out


def merged_ranges(keys, ranges):
    """几个键按行号排好，前一个的末行紧挨后一个的首行时并成一段。"""
    out = []
    for key in sorted(keys, key=lambda k: ranges[k][0]):
        a, b = ranges[key]
        if out and out[-1][1] + 1 == a:
            out[-1] = (out[-1][0], b, out[-1][2] + [key])
        else:
            out.append((a, b, [key]))
    return out


def read_source(path, which):
    raw = pathlib.Path(path).read_bytes()
    digest = hashlib.sha256(raw).hexdigest()
    if digest != SHA[which]:
        raise SystemExit(f"{path} 的 SHA-256 是 {digest}，与卡 0 记录的 {SHA[which]} 不一致")
    text = raw.decode("utf-8")
    if "\r" in text:
        raise SystemExit(f"{path} 有 CR 换行，切片规则按 LF 写的")
    return text.split("\n")


def write_package(skill_id, files, manifest):
    for root in (ROOT / "packages" / skill_id, BUILTIN / skill_id):
        if root.exists():
            shutil.rmtree(root)
        for rel, content in files.items():
            p = root / rel
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(content, encoding="utf-8", newline="\n")
        (root / "package.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


def build_wenxi15(path):
    lines = read_source(path, "wenxi15")
    json.loads("\n".join(lines))
    ranges = key_ranges(lines)
    src = "7.3.json"
    skill = frontmatter(
        "yz-shots-wenxi15",
        "yoozee · 分镜（文戏 15，7.3 规则书）",
        "柚子「生成分段」文戏 15 档专用（视频模型单段上限 15 秒）：真人短剧分镜智能体 7.3 的规则书原文切片，一个小节写成一个 10～15 秒的片段。正文和卡片是 7.3.json 原文（按顶层键整段摘出，一字不改），柚子只追加了流程接入说明。",
        1974,
    )
    body = ["# 分镜（文戏 15）：7.3 规则书", "", "下面是 7.3.json 里用途与输入约定的原文（JSON 片段，键名与值一字不改）。", ""]
    for key in WENXI15["skill"]:
        a, b = ranges[key]
        body += ["```json", excerpt(src, lines, a, b, key), "```", ""]
    files = {"SKILL.md": skill + "\n" + "\n".join(body) + NOTE_WENXI15}
    for rel, title, keys in WENXI15["cards"]:
        parts = [f"# 7.3 规则书 · {title}", "", "以下是 7.3.json 的原文片段（按原文件的顺序、缩进摘出，键名与值一字不改；行号相连的键并成一段）。", ""]
        for a, b, names in merged_ranges(keys, ranges):
            parts += ["```json", excerpt(src, lines, a, b, "、".join(names)), "```", ""]
        files[rel] = "\n".join(parts)
    manifest = {"id": "yz-shots-wenxi15", "step": "chapter.shots#wenxi15", "contract": "section.wenxi15@1", "profiles": PROFILES, "version": "1.0.0", "files": sorted(files), "eval": []}
    write_package("yz-shots-wenxi15", files, manifest)


def build_wenxi30(path):
    lines = read_source(path, "wenxi30")
    src = "即梦剧本-分镜_优化版.txt"
    skill = frontmatter(
        "yz-shots-wenxi30",
        "yoozee · 分镜（文戏 30，即梦小节分镜）",
        "柚子「生成分段」文戏 30 档专用（视频模型单段上限 30 秒）：《即梦剧本-分镜_优化版》任务说明书原文切片，一个小节写成一个不超过 30 秒的即梦小节（导演分镜字段；柚子自己编译视频提示词，不写即梦粘贴提示词）。正文和卡片是原文按行摘出（一字不改），柚子只追加了流程接入说明。",
        1973,
    )
    body = ["# 分镜（文戏 30）：即梦小节分镜", "", "下面是《即梦剧本-分镜_优化版》执行说明、官方格式校准、第一原则和全局参数四节的原文。", ""]
    for a, b in WENXI30["skill"]:
        body += [excerpt(src, lines, a, b), ""]
    files = {"SKILL.md": skill + "\n" + "\n".join(body) + NOTE_WENXI30}
    for rel, title, spans in WENXI30["cards"]:
        parts = [f"# 即梦小节分镜 · {title}", "", "以下是《即梦剧本-分镜_优化版》对应章节的原文（按行摘出，一字不改）。", ""]
        for a, b in spans:
            parts += [excerpt(src, lines, a, b), ""]
        files[rel] = "\n".join(parts)
    manifest = {"id": "yz-shots-wenxi30", "step": "chapter.shots#wenxi30", "contract": "section.wenxi30@1", "profiles": PROFILES, "version": "1.0.0", "files": sorted(files), "eval": []}
    write_package("yz-shots-wenxi30", files, manifest)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--wenxi15")
    parser.add_argument("--wenxi30")
    parser.add_argument("--wuxi")
    args = parser.parse_args()
    if not (args.wenxi15 or args.wenxi30 or args.wuxi):
        parser.error("至少给一个源文件")
    if args.wenxi15:
        build_wenxi15(args.wenxi15)
    if args.wenxi30:
        build_wenxi30(args.wenxi30)
    if args.wuxi:
        build_wuxi(args.wuxi)


if __name__ == "__main__":
    main()
