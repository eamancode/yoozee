#!/usr/bin/env python3
"""分段表 → 任务。分段表是 markdown（进度表 + 每段一个 json 块）；这里只读 json 块，交给适配翻译成模型方言。
  python3 {{TOOLS}}/segments.py list [--file 剧本/分段表.md]
  python3 {{TOOLS}}/segments.py jobs --adapter video_minimax_h3 [--ids S01,S02] [--res 768P|2K] [--n 2] --out 生成/jobs.json
  python3 {{TOOLS}}/segments.py run --adapter video_minimax_h3 --jobs 生成/jobs.json
  python3 {{TOOLS}}/segments.py status [--file 剧本/分段表.md]        从表里数"现在到哪了"
段长上限按需求文档里的生成模型（kb-dialects/models.json）核：list 标出超长段，jobs 遇到超长段直接拒绝。
"""
import argparse, importlib, json, os, re, sys
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import common as C


def load(path):
    text = open(C.abspath(path), encoding="utf-8").read()
    segs = []
    for block in re.findall(r"```json\s*(\{.*?\})\s*```", text, re.S):
        try:
            segs.append(json.loads(block))
        except json.JSONDecodeError as e:
            raise SystemExit(f"分段表里有一段 json 写错了：{e}\n{block[:200]}")
    return [s for s in segs if not str(s.get("id", "")).startswith("示例")]


def adapter(name):
    sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "adapters"))
    return importlib.import_module(name)


def main():
    ap = argparse.ArgumentParser(); sp = ap.add_subparsers(dest="cmd", required=True)
    for c in ("list", "status"):
        p = sp.add_parser(c); p.add_argument("--file", default="剧本/分段表.md")
    p = sp.add_parser("jobs"); p.add_argument("--file", default="剧本/分段表.md"); p.add_argument("--adapter", required=True); p.add_argument("--ids"); p.add_argument("--res", default="768P"); p.add_argument("--n", type=int, default=1); p.add_argument("--out", required=True)
    p = sp.add_parser("run"); p.add_argument("--adapter", required=True); p.add_argument("--jobs", required=True); p.add_argument("--poll", type=int, default=20)
    a = ap.parse_args()
    if a.cmd == "list":
        m = C.model(C.project_model()); hi = m["duration"]["max"]
        segs = load(a.file)
        for s in segs:
            over = "  ✗ 超过上限" if float(s.get("dur", 0)) > hi else ""
            print(f"{s['id']:<6} {s.get('scene', ''):<4} {s.get('dur', '?'):>3}s {s.get('mode', '')}  镜头 {len(s.get('shots', []))}  台词 {sum(len(sh.get('lines', [])) for sh in s.get('shots', []))}{over}")
        print(f"合计 {sum(float(s.get('dur', 0)) for s in segs):.0f} 秒；模型 {m['label']}，一段最长 {hi} 秒")
    elif a.cmd == "status":
        text = open(C.abspath(a.file), encoding="utf-8").read()
        rows = [r for r in text.splitlines() if r.startswith("| S")]
        cols = ["剧本", "参考", "生成", "过审", "入片", "验收"]
        counts = [sum(1 for r in rows if len(r.split("|")) > 5 + i and r.split("|")[5 + i].strip() not in ("", "○") and "✗" not in r.split("|")[5 + i]) for i in range(6)]  # 带 ✗ 的格子是被反馈划掉的，不算
        print(f"{len(rows)} 段 · " + " · ".join(f"{c} {n}" for c, n in zip(cols, counts)))
    elif a.cmd == "jobs":
        A = adapter(a.adapter); want = set(a.ids.split(",")) if a.ids else None
        import compile as P
        m = C.model(C.project_model())
        picked = [s for s in load(a.file) if not want or s["id"] in want]
        errors = [i["message"] for s in picked for i in P.compile_segment(s, m)["issues"] if i["level"] == "error"]
        if errors:
            sys.exit("编译检查没过，先改分段表：\n" + "\n".join(errors))
        jobs = [A.build_job(s, a.res, n=a.n) for s in picked]
        missing = sorted({p for j in jobs for p in A.job_inputs(j) if not os.path.exists(C.abspath(p))})
        if missing:
            sys.exit(f"缺素材：{missing}")
        C.write_json(C.abspath(a.out), jobs)
        secs = sum(j["duration"] * j.get("n", 1) for j in jobs); price = A.CAPABILITIES["price_per_sec"].get(a.res, 0)
        fee = A.CAPABILITIES.get("ref_image_fee", {"free": 99, "per_image": 0})
        extra = sum(max(0, len(j.get("ref_images", [])) - fee["free"]) * fee["per_image"] * j.get("n", 1) for j in jobs)
        print(f"{len(jobs)} 段，{secs} 秒，约 {secs * price + extra:.1f} 美元（含参考图附加 {extra:.2f}）→ {a.out}")
    else:
        A = adapter(a.adapter); res = A.run_jobs(C.read_json(C.abspath(a.jobs)), a.poll)
        print(json.dumps(res, ensure_ascii=False, indent=1))


if __name__ == "__main__":
    main()
