#!/usr/bin/env python3
"""装段（yz-pack）：镜头表 → 段。确定性代码，三端（本机 Python / 柚子 Go / 前端预览）共享 tests/fixtures/pack-*.json。
  python3 {{TOOLS}}/pack.py [--shots 剧本/镜头表.json] [--script 剧本/剧本.json] [--bible 剧本/资产圣经.json]
                           [--model seedance-2.5] [--profile live-realism] [--out 剧本/分段.json] [--target 15-25]
策略：
- 按场分组、按台词字速 + 动作 + 表情估时（与 check.py 同一算法）。
- Seedance 2.5（上限 30 秒）：一场合计 ≤30 秒直接装成一段；超过才找切点。其他模型按目标区间（默认 15～25 秒）装。
- 切点优先级：动作完成点（无台词的动作镜）> 台词句末 > 景别变化处；接话（相邻两镜都有台词且前一镜无动作完成）不拆；两段尽量均衡、都不低于下限。
- 同场拆成多段时，后段带 continuity_bridge：同一套参考同编号、前段末镜 end_state、再锚定文本、共用声床；bridge_frame / bridge_clip 留给 yz-shoot 选中后补。
- refs 由镜头 refs_hint 在 bible 里查文件取并集，按模型上限截断（近景只挂特写，中远景挂全身正面）。
- 没有合法切点时退出码 3 并列出原因：这时才由模型改 est 或并镜，不删台词。"""
import argparse
import json
import math
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import common as C  # noqa: E402
import check as K  # noqa: E402

CAM_X = {"X1": "正面", "X2": "侧 45 度", "X3": "侧面", "X4": "背面"}
CAM_Y = {"Y1": "贴地大仰角", "Y2": "仰拍", "Y3": "微仰", "Y4": "平视", "Y5": "微俯", "Y6": "俯拍", "Y7": "正上方俯拍"}
MOVE_ZH = {"fixed": "固定机位", "push": "推近", "pull": "拉远", "crane_up": "升起", "crane_down": "下降", "truck": "横移", "pan": "摇镜", "tilt": "俯仰摇", "roll": "滚转", "zoom": "变焦", "rack_focus": "焦点转换", "track": "跟拍", "orbit": "环绕", "spiral": "螺旋环绕", "handheld_micro": "手持微晃", "handheld": "手持晃动", "handheld_violent": "手持剧烈晃动", "dolly_zoom": "滑动变焦", "snorricam": "主体稳定背景晃动", "jib": "摇臂", "fpv": "FPV 穿越", "one_take": "一镜到底"}
F_ZH = {"pov": "主观视角", "ots": "过肩", "voyeur": "透过门缝 / 栅栏偷窥", "mirror": "通过镜面反射", "dutch": "荷兰角倾斜构图", "symmetry": "对称构图", "leading": "引导线构图", "frame": "框中框构图", "negspace": "大量留白"}


def camera_text(sh):
    cam = sh.get("cam", {})
    f = cam.get("f") or {}
    parts = [CAM_X.get(cam.get("x"), ""), CAM_Y.get(cam.get("y"), "")]
    if f.get("identity"):
        parts.append(F_ZH[f["identity"]])
    if f.get("geometry"):
        parts.append(F_ZH[f["geometry"]] + (f" {f['dutch_deg']}°" if f.get("dutch_deg") else ""))
    parts += [MOVE_ZH.get(m, m) for m in sh.get("move", [])]
    if f.get("lens_mm"):
        parts.append(f"{int(f['lens_mm'])}mm")
    if (sh.get("dof") or f.get("dof")) == "shallow":
        parts.append("浅景深")
    elif (sh.get("dof") or f.get("dof")) == "deep":
        parts.append("深景深")
    return "，".join(p for p in parts if p)


CONTACT_ZH = {"hit": "命中", "graze": "擦过", "blocked": "被挡下", "miss": "落空", "none": "没有接触"}
_NIL = {"", "无", "——", "-", "none", "None"}


def fight_text(f, people=()):
    """因果动作单元写成一句话：发起 → 路径 → 反应 → 接触 → 力的传递 → 结束姿态；空项和「无」不写。
    纯反应镜 / 空镜（没有发起者）只写接触判定与结束姿态。路径已写到目标就不写「打向」；
    「对方」只在目标是镜头里的人物、反应又没自带主语时写；接触判定与反应、力的传递重复时不写判定。"""
    def ok(k):
        v = (f.get(k) or "").strip()
        return v if v not in _NIL else ""
    person = lambda text: bool(text) and any(p and text.startswith(p) for p in people)
    parts = []
    if ok("initiator"):
        parts.append(ok("initiator") + "发起" + (f"，{ok('path')}" if ok("path") else "")
                     + (f"，打向{ok('target')}" if ok("target") and ok("target") not in ok("path") else ""))
    elif ok("path"):
        parts.append(ok("path"))
    if ok("response"):
        parts.append("对方" + ok("response") if ok("initiator") and person(ok("target")) and not person(ok("response")) else ok("response"))
    c = CONTACT_ZH.get(f.get("contact"))
    said = ok("response") + ok("force_transfer")
    if c and c not in said and not (f.get("contact") in ("none", "miss") and "接触" in said):
        parts.append(c)
    if ok("force_transfer"):
        parts.append(ok("force_transfer"))
    if ok("end_pose"):
        parts.append("结束在：" + ok("end_pose"))
    return ("动作：" + "；".join(parts) + "。") if parts else ""


