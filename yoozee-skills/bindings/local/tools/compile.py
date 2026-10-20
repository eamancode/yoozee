#!/usr/bin/env python3
"""方言编译（prompt.compile）：中性分段 → 所选模型的提示词与参数，并检查段长、参考数量、首尾帧互斥、字数。
  python3 {{TOOLS}}/compile.py S03 [--model seedance-2.0] [--file 剧本/分段表.md] [--out 生成/S03_prompt.json]
  python3 {{TOOLS}}/compile.py --json 段.json --model seedance-2.5
  python3 {{TOOLS}}/compile.py all [--model ...]          整张分段表逐段检查，只报问题
不给 --model 就读需求文档里的「生成模型」。有 error 级问题时退出码 2。

编译是确定性的：同一段、同一模型、同一风格块，结果逐字相同。柚子后端的 compile_prompt 用同一套规则（Go 实现），
两边共用 yoozee-skills/tests/fixtures 下的样例做对照测试。"""
import argparse
import json
import math
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

HERE = os.path.dirname(os.path.abspath(__file__))


def _block(name):
    with open(os.path.join(HERE, name), encoding="utf-8") as f:
        return f.read().strip()


def blocks(dialect):
    """风格块和表演块：Seedance 用中文，MiniMax-H3 用英文（实测稳定）。口味不是真人剧时由调用方传入自己的风格块。"""
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
    has_refs = bool(seg.get("refs") or seg.get("voices"))
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
        refs = seg.get("refs", [])
        imgs = [r for r in refs if r.get("role") != "motion"]
        vids = [r for r in refs if r.get("role") == "motion"]
        auds = seg.get("voices", [])
        lim = m.get("refs", {})
        for name, have, cap in (("参考图", len(imgs), lim.get("images")), ("参考视频", len(vids), lim.get("videos")), ("参考音频", len(auds), lim.get("audios"))):
            if cap is not None and have > cap:
                out.append(("error", f"{seg['id']} {name} {have} 个，超过 {m['label']} 的上限 {cap}"))
        total = lim.get("total")
        if total is not None and len(imgs) + len(vids) + len(auds) > total:
            out.append(("error", f"{seg['id']} 参考素材合计 {len(imgs) + len(vids) + len(auds)} 个，超过 {m['label']} 的上限 {total}"))
    return out


def segment_sound(seg, sep):
    """这一段的声音：段级 sound；没写时按镜头顺序接各镜头的 sound（去掉句尾标点、去重，用 sep 分隔）。
    声音记在镜头上，拆段、合段、移镜头之后跟着镜头走。与柚子后端 Segment.SoundText 一致。"""
    sound = (seg.get("sound") or "").strip()
    if sound:
        return sound
    parts = []
    for sh in seg.get("shots", []):
        part = (sh.get("sound") or "").strip().rstrip("。；;，,. ")
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
    decl, files = [], []
    if mode == "lock_first_frame":
        decl.append("以@图片1为首帧（构图、人物、服装和光线照此）")
        files.append({"file": seg.get("first_frame", ""), "tag": "@图片1", "role": "first_frame"})
        if seg.get("last_frame"):
            decl.append("以@图片2为尾帧，视频结束在这一画面")
            files.append({"file": seg["last_frame"], "tag": "@图片2", "role": "last_frame"})
    elif mode == "lock_identity":
        i = j = 0
        for r in seg.get("refs", []):
            note = (r.get("note") or "").strip()
            if r.get("role") == "motion":
                j += 1
                decl.append(f"@视频{j}为运镜参考" + (f"：{note}" if note else ""))
                files.append({"file": r["file"], "tag": f"@视频{j}", "role": "motion"})
                continue
            i += 1
            tpl = SEEDANCE_ROLE.get(r.get("role"), SEEDANCE_ROLE["frame"])
            decl.append(tpl.format(i=i, who=note or "该人物", tail=(f"：{note}" if note else "")))
            files.append({"file": r["file"], "tag": f"@图片{i}", "role": r.get("role")})
        for k, v in enumerate(seg.get("voices", []), 1):
            decl.append(f"@音频{k}为{v['character']}的音色参考（只取音色，不复用其中的话）")
            files.append({"file": v["file"], "tag": f"@音频{k}", "role": "voice"})
    dur = _submit_duration(seg["dur"], m)
    head = ("；".join(decl) + "；" if decl else "") + f"生成{dur}秒视频。整体风格：{style}{acting}"
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
            how = [p for p in (("画外音，嘴不动" if ln.get("vo") else ""), (ln.get("delivery") or "").strip().rstrip("。，,.")) if p]
            text += f"{ln['speaker']}（{'，'.join(how)}）：“{ln['text']}”" if how else f"{ln['speaker']}：“{ln['text']}”"
        if k == anchor_k:
            text = REANCHOR + text
        lines.append(f"{_sec(start)}-{_sec(end)}秒：{text}")
    sound = segment_sound(seg, "；").rstrip("。")
    lines.append(f"声音：{sound or '环境声与动作音效'}；不要背景音乐和配乐。")
    constraint = SEEDANCE_CONSTRAINT
    lang = (seg.get("lang") or "").strip()
    if lang and lang.lower() not in ("chinese", "中文", "zh"):
        constraint += f"台词用{lang}说。"
    lines.append(constraint)
    return "\n".join(lines), files, dur


