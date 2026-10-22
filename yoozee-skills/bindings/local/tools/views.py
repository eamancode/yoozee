#!/usr/bin/env python3
"""把 JSON 产物渲染成人看的 Markdown 视图（只读派生，改 JSON 再重渲）。
  python3 {{TOOLS}}/views.py all                       需求 / 剧本 / 镜头表 / 分段表 / 连戏表 有什么渲什么
  python3 {{TOOLS}}/views.py brief|script|shots|segments|bible [输入.json] [--out 文件.md]
分段表.md 保留 1.0 的样式（进度表 + 每段一个 json 块），1.0 的 segments.py / review_seg.py 仍能读。"""
import argparse
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import common as C  # noqa: E402

PAIRS = {"brief": ("需求.json", "需求文档.md"), "script": ("剧本/剧本.json", "剧本/剧本.md"), "shots": ("剧本/镜头表.json", "剧本/镜头表.md"),
         "segments": ("剧本/分段.json", "剧本/分段表.md"), "bible": ("剧本/资产圣经.json", "剧本/连戏表.md")}


def fact(b, k):
    f = b.get(k) or {}
    return f"{f.get('value', '')}（{f.get('status', '待答')}）" if f else "（待答）"


def render_brief(b):
    out = [f"# {b['title']} —— 需求文档", "", f"> 生成模型：{b['video_model']}；授权档位：{'每步确认' if b['auth_mode'] == 'step' else '全权'}；风格档案：{b['style_profile']}；剧本来源：{b['script_mode']}" + (f"；入口：{b['entry']}" if b.get("entry") else ""), "",
           "## 1. 概述"]
    for k, label in (("purpose", "拿去干什么、给谁看"), ("platform", "平台与画幅"), ("duration", "时长"), ("taste", "口味"), ("budget", "预算"), ("acceptance", "验收标准")):
        out.append(f"- {label}：{fact(b, k)}")
    if b.get("duanju_mode"):
        out += ["", "## 短剧速产", f"- {json.dumps(b['duanju_mode'], ensure_ascii=False)}"]
    out += ["", "## 2. 要求与反馈（R-）", "| 编号 | 内容 | 指向哪几段 | 原话 | 状态 |", "|---|---|---|---|---|"]
    for it in b.get("items", []):
        out.append(f"| {it['id']} | {it['content']} | {'、'.join(it.get('segments', []))} | {it.get('quote', '')}（{it.get('date', '')}） | {it['status']} |")
    out += ["", "## 3. 不做"] + [f"- {x}" for x in b.get("not_doing", [])]
    out += ["", "## 4. 约束"] + [f"- {x}" for x in b.get("constraints", [])]
    out += ["", "## 5. 待确认（Q-）", "| 编号 | 问题 | 挡不挡开工 | 状态 |", "|---|---|---|---|"]
    for q in b.get("questions", []):
        out.append(f"| {q['id']} | {q['question']} | {'挡' if q['blocking'] else '不挡'} | {q['status']} |")
    out += ["", "## 6. 替他定了什么"] + [f"- {x}" for x in b.get("decided_for_you", [])]
    return "\n".join(out) + "\n"


