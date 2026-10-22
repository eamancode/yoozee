#!/usr/bin/env python3
"""方言编译（prompt.compile）：中性分段 → 所选模型的提示词与参数，并检查段长、参考数量、首尾帧互斥、字数。
  python3 {{TOOLS}}/compile.py S03 [--model seedance-2.5] [--profile live-realism] [--file 剧本/分段.json] [--paste] [--out 生成/S03_prompt.json]
  python3 {{TOOLS}}/compile.py --json 段.json --model seedance-2.5
  python3 {{TOOLS}}/compile.py all [--model ...]          整张分段表逐段检查，只报问题
不给 --model 就读需求文档的生成模型；不给 --profile 就读需求文档的风格档案（没有需求文档时用 1.0 的默认风格块）。有 error 级问题时退出码 2。

编译是确定性的：同一段、同一模型、同一风格块，结果逐字相同。柚子后端的 compile_prompt 用同一套规则（Go 实现），
两边共用 yoozee-skills/tests/fixtures 下的样例做对照测试。2.0 的新字段（refs[].state、continuity_bridge、warnings / risk_tags、profile 风格块）
只在段里出现时才改变输出，1.0 的样例输出不变。"""
import argparse
import json
import re
import math
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

HERE = os.path.dirname(os.path.abspath(__file__))


def _block(name):
    with open(os.path.join(HERE, name), encoding="utf-8") as f:
        return f.read().strip()


def blocks(dialect, profile=None):
    """风格块和表演块：Seedance 用中文，MiniMax-H3 用英文（实测稳定）。给了风格档案就用档案的 style_block / acting_block。"""
    if profile:
        sb, ab = profile.get("style_block") or {}, profile.get("acting_block") or {}
        if dialect == "seedance":
            return sb.get("zh") or sb.get("en") or "", ab.get("zh") or ab.get("en") or ""
        return sb.get("en") or sb.get("zh") or "", ab.get("en") or ab.get("zh") or ""
    if dialect == "seedance":
        return _block("video_style.zh.txt"), _block("acting.zh.txt")
    return _block("video_style.txt"), _block("acting.txt")


def dialect_of(model_id):
    return "seedance" if model_id.startswith("seedance") else "minimax-h3"


def _sec(x):
    """秒数显示：整数不带小数点，其余保留到 0.1。"""
    x = round(float(x), 1)
    return str(int(x)) if x == int(x) else f"{x:.1f}"


def _mmss(sec):
    return f"{int(sec // 60):02d}:{sec % 60:06.3f}"


SEEDANCE_ROLE = {
    "identity": "@图片{i}为人物参考（{who}；脸、发型、小特征和服装照此，忽略灰色背景）",
    "location": "@图片{i}为场景参考（布局、材质、颜色和光线照此，图中无人）{tail}",
    "plate": "@图片{i}为这一机位身后的背景{tail}",
    "prop": "@图片{i}为道具参考{tail}",
    "frame": "@图片{i}只作构图参考（取景、光线、位置），人物的脸以人物参考为准{tail}",
}
H3_ROLE = {
    "identity": "is the exact identity reference of {note}: match the face, hairstyle and distinguishing marks exactly and wear exactly what the reference wears; ignore its plain grey background",
    "location": "is the location reference: keep its layout, materials, colours and light; it contains no people. {note}",
    "plate": "is the background for one camera side: {note}",
    "prop": "is the exact prop: {note}",
    "frame": "is a composition reference only (framing, light, seating); the faces must follow the identity references, not this picture. {note}",
}
SEEDANCE_CONSTRAINT = "约束：人物与参考一致、场景连续、光源方向不变、口型与台词同步、无字幕、无水印、除参考里已有的字外不出现可读文字。"
REANCHOR = "（再锚点：人物形象、服装与光源方向与开头一致）"
BRIDGE_FRAME_NOTE = "上一段的末帧：构图、人物位置与此刻状态照此，人脸以人物参考为准"
BRIDGE_CLIP_NOTE = "上一段末尾的运镜与姿态连续参考，不复刻其中的人物与场景"

