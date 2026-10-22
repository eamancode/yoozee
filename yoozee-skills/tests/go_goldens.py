#!/usr/bin/env python3
"""生成柚子后端 Go 移植（backend/internal/drama）的黄金对照：Python 参考实现的输出原样落盘，Go 测试逐条比。
  python3 yoozee-skills/tests/go_goldens.py
写到 backend/internal/drama/testdata/golden/：
- check-*.json        check.py shots 的结果（不含 R-SCHEMA：那组消息来自 jsonschema，Go 侧另测）
- online-rows.json    rows_to_shotlist 的镜头表与统计（线上实测七床 E01 的分镜行）
- online-script.json  script.py from-md（七床 E01 章节正文）
- online-pack-*.json  pack.py 装段（无资产圣经，按模型）
改了参考实现或夹具后重跑，再看 diff。"""
import collections
import json
import os
import pathlib
import re
import subprocess
import sys

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent
REPO = ROOT.parent
TOOLS = ROOT / "bindings" / "local" / "tools"
sys.path.insert(0, str(TOOLS))
import common as C  # noqa: E402
import check as K  # noqa: E402
import pack as PK  # noqa: E402
import script as SC  # noqa: E402

OUT = REPO / "backend" / "internal" / "drama" / "testdata" / "golden"
ONLINE = REPO / "backend" / "internal" / "drama" / "testdata" / "online-qichuang"
SAMPLE = HERE / "sample"
MODELS = {m["id"]: m for m in json.loads((ROOT / "kb" / "kb-dialects" / "models.json").read_text(encoding="utf-8"))["models"]}


def load(p):
    return json.loads(pathlib.Path(p).read_text(encoding="utf-8"))


def dump(name, obj):
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / name).write_text(json.dumps(obj, ensure_ascii=False, indent=1) + "\n", encoding="utf-8", newline="\n")


def check(shotlist, script, bible, profile_id):
    K.load_rules()
    rep = K.check_shots(shotlist, {"script": script, "bible": bible, "profile": C.profile(profile_id)})
    d = rep.to_dict()
    d["items"] = [i for i in d["items"] if i["rule"] != "R-SCHEMA"]
    d["errors"] = sum(1 for i in d["items"] if i["level"] == "error")
    d["warnings"] = len(d["items"]) - d["errors"]
    return d


def rows_to_shotlist(rows, script):
    """与 .local/yozi-ab/run/rows_to_shotlist.py 相同的转换（函数化，便于落盘统计）。"""
    norm = lambda s: re.sub(r"[\s，。！？、“”‘’…—,.!?:：;；~～\-]", "", s or "")
    by_text = collections.defaultdict(list)
    for sc in script["scenes"]:
        for b in sc["beats"]:
            if b.get("kind") in ("line", "vo"):
                by_text[norm(b["text"])].append({"id": b["id"], "speaker": b.get("speaker"), "scene": sc["id"]})

    def pick(cands, speaker, scene):
        if len(cands) == 1:
            return cands[0]["id"]
        for key in ("speaker", "scene"):
            want = speaker if key == "speaker" else scene
            sub = [c for c in cands if want and c[key] == want]
            if len(sub) == 1:
                return sub[0]["id"]
            if sub:
                cands = sub
        return cands[0]["id"] if cands else None
    scene_ids = [sc["id"] for sc in script["scenes"]]
    stats = collections.Counter()
    shots = []
    for i, row in enumerate(rows, 1):
        spec = row.get("spec")
        if not isinstance(spec, dict):
            stats["no_spec"] += 1
            spec = {"id": f"E01S00-{i:02d}", "scene": "E01S00", "size": row.get("shotSize", ""), "action": row.get("plotDescription", ""), "lines": [], "est": row.get("durationSeconds") or 3}
            for ln in (row.get("dialogue") or "").split("\n"):
                m = re.match(r"^\s*([^：:]{1,16})[：:]\s*(.+)$", ln)
                if m:
                    spec["lines"].append({"speaker": m.group(1), "text": m.group(2)})
        lines = []
        for ln in spec.get("lines") or []:
            t = ln.get("text") or ""
            if not norm(t):
                continue
            speaker = ln.get("speaker") or spec.get("focus")
            scene = spec.get("scene")
            lid = pick(by_text.get(norm(t), []), speaker, scene)
            if not lid:
                cands = [c for k, v in by_text.items() if norm(t) in k or k in norm(t) for c in v]
                lid = pick(cands, speaker, scene) if cands else None
                stats["line_fuzzy" if lid else "line_unmatched"] += 1
            else:
                stats["line_exact"] += 1
            d = dict(ln)
            d.pop("text", None)
            d.pop("speaker", None)
            if lid:
                d["line_id"] = lid
            else:
                d["text"] = t
                d["speaker"] = speaker
            lines.append(d)
        spec["lines"] = lines
        ep_scenes = [l["line_id"][:6] for l in lines if l.get("line_id")]
        if ep_scenes:
            sc = collections.Counter(ep_scenes).most_common(1)[0][0]
            if spec.get("scene") != sc:
                stats["scene_remap"] += 1
            spec["scene"] = sc
        elif spec.get("scene") not in scene_ids:
            stats["scene_unknown"] += 1
        if not spec.get("est"):
            spec["est"] = row.get("durationSeconds") or 3
        spec.setdefault("id", f"{spec['scene']}-{i:02d}")
        shots.append(spec)
    ids = collections.Counter(s["id"] for s in shots)
    for s in shots:
        if ids[s["id"]] > 1:
            stats["dup_id"] += 1
            s["id"] = f"{s['scene']}-{shots.index(s) + 1:02d}x"
    return {"profile": "live-realism", "episode": 1, "shots": shots}, {"rows": len(rows), "shots": len(shots), **stats}


def main():
    # 样例项目与坏样例
    script, bible = load(SAMPLE / "剧本" / "剧本.json"), load(SAMPLE / "剧本" / "资产圣经.json")
    dump("check-sample.json", check(load(SAMPLE / "剧本" / "镜头表.json"), script, bible, "live-realism"))
    for name in ("check-bad-shots", "check-bad-shots21"):
        spec = load(HERE / "fixtures" / f"{name}.json")
        dump(f"{name}.json", check(spec, script, bible, spec["profile"]))
    # 线上实测：章节正文 → 剧本；分镜行 → 镜头表；检查（无资产圣经）；装段
    body = (ONLINE / "chapter.md").read_text(encoding="utf-8")
    online_script = SC.from_md(body, "七床")
    dump("online-script.json", online_script)
    rows = load(ONLINE / "rows.json")["rows"]
    shotlist, stats = rows_to_shotlist(rows, online_script)
    dump("online-rows.json", {"shotlist": shotlist, "stats": stats})
    dump("check-online.json", check(shotlist, online_script, None, "live-realism"))
    for model_id in ("seedance-2.0", "seedance-2.5"):
        segs, problems = PK.pack(json.loads(json.dumps(shotlist)), online_script, None, MODELS[model_id], "live-realism")
        dump(f"online-pack-{model_id}.json", {"segments": segs, "problems": problems})
    print(f"✓ 已写 {OUT.relative_to(REPO)}")


if __name__ == "__main__":
    main()