def render_script(s):
    out = [f"# {s['title']} —— 剧本", ""]
    for sc in s["scenes"]:
        out.append(f"## {sc['id']} {sc.get('heading', '')}" + (f"　〔{sc['kind']}〕" if sc.get("kind") else ""))
        meta = []
        if sc.get("characters"):
            meta.append("人物：" + "、".join(sc["characters"]))
        if sc.get("function"):
            meta.append("功能：" + sc["function"])
        if sc.get("emotion"):
            meta.append("情绪：" + sc["emotion"])
        if sc.get("time_jump"):
            meta.append("时间跨度：" + sc["time_jump"])
        if meta:
            out.append("> " + "；".join(meta))
        if sc.get("fight"):
            out.append("> 打斗：" + " → ".join(p["phase"] for p in sc["fight"].get("phase_plan", [])) + "；参战：" + "、".join(f"{x['name']}（{x.get('style', '')} {x.get('weapon', '')}）" for x in sc["fight"].get("sides", [])))
        out.append("")
        for b in sc["beats"]:
            tag = f" `{b['tag']}`" if b.get("tag") else ""
            if b["kind"] in ("line", "vo"):
                out.append(f"- **{b['id']}** {b.get('speaker', '')}{'（画外音）' if b['kind'] == 'vo' else ''}：“{b['text']}”{tag}")
            else:
                out.append(f"- {b['id']} 〔{b['kind']}〕{b['text']}{tag}" + (f"　→ 状态：{b['state_change']}" if b.get("state_change") else ""))
        out.append("")
    if s.get("screen_text"):
        out += ["## 画面里的字（逐字）"] + [f"- {t.get('who', '')}：{t['text']}（{t.get('when', '')}）" for t in s["screen_text"]] + [""]
    return "\n".join(out) + "\n"


def render_shots(sl):
    out = [f"# 镜头表（档案 {sl.get('profile', '')}）", "", "| 镜 | 场 | 目的 | 景别 | 机位 | 运镜 | 台词 | 估时 | 风险 |", "|---|---|---|---|---|---|---|---|---|"]
    for sh in sl["shots"]:
        cam = sh.get("cam", {})
        out.append(f"| {sh['id']} | {sh['scene']} | {sh['purpose']} | {sh.get('size', '')} | {cam.get('z', '')}{cam.get('y', '')}{cam.get('x', '')} | {'+'.join(sh.get('move', []))} | {'、'.join(l['line_id'] for l in sh.get('lines', []))} | {sh.get('est', '')} | {'、'.join(sh.get('risk_tags', []))} |")
    out.append("")
    for sh in sl["shots"]:
        out.append(f"## {sh['id']} {sh['purpose']}")
        out.append(f"- 画面：{sh.get('action', '')}")
        if sh.get("blocking"):
            out.append("- 站位：" + "；".join(f"{b['who']}在{b['at']}" + (f"朝{b['facing']}" if b.get("facing") else "") for b in sh["blocking"]))
        if sh.get("performance"):
            out.append("- 表演：" + "；".join(sh["performance"].get("beats", [])))
        if sh.get("light"):
            out.append(f"- 光：{sh['light'].get('source_anchor', '')} {sh['light'].get('direction', '')}")
        if sh.get("sfx") or sh.get("voice"):
            out.append(f"- 声：{sh.get('sfx', '')} {sh.get('voice', '')}")
        out.append(f"- 起止：{sh.get('start_state', '')} → {sh.get('end_state', '')}")
        out.append("")
    return "\n".join(out) + "\n"


def render_segments(segs):
    m = C.model(C.project_model())
    out = ["# 分段表（进度表）", "", f"> 生成模型：{m['label']}，一段最长 {m['duration']['max']} 秒。一段一次生成，段内多镜头。六格全满才算这段做完。每段下面的 json 块由 pack.py 从镜头表装出，改镜头表再重装；拆合段用 segment-edit 或直接改 分段.json。", "",
           "| 段 | 场 | 内容 | 秒 | 剧本 | 参考 | 生成 | 过审 | 入片 | 验收 |", "|---|---|---|---|---|---|---|---|---|---|"]
    for s in segs:
        out.append(f"| {s['id']} | {s['scene']} | {s.get('title', '')} | {s['dur']} | ✓ | | | | | |")
    out.append(f"\n合计 {sum(s['dur'] for s in segs)} 秒\n")
    for s in segs:
        out.append(f"## {s['id']} {s.get('title', '')}" + (f"　（桥 ← {s['continuity_bridge']['prev_segment']}）" if s.get("continuity_bridge") else ""))
        out.append("```json")
        out.append(json.dumps(s, ensure_ascii=False, indent=1))
        out.append("```")
        out.append("")
    return "\n".join(out) + "\n"