# risk_tags → ⚠️ 文案（只追加本段真有的风险，不堆通用负面词；来源 Higgsfield 失败模式警告 / camera-codec 排查表 / 即梦 16.8）
RISK_WARNINGS = {
    "limb_distortion": "⚠️肢体结构正确：五指清晰、关节不畸形、身体不僵硬、不扭曲。",
    "model_clip": "⚠️武器、头发、身体不穿模，膝盖不穿透地面，身体不互相穿透。",
    "physics": "⚠️动作符合物理：脚踏实地、不同手同脚、重心真实、步伐不僵硬。",
    "bg_extras": "⚠️背景不出现无关人物，背景虚化适度。",
    "proportion": "⚠️两人身体比例与服装风格一致。",
    "handle_contamination": "⚠️各参考只按声明的用途使用：人物参考只取人物，场景参考只取场景，不把人物参考里的背景或场景参考里的任何人形带进画面。",
    "identity_drift": "⚠️人物面部、发型、小特征与服装全程与人物参考一致，不随景别和光线变化。",
    "light_spill": "⚠️屏幕、灯具只作为画面里发光的物件，不向人物皮肤与服装投射大面积色光。",
    "prop_misplace": "⚠️道具位置与状态按描述固定，不凭空出现或消失，不复制成多件。",
    "insert_focus_drift": "⚠️插入镜头的焦点落在道具与手上，不漂到背景。",
    "closeup_rotation": "⚠️近景时机位不做大幅环绕或旋转，运动幅度小、匀速。",
    "three_axis": "⚠️每个时间段只做一到两种运镜，不同时推拉、升降、环绕。",
    "phantom_across_barrier": "⚠️河流、墙、门、车辆等阻隔物的另一侧没有人，除非描述里写了。",
    "readable_text": "⚠️除参考里已有的字外不出现任何可读文字、字幕、水印。",
    "group_same_face": "⚠️群像人物面孔各不相同，按描述的差异化特征区分，不复制同一张脸。",
    "big_action": "⚠️大动作（走路、起身、转身）完整、连贯、重心真实，不穿模、不多出肢体。",
    "long_take_drift": "⚠️段落较长：人物形象、服装、光源方向在后半段与开头完全一致。",
}


def _risk_tags(seg):
    tags = list(seg.get("warnings") or [])
    for sh in seg.get("shots", []):
        tags += sh.get("risk_tags") or []
    out = []
    for t in tags:
        if t in RISK_WARNINGS and t not in out:
            out.append(t)
    return out


def _issues(seg, m):
    out = []
    dur = float(seg.get("dur", 0))
    lo, hi, step = m["duration"]["min"], m["duration"]["max"], m["duration"].get("step") or 1
    if dur > hi:
        out.append(("error", f"{seg['id']} 时长 {_sec(dur)} 秒，超过 {m['label']} 一段的上限 {hi} 秒：拆成两段（yz-adapt）"))
    elif dur < lo:
        out.append(("warn", f"{seg['id']} 时长 {_sec(dur)} 秒，低于 {m['label']} 的下限 {lo} 秒：按 {lo} 秒出再剪"))
    shots = seg.get("shots", [])
    last = -1.0
    for k, sh in enumerate(shots, 1):
        at = float(sh.get("at", 0))
        if at <= last and k > 1:
            out.append(("error", f"{seg['id']} 第 {k} 个镜头的开始秒 {at} 不大于上一个"))
        if at >= dur and dur > 0:
            out.append(("error", f"{seg['id']} 第 {k} 个镜头从 {at} 秒开始，超出段长 {_sec(dur)} 秒"))
        last = at
    mode = seg.get("mode", "lock_identity")
    bridge = seg.get("continuity_bridge") or {}
    has_refs = bool(seg.get("refs") or seg.get("voices") or bridge.get("bridge_frame") or bridge.get("bridge_clip"))
    if mode == "lock_first_frame":
        if not seg.get("first_frame"):
            out.append(("error", f"{seg['id']} 是锁首帧模式但没有 first_frame"))
        if has_refs:
            ex = m.get("frames_exclusive_with_refs")
            if ex is True:
                out.append(("error", f"{seg['id']} 锁首帧时不能同时挂参考图、参考视频或参考音频（{m['label']} 互斥）"))
            elif ex is None:
                out.append(("warn", f"{seg['id']} 锁首帧又挂了参考：{m['label']} 是否互斥尚未校准，参考可能被忽略"))
    else:
        refs = list(seg.get("refs", []))
        imgs = [r for r in refs if r.get("role") != "motion"] + ([1] if bridge.get("bridge_frame") else [])
        vids = [r for r in refs if r.get("role") == "motion"] + ([1] if bridge.get("bridge_clip") else [])
        auds = seg.get("voices", [])
        lim = m.get("refs", {})
        for name, have, cap in (("参考图", len(imgs), lim.get("images")), ("参考视频", len(vids), lim.get("videos")), ("参考音频", len(auds), lim.get("audios"))):
            if cap is not None and have > cap:
                out.append(("error", f"{seg['id']} {name} {have} 个，超过 {m['label']} 的上限 {cap}"))
        total = lim.get("total")
        if total is not None and len(imgs) + len(vids) + len(auds) > total:
            out.append(("error", f"{seg['id']} 参考素材合计 {len(imgs) + len(vids) + len(auds)} 个，超过 {m['label']} 的上限 {total}"))
        files_total = m.get("files_total")
        if files_total is not None and len(imgs) + len(vids) + len(auds) > files_total:
            out.append(("error", f"{seg['id']} 参考文件合计 {len(imgs) + len(vids) + len(auds)} 个，超过 {m['label']} 的混合上限 {files_total}"))
    return out


