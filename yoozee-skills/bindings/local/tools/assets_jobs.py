#!/usr/bin/env python3
"""从资产圣经确定性地生成出图任务（一次到底用，不靠模型现写提示词）：
  python3 {{TOOLS}}/assets_jobs.py [--episodes 1,2] [--out 素材库/jobs.json] [--only characters|locations|props] [--force]
- 人物：每个 base 变体一张三视图（16:9 灰底，胸上特写 | 全身正面 | 全身侧面），提示词从 visual_anchor / lock_card / costume 拼；
  extra 层级也出三视图（裁图只取正面也够）。服装变体（kind=costume）有 trigger_beat 在所选集内时也出。
- 场景：每个场景一张无人空景（9:16）；在所选集里出现两人以上对话场（有 spatial_maps）的场景再出 A 侧 / B 侧；
  提示词从 plain_view / depth / light_source / anchors 拼，固定追加「中国大陆公立 / 民用场景，标牌只中文或留白，无英文字母无真实 logo，无人」。
- 道具：priority ≥2 的道具一张 1:1；有 states[].image 路径的状态各一张。
- 已有文件默认跳过（--force 重出）；文件名就是 bible 里写的路径，没写路径的按「名字_变体」补上并回写 bible。
- 真人剧（档案 medium=live_action）由图片适配前置写实约束块；其他档案 no_style=true 并前置档案 style_block。
产出 jobs.json 交给 {{TOOLS}}/adapters/image_openai_compat.py --jobs；之后 run.py 会裁三视图并回填 crops。"""
import argparse
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import common as C  # noqa: E402

SHEET = ("Character costume reference sheet of ONE person, photographed in a photo studio against a clean, uniform, flat mid-grey seamless backdrop. "
         "Three side-by-side panels of equal height with plain grey gaps between them, no lines, no text, no labels, no numbers, no watermark. "
         "LEFT panel: chest-up portrait at a 3/4 angle, face large and clear. MIDDLE panel: full body front view, relaxed A-pose, arms slightly away from the torso, head to shoes fully in frame. "
         "RIGHT panel: full body true 90-degree left side profile, same pose, same scale, same floor baseline. The same person in the same outfit in all three panels.\n"
         "Identity: {identity}\nOutfit: {costume}\nSoft even studio light, weak shadows, stable white balance, no wide-angle distortion.")
PLATE_TAIL = (" Completely empty, nobody present, no figures, no silhouettes, no shadows of people, still objects only. "
              "This is a real-world location in mainland China: every sign, plaque, notice and label is either blank or in simplified Chinese characters too small to read; "
              "there are absolutely no English words, no Latin letters and no real-world logos anywhere in the image.")
SIDE_A = "Background plate for a reverse-angle shot, vertical 9:16, eye level, 50mm lens: the camera stands at {side} looking toward {toward}. {view}"
PROP = "Product photograph of {name}: {desc}. Lying on a neutral surface, lit by a single soft lamp, shallow depth of field, no people, no hands, no readable text, no logos."


def slug(s):
    return re.sub(r"[\\/:*?\"<>|\s]+", "_", s)


