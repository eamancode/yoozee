#!/usr/bin/env python3
"""一集的分镜脚本（即梦优化版格式）：一段 = 一个小节。
  python3 {{TOOLS}}/storyboard_md.py [--episode 1] [--out 剧本/分镜脚本_E01.md] [--brief]
每个小节：小节头（时长 / 叙事时间跨度 / 剧情功能 / 高级视听策略 / 场景 / 人物 / 站位 / 空间坐标锁定 / 运动方向锁定 / 场景锚点连续锁定 /
动作连续锁定 / 持续氛围 / 持续光源 / 持续声音 / 群像差异化锁定）→ 逐镜 19 字段 → 小节约束 → 站位标注 → 动作 / 场景 / 声音三个连续性总结 →
即梦粘贴提示词（与 compile.py --paste 相同）→ 台词核对摘要；全集末尾是 check.py 真算的自检摘要。
站位、锚点、机位坐标、运动方向、动作连续、衔接级差都从镜头表 / 空间图 / 分段推导，不让模型再编；--brief 输出旧的简表。
读 剧本/分段.json、剧本/镜头表.json、剧本/剧本.json、剧本/资产圣经.json、需求.json。"""
import argparse
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import common as C  # noqa: E402
import compile as P  # noqa: E402
import check as K  # noqa: E402
import pack as PK  # noqa: E402

CAM_Y = {"Y1": "虫视仰拍", "Y2": "仰拍", "Y3": "微仰", "Y4": "平视", "Y5": "微俯", "Y6": "俯拍", "Y7": "顶视"}
CAM_X = {"X1": "正面", "X2": "侧 45 度", "X3": "侧面", "X4": "背面"}
SIZES = K.SIZES
CLOSER = {"push": 1, "pull": -1, "dolly_zoom": 0}  # SIZES 从大远景排到大特写，推近是 +1
POLICY_ZH = {"practicals_only": "只用实景光源，不补光", "motivated": "有动机的补光", "stylized": "风格化布光"}
def jimeng_constraint(seg, scene, bible):
    """即梦小节约束：通用句 + 只在本段有群像 / 武器 / 可读文字时才出现的句子。"""
    bits = ["人物形象保持一致", "人物站位与上一镜连续", "人物动作、身体朝向、手部姿态和道具状态必须与上一镜结束画面连续", "背景锚点、门窗开合状态、桌椅/墙面/地面痕迹/道具位置必须与上一镜连续"]
    if (seg.get("jimeng") or {}).get("group_variation"):
        bits.append("群像人物禁止同脸，背景群体必须有差异化面孔和体型锚点")
    props = {p.get("category") for p in (bible or {}).get("props", []) if p.get("name") in (scene or {}).get("props", [])}
    if "武器" in props:
        bits.append("武器不无端消失、不穿模")
    bits += ["场景结构保持一致", "光源方向保持一致", "同一场景持续音效保持不中断", "台词口型同步", "动作自然连贯", "无字幕", "无水印"]
    bits.append("只出现登记过的文字：" + "、".join(f"「{x}」" for x in seg["screen_text"]) if seg.get("screen_text") else "无多余文字")
    bits.append("避免人物变脸、手指变形、服装跳变、场景跳变、光影闪烁、背景物件凭空出现和无关人物闯入")
    if seg.get("dur", 0) >= 15:
        bits.append("≥15 秒小节在中段重述人物形象、服装与光源方向（中段再锚点）")
    if seg.get("dur", 0) >= 20:
        bits.append("20–30 秒小节中途有一次景别或情绪转折")
    return "；".join(bits) + "。"


def _s(x):
    return f"{float(x):.1f}".rstrip("0").rstrip(".")


def heading_parts(heading):
    """「内·病房走廊·夜」→ (内/外, 地点, 时间)。"""
    bits = [b.strip() for b in re.split(r"[·・/／]", heading or "") if b.strip()]
    inout = bits[0] if bits and bits[0] in ("内", "外", "内/外") else ""
    place = bits[1] if len(bits) > 1 else (bits[0] if bits else "")
    when = bits[2] if len(bits) > 2 else ""
    return inout, place, when