def segment_sound(seg, sep):
    """这一段的声音：段级 sound；没写时按镜头顺序接各镜头的 sound（去掉句尾标点、去重，用 sep 分隔）。
    声音记在镜头上，拆段、合段、移镜头之后跟着镜头走。与柚子后端 Segment.SoundText 一致。"""
    sound = (seg.get("sound") or "").strip()
    if sound:
        return sound
    parts = []
    for sh in seg.get("shots", []):
        for part in re.split(r"[；;]", (sh.get("sound") or "")):  # 一镜多条声音按分号拆开再去重，重复的环境声只写一次
            part = part.strip().rstrip("。；;，,. ")
            if part and part not in parts:
                parts.append(part)
    return sep.join(parts)


def segment_rules(seg):
    """这一段写死的约束：段级 rules；没写时取最后一镜的 end_state（这一段结束时的连戏状态）。与 Segment.RulesText 一致。"""
    rules = (seg.get("rules") or "").strip()
    if rules:
        return rules
    shots = seg.get("shots") or []
    return (shots[-1].get("end_state") or "").strip() if shots else ""


def _submit_duration(dur, m):
    lo, hi, step = m["duration"]["min"], m["duration"]["max"], m["duration"].get("step") or 1
    n = math.ceil(round(float(dur), 3) / step) * step
    return int(max(lo, min(hi, n)))