def fight_people(sh):
    """一镜里的人物：焦点、站位里的人和发起者。"""
    out = []
    for name in [sh.get("focus")] + [b.get("who") for b in sh.get("blocking", [])] + [(sh.get("fight") or {}).get("initiator")]:
        name = (name or "").strip()
        if name and name not in out:
            out.append(name)
    return out


ARCS = None
EXPRS = None
ACTS = None
BIG_SIGNALS = ("掩面", "嘶吼", "大哭", "捶地", "瘫坐", "跪倒", "狂笑", "嚎啕", "怒吼", "尖叫", "扑", "剧烈", "失控", "决堤", "滚落", "大颗", "涌出", "夺眶", "甩落", "瘫")


def _vocab():
    global ARCS, EXPRS, ACTS
    if ARCS is None:
        ARCS = C.vocab_by_id("kb-performance", "vocab/emotion-arcs.jsonl")
        EXPRS = C.vocab_by_id("kb-performance", "vocab/expressions.jsonl")
        ACTS = C.vocab_by_id("kb-performance", "vocab/action-patterns.jsonl")
    return ARCS, EXPRS, ACTS


def _fmt(x):
    return f"{x:.1f}".rstrip("0").rstrip(".")


def expand_arc(sh, est, profile, prev_phase_end=None):
    """情绪弧 → 分秒表演链（DESIGN-2.1 §5.1）：
    取 phase_from..phase_to（continues 时从上一镜停下的段接着）；按 est/弧总秒等比缩放，单段不短于档案 expression_min_sec，
    不够就并入前一段；est<3 只取前两段；micro 粒度每段 2 条信号否则 1 条；hidden7 删大幅度信号；台词所在段标出。
    返回 (文本, 用到的段号列表, 弧的 family)；没有弧返回 (None, [], None)。"""
    arc_ref = sh.get("emotion_arc")
    if not arc_ref:
        return None, [], None
    arcs, _, _ = _vocab()
    arc = arcs.get(arc_ref["id"])
    if not arc or not arc.get("phases"):
        return None, [], None
    phases = arc["phases"]
    a = (arc_ref.get("phase_from") or (prev_phase_end + 1 if prev_phase_end else 1)) - 1
    b = arc_ref.get("phase_to") or len(phases)
    a = max(0, min(a, len(phases) - 1))
    b = max(a + 1, min(b, len(phases)))
    chosen = phases[a:b]
    if est < 3 and len(chosen) > 2:
        chosen = chosen[:2]
    min_sec = float((profile.get("rules") or {}).get("expression_min_sec", 0.5))
    total = sum(ph["to"] - ph["from"] for ph in chosen) or 1.0
    scale = est / total
    merged = []
    for ph in chosen:
        d = (ph["to"] - ph["from"]) * scale
        if merged and d < min_sec:
            merged[-1]["dur"] += d
            merged[-1]["signals"] = merged[-1]["signals"] + ph["signals"]
            merged[-1]["label"] += "→" + ph["label"]
        else:
            merged.append({"label": ph["label"], "dur": d, "signals": list(ph["signals"]), "inner": ph.get("inner", "")})
    if len(merged) > 1 and merged[0]["dur"] < min_sec:  # 首段太短并进后一段
        merged[1]["dur"] += merged[0]["dur"]
        merged[1]["signals"] = merged[0]["signals"] + merged[1]["signals"]
        merged[1]["label"] = merged[0]["label"] + "→" + merged[1]["label"]
        merged = merged[1:]
    intensity = arc_ref.get("intensity") or (sh.get("performance") or {}).get("restraint") or "normal"
    n_sig = 2 if profile.get("performance_grain") == "micro" else 1
    if intensity == "open":
        n_sig += 1
    line_at = None
    if sh.get("lines"):
        line_at = min(len(merged) - 1, max(0, round(len(merged) * 0.4)))  # 台词落在中前段：先有反应再开口
    parts, t = [], 0.0
    for i, m in enumerate(merged):
        sig = m["signals"]
        if intensity == "hidden7":
            sig = [x for x in sig if not any(k in x for k in BIG_SIGNALS)] or sig[:1]
        # 挑信号：一条取段末那一下（关键动作多在后面），两条取首尾，三条首中尾
        if len(sig) > n_sig:
            sig = [sig[-1]] if n_sig == 1 else ([sig[0], sig[-1]] if n_sig == 2 else [sig[0], sig[len(sig) // 2], sig[-1]])
        seg = f"{_fmt(t)}–{_fmt(t + m['dur'])}秒：" + "，".join(sig)
        if i == line_at:
            seg += "（台词落在这一段）"
        parts.append(seg)
        t += m["dur"]
    text = "；".join(parts)
    if intensity == "hidden7":
        text += "；幅度压到最小，藏七露三"
    return text, list(range(a + 1, a + 1 + len(chosen))), arc.get("family")


FAMILY_DEFAULT_TERM = {"sadness": "红眼眶", "love": "含情脉脉", "smile": "微笑", "cold": "冷酷", "shock": "睁大双眼"}


def expression_terms(sh, family, used_phases=None):
    """表情核心词：镜头写了就用；没写就在词典里按弧段的标签 / 信号做关键词匹配（取命中最多的一条的前两个词），
    一个都不命中按情绪族取温和的默认词（红眼眶 / 含情脉脉 / 微笑 / 冷酷 / 睁大双眼），不取该族第一条。"""
    if sh.get("expression_terms"):
        return list(sh["expression_terms"])[:3]
    if not family:
        return []
    arcs, exprs, _ = _vocab()
    arc = arcs.get((sh.get("emotion_arc") or {}).get("id")) or {}
    phases = arc.get("phases") or []
    sel = [phases[i - 1] for i in (used_phases or []) if 0 < i <= len(phases)] or phases
    hay = "".join("".join(ph.get("signals", [])) for ph in sel)  # 只按可见信号匹配，不按段标签（「震惊期」会把克制镜头匹配成惊掉下巴）
    intensity = (sh.get("emotion_arc") or {}).get("intensity") or "normal"
    best, score = None, 0
    for e in exprs.values():
        if intensity == "hidden7" and any(w in e["name"] for w in ("极度", "嚎啕", "暴怒", "狂喜", "目瞪口呆", "惊掉")):
            continue
        keys = [e["name"]] + [t for t in e.get("zh_terms", []) if len(t) >= 2]
        sc = sum(2 for k in keys if k in hay) + sum(1 for k in keys if len(k) >= 3 and any(k[i:i + 2] in hay for i in range(len(k) - 1)))
        if sc > score:
            best, score = e, sc
    if best and score >= 3:
        return best["zh_terms"][:2]
    name = FAMILY_DEFAULT_TERM.get(family)
    e = next((x for x in exprs.values() if x["name"] == name), None)
    return e["zh_terms"][:2] if e else []


FORBID_TAGS = [("穿模", "model_clip"), ("穿透", "model_clip"), ("穿帮", "bg_extras"), ("虚化过度", "bg_extras"), ("反物理", "physics"), ("脚离地", "physics"), ("同手同脚", "physics"), ("步伐僵硬", "physics"),
               ("扭曲", "limb_distortion"), ("僵硬", "limb_distortion"), ("畸形", "limb_distortion"), ("多指", "limb_distortion"), ("少指", "limb_distortion"), ("多余手指", "limb_distortion"), ("比例", "proportion")]


def forbid_tags(forbid):
    """动作模式的禁止项 → 风险标签（编译器按标签追加 ⚠️，与 risk_tags 同一条路）。"""
    out = []
    for f in forbid or []:
        for k, tag in FORBID_TAGS:
            if k in f and tag not in out:
                out.append(tag)
    return out


def action_pattern_text(sh):
    """动作模式 → 动作句片段 + 禁止项列表。"""
    ap = sh.get("action_pattern")
    if not ap:
        return "", []
    _, _, acts = _vocab()
    pat = acts.get(ap["id"])
    if not pat:
        return "", []
    if pat.get("parent"):  # 手部特写子条目：task 是样例清单（婚戒 / 信物 / 毒药），不进提示词
        bits = []
    else:
        bits = [x for x in (pat.get("process") or []) if "{{" not in x and x.count("、") < 3 and not x.startswith("电影级")]  # 泛泛的动作清单不进提示词，禁止项仍进 ⚠️
    if ap.get("variant"):
        bits = [ap["variant"]]  # 写了变体就只用变体
    elif pat.get("variants") and not bits and not pat.get("parent"):
        bits = [v for v in pat["variants"] if v.count("、") < 3][:1]
    if ap.get("role") and pat.get("roles"):
        want = "女主" if ap["role"] == "主动" else "男主"
        bits += [r for r in pat["roles"] if r.startswith(want)][:1]
    bits = [b for b in bits if b][:4]
    txt = f"动作模式·{pat['name'].split('-')[0]}：" + "；".join(bits) + "。" if bits else ""
    return txt, list(pat.get("forbid") or [])


def expression_text(sh, est, profile, prev_phase_end=None):
    """这一镜的「表情」：弧展开 + 手写节拍；都没有返回空。返回 (文本, 核心词, 用到的弧段号)。"""
    arc_txt, used, fam = expand_arc(sh, est, profile, prev_phase_end)
    beats = (sh.get("performance") or {}).get("beats") or []
    txt = arc_txt or ""
    if beats:
        txt += ("；" if txt else "") + "节拍：" + "；".join(beats)
    terms = expression_terms(sh, fam, used) if (arc_txt or beats) else []
    return txt, terms, used


def light_full_text(light):
    """灯光七项句（即梦格式）；一项都没写返回空。"""
    keys = [("key", "主光"), ("fill", "辅光"), ("rim", "轮廓光"), ("ratio", "光比"), ("kelvin", "色温"), ("shadow", "阴影"), ("exposure", "曝光"), ("change", "光线变化")]
    return "；".join(f"{zh}：{light[k]}" for k, zh in keys if light.get(k))


def positions_text(sh):
    """本镜站位句：谁在哪个锚点、朝向、身后、被谁挡。"""
    bits = []
    for b in sh.get("blocking", []) or []:
        t = f"{b.get('who', '')}在{b.get('at', '')}"
        if b.get("facing"):
            t += f"，{b['facing']}"
        if b.get("behind"):
            t += f"，身后是{b['behind']}"
        if b.get("occluded_by"):
            o = b["occluded_by"]
            t += f"，被{o}" + ("" if o.endswith(("挡住", "遮住", "挡", "遮")) else "挡住一部分")
        bits.append(t)
    if sh.get("camera_side"):
        bits.append(f"机位在{sh['camera_side']}")
    return "；".join(bits)


def anchors_text(sh, scene, bible):
    """本镜可见场景锚点：站位里提到的锚点 + 场景版本 + 道具状态。"""
    loc = None
    for lo in (bible or {}).get("locations", []):
        if lo.get("name") == (scene or {}).get("location_ref"):
            loc = lo
    known = set((loc or {}).get("anchors") or [])
    seen, behind = [], []
    for b in sh.get("blocking", []) or []:
        if b.get("at") and b["at"] not in seen:
            seen.append(b["at"])
        bh = b.get("behind")
        if bh:
            (seen if bh in known and bh not in seen else behind).append(bh) if (bh in known or bh not in behind) else None
    extra = [h for h in sh.get("refs_hint", []) if "·" in h]
    parts = []
    if seen:
        parts.append("锚点：" + "、".join(seen))
    if behind:
        parts.append("身后：" + "、".join(dict.fromkeys(behind)))
    if behind:
        parts.append("身后：" + "、".join(dict.fromkeys(behind)))
    if extra:
        parts.append("状态：" + "、".join(extra))
    if loc and loc.get("light_source"):
        parts.append("光源：" + loc["light_source"])
    return "；".join(parts)


def motion_text(sh):
    """本镜运动方向句：从 fight.path、blocking.facing 与起止状态推。"""
    f = sh.get("fight") or {}
    bits = []
    if f.get("initiator") and f.get("path") and f["initiator"] not in ("无", ""):
        bits.append(f"{f['initiator']}{f['path']}")
    if f.get("response") and f["response"] not in ("无", ""):
        bits.append(f"{f['response']}")
    moves = [MOVE_ZH.get(m, m) for m in sh.get("move", []) if m not in ("fixed", "handheld_micro", "handheld", "handheld_violent", "rack_focus")]
    if moves:
        bits.append("机位" + "、".join(moves))
    if not bits:
        ss, es = sh.get("start_state") or "", sh.get("end_state") or ""
        bits.append("人物原地；" if ss == es else f"从「{ss}」到「{es}」")
    return "；".join(bits)


VFX = None
MARTIAL = None


def _action_vocab():
    global VFX, MARTIAL
    if VFX is None:
        VFX = C.vocab_by_id("kb-action", "vocab/vfx.jsonl")
        MARTIAL = C.vocab_by_id("kb-action", "vocab/martial.jsonl")
    return VFX, MARTIAL


def martial_name(mid):
    _, m = _action_vocab()
    e = m.get(mid)
    return e["name"] if e else ""


def vfx_text(v):
    """特效条目 → 文字：名称 + 执行描述第一句（词库里的 AI技术执行）+ 镜头写的属性 / 范围 / 秒数。模型不认识 vfx-xxx 编号。"""
    vfx, _ = _action_vocab()
    e = vfx.get(v.get("id"), {})
    name = e.get("name") or v.get("id", "")
    exe = (e.get("AI技术执行") or e.get("定义") or "").split("。")[0]
    bits = [exe] if exe else []
    if v.get("attribute"):
        bits.append(v["attribute"])
    if v.get("scale"):
        bits.append("范围：" + v["scale"])
    return f"特效·{name}：" + "，".join(bits) + (f"，持续 {v['seconds']} 秒" if v.get("seconds") else "") + "。"


VFX = None
MARTIAL = None


def _action_vocab():
    global VFX, MARTIAL
    if VFX is None:
        VFX = C.vocab_by_id("kb-action", "vocab/vfx.jsonl")
        MARTIAL = C.vocab_by_id("kb-action", "vocab/martial.jsonl")
    return VFX, MARTIAL


def martial_name(mid):
    _, m = _action_vocab()
    e = m.get(mid)
    return e["name"] if e else ""


def vfx_text(v):
    """特效条目 → 文字：名称 + 执行描述第一句（词库里的 AI技术执行）+ 镜头写的属性 / 范围 / 秒数。模型不认识 vfx-xxx 编号。"""
    vfx, _ = _action_vocab()
    e = vfx.get(v.get("id"), {})
    name = e.get("name") or v.get("id", "")
    exe = (e.get("AI技术执行") or e.get("定义") or "").split("。")[0]
    bits = [exe] if exe else []
    if v.get("attribute"):
        bits.append(v["attribute"])
    if v.get("scale"):
        bits.append("范围：" + v["scale"])
    return f"特效·{name}：" + "，".join(bits) + (f"，持续 {v['seconds']} 秒" if v.get("seconds") else "") + "。"


def shot_text(sh, est=None, profile=None, prev_phase_end=None):
    text = (sh.get("action") or "").strip()
    pat_txt, _ = action_pattern_text(sh)
    if pat_txt:
        text += pat_txt
    perf = sh.get("performance") or {}
    if est is not None and profile is not None and sh.get("emotion_arc"):
        expr, terms, _ = expression_text(sh, est, profile, prev_phase_end)
        if expr:
            text += "表情：" + expr + "。"
        if terms:
            text += "核心词：" + "、".join(terms) + "。"
    elif perf.get("beats"):
        text += "表演：" + "；".join(perf["beats"]) + "。"
    if sh.get("state"):
        text += f"此刻人物状态：{sh['state']}。"
    light = sh.get("light") or {}
    if light.get("source_anchor"):
        text += f"光来自{light['source_anchor']}" + (f"，{light['direction']}" if light.get("direction") else "") + "。"
    lf = light_full_text(light)
    if lf:
        text += lf + "。"
    f = sh.get("fight")
    if f:
        text += fight_text(f, fight_people(sh))
    if f and f.get("martial_ids"):
        names = [martial_name(i) for i in f["martial_ids"] if martial_name(i)]
        if names:
            text += "流派：" + "、".join(names) + "。"
    for v in sh.get("vfx", []):
        text += vfx_text(v)
    return text


def sound_text(sh):
    s = (sh.get("sfx") or "").strip()
    if sh.get("voice"):
        s = (s + "；" if s else "") + sh["voice"]
    if sh.get("bridge") == "in":
        s = (s + "；" if s else "") + "声音先入"
    elif sh.get("bridge") == "out":
        s = (s + "；" if s else "") + "声音延续到下一镜"
    return s


def find_refs(sh, bible, scene, lines_by_id):
    """按 refs_hint（人物名 / 人物·变体 / 场景名 / 道具名）在 bible 里找文件；没写 hint 时按 blocking 的人物和场的 location_ref。"""
    hints = list(sh.get("refs_hint") or [])
    if not hints:
        hints = [b["who"] for b in sh.get("blocking", [])] + ([scene.get("location_ref")] if scene and scene.get("location_ref") else [])
    refs, voices = [], []
    close = sh.get("cam", {}).get("z") in ("Z1", "Z2", "Z3", "Z4")
    for h in hints:
        name, _, variant = h.partition("·")
        ch = next((c for c in bible.get("characters", []) if c["name"] == name), None)
        if ch:
            v = next((x for x in ch["variants"] if x.get("label") == variant), None) if variant else next((x for x in ch["variants"] if x.get("kind", "base") == "base"), ch["variants"][0] if ch["variants"] else None)
            if v:
                crops = v.get("crops") or []
                pick = next((c for c in crops if "特写" in c), crops[0] if crops else v.get("sheet")) if close else next((c for c in crops if "正面" in c), crops[0] if crops else v.get("sheet"))
                if pick:
                    note = f"{ch['name']}" + (f"（{variant}）" if variant else "") + ("，" + "；".join((v.get("lock_card") or [])[:3]) if v.get("lock_card") else "")
                    refs.append({"file": pick, "role": "identity", "note": note})
                if ch.get("speaks") and v.get("voice") and any(lines_by_id.get(ln.get("line_id"), {}).get("speaker") == ch["name"] for ln in sh.get("lines", [])):
                    voices.append({"character": ch["name"], "file": v["voice"]})
            continue
        lo = next((l for l in bible.get("locations", []) if l["name"] == name), None)
        if lo:
            side = sh.get("camera_side") or ""
            file = lo.get("side_a") if "A" in side.upper() else lo.get("side_b") if "B" in side.upper() else lo.get("plate")
            if variant:
                ver = next((x for x in lo.get("versions", []) if x.get("label") == variant), None)
                file = (ver or {}).get("plate") or file
            if file:
                which = "A 侧机位身后" if file == lo.get("side_a") else "B 侧机位身后" if file == lo.get("side_b") else ""
                refs.append({"file": file, "role": "plate" if which else "location", "note": lo["name"] + (f"（{variant}）" if variant else "") + (f"，{which}" if which else "")})
            continue
        pr = next((p for p in bible.get("props", []) if p["name"] == name), None)
        if pr:
            file = pr.get("image")
            if variant:
                st = next((x for x in pr.get("states", []) if x.get("when") == variant), None)
                file = (st or {}).get("image") or file
            if file:
                refs.append({"file": file, "role": "prop", "note": pr["name"] + (f"（{variant}）" if variant else "")})
    return refs, voices


def dedupe_identity(refs):
    """同一个人在一段里只挂一张：段里有近景就挂特写，否则挂全身正面；其他角色的参考按文件去重。"""
    by_person, out = {}, []
    for r in refs:
        if r["role"] != "identity":
            continue
        name = r["note"].split("，")[0].split("（")[0]
        cur = by_person.get(name)
        if cur is None or ("特写" in r["file"] and "特写" not in cur["file"]):
            by_person[name] = r
    seen = set()
    for r in refs:
        if r["role"] == "identity":
            name = r["note"].split("，")[0].split("（")[0]
            pick = by_person[name]
            if pick["file"] not in seen:
                seen.add(pick["file"])
                out.append(pick)
        elif r["file"] not in seen:
            seen.add(r["file"])
            out.append(r)
    return out


REF_RANK = {"identity": 0, "location": 1, "plate": 1, "prop": 2}


def image_ref_limit(model, voices):
    """一段最多挂几张参考图（与保存时的校验同一套上限）：图片上限；有合计 / 混合上限时再扣掉参考音频。没写上限时返回 None。"""
    lim = model.get("refs", {})
    caps = [lim.get("images")] + [None if c is None else c - voices for c in (lim.get("total"), model.get("files_total"))]
    caps = [max(0, c) for c in caps if c is not None]
    return min(caps) if caps else None


def cap_refs(refs, limit, own=None):
    """把参考图截到 limit 张：人物 > 场景 > 道具 > 其他，同级先留 own 里的（本段镜头自己查出的），同一个人已有本段自己的那张时
    前一段沿用来的另一张排到最后；留下的保持原顺序。motion 不占图片名额。"""
    images = [i for i, r in enumerate(refs) if r.get("role") != "motion"]
    if len(images) <= limit:
        return refs
    mine = (lambda r: True) if own is None else (lambda r: r["file"] in own)
    own_people = {r["note"].split("，")[0].split("（")[0] for r in refs if r["role"] == "identity" and mine(r)}

    def rank(r):
        n = REF_RANK.get(r["role"], 3) * 2
        if not mine(r):
            n += 1
            if r["role"] == "identity" and r["note"].split("，")[0].split("（")[0] in own_people:
                n = 99
        return n
    keep = set(sorted(images, key=lambda i: rank(refs[i]))[:limit])
    return [r for i, r in enumerate(refs) if r.get("role") == "motion" or i in keep]


def dedupe(items, key):
    seen, out = set(), []
    for it in items:
        k = key(it)
        if k not in seen:
            seen.add(k)
            out.append(it)
    return out


def cut_score(a, b):
    """在镜头 a 之后切、b 之前开始的分数：越高越好；None 表示不许切。"""
    if a.get("lines") and b.get("lines") and not K.has_action_verb(a.get("action")):
        return None  # 接话不拆
    score = 0
    if not a.get("lines"):
        score += 3  # 动作完成点
    else:
        last = a["lines"][-1]
        score += 2  # 台词句末
    if a.get("size") != b.get("size"):
        score += 1
    if a.get("end_state"):
        score += 1
    return score


def split_scene(shots, ests, hi, lo, target):
    """返回切点索引列表（在该索引之后切）。整场 ≤ hi 不切。"""
    total = sum(ests)
    if total <= hi:
        return []
    n_min = math.ceil(total / hi)
    best = None
    for n in range(n_min, min(len(shots), n_min + 3) + 1):
        ideal = [total * i / n for i in range(1, n)]
        cuts, acc, last = [], 0.0, 0
        ok = True
        for goal in ideal:
            cands = []
            for k in range(last, len(shots) - 1):
                acc_k = sum(ests[last:k + 1])
                if acc_k > hi:
                    break
                sc = cut_score(shots[k], shots[k + 1])
                if sc is None or acc_k < lo:
                    continue
                cands.append((abs(sum(ests[:k + 1]) - goal), -sc, k))
            if not cands:
                ok = False
                break
            cands.sort()
            k = cands[0][2]
            cuts.append(k)
            last = k + 1
        if not ok:
            continue
        rest = sum(ests[last:])
        if rest > hi or rest < lo:
            continue
        spread = max(sum(ests[(cuts[i - 1] + 1 if i else 0):cuts[i] + 1]) for i in range(len(cuts))) if cuts else 0
        cand = (n, spread, cuts)
        if best is None or cand < best:
            best = cand
    return best[2] if best else None


FUNCTION_MAP = [("建立", "建立空间"), ("反转", "反转"), ("揭", "信息揭示"), ("真相", "信息揭示"), ("发现", "信息揭示"), ("先知", "信息揭示"), ("悬", "信息揭示"), ("交手", "动作推进"), ("打斗", "动作推进"), ("搏", "动作推进"), ("追", "动作推进"), ("穿越", "动作推进"), ("崩溃", "情绪爆发"), ("爆发", "情绪爆发"), ("兑付", "情绪爆发"), ("告白", "情绪爆发"), ("钩", "收束留白"), ("留白", "收束留白"), ("对话", "对话拉扯"), ("拉扯", "对话拉扯"), ("施压", "对话拉扯")]
ATMOS_MAP = [("压", "压抑"), ("紧", "紧张"), ("冷", "冷寂"), ("暖", "温暖"), ("燃", "高燃"), ("疑", "神秘"), ("荒", "荒诞"), ("甜", "浪漫"), ("惊", "紧张"), ("怕", "紧张"), ("悲", "压抑"), ("追", "紧张"), ("护", "紧张")]


def _map(text, table, default):
    for k, v in table:
        if k in (text or ""):
            return v
    return default


def cast_lines(shots, refs, bible):
    """没有身份参考图的在场人物 → 外貌声明（lock_card 前 6 条），编译时写进段头；有参考图的不写，免得和图打架。"""
    have = {r["note"].split("，")[0].split("（")[0] for r in refs if r.get("role") == "identity" and os.path.exists(C.abspath(r["file"]))}  # 图还没出就先用文字声明兜底
    names = []
    for sh in shots:
        for b in sh.get("blocking", []) or []:
            if b.get("who") and b["who"] not in names:
                names.append(b["who"])
        if sh.get("focus") and sh["focus"] not in names:
            names.append(sh["focus"])
    out = []
    for ch in (bible or {}).get("characters", []):
        if ch["name"] in names and ch["name"] not in have:
            v = (ch.get("variants") or [{}])[0]
            lock = (v.get("lock_card") or ch.get("anchors") or [])[:6]
            if lock:
                out.append({"name": ch["name"], "lock": lock})
    return out


def screen_texts_for(shots, scene, lines_by_id):
    """本段镜头动作里引用到的、剧本登记过的画面文字（screen_text 节拍）。"""
    texts = []
    for b in (scene or {}).get("beats", []):
        if b.get("kind") == "screen_text" and b.get("text"):
            texts.append(b["text"])
    hay = "".join((sh.get("action") or "") for sh in shots)
    return [t for t in dict.fromkeys(texts) if t in hay] or ([] if not texts else [t for t in texts if any(t in (sh.get("action") or "") for sh in shots)])


def jimeng_block(scene, shots, bible, seg_shots):
    """即梦小节头：叙事时间跨度 / 剧情功能 / 高级视听策略 / 持续氛围 / 持续光源 / 持续声床 / 群像差异化，全部从已有产物推导。"""
    if not scene:
        return None
    span = scene.get("time_jump") or scene.get("story_time") or "与生成时长同步，无时间跳跃"
    func = _map(scene.get("function", ""), FUNCTION_MAP, "对话拉扯" if scene.get("kind", "dialogue") == "dialogue" else "动作推进")
    strat = "；".join(dict.fromkeys(sh["strategy"] for sh in shots if sh.get("strategy"))) or ""
    emo = (scene.get("emotion") or "").split("→")[-1].strip()
    atmos = _map(emo, ATMOS_MAP, emo or ("高燃" if scene.get("kind") == "action" else "紧张"))  # 映射不到就用场的情绪末词本身
    srcs = [sh.get("light", {}).get("source_anchor") for sh in shots if sh.get("light", {}).get("source_anchor")]
    light = max(dict.fromkeys(srcs), key=srcs.count) if srcs else ""  # 平手取先出现的，保证装段确定性
    bed = seg_shared_bed(seg_shots)
    gv = "；".join(f"{c['name']}：{c['group_variation']}" for c in (bible or {}).get("characters", []) if c.get("group_variation") and c["name"] in (scene.get("characters") or []))
    out = {"narrative_span": span, "function": func, "atmosphere": atmos}
    if strat:
        out["strategy"] = strat
    if light:
        out["light_source"] = light
    if bed:
        out["sound_bed"] = bed
    if gv:
        out["group_variation"] = gv
    return out


def seg_shared_bed(seg_shots):
    frags = []
    for s in seg_shots:
        for part in re.split(r"[；;]", s.get("sound") or ""):
            part = part.strip("。 ")
            if part:
                frags.append(part)
    if not frags:
        return ""
    best = max(dict.fromkeys(frags), key=frags.count)
    return best if frags.count(best) >= 2 else ""


def build_segment(seg_id, scene_id, scene, shots, ests, bible, lines_by_id, model, profile_id, prev=None, profile=None, arc_state=None):
    dur_raw = sum(ests)
    step = model["duration"].get("step") or 1
    dur = int(max(model["duration"]["min"], min(model["duration"]["max"], math.ceil(dur_raw / step) * step)))
    at, seg_shots, refs, voices, risk = 0.0, [], [], [], []
    profile = profile or C.profile(profile_id)
    arc_end = arc_state if arc_state is not None else {}  # shot_id → 弧停在第几段（给 continues 用；由 pack() 跨段持有）
    voice_profiles = {c["name"]: c.get("voice_profile") or {} for c in (bible or {}).get("characters", [])}
    for sh, e in zip(shots, ests):
        lines = []
        for ln in sh.get("lines", []):
            beat = lines_by_id.get(ln.get("line_id"))
            if beat:
                item = {"speaker": beat.get("speaker", ""), "text": beat["text"], "line_id": beat["id"]}
                if beat.get("kind") == "vo" or ln.get("offscreen"):
                    item["vo"] = True
                if ln.get("delivery"):
                    item["delivery"] = ln["delivery"]
                for k in ("pace", "tone", "emotion", "placement"):
                    if ln.get(k):
                        item[k] = ln[k]
                vp = voice_profiles.get(item["speaker"])
                if vp and vp.get("timbre"):
                    item["timbre"] = vp["timbre"] + (f"，{vp['line_quality']}" if vp.get("line_quality") else "")
                lines.append(item)
        prev_end = arc_end.get((sh.get("emotion_arc") or {}).get("continues"))
        s = {"at": round(at, 1), "size": sh.get("size"), "camera": camera_text(sh), "text": shot_text(sh, e, profile, prev_end), "shot_id": sh["id"]}
        expr, terms, used = expression_text(sh, e, profile, prev_end)
        if used:
            arc_end[sh["id"]] = used[-1]
        if expr:
            s["expression"] = expr
        if terms:
            s["expression_terms"] = terms
        for k in ("purpose", "composition", "vo", "start_state"):
            if sh.get(k):
                s[k] = sh[k]
        if sh.get("transition"):
            s["transition"] = dict(sh["transition"])
        for k, fn in (("positions", positions_text), ("motion", motion_text)):
            v = fn(sh)
            if v:
                s[k] = v
        anc = anchors_text(sh, scene, bible)
        if anc:
            s["anchors"] = anc
        lf = light_full_text(sh.get("light") or {})
        if lf:
            s["light_full"] = lf
        _, forbid = action_pattern_text(sh)
        risk += [tag for tag in forbid_tags(forbid) if tag not in risk]
        if lines:
            s["lines"] = lines
        snd = sound_text(sh)
        if snd:
            s["sound"] = snd
        if sh.get("end_state"):
            s["end_state"] = sh["end_state"]
        if sh.get("risk_tags"):
            s["risk_tags"] = list(sh["risk_tags"])
            risk += sh["risk_tags"]
        seg_shots.append(s)
        r, v = find_refs(sh, bible, scene, lines_by_id)
        refs += r
        voices += v
        at += e
    scale = dur / dur_raw if dur_raw and dur < dur_raw else 1.0
    if scale < 1:
        for s in seg_shots:
            s["at"] = round(s["at"] * scale, 1)
    refs = dedupe_identity(refs)
    voices = dedupe(voices, lambda v: v["file"])
    lim = model.get("refs", {})
    if lim.get("audios") is not None:
        voices = voices[:lim["audios"]]
    image_limit = image_ref_limit(model, len(voices))
    if image_limit is not None:
        refs = cap_refs(refs, image_limit)
    last = shots[-1]
    rules = (last.get("end_state") or "").strip().rstrip("。；;，,！!？? ")  # 末尾标点去掉再接视线，收句由下面统一加
    gaze = "；".join(f"{b['who']}{b['facing']}" if re.search(r"^(面向|背对|转|看|正面|被|退|面朝|朝|侧)", b["facing"]) else f"{b['who']}看向{b['facing']}" for b in last.get("blocking", []) if b.get("facing"))
    if gaze:
        rules += ("；" if rules else "") + "视线：" + gaze
    if rules:
        rules = "本段结束时：" + rules + "。"  # 这是段末连戏状态，不是开场状态；编译稿放在风格块之后
    seg = {"id": seg_id, "scene": scene_id, "title": shots[0].get("purpose", "")[:40], "dur": dur, "mode": "lock_identity" if refs else "text_only",
           "shots": seg_shots, "shot_ids": [s["id"] for s in shots], "profile": profile_id}
    jm = jimeng_block(scene, shots, bible, seg_shots)
    if jm:
        seg["jimeng"] = jm
    cast = cast_lines(shots, refs, bible)
    if cast:
        seg["cast"] = cast
    st = screen_texts_for(shots, scene, lines_by_id)
    if st:
        seg["screen_text"] = st
    if refs:
        seg["refs"] = refs
    if voices:
        seg["voices"] = voices
    if rules:
        seg["rules"] = rules
    if last.get("purpose"):
        seg["hook"] = last["purpose"]
    if scene and scene.get("time_jump"):
        seg["story_span"] = scene["time_jump"]
    if risk:
        seg["warnings"] = sorted(set(risk))
    if prev is not None:
        prev_end = (prev["shots"][-1].get("end_state") or prev.get("rules") or "").strip()
        shared = [r["file"] for r in prev.get("refs", []) if r.get("role") != "motion"]
        # 后段参考与前段同一套、同顺序：前段有的放前面，本段新增的接在后面
        own = [r for r in refs if r["file"] not in shared]
        seg["refs"] = [next(r for r in prev["refs"] if r["file"] == f) for f in shared] + own if shared else refs
        if image_limit is not None and len(seg["refs"]) > image_limit:
            # 前一段的参考加上本段新增的超过名额：同一套优先级再截一次，同级先留本段自己用到的；演员表、共用参考只记留下的
            seg["refs"] = cap_refs(seg["refs"], image_limit, {r["file"] for r in refs})
            kept = {r["file"] for r in seg["refs"]}
            refs = [r for r in refs if r["file"] in kept]
            shared = [f for f in shared if f in kept]
            cast = cast_lines(shots, refs, bible)
            if cast:
                seg["cast"] = cast
            else:
                seg.pop("cast", None)
        if not seg["refs"]:
            seg.pop("refs")
            seg["mode"] = "text_only"
        frags = [f.strip() for sh in prev["shots"] for f in (sh.get("sound") or "").split("；") if f.strip()]
        bed = max(frags, key=lambda f: (frags.count(f), -frags.index(f))) if frags else ""
        seg["continuity_bridge"] = {
            "prev_segment": prev["id"], "handoff_state": prev_end, "shared_refs": shared,
            "reanchor_text": f"承接上一段（{prev['id']}）结尾：{prev_end}；人物形象、服装、光源方向与上一段完全一致。" if prev_end else "人物形象、服装、光源方向与上一段完全一致。",
            "shared_bed": bed}
    return seg


def pack(shotlist, script, bible, model, profile_id, target=(15, 25)):
    arc_state = {}
    arc_state = {}
    lines_by_id = K.script_lines(script) if script else {}
    scenes = K.scene_map(script) if script else {}
    hi, lo = model["duration"]["max"], model["duration"]["min"]
    is25 = hi >= 30
    groups, order = {}, []
    for sh in shotlist["shots"]:
        if sh["scene"] not in groups:
            order.append(sh["scene"])
        groups.setdefault(sh["scene"], []).append(sh)
    segments, problems, n = [], [], 0
    for scene_id in order:
        shots = groups[scene_id]
        scene = scenes.get(scene_id)
        ests = [K.est_seconds(sh, lines_by_id) for sh in shots]
        if is25:
            cuts = split_scene(shots, ests, hi, lo, (lo, hi))
        else:
            cuts = split_scene(shots, ests, min(hi, target[1]), lo, target)
            if cuts == [] and sum(ests) > target[1] and sum(ests) <= hi:
                cuts = []
        if cuts is None:
            problems.append(f"场 {scene_id} 合计 {sum(ests):.1f} 秒，找不到合法切点（接话不拆、每段在 [{lo},{hi}] 内）：改镜头 est 或并镜，不删台词")
            cuts = []
        bounds = [-1] + cuts + [len(shots) - 1]
        prev = None
        for i in range(len(bounds) - 1):
            a, b = bounds[i] + 1, bounds[i + 1] + 1
            n += 1
            seg = build_segment(f"S{n:02d}", scene_id, scene, shots[a:b], ests[a:b], bible or {}, lines_by_id, model, profile_id, prev, arc_state=arc_state)
            segments.append(seg)
            prev = seg
    return segments, problems


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--shots", default="剧本/镜头表.json")
    ap.add_argument("--script", default="剧本/剧本.json")
    ap.add_argument("--bible", default="剧本/资产圣经.json")
    ap.add_argument("--model")
    ap.add_argument("--profile")
    ap.add_argument("--target", default="15-25")
    ap.add_argument("--out", default="剧本/分段.json")
    ap.add_argument("--project")
    a = ap.parse_args()
    if a.project:
        import pathlib
        C.ROOT = pathlib.Path(a.project).resolve()
    shotlist = C.read_json(C.abspath(a.shots))
    script = K.load_optional(a.script)
    bible = K.load_optional(a.bible)
    model = C.model(a.model or C.project_model())
    profile_id = a.profile or shotlist.get("profile") or C.project_profile()
    t = tuple(int(x) for x in a.target.split("-"))
    segs, problems = pack(shotlist, script, bible, model, profile_id, t)
    C.write_json(C.abspath(a.out), segs)
    for s in segs:
        br = " ← 桥 " + s["continuity_bridge"]["prev_segment"] if s.get("continuity_bridge") else ""
        print(f"{s['id']:<5} {s['scene']:<8} {s['dur']:>3}s 镜头 {len(s['shots'])} 参考 {len(s.get('refs', []))}{br}")
    print(f"共 {len(segs)} 段，合计 {sum(s['dur'] for s in segs)} 秒 → {a.out}（{model['label']}，一段最长 {model['duration']['max']} 秒）")
    for p in problems:
        print("✗ " + p)
    sys.exit(3 if problems else 0)


if __name__ == "__main__":
    main()
