"""把 yoozee 测试项目（需求.json、资产圣经.json、剧本.md）转成评测章节的种子（与七床 seed.json 同一形状）。

用法：python build_seed.py <测试项目目录> <画风快照 JSON 文件> <输出目录>
输出目录里写 seed.json 和 chapter.txt；第 1 集出场的角色、场景、道具确认成资产，其余角色留作待确认。
"""
import json
import os
import subprocess
import sys

project_dir, style_file, out_dir = sys.argv[1:4]
here = os.path.dirname(os.path.abspath(__file__))
os.makedirs(out_dir, exist_ok=True)
chapter_path = os.path.join(out_dir, "chapter.txt")
subprocess.run([sys.executable, os.path.join(here, "script_to_chapter.py"), os.path.join(project_dir, "剧本", "剧本.md"), chapter_path], check=True)
chapter = open(chapter_path, encoding="utf-8").read()
need = json.load(open(os.path.join(project_dir, "需求.json"), encoding="utf-8"))
bible = json.load(open(os.path.join(project_dir, "剧本", "资产圣经.json"), encoding="utf-8"))
style = open(style_file, encoding="utf-8").read().strip()


def base_variant(c):
    for v in c.get("variants", []):
        if v.get("kind") == "base":
            return v
    return (c.get("variants") or [{}])[0]


def character(c):
    v = base_variant(c)
    voice = c.get("voice_profile") or {}
    appearance = "，".join(x for x in [v.get("face_hair", ""), v.get("marks", "")] if x and x != "无")
    held = [p["name"] for p in bible.get("props", []) if p.get("holder") == c["name"]]
    return {
        "name": c["name"],
        "aliases": [],
        "role": "主角" if c.get("tier") == "lead" else "配角",
        "appearance": appearance or (c.get("visual_anchor") or "").split("；")[0],
        "clothing": v.get("costume", ""),
        "physique": "",
        "personality": "，".join(c.get("derived", [])),
        "props": "、".join(held),
        "consistencyPrompt": "、".join(v.get("lock_card", [])),
        "multiViewPrompt": f"{c['name']}，正面、侧面、背面三视图，纯色背景",
        "voiceLanguage": "普通话",
        "voiceAge": voice.get("age_feel", ""),
        "voiceTimbre": voice.get("timbre", ""),
    }


characters, pending = [], []
for c in bible.get("characters", []):
    if c.get("tier") == "extra":
        continue
    item = character(c)
    if c["name"] in chapter:
        characters.append(item)
    else:
        pending.append({"name": c["name"], "category": "character", "details": item})
scenes = []
for loc in bible.get("locations", []):
    if loc["name"] not in chapter:
        continue
    scenes.append({"name": loc["name"], "description": loc.get("plain_view", ""), "prompt": loc.get("plain_view", ""), "anchors": loc.get("anchors", [])})
props = []
for p in bible.get("props", []):
    if not str(p.get("first_seen", "")).startswith("E01"):
        continue
    desc = "；".join(p.get("evidence", [])[:2])
    props.append({"name": p["name"], "description": desc, "prompt": desc})
seed = {
    "project": {
        "name": f"yoozee 评测·{need['title']}",
        "stylePresetId": json.loads(style)["presetId"],
        "styleProfileJson": style,
        "briefJson": json.dumps({"title": f"yoozee 评测·{need['title']}", "episode_seconds": 90}, ensure_ascii=False),
    },
    "unit": {"title": f"第一集 {need['title']}"},
    "characters": characters,
    "scenes": scenes,
    "props": props,
    "pending": pending,
}
with open(os.path.join(out_dir, "seed.json"), "w", encoding="utf-8", newline="\n") as f:
    json.dump(seed, f, ensure_ascii=False, indent=1)
    f.write("\n")
print(out_dir, len(characters), "角色", len(scenes), "场景", len(props), "道具", len(pending), "待确认")