def compile_seedance(seg, m, style, acting):
    mode = seg.get("mode", "lock_identity")
    bridge = seg.get("continuity_bridge") or {}
    decl, files = [], []
    if mode == "lock_first_frame":
        decl.append("以@图片1为首帧（构图、人物、服装和光线照此）")
        files.append({"file": seg.get("first_frame", ""), "tag": "@图片1", "role": "first_frame"})
        if seg.get("last_frame"):
            decl.append("以@图片2为尾帧，视频结束在这一画面")
            files.append({"file": seg["last_frame"], "tag": "@图片2", "role": "last_frame"})
    elif mode == "lock_identity":
        i = j = 0
        for c in seg.get("cast", []):
            decl.append(f"人物·{c['name']}：" + "，".join(c["lock"]))
        for r in seg.get("refs", []):
            note = (r.get("note") or "").strip()
            if r.get("state"):
                note = (note + "；" if note else "") + f"此刻{r['state'].strip()}"
            if r.get("role") == "motion":
                j += 1
                decl.append(f"@视频{j}为运镜参考" + (f"：{note}" if note else ""))
                files.append({"file": r["file"], "tag": f"@视频{j}", "role": "motion"})
                continue
            i += 1
            tpl = SEEDANCE_ROLE.get(r.get("role"), SEEDANCE_ROLE["frame"])
            decl.append(tpl.format(i=i, who=note or "该人物", tail=(f"：{note}" if note else "")))
            files.append({"file": r["file"], "tag": f"@图片{i}", "role": r.get("role")})
        if bridge.get("bridge_frame"):
            i += 1
            decl.append(SEEDANCE_ROLE["frame"].format(i=i, tail=f"：{BRIDGE_FRAME_NOTE}"))
            files.append({"file": bridge["bridge_frame"], "tag": f"@图片{i}", "role": "frame"})
        if bridge.get("bridge_clip"):
            j += 1
            decl.append(f"@视频{j}为运镜参考：{BRIDGE_CLIP_NOTE}")
            files.append({"file": bridge["bridge_clip"], "tag": f"@视频{j}", "role": "motion"})
        for k, v in enumerate(seg.get("voices", []), 1):
            decl.append(f"@音频{k}为{v['character']}的音色参考（只取音色，不复用其中的话）")
            files.append({"file": v["file"], "tag": f"@音频{k}", "role": "voice"})
    if mode != "lock_identity":  # 纯文字 / 首帧模式：没有身份参考图的人物用外貌声明兜底
        for c in seg.get("cast", []):
            decl.append(f"人物·{c['name']}：" + "，".join(c["lock"]))
    dur = _submit_duration(seg["dur"], m)
    head = ("；".join(decl) + "；" if decl else "") + f"生成{dur}秒视频。整体风格：{style}{acting}"
    reanchor_text = (bridge.get("reanchor_text") or "").strip()
    if reanchor_text:
        head += reanchor_text if reanchor_text.endswith(("。", "！", "？")) else reanchor_text + "。"
    head += segment_rules(seg)
    lines = [head]
    shots = seg["shots"]
    anchor_k = None
    if m["id"] == "seedance-2.5" and float(seg["dur"]) > 15 and len(shots) > 1:
        mid = float(seg["dur"]) / 2
        anchor_k = min(range(1, len(shots)), key=lambda k: abs(float(shots[k].get("at", 0)) - mid))
    for k, sh in enumerate(shots):
        start = float(sh.get("at", 0))
        end = float(shots[k + 1].get("at", 0)) if k + 1 < len(shots) else float(seg["dur"])
        text = sh["text"].strip()
        lead = "，".join(x.strip() for x in (sh.get("size"), sh.get("camera")) if x and x.strip())
        if lead:
            text = f"{lead}。{text}"
        if not text.endswith(("。", "！", "？", ".", "!", "?")):
            text += "。"
        for ln in sh.get("lines", []):
            text += line_text(ln)
        if k == anchor_k:
            text = REANCHOR + text
        lines.append(f"{_sec(start)}-{_sec(end)}秒：{text}")
    sound = segment_sound(seg, "；").rstrip("。")
    bed = (bridge.get("shared_bed") or "").strip().rstrip("。")
    if bed:
        parts = [x.strip() for x in sound.split("；") if x.strip()]
        sound = "；".join([bed + "（与上一段相同、不中断）"] + [x for x in parts if x != bed])
    lines.append(f"声音：{sound or '环境声与动作音效'}；不要背景音乐和配乐。")
    for t in _risk_tags(seg):
        if t == "readable_text" and seg.get("screen_text"):
            lines.append("⚠️画面里只出现这些登记过的文字：" + "、".join(f"「{x}」" for x in seg["screen_text"]) + "，写法要正确；其他地方不出现任何可读文字、字幕、水印。")
        else:
            lines.append(RISK_WARNINGS[t])
    gv = (seg.get("jimeng") or {}).get("group_variation")
    if gv:
        lines.append("⚠️群像差异化：" + gv + "；禁止同脸。")
    constraint = SEEDANCE_CONSTRAINT
    if seg.get("screen_text"):
        constraint = constraint.replace("除参考里已有的字外不出现可读文字", "除登记的文字外不出现可读文字")
    if seg.get("cast"):
        constraint = constraint.replace("人物与参考一致", "人物外貌全程与开头的人物声明一致")
    lang = (seg.get("lang") or "").strip()
    if lang and lang.lower() not in ("chinese", "中文", "zh"):
        constraint += f"台词用{lang}说。"
    lines.append(constraint)
    return "\n".join(lines), files, dur


