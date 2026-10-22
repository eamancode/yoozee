#!/usr/bin/env python3
"""剧本契约的小帮手：给节拍编 id、列台词、统计。
  python3 {{TOOLS}}/script.py ids   [剧本/剧本.json]        按场重编节拍 id（E01S03L07 / E01S03B02），已有合规 id 的不动
  python3 {{TOOLS}}/script.py lines [剧本/剧本.json]        列出全部台词 id、说话人、原文（给 yz-direct 分配到镜头）
  python3 {{TOOLS}}/script.py stats [剧本/剧本.json]        每场台词数、字数、估时
  python3 {{TOOLS}}/script.py from-md 剧本.md --out 剧本/剧本.json   把「人物：台词」格式的纯文本剧本粗转成契约（场标题行以「场」或 INT/EXT 开头）"""
import argparse
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import common as C  # noqa: E402

BEAT_RE = re.compile(r"^E\d{2}S\d{2}[a-z]?[LB]\d{2,3}$")


def assign_ids(script):
    for sc in script["scenes"]:
        nl = nb = 0
        for b in sc["beats"]:
            if BEAT_RE.match(b.get("id", "")) and b["id"].startswith(sc["id"]):
                m = re.search(r"([LB])(\d+)$", b["id"])
                if m.group(1) == "L":
                    nl = max(nl, int(m.group(2)))
                else:
                    nb = max(nb, int(m.group(2)))
        for b in sc["beats"]:
            if BEAT_RE.match(b.get("id", "")) and b["id"].startswith(sc["id"]):
                continue
            if b.get("kind") in ("line", "vo"):
                nl += 1
                b["id"] = f"{sc['id']}L{nl:02d}"
            else:
                nb += 1
                b["id"] = f"{sc['id']}B{nb:02d}"
    return script


def from_md(text, title):
    scenes, cur = [], None
    for raw in text.splitlines():
        ln = raw.strip()
        if not ln:
            continue
        if re.match(r"^(第?[一二三四五六七八九十\d]+场|场\s*\d+|INT\.|EXT\.|内[·.]|外[·.])", ln) or ln.startswith("## "):
            cur = {"id": f"E01S{len(scenes) + 1:02d}", "heading": ln.lstrip("# ").strip(), "characters": [], "beats": []}
            scenes.append(cur)
            continue
        if cur is None:
            cur = {"id": "E01S01", "heading": "未命名场", "characters": [], "beats": []}
            scenes.append(cur)
        m = re.match(r"^([^：:（(]{1,8})(（[^）]*）)?[：:]\s*[“\"]?(.+?)[”\"]?$", ln)
        if m and not ln.startswith(("（", "(")):
            who, text_ = m.group(1).strip(), m.group(3).strip()
            cur["beats"].append({"id": "", "kind": "vo" if "画外" in (m.group(2) or "") or "OS" in (m.group(2) or "") else "line", "speaker": who, "text": text_, "tag": "原文事实"})
            if who not in cur["characters"]:
                cur["characters"].append(who)
        else:
            cur["beats"].append({"id": "", "kind": "action", "text": ln.strip("（）()"), "tag": "原文事实"})
    return assign_ids({"title": title, "scenes": scenes})


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("cmd", choices=["ids", "lines", "stats", "from-md"])
    ap.add_argument("path", nargs="?", default="剧本/剧本.json")
    ap.add_argument("--out")
    ap.add_argument("--title", default="未命名")
    a = ap.parse_args()
    if a.cmd == "from-md":
        script = from_md(open(C.abspath(a.path), encoding="utf-8").read(), a.title)
        out = C.abspath(a.out or "剧本/剧本.json")
        C.write_json(out, script)
        print(f"✓ {len(script['scenes'])} 场 → {a.out or '剧本/剧本.json'}（粗转：场次、人物、台词 / 动作的归类要人看一遍）")
        return
    script = C.read_json(C.abspath(a.path))
    if a.cmd == "ids":
        assign_ids(script)
        C.write_json(C.abspath(a.out or a.path), script)
        print("✓ id 已编")
    elif a.cmd == "lines":
        for sc in script["scenes"]:
            for b in sc["beats"]:
                if b.get("kind") in ("line", "vo"):
                    print(f"{b['id']}\t{b.get('speaker', '')}\t{b['text']}")
    else:
        for sc in script["scenes"]:
            lines = [b for b in sc["beats"] if b.get("kind") in ("line", "vo")]
            chars = sum(len(re.sub(r"[\s，。！？、“”…—,.!?]", "", b["text"])) for b in lines)
            print(f"{sc['id']} {sc.get('heading', '')}: 台词 {len(lines)} 句 {chars} 字，约 {chars / 4.5 + len(lines) * 0.3:.1f} 秒口播")


if __name__ == "__main__":
    main()
