#!/usr/bin/env python3
"""分段表 → 任务。分段表是 markdown（进度表 + 每段一个 json 块）；这里只读 json 块，交给适配翻译成模型方言。
  python3 {{TOOLS}}/segments.py list [--file 剧本/分段表.md]
  python3 {{TOOLS}}/segments.py jobs --adapter video_minimax_h3 [--ids S01,S02] [--res 768P|2K] [--n 2] --out 生成/jobs.json
  python3 {{TOOLS}}/segments.py run --adapter video_minimax_h3 --jobs 生成/jobs.json
  python3 {{TOOLS}}/segments.py status [--file 剧本/分段表.md]        从表里数"现在到哪了"
段长上限按需求文档里的生成模型（kb-dialects/models.json）核：list 标出超长段，jobs 遇到超长段直接拒绝。
jobs、run 只认需求文档生成模型的适配（适配 CAPABILITIES["model"]），别家的直接拒绝；画幅取需求文档「平台与画幅」里的比例。
"""
import argparse, importlib, json, os, re, sys
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import common as C

ADAPTERS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "adapters")


def load(path):
    """读分段：2.0 的 剧本/分段.json（数组）或 1.0 的 剧本/分段表.md（每段一个 json 块）。给的是 .md 但同目录有 分段.json 时优先读 json。"""
    ap = C.abspath(path)
    if ap.endswith(".md") and os.path.exists(os.path.join(os.path.dirname(ap), "分段.json")):
        ap = os.path.join(os.path.dirname(ap), "分段.json")
    if ap.endswith(".json"):
        data = C.read_json(ap)
        segs = data if isinstance(data, list) else data.get("segments", [])
        return [s for s in segs if not str(s.get("id", "")).startswith("示例")]
    text = open(ap, encoding="utf-8").read()
    segs = []
    for block in re.findall(r"```json\s*(\{.*?\})\s*```", text, re.S):
        try:
            segs.append(json.loads(block))
        except json.JSONDecodeError as e:
            raise SystemExit(f"分段表里有一段 json 写错了：{e}\n{block[:200]}")
    return [s for s in segs if not str(s.get("id", "")).startswith("示例")]


def adapter(name):
    if ADAPTERS not in sys.path:
        sys.path.insert(0, ADAPTERS)
    return importlib.import_module(name)


def check_adapter(name, A, m):
    """适配的模型必须是需求文档的生成模型：分段按它的段长上限和方言写，换一家生成会超长或读错提示词。"""
    have = A.CAPABILITIES.get("model")
    if have == m["id"]:
        return
    fits = []
    for f in sorted(os.listdir(ADAPTERS)):
        if f.startswith("video_") and f.endswith(".py"):
            try:
                if adapter(f[:-3]).CAPABILITIES.get("model") == m["id"]:
                    fits.append(f[:-3])
            except Exception:  # 导入不了的适配本来也用不了
                continue
    labels = {x["id"]: x["label"] for x in C.models_json()["models"]}
    what = f"是 {labels.get(have, have)} 的适配" if have else "没在 CAPABILITIES 里声明 model（models.json 的 id）"
    hint = (f"可用的适配：{'、'.join(fits)}" if fits else
            f"本机还没有 {m['label']} 的视频适配：先用 yz-calibrate 照 adapters/适配规格.md 写一个，或用 compile.py 出粘贴版交给用户手动生成")
    sys.exit(f"{name} {what}，需求文档的生成模型是 {m['label']}：{hint}")


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
        check_adapter(a.adapter, A, m)
        picked = [s for s in load(a.file) if not want or s["id"] in want]
        errors = [i["message"] for s in picked for i in P.compile_segment(s, m)["issues"] if i["level"] == "error"]
        if errors:
            sys.exit("编译检查没过，先改分段表：\n" + "\n".join(errors))
        ratio = C.project_ratio()
        try:
            jobs = [A.build_job(s, a.res, n=a.n, ratio=ratio) for s in picked]
        except ValueError as e:
            sys.exit(f"出不了任务：{e}")
        missing = sorted({p for j in jobs for p in A.job_inputs(j) if not os.path.exists(C.abspath(p))})
        if missing:
            sys.exit(f"缺素材：{missing}")
        C.write_json(C.abspath(a.out), jobs)
        secs = sum(j["duration"] * j.get("n", 1) for j in jobs); price = A.CAPABILITIES["price_per_sec"].get(a.res, 0)
        fee = A.CAPABILITIES.get("ref_image_fee", {"free": 99, "per_image": 0})
        extra = sum(max(0, len(j.get("ref_images", [])) - fee["free"]) * fee["per_image"] * j.get("n", 1) for j in jobs)
        ratios = "、".join(sorted({j["ratio"] for j in jobs if j.get("ratio")}))
        print(f"{len(jobs)} 段，{secs} 秒，{'画幅 ' + ratios + '，' if ratios else ''}约 {secs * price + extra:.1f} 美元（含参考图附加 {extra:.2f}）→ {a.out}")
    else:
        A = adapter(a.adapter); check_adapter(a.adapter, A, C.model(C.project_model()))
        res = A.run_jobs(C.read_json(C.abspath(a.jobs)), a.poll)
        print(json.dumps(res, ensure_ascii=False, indent=1))


if __name__ == "__main__":
    main()
