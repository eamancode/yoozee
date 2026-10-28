#!/usr/bin/env python3
"""按小节生成（M8 卡 F）的两套文戏分镜技能包：从资产库原文按卡 0 文档的切片取段，原文一字不改。

  python yoozee-skills/tools/slice_section_skills.py \
      --wenxi15 "E:/AI影视/资产/分镜-文戏15/7.3.json" \
      --wenxi30 "E:/AI影视/资产/分镜-文戏30/即梦剧本-分镜_优化版.txt"

写出 yoozee-skills/packages/yz-shots-wenxi15、yz-shots-wenxi30，并同步到 backend/builtin/（内置种子）。
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
}

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
- 输出用本说明书的导演分镜字段格式（小节头、每个镜头的全部字段、小节约束、站位标注、连续性总结、即梦粘贴提示词），不要改写成 JSON 或表格。柚子按镜头字段编译视频提示词。
- 台词按平台给的本节台词表逐字照抄，每句只写在一个镜头里。
- 画风：项目锁定的风格（【全局风格锚】【项目画风】）优先于本说明书默认的 3D 动漫，按项目风格写。
- 角色、场景、道具写平台给的资产原名（同一人有子卡时写「名·变体」）。
"""


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
        "柚子「生成分段」文戏 30 档专用（视频模型单段上限 30 秒）：《即梦剧本-分镜_优化版》任务说明书原文切片，一个小节写成一个不超过 30 秒的即梦小节（导演分镜字段 + 即梦粘贴提示词）。正文和卡片是原文按行摘出（一字不改），柚子只追加了流程接入说明。",
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
    parser.add_argument("--wenxi15", required=True)
    parser.add_argument("--wenxi30", required=True)
    args = parser.parse_args()
    build_wenxi15(args.wenxi15)
    build_wenxi30(args.wenxi30)


if __name__ == "__main__":
    main()