def compile_h3(seg, m, style, acting):
    mode = seg.get("mode", "lock_identity")
    bridge = seg.get("continuity_bridge") or {}
    files = []
    rules = segment_rules(seg)
    reanchor_text = (bridge.get("reanchor_text") or "").strip()
    head = style + " " + acting + " " + (reanchor_text + " " if reanchor_text else "") + (rules + " " if rules else "")
    body = "integrated_multimodal_description:\n"
    speakers = []
    if mode == "lock_first_frame":
        files.append({"file": seg.get("first_frame", ""), "tag": "first_frame", "role": "first_frame"})
        head += "Keep the exact framing, faces, wardrobe and lighting of the first frame. "
        if seg.get("last_frame"):
            files.append({"file": seg["last_frame"], "tag": "last_frame", "role": "last_frame"})
            head += "The video must end exactly on the last frame. "
    elif mode == "lock_identity":
        roles = []
        refs = list(seg.get("refs", []))
        if bridge.get("bridge_frame"):
            refs.append({"file": bridge["bridge_frame"], "role": "frame", "note": "last frame of the previous segment: keep framing, positions and the current state; faces follow the identity references"})
        for i, r in enumerate(refs, 1):
            note = (r.get("note") or "")
            if r.get("state"):
                note = (note + "; " if note else "") + f"current state: {r['state'].strip()}"
            files.append({"file": r["file"], "tag": f"<Picture {i}>", "role": r.get("role")})
            roles.append(f"<Picture {i}> " + H3_ROLE.get(r.get("role"), "{note}").format(note=note).strip())
        for k, v in enumerate(seg.get("voices", []), 1):
            speakers.append(v["character"])
            files.append({"file": v["file"], "tag": f"<Audio {k}>", "role": "voice"})
            roles.append(f"<Audio {k}> gives only the sound of the voice (timbre) of {v['character']} (S{k})")
        if roles:
            body += "Reference roles: " + "; ".join(roles) + "."
            body += (" How each line is delivered (volume, pitch, emotion, pace, breath) comes only from the acting described in the scene, never from the audio references; do not reuse any words from them." if speakers else "") + "\n"
    lang = seg.get("lang") or "Chinese"
    for k, sh in enumerate(seg["shots"], 1):
        line = (head if k == 1 else "") + sh["text"].strip()
        for ln in sh.get("lines", []):
            s_idx = speakers.index(ln["speaker"]) + 1 if ln["speaker"] in speakers else 1
            vo = " [off-screen voiceover]" if ln.get("vo") else ""
            how = (ln.get("delivery") or "").strip().rstrip(".,。，")
            line += f"{vo} (S{s_idx}), {how}:\n<d>[{lang}] {ln['text']}</d>" if how else f"{vo} (S{s_idx}):\n<d>[{lang}] {ln['text']}</d>"
        body += (f"[Shot {k}] " if k == 1 else f"[Shot {k}] At {_mmss(float(sh.get('at', 0)))}, ") + line + "\n"
    body += "\noverall_soundscape: " + (segment_sound(seg, "; ") or "quiet room tone") + "\n\nnon_diegetic_music: N/A"
    warns = [RISK_WARNINGS[t] for t in _risk_tags(seg)]
    if warns:
        body += "\n\nconstraints: " + " ".join(warns)
    return body, files, _submit_duration(seg["dur"], m)


def paste_version(result):
    """即梦网页粘贴版：提示词 + 上传清单（哪个文件对应哪个编号）。"""
    ups = "；".join(f"{f['tag']} ← {f['file']}（{f['role']}）" for f in result["files"])
    return result["prompt"] + ("\n\n【上传素材，按编号顺序】" + ups if ups else "") + f"\n【时长】{result['duration']} 秒"


def line_text(ln):
    """一句台词：三元齐全时写即梦四元「音色 + 语速 + 语调 + 情感」，否则沿用 delivery 一句话。"""
    vo = "画外音，嘴不动" if ln.get("vo") else ""
    if ln.get("pace") and ln.get("tone") and ln.get("emotion"):
        how = [p for p in (vo, (f"音色：{ln['timbre']}" if ln.get("timbre") else ""), f"语速：{ln['pace']}", f"语调：{ln['tone']}", f"情感：{ln['emotion']}", (ln.get("placement") or "")) if p]
        return f"{ln['speaker']}（{'；'.join(how)}）：“{ln['text']}”"
    how = [p for p in (vo, (ln.get("delivery") or "").strip().rstrip("。，,.")) if p]
    return f"{ln['speaker']}（{'，'.join(how)}）：“{ln['text']}”" if how else f"{ln['speaker']}：“{ln['text']}”"