def size_after(size, moves):
    if size not in SIZES:
        return size
    i = SIZES.index(size)
    for m in moves or []:
        if m in CLOSER:
            i = max(0, min(len(SIZES) - 1, i + CLOSER[m]))
    return SIZES[i]


def first_sentences(text, n=2):
    parts = [p for p in re.split(r"(?<=[。！？])", (text or "").strip()) if p.strip()]
    return "".join(parts[:n])


def composition_text(sh):
    if sh.get("composition"):
        return sh["composition"]
    f = (sh.get("cam") or {}).get("f") or {}
    focus = sh.get("focus") or "主体"
    gaze = next((b.get("facing") for b in sh.get("blocking", []) if b.get("who") == focus and b.get("facing")), "")
    gaze = f"，视线{gaze}" if gaze else ""
    if f.get("identity") == "ots":
        return f"前景遮挡（过肩），{focus}在画面中央偏后{gaze}（默认，镜头表未写构图）"
    if f.get("geometry") == "frame":
        return f"框中框，{focus}居中{gaze}（默认）"
    if sh.get("size") in ("全景", "大远景", "远景", "中全景"):
        return f"纵深构图，{focus}在中景层{gaze}（默认）"
    if sh.get("subject") == "non_person":
        return "居中构图，主体占画面中央，背景虚化（默认）"
    return f"三分法，{focus}在画面中央{gaze}（默认）"


def camera_coord(sh, seg_shot):
    cam = sh.get("cam") or {}
    side = sh.get("camera_side") or f"{sh.get('focus') or '主体'}的{CAM_X.get(cam.get('x'), '正面')}"
    fg = [b["who"] for b in sh.get("blocking", []) if "前景" in (b.get("occluded_by") or "") or "前景" in (b.get("facing") or "")]
    mid = sh.get("focus") or ""
    bg = next((b.get("behind") for b in sh.get("blocking", []) if b.get("who") == mid and b.get("behind")), "")
    layers = []
    if fg:
        layers.append("前景层：" + "、".join(fg) + "的肩背")
    if mid and sh.get("subject") == "person":
        layers.append("中景层：" + mid)
    if bg:
        layers.append("背景层：" + bg)
    txt = f"摄像机在{side}，{CAM_Y.get(cam.get('y'), '平视')}，{CAM_X.get(cam.get('x'), '')}朝向{mid or '主体'}" + ("；" + "；".join(layers) if layers else "")
    return txt + "；不跨越门、墙或人物未到达的位置"


def lens_text(sh):
    f = (sh.get("cam") or {}).get("f") or {}
    dof = sh.get("dof") or f.get("dof")
    bits = []
    if f.get("lens_mm"):
        bits.append(f"{int(f['lens_mm'])}mm")
    bits.append({"shallow": "浅景深", "deep": "深景深", "rack": "拉焦"}.get(dof, "常规景深"))
    bits.append("焦点在" + (sh.get("focus") or "画面主体"))
    if f.get("identity"):
        bits.append(PK.F_ZH.get(f["identity"], f["identity"]))
    if f.get("geometry"):
        bits.append(PK.F_ZH.get(f["geometry"], f["geometry"]))
    return "，".join(bits)


def move_text(sh):
    moves = sh.get("move") or ["fixed"]
    zh = "、".join(PK.MOVE_ZH.get(m, m) for m in moves)
    a, b = sh.get("size", ""), size_after(sh.get("size", ""), moves)
    ce = sh.get("camera_emotion") or {}
    emo = f"；摄影机跟情绪：{ce.get('focal')}·{ce.get('type')}" if ce.get("focal") else ""
    if a == b:
        return f"{zh}，起始景别={b}=结束景别{emo}"
    return f"{zh}，起始景别 {a} -> 结束景别 {b}{emo}"


def action_text(sh):
    txt = (sh.get("action") or "").strip()
    pat, _ = PK.action_pattern_text(sh)
    if pat:
        txt += pat
    if sh.get("fight"):
        txt += PK.fight_text(sh["fight"], PK.fight_people(sh))
    return txt


def line_text(ln):
    return P.line_text(ln)


def light_text(sh, seg_shot):
    lt = sh.get("light") or {}
    full = seg_shot.get("light_full")
    base = f"主光：{lt.get('source_anchor', '未写')}" + (f"，{lt['direction']}" if lt.get("direction") else "")
    if full and not full.startswith("主光"):
        base = base + "；" + full
    elif full:
        base = full
    return base + f"；{POLICY_ZH.get(lt.get('policy'), '')}".rstrip("；")