def render_bible(b):
    out = ["# 连戏表（资产圣经）", "", "## 人物", "| 人物 | 层级 | 变体 | 锚点 | 锁定卡 | 定妆照 | 音色 | 校验 |", "|---|---|---|---|---|---|---|---|"]
    for ch in b.get("characters", []):
        for v in ch.get("variants", []):
            out.append(f"| {ch['name']} | {ch.get('tier', '')} | {v['label']} | {'；'.join(ch.get('anchors', []))} | {len(v.get('lock_card', []))} 条 | {v.get('sheet', '')} + {len(v.get('crops', []))} 张 | {v.get('voice', '')} | {'✓' if v.get('checked') else ''} |")
    out += ["", "## 道具", "| 道具 | 类别 | 持有 | 状态 | 图 | 校验 |", "|---|---|---|---|---|---|"]
    for p in b.get("props", []):
        out.append(f"| {p['name']} | {p.get('category', '')} | {p.get('holder', '')} | {'；'.join(s.get('when', '') + ':' + s.get('desc', '') for s in p.get('states', []))} | {p.get('image', '')} | {'✓' if p.get('checked') else ''} |")
    out += ["", "## 场景", "| 场景 | 空间类型 | 光源 | 锚点 | 空景 / A / B | 四方位 | 禁人物自检 | 校验 |", "|---|---|---|---|---|---|---|---|"]
    for l in b.get("locations", []):
        out.append(f"| {l['name']} | {l.get('space_type', '')} | {l.get('light_source', '')} | {'；'.join(l.get('anchors', []))} | {l.get('plate', '')} / {l.get('side_a', '')} / {l.get('side_b', '')} | {'✓' if l.get('views') else ''} | {'✓' if l.get('no_people_checked') else ''} | {'✓' if l.get('checked') else ''} |")
        if l.get("plain_view"):
            out.append(f"\n> {l['name']}：{l['plain_view']}\n")
    out += ["", "## 伏笔物件"] + [f"- {p['id']}：{p['what']}（{p.get('scene', '')}）" for p in b.get("plants", [])]
    out += ["", "## 空间图"]
    for m in b.get("spatial_maps", []):
        out.append(f"### {m['scene']}" + (f"（主视角：{m['main_view']}）" if m.get("main_view") else "") + ("　✓ 已确认" if m.get("approved") else ""))
        out.append("```")
        out.append(m["map"])
        out.append("```")
        for p in m.get("positions", []):
            out.append(f"- {p['who']}：{p['at']}" + (f"，{p['meters']}" if p.get("meters") else "") + (f"，朝{p['facing']}" if p.get("facing") else "") + (f"，被{p['occluded_by']}遮挡" if p.get("occluded_by") else ""))
        for c in m.get("cameras", []):
            out.append(f"- 机位 {c['shot']}：{c['side']}" + (f"，身后 {c['plate']}" if c.get("plate") else ""))
        out.append("")
    return "\n".join(out) + "\n"


RENDER = {"brief": render_brief, "script": render_script, "shots": render_shots, "segments": render_segments, "bible": render_bible}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("kind", choices=list(PAIRS) + ["all"])
    ap.add_argument("path", nargs="?")
    ap.add_argument("--out")
    ap.add_argument("--project")
    a = ap.parse_args()
    if a.project:
        import pathlib
        C.ROOT = pathlib.Path(a.project).resolve()
    kinds = list(PAIRS) if a.kind == "all" else [a.kind]
    for kind in kinds:
        src, dst = PAIRS[kind]
        src = a.path or src if a.kind != "all" else src
        dst = a.out or dst if a.kind != "all" else dst
        p = C.abspath(src)
        if not os.path.exists(p):
            if a.kind == "all":
                continue
            sys.exit(f"找不到 {src}")
        text = RENDER[kind](C.read_json(p))
        out = C.abspath(dst)
        os.makedirs(os.path.dirname(out), exist_ok=True)
        open(out, "w", encoding="utf-8", newline="\n").write(text)
        print(f"✓ {dst}")


if __name__ == "__main__":
    main()