def compile_segment(seg, m, style=None, acting=None, profile=None):
    """返回 {segment, model, duration, prompt, paste, files, chars, warnings, issues}。issues 里有 error 就不能提交。"""
    d = dialect_of(m["id"])
    s0, a0 = blocks(d, profile)
    style, acting = (style if style is not None else s0), (acting if acting is not None else a0)
    issues = _issues(seg, m)
    if d == "seedance":
        prompt, files, dur = compile_seedance(seg, m, style, acting)
    else:
        prompt, files, dur = compile_h3(seg, m, style, acting)
    cap = m.get("prompt_max_chars_measured") if m.get("prompt_max_chars_measured") is not None else m.get("prompt_max_chars")
    soft_cap = None if cap is not None else 4000
    if soft_cap and len(prompt) > soft_cap:
        issues.append(("warn", f"{seg['id']} 提示词 {len(prompt)} 字符，超过软预算 {soft_cap}（{m['label']} 上限未实测，先按软预算控长）"))
    if cap is not None and len(prompt) > cap:
        issues.append(("error", f"{seg['id']} 提示词 {len(prompt)} 字符，超过 {m['label']} 的上限 {cap}：先压缩风格词和重复场景词，不删台词和关键动作"))
    if float(seg.get("dur", 0)) != dur and not any(lvl == "error" and "超过" in msg and "时长" in msg for lvl, msg in issues):
        issues.append(("warn", f"{seg['id']} 按 {dur} 秒提交（模型时长步长为 {m['duration'].get('step') or 1} 秒）"))
    result = {"segment": seg["id"], "model": m["id"], "duration": dur, "prompt": prompt, "files": files, "chars": len(prompt),
              "warnings": [RISK_WARNINGS[t] for t in _risk_tags(seg)],
              "issues": [{"level": lvl, "message": msg} for lvl, msg in issues]}
    result["paste"] = paste_version(result) if d == "seedance" else prompt
    return result


def main():
    import common as C
    import segments as S
    ap = argparse.ArgumentParser()
    ap.add_argument("seg", nargs="?", help="段号，或 all")
    ap.add_argument("--model")
    ap.add_argument("--profile", help="风格档案 id；默认读需求文档的 style_profile")
    ap.add_argument("--file", default="剧本/分段.json")
    ap.add_argument("--json", help="直接给一段中性分段的 JSON 文件")
    ap.add_argument("--style-file", help="换掉默认风格块（优先于档案）")
    ap.add_argument("--paste", action="store_true", help="输出即梦粘贴版（提示词 + 上传清单）")
    ap.add_argument("--out")
    a = ap.parse_args()
    m = C.model(a.model or C.project_model())
    prof = None
    pid = a.profile or (C.project_brief() or {}).get("style_profile")
    if pid:
        prof = C.profile(pid)
    style = open(a.style_file, encoding="utf-8").read().strip() if a.style_file else None
    if a.json:
        segs = [C.read_json(a.json)]
    else:
        path = a.file
        if not os.path.exists(C.abspath(path)) and os.path.exists(C.abspath("剧本/分段表.md")):
            path = "剧本/分段表.md"
        segs = S.load(path)
        if a.seg and a.seg != "all":
            segs = [s for s in segs if s["id"] == a.seg]
            if not segs:
                sys.exit(f"分段表里没有 {a.seg}")
    bad = False
    results = []
    for seg in segs:
        r = compile_segment(seg, m, style=style, profile=prof)
        results.append(r)
        bad = bad or any(i["level"] == "error" for i in r["issues"])
        if a.seg == "all":
            for i in r["issues"]:
                print(("✗ " if i["level"] == "error" else "⚠ ") + i["message"])
        else:
            print(f"== {r['segment']} · {m['label']} · {r['duration']} 秒 · {r['chars']} 字符" + (f" · 档案 {pid}" if pid else ""))
            print(r["paste"] if a.paste else r["prompt"])
            if r["files"] and not a.paste:
                print("参考：" + "；".join(f"{f['tag']} {f['file']}（{f['role']}）" for f in r["files"]))
            for i in r["issues"]:
                print(("✗ " if i["level"] == "error" else "⚠ ") + i["message"])
    if a.seg == "all" and not bad:
        print(f"✓ {len(segs)} 段都能在 {m['label']} 上编译")
    if a.out:
        C.write_json(C.abspath(a.out), results if len(results) > 1 else results[0])
    sys.exit(2 if bad else 0)


if __name__ == "__main__":
    main()