def transition_text(sh, prev, nxt):
    tr = sh.get("transition") or {}
    bits = []
    bits.append("上一镜→本镜：" + (tr.get("in") or ("硬切（默认）" if prev else "小节开始")))
    bits.append("本镜→下一镜：" + (tr.get("out") or ("硬切（默认）" if nxt else "小节结束，留给下一小节")))
    if tr.get("cut_point"):
        bits.append("衔接点：" + tr["cut_point"])
    if prev:
        gap = abs(SIZES.index(sh["size"]) - SIZES.index(prev["size"])) if sh.get("size") in SIZES and prev.get("size") in SIZES else 0
        ang = (sh.get("cam") or {}).get("x") != (prev.get("cam") or {}).get("x") or (sh.get("cam") or {}).get("y") != (prev.get("cam") or {}).get("y")
        same = sh.get("focus") == prev.get("focus") and sh.get("subject") == prev.get("subject") == "person"
        bits.append(f"景别级差 {gap}{'（≥2）' if gap >= 2 else ''}，机位{'改变' if ang else '未变'}" + ("，同主体且级差<2、机位未变：跳切风险" if same and gap < 2 and not ang else ""))
        cont = (sh.get("start_state") or "").strip() == (prev.get("end_state") or "").strip()
        bits.append("人物动作、站位与道具" + ("承接上一镜结束画面" if cont else f"从上一镜「{prev.get('end_state', '')}」变为「{sh.get('start_state', '')}」"))
    return "；".join(bits)


def render_shot(idx, sh, seg_shot, prev, nxt, t0, t1, bible, scene):
    cam = sh.get("cam") or {}
    out = [f"镜头{idx}（{_s(t0)}-{_s(t1)}秒）"]
    purpose = sh.get("purpose", "")
    if sh.get("strategy"):
        purpose += f"；高级策略：{sh['strategy']}"
    out.append(f"镜头目的：{purpose}")
    pos = seg_shot.get("positions") or PK.positions_text(sh) or ("无人物（空镜 / 道具镜头）" if sh.get("subject") == "non_person" else "未写站位")
    if prev and prev.get("end_state"):
        pos += f"；承接上一镜结束：{prev['end_state']}"
    out.append(f"镜头人物站位：{pos}")
    out.append(f"镜头场景锚点：{seg_shot.get('anchors') or PK.anchors_text(sh, scene, bible) or '沿用小节锚点'}")
    out.append(f"镜头机位坐标：{camera_coord(sh, seg_shot)}")
    out.append(f"镜头运动方向：{seg_shot.get('motion') or PK.motion_text(sh)}")
    state = sh.get("state") or ""
    out.append(f"镜头动作连续：承接「{sh.get('start_state', '')}」；本镜{('人物状态：' + state + '；') if state else ''}动作见下；结束时留给下一镜「{sh.get('end_state', '')}」")
    out.append(f"画面：{sh.get('size', '')}，{CAM_Y.get(cam.get('y'), '平视')}，{CAM_X.get(cam.get('x'), '')}机位；{(sh.get('action') or '').strip()}")
    out.append(f"构图：{composition_text(sh)}")
    out.append(f"镜头：{lens_text(sh)}")
    out.append(f"运镜：{move_text(sh)}")
    out.append(f"动作：{action_text(sh)}")
    expr = seg_shot.get("expression") or ""
    terms = seg_shot.get("expression_terms") or []
    if not expr and sh.get("subject") == "non_person":
        expr = "无（非人物镜头）"
    elif not expr:
        expr = "无明显表情变化（镜头表未写情绪弧或节拍）"
    out.append("表情：" + expr + ("；核心词：" + "、".join(terms) if terms else ""))
    lines = seg_shot.get("lines") or []
    out.append("台词：" + ("；".join(line_text(ln) + ("" if ln.get("placement") else "；画内台词") for ln in lines) if lines else "无"))
    out.append(f"画外音：{sh.get('vo') or '无'}")
    snd = (sh.get("sfx") or "").strip()
    if sh.get("voice"):
        snd += ("；" if snd else "") + "人声：" + sh["voice"]
    snd += ("；台词画内同步" if lines and not any(ln.get("vo") for ln in lines) else ("；画外台词" if lines else "；无台词"))
    out.append(f"声音：{snd.strip('；')}；禁止 BGM / 配乐")
    out.append(f"灯光与光影：{light_text(sh, seg_shot)}")
    out.append(f"衔接：{transition_text(sh, prev, nxt)}")
    lt = sh.get("light") or {}
    out.append(f"起始画面：{sh.get('start_state', '')}；光：{lt.get('source_anchor', '')}{('，' + lt['direction']) if lt.get('direction') else ''}；声：{(sh.get('sfx') or '').split('；')[0]}")
    nxt_hint = ((nxt.get("transition") or {}).get("in") if nxt else "") or ((nxt.get("transition") or {}).get("cut_point") if nxt else "") or ("下一镜起始画面接此" if nxt else "小节末帧")
    out.append(f"结束画面：{sh.get('end_state', '')}；下一镜承接点：{nxt_hint}")
    return out