def identity_text(ch, v):
    va = ch.get("visual_anchor")
    bits = []
    if isinstance(va, str) and va.strip():
        bits.append(va.strip())
    elif isinstance(va, dict):
        bits += [str(x) for x in va.values() if isinstance(x, str)]
    for k in ("age", "face_hair", "marks", "glasses"):
        if v.get(k) and v[k] not in ("无", "——"):
            bits.append(f"{k}: {v[k]}")
    bits += [a for a in (ch.get("anchors") or []) if a not in "".join(bits)]
    return "；".join(dict.fromkeys(bits))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--episodes", default="", help="只出这些集里用到的资产，如 1,2；空 = 全部")
    ap.add_argument("--out", default="素材库/jobs.json")
    ap.add_argument("--only", choices=["characters", "locations", "props"])
    ap.add_argument("--force", action="store_true")
    ap.add_argument("--project")
    a = ap.parse_args()
    if a.project:
        import pathlib
        C.ROOT = pathlib.Path(a.project).resolve()
    bible_path = C.abspath("剧本/资产圣经.json")
    bible = C.read_json(bible_path)
    script = C.read_json(C.abspath("剧本/剧本.json")) if os.path.exists(C.abspath("剧本/剧本.json")) else {"scenes": []}
    prof = C.profile(C.project_profile())
    live = prof.get("medium") == "live_action"
    style_prefix = "" if live else ((prof.get("style_block") or {}).get("zh", "") + "\n")
    eps = {int(x) for x in a.episodes.split(",") if x.strip()} if a.episodes else None
    scenes = [sc for sc in script.get("scenes", []) if eps is None or int(sc["id"][1:3]) in eps]
    in_scope_chars = {c for sc in scenes for c in sc.get("characters", [])} if eps is not None else None
    in_scope_locs = {sc.get("location_ref") for sc in scenes} if eps is not None else None
    in_scope_props = {p for sc in scenes for p in sc.get("props", [])} if eps is not None else None
    two_person = {sc.get("location_ref") for sc in scenes if len(sc.get("characters", [])) >= 2}
    beats_in_scope = {b["id"] for sc in scenes for b in sc.get("beats", [])}
    jobs, changed = [], False

    def add(jid, out, prompt, aspect, no_style=False):
        if not a.force and os.path.exists(C.abspath(out)) and os.path.getsize(C.abspath(out)) > 0:
            return
        jobs.append({"id": jid, "out": out, "prompt": style_prefix + prompt, "aspect": aspect, "no_style": no_style or not live, "force": a.force})

    if a.only in (None, "characters"):
        for ch in bible.get("characters", []):
            if in_scope_chars is not None and ch["name"] not in in_scope_chars:
                continue
            for v in ch.get("variants", []):
                if v.get("kind") == "costume" and eps is not None and v.get("trigger_beat") not in beats_in_scope:
                    continue
                label = v.get("label") or "base"
                stem = f"{ch['name']}_{label}" if v.get("kind") == "costume" else ch["name"]
                if not v.get("sheet"):
                    v["sheet"] = f"素材库/人物/{slug(stem)}_三视图.png"
                    changed = True
                if not v.get("crops"):
                    v["crops"] = [f"素材库/人物/{slug(stem)}_{k}.png" for k in ("特写", "全身正面", "全身侧面")]
                    changed = True
                prompt = SHEET.format(identity=identity_text(ch, v), costume=v.get("costume") or "；".join(v.get("lock_card") or []))
                add(f"{stem}_三视图", v["sheet"], prompt, "16:9")

    if a.only in (None, "locations"):
        for lo in bible.get("locations", []):
            if in_scope_locs is not None and lo["name"] not in in_scope_locs:
                continue
            base = f"素材库/场景/{slug(lo['name'])}_空景.png"
            if not lo.get("plate"):
                lo["plate"] = base
                changed = True
            view = (lo.get("plain_view") or "").strip()
            extra = "；".join(x for x in (lo.get("depth"), lo.get("light_source"), ("锚点：" + "、".join(lo["anchors"])) if lo.get("anchors") else "") if x)
            add(f"{lo['name']}_空景", lo["plate"], f"Vertical 9:16 frame, eye level, slight downward tilt. {view} {extra}." + PLATE_TAIL, "9:16")
            if lo["name"] in two_person:
                anchors = lo.get("anchors") or ["主体", "另一侧"]
                if not lo.get("side_a"):
                    lo["side_a"] = lo["plate"].replace("_空景", "_A侧")
                    changed = True
                if not lo.get("side_b"):
                    lo["side_b"] = lo["plate"].replace("_空景", "_B侧")
                    changed = True
                add(f"{lo['name']}_A侧", lo["side_a"], SIDE_A.format(side=f"the {anchors[-1]} side", toward=anchors[0], view=view) + PLATE_TAIL, "9:16")
                add(f"{lo['name']}_B侧", lo["side_b"], SIDE_A.format(side=f"the {anchors[0]} side", toward=anchors[-1], view=view) + PLATE_TAIL, "9:16")

    if a.only in (None, "props"):
        for pr in bible.get("props", []):
            if in_scope_props is not None and pr["name"] not in in_scope_props:
                continue
            if (pr.get("priority") or 1) < 2:
                continue
            if not pr.get("image"):
                pr["image"] = f"素材库/道具/{slug(pr['name'])}.png"
                changed = True
            desc = "；".join(pr.get("evidence") or []) or pr.get("category", "")
            add(pr["name"], pr["image"], PROP.format(name=pr["name"], desc=desc), "1:1")
            for st in pr.get("states") or []:
                if st.get("image"):
                    add(f"{pr['name']}_{st.get('when', '')}", st["image"], PROP.format(name=pr["name"], desc=(desc + "；此刻状态：" + (st.get("desc") or ""))), "1:1")

    if changed:
        C.write_json(bible_path, bible)
    out = C.abspath(a.out)
    os.makedirs(os.path.dirname(out), exist_ok=True)
    C.write_json(out, jobs)
    print(f"✓ {len(jobs)} 个出图任务 → {a.out}" + ("（资产圣经已回填文件路径）" if changed else ""))


if __name__ == "__main__":
    main()