def compile_h3(seg, m, style, acting):
    mode = seg.get("mode", "lock_identity")
    files = []
    rules = segment_rules(seg)
    head = style + " " + acting + " " + (rules + " " if rules else "")
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
        for i, r in enumerate(seg.get("refs", []), 1):
            files.append({"file": r["file"], "tag": f"<Picture {i}>", "role": r.get("role")})
            roles.append(f"<Picture {i}> " + H3_ROLE.get(r.get("role"), "{note}").format(note=r.get("note", "")).strip())
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
    return body, files, _submit_duration(seg["dur"], m)


def compile_segment(seg, m, style=None, acting=None):
    """返回 {model, duration, prompt, files, chars, issues}。issues 里有 error 就不能提交。"""
    d = dialect_of(m["id"])
    s0, a0 = blocks(d)
    style, acting = (style if style is not None else s0), (acting if acting is not None else a0)
    issues = _issues(seg, m)
    if d == "seedance":
        prompt, files, dur = compile_seedance(seg, m, style, acting)
    else:
        prompt, files, dur = compile_h3(seg, m, style, acting)
    cap = m.get("prompt_max_chars")
    if cap is not None and len(prompt) > cap:
        issues.append(("error", f"{seg['id']} 提示词 {len(prompt)} 字符，超过 {m['label']} 的上限 {cap}：先压缩风格词和重复场景词，不删台词和关键动作"))
    if float(seg.get("dur", 0)) != dur and not any(lvl == "error" and "超过" in msg and "时长" in msg for lvl, msg in issues):
        issues.append(("warn", f"{seg['id']} 按 {dur} 秒提交（模型时长步长为 {m['duration'].get('step') or 1} 秒）"))
    return {"segment": seg["id"], "model": m["id"], "duration": dur, "prompt": prompt, "files": files, "chars": len(prompt),
            "issues": [{"level": lvl, "message": msg} for lvl, msg in issues]}


def main():
    import common as C
    import segments as S
    ap = argparse.ArgumentParser()
    ap.add_argument("seg", nargs="?", help="段号，或 all")
    ap.add_argument("--model")
    ap.add_argument("--file", default="剧本/分段表.md")
    ap.add_argument("--json", help="直接给一段中性分段的 JSON 文件")
    ap.add_argument("--style-file", help="口味不是真人剧时，换掉默认风格块")
    ap.add_argument("--out")
    a = ap.parse_args()
    m = C.model(a.model or C.project_model())
    style = open(a.style_file, encoding="utf-8").read().strip() if a.style_file else None
    if a.json:
        segs = [C.read_json(a.json)]
    else:
        segs = S.load(a.file)
        if a.seg and a.seg != "all":
            segs = [s for s in segs if s["id"] == a.seg]
            if not segs:
                sys.exit(f"分段表里没有 {a.seg}")
    bad = False
    results = []
    for seg in segs:
        r = compile_segment(seg, m, style=style)
        results.append(r)
        bad = bad or any(i["level"] == "error" for i in r["issues"])
        if a.seg == "all":
            for i in r["issues"]:
                print(("✗ " if i["level"] == "error" else "⚠ ") + i["message"])
        else:
            print(f"== {r['segment']} · {m['label']} · {r['duration']} 秒 · {r['chars']} 字符")
            print(r["prompt"])
            if r["files"]:
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