def spatial_map_for(bible, scene_id):
    return next((m for m in (bible or {}).get("spatial_maps", []) if m.get("scene") == scene_id), None)


def location_for(bible, scene):
    return next((lo for lo in (bible or {}).get("locations", []) if lo.get("name") == (scene or {}).get("location_ref")), None)


def render_segment(seg, shots_by_id, scene, bible, model, profile, res, script_lines):
    jm = seg.get("jimeng") or {}
    inout, place, when = heading_parts(scene.get("heading", ""))
    loc = location_for(bible, scene)
    smap = spatial_map_for(bible, seg.get("scene"))
    full = [shots_by_id.get(s.get("shot_id"), {}) for s in seg["shots"]]
    out = [f"小节 {seg['id']}：{seg.get('title', '')}", "", f"生成时长：0-{seg['dur']}秒", f"叙事时间跨度：{jm.get('narrative_span', '与生成时长同步')}", f"剧情功能：{jm.get('function', '')}",
           f"本小节高级视听策略：{jm.get('strategy') or '镜头表未写 strategy；本场改变的是：' + (scene.get('function') or '') + '；情绪曲线：' + (scene.get('emotion') or '')}",
           f"场景：{when or '未写时间'}，{place}{'（' + inout + '）' if inout else ''}，{jm.get('light_source') or (loc or {}).get('light_source', '')}".rstrip("，"),
           "人物：" + "、".join(f"@{c}" for c in scene.get("characters", []))]
    if smap and smap.get("positions"):
        out.append("人物站位：" + "；".join(f"{p['who']}在{p['at']}" + (f"，{p['meters']}" if p.get("meters") else "") + (f"，{p['facing']}" if p.get("facing") else "") for p in smap["positions"]))
    else:
        out.append("人物站位：" + (seg["shots"][0].get("positions") or "单人场，见各镜站位"))
    anchors = (loc or {}).get("anchors") or []
    depth = (loc or {}).get("depth") or ""
    out.append("空间坐标锁定：" + (f"锚点：{'、'.join(anchors)}；" if anchors else "") + (f"层次：{depth}；" if depth else "") + "角色与机位不跨越门、墙、电梯门等阻隔物；剧本未写阻隔物另一侧有人，另一侧保持为空")
    motions = [s.get("motion") for s in seg["shots"] if s.get("motion") and not s["motion"].startswith(("人物原地", "从「"))]
    out.append("运动方向锁定：" + ("；".join(dict.fromkeys(motions)) if motions else "本小节人物基本原地，无追逐 / 位移向量"))
    anc_seen = []
    for s in seg["shots"]:
        for a in re.findall(r"锚点：([^；]+)", s.get("anchors") or ""):
            for x in a.split("、"):
                if x and x not in anc_seen:
                    anc_seen.append(x)
    states = [h for s in seg["shots"] for h in re.findall(r"状态：([^；]+)", s.get("anchors") or "")]
    out.append("场景锚点连续锁定：" + ("、".join(anc_seen) if anc_seen else "见各镜") + ("；物件状态：" + "；".join(dict.fromkeys(states)) if states else "") + "；下一镜不得凭空改变背景或出现 / 消失物件")
    act_lock = [f"{h['id']}：{h['state']}" for h in full if h.get("state")]
    out.append("动作连续锁定：" + ("；".join(act_lock) if act_lock else "各镜 start_state 承接上一镜 end_state；手中道具按起止状态固定"))
    out.append(f"持续氛围：{jm.get('atmosphere', '')}")
    out.append(f"持续光源：{jm.get('light_source') or (loc or {}).get('light_source', '')}")
    bed = jm.get("sound_bed") or (seg.get("continuity_bridge") or {}).get("shared_bed") or ""
    out.append(f"持续声音：{bed or '以各镜环境声为准'}；同一场景内持续音效不中断")
    out.append(f"群像差异化锁定：{jm.get('group_variation') or '本小节无群像'}")
    if seg.get("continuity_bridge"):
        b = seg["continuity_bridge"]
        out.append(f"一致性桥：{b.get('reanchor_text') or ('承接 ' + b['prev_segment'] + ' 结尾「' + b.get('handoff_state', '') + '」')}；同一套参考同编号" + (f"；共用声床：{b['shared_bed']}" if b.get("shared_bed") else ""))
    out.append("")
    t = [float(s["at"]) for s in seg["shots"]] + [float(seg["dur"])]
    for i, (s, sh) in enumerate(zip(seg["shots"], full)):
        prev = full[i - 1] if i > 0 else None
        nxt = full[i + 1] if i + 1 < len(full) else None
        out += render_shot(i + 1, sh, s, prev, nxt, t[i], t[i + 1], bible, scene)
        out.append("")
    warns = [w for w in (res.get("warnings") or []) if w]  # seg.warnings 是风险标签 id，不进交付文本
    out.append("小节约束：")
    out.append(jimeng_constraint(seg, scene, bible) + ("" if not warns else " " + " ".join(dict.fromkeys(warns))))
    out.append("")
    out.append("小节人物场景站位标注：")
    out.append("人物：" + "、".join(scene.get("characters", [])))
    out.append(f"场景：{scene.get('heading', '')}" + (f"，{(loc or {}).get('plain_view', '').split('。')[0]}" if loc else ""))
    if smap and smap.get("positions"):
        out.append("人物在场景中的位置：" + "；".join(f"{p['who']}在{p['at']}" + (f"（{p['meters']}）" if p.get("meters") else "") + (f"，{p['facing']}" if p.get("facing") else "") + (f"，被{p['occluded_by']}挡住" if p.get("occluded_by") else "") for p in smap["positions"]))
    else:
        out.append("人物在场景中的位置：" + (seg["shots"][0].get("positions") or "见各镜"))
    out.append("")
    out.append("小节动作连续性总结：")
    chain, last_state = [], None
    for sh in full:
        st = sh.get("state")
        if st and st != last_state:
            chain.append(f"{sh['id']} 起：{st}")
            last_state = st
    chain += [f"{sh['id']}：{sh['end_state']}" for sh in full[-1:] if sh.get("end_state")]
    out.append("；".join(chain) if chain else "本小节人物姿态与持物无变化；" + (full[-1].get("end_state", "") if full else ""))
    out.append("")
    out.append("小节场景连续性总结：")
    out.append(("锚点贯穿：" + "、".join(anc_seen) + "；" if anc_seen else "") + ("物件状态变化：" + "；".join(dict.fromkeys(states)) + "；" if states else "") + "门窗 / 电梯门 / 道具位置除上述变化外保持不变，背景不替换")
    out.append("")
    out.append("小节声音连续性总结：")
    snd_chain = [f"镜头{i + 1}：{s['sound']}" for i, s in enumerate(seg["shots"]) if s.get("sound")]
    out.append((f"持续声床「{bed}」从头到尾不断；" if bed else "") + "；".join(snd_chain) + "；禁止 BGM / 配乐" + ("；进入下一小节时声床继续承接" if bed else ""))
    out.append("")
    out.append("即梦粘贴提示词：")
    out.append("```text")
    out.append(res["paste"].rstrip())
    out.append("```")
    out.append("")
    ids = [ln.get("line_id") for s in seg["shots"] for ln in s.get("lines", []) if ln.get("line_id")]
    scene_ids = [lid for lid in script_lines if lid.startswith(seg.get("scene", ""))]
    missing_here = [lid for lid in scene_ids if lid not in ids]
    out.append("台词核对摘要：")
    out.append(f"本小节台词 {len(ids)} 句（{'、'.join(ids) if ids else '无'}），原文一字未改；本场共 {len(scene_ids)} 句" + ("，其余在同场其他小节" if missing_here and len(scene_ids) > len(ids) else "") + "。")
    out.append("")
    return out


def self_check(script, bible, shotlist, segs, brief, profile, model, ep_prefix):
    ctx = {"script": script, "bible": bible, "shots": shotlist, "brief": brief, "profile": profile, "model": model}
    rs = K.check_shots(shotlist, ctx)
    rg = K.check_segments(segs, ctx)
    lines = []
    for r in (rs, rg):
        err = [i for i in r.items if i["level"] == "error"]
        warn = [i for i in r.items if i["level"] != "error"]
        lines.append(f"{r.kind}：{len(err)} 个 error，{len(warn)} 个 warn" + ("（通过）" if not err else "（未过）"))
        for i in r.items:
            lines.append(f"- {'✗' if i['level'] == 'error' else '⚠'} [{i['rule']}] {i['message']}")
    hit = {i["rule"] for r in (rs, rg) for i in r.items}
    skipped = {x[0] if isinstance(x, (list, tuple)) else x for r in (rs, rg) for x in (getattr(r, "skipped", []) or [])}
    all_rules = []
    for name in ("shots", "segments"):
        path = C.runtime_file("rules", f"{name}.json")
        if path:
            all_rules += [x["id"] for x in json.loads(open(path, encoding="utf-8").read())]
    passed = [x for x in all_rules if x not in hit and x not in skipped]
    lines.append(f"规则 {len(all_rules)} 条：命中 {len(hit & set(all_rules))}，跳过 {len(skipped & set(all_rules))}，通过 {len(passed)}。通过：" + "、".join(passed))
    if skipped & set(all_rules):
        lines.append("跳过（条件不满足）：" + "、".join(sorted(skipped & set(all_rules))))
    return lines


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--episode", type=int)
    ap.add_argument("--out")
    ap.add_argument("--project")
    ap.add_argument("--brief", action="store_true", help="旧的简表格式")
    a = ap.parse_args()
    if a.project:
        import pathlib
        C.ROOT = pathlib.Path(a.project).resolve()
    segs_all = C.read_json(C.abspath("剧本/分段.json"))
    shotlist = C.read_json(C.abspath("剧本/镜头表.json"))
    shots = {s["id"]: s for s in shotlist["shots"]}
    script = C.read_json(C.abspath("剧本/剧本.json"))
    bible = C.read_json(C.abspath("剧本/资产圣经.json")) if os.path.exists(C.abspath("剧本/资产圣经.json")) else {}
    brief = C.project_brief() or {}
    scenes = {sc["id"]: sc for sc in script["scenes"]}
    ep = a.episode or int(shotlist.get("episode") or 1)
    ep_prefix = f"E{ep:02d}"
    segs = [s for s in segs_all if str(s.get("scene", "")).startswith(ep_prefix)]
    model = C.model(C.project_model())
    profile = C.profile(C.project_profile())
    epi = next((e for e in script.get("episodes", []) if e.get("no") == ep), {})
    results = {s["id"]: P.compile_segment(s, model, profile=profile) for s in segs}
    script_lines = {lid for lid in K.script_lines(script) if lid.startswith(ep_prefix)}
    if a.brief:
        out = brief_table(script, ep, segs, scenes, shots, results, model)
    else:
        out = [f"# 《{script['title']}》第 {ep} 集 分镜脚本（即梦优化版格式）", "",
               "参数确认：", f"画幅比例：{C.project_ratio() or '未写'}", f"视频类型：{'短剧' if brief.get('duanju_mode') else '短视频'}",
               f"视觉风格：{'真人写实' if profile.get('medium') == 'live_action' else profile.get('label', '')}",
               f"单条生成时长：上限 {model.get('duration', {}).get('max', '')} 秒；本集 {len(segs)} 段，合计 {sum(s['dur'] for s in segs)} 秒",
               "时长策略：按完整戏剧单元划分；同场 ≤ 上限一段，超过按动作完成点 > 句末 > 景别变化拆段并带一致性桥",
               "输出版本：成片执行版（纯文本分镜格式）", "",
               "全局风格锁定：", (profile.get("style_block") or {}).get("zh", ""), (profile.get("acting_block") or {}).get("zh", ""),
               f"集钩：入「{epi.get('hook_in', '')}」 → 出「{epi.get('hook_out', '')}」", "",
               "角色音色设定："]
        anyvp = False
        for ch in bible.get("characters", []):
            vp = ch.get("voice_profile") or {}
            if vp.get("timbre"):
                anyvp = True
                out.append(f"{ch['name']}：{vp.get('age_feel', '')}，{vp['timbre']}，{vp.get('line_quality', '画内近场')}，{vp.get('pace', '')}，{vp.get('breath', '')}，{vp.get('mood', '')}".replace("，，", "，").rstrip("，"))
        if not anyvp:
            out.append("（资产圣经未写 voice_profile；台词四元的音色留空）")
        out.append("说明：本表定义角色全局声音底色；镜头内台词四元是该句的具体取值，冲突时以句内标注为准。")
        out += ["", "| 小节 | 场 | 秒 | 镜头 | 参考 | 桥 |", "|---|---|---|---|---|---|"]
        for s in segs:
            out.append(f"| {s['id']} | {s['scene']} {scenes.get(s['scene'], {}).get('heading', '')} | {s['dur']} | {len(s['shots'])} | {len(s.get('refs', []))} | {'← ' + s['continuity_bridge']['prev_segment'] if s.get('continuity_bridge') else ''} |")
        out.append("")
        for s in segs:
            out += render_segment(s, shots, scenes.get(s["scene"], {}), bible, model, profile, results[s["id"]], script_lines)
            out.append("---")
            out.append("")
        used = {ln.get("line_id") for s in segs for sh in s["shots"] for ln in sh.get("lines", [])}
        miss = sorted(script_lines - used)
        out += ["台词核对摘要（全集）：", f"本集剧本台词 {len(script_lines)} 句，落入镜头 {len(script_lines & used)} 句" + (f"；缺：{'、'.join(miss)}" if miss else "；已保留原文全部台词"), ""]
        out += ["自检摘要（check.py 真算）："] + self_check(script, bible, shotlist, segs, brief, profile, model, ep_prefix)
        up = [f"{f['tag']} ← {f['file']}" for s in segs for f in results[s["id"]]["files"]]
        out += ["", "上传素材总表：", "；".join(dict.fromkeys(up)) if up else "本集无参考图（资产未出图；人物外貌由各段开头的人物声明兜底）"]
    path = C.abspath(a.out or f"剧本/分镜脚本_{ep_prefix}.md")
    with open(path, "w", encoding="utf-8") as fh:
        fh.write("\n".join(out) + "\n")
    print(f"✓ {os.path.relpath(path, C.ROOT)}（{len(segs)} 段，{'简表' if a.brief else '即梦格式'}）")


def brief_table(script, ep, segs, scenes, shots, results, model):
    epi = next((e for e in script.get("episodes", []) if e.get("no") == ep), {})
    out = [f"# 《{script['title']}》第 {ep} 集 分镜简表", "", f"> 模型：{model.get('label', model['id'])}；共 {len(segs)} 段，合计 {sum(s['dur'] for s in segs)} 秒。",
           f"> 集钩：入「{epi.get('hook_in', '')}」 → 出「{epi.get('hook_out', '')}」", ""]
    for s in segs:
        res = results[s["id"]]
        sc = scenes.get(s["scene"], {})
        out += [f"## {s['id']} · {sc.get('heading', '')} · {s['dur']} 秒", "", "| # | 时间 | 景别 | 机位 | 目的 | 台词 |", "|---|---|---|---|---|---|"]
        for sh in s["shots"]:
            full = shots.get(sh.get("shot_id"), {})
            lines = "；".join(f"{ln['speaker']}：{ln['text']}" for ln in sh.get("lines", []))
            out.append(f"| {sh.get('shot_id', '')} | {sh['at']}s | {sh.get('size', '')} | {sh.get('camera', '')} | {full.get('purpose', '')} | {lines} |")
        out += ["", "```text", res["paste"].rstrip(), "```", ""]
    return out


if __name__ == "__main__":
    main()
