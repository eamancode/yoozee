#!/usr/bin/env python3
"""图片适配样例：本机 Codex CLI 内置 image_gen。自动前置写实约束（{{TOOLS}}/realism.txt）。2026-10 实测，详见项目 工具档案/Codex-image_gen.md。

单张： python3 {{TOOLS}}/adapters/image_codex.py --prompt "..." --out 素材库/人物/A_front.png [--ref img ...] [--roles "Image 1 = ..."] [--aspect 21:9] [--no-style]
批量： python3 {{TOOLS}}/adapters/image_codex.py --jobs 素材库/jobs.json [--parallel 3]
       jobs.json = [{"id":"A_front","out":"...","prompt":"...","refs":["..."],"roles":"...","aspect":"21:9"}]
"""
import argparse
import concurrent.futures as cf
import json
import os
import pathlib
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import common as C  # noqa: E402

TAG = "image_codex"
LOGS = C.LOG_DIR / "codex_img"
LOGS.mkdir(exist_ok=True)

REALISM = open(os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "realism.txt"), encoding="utf-8").read().strip()
ASPECT = {
    "21:9": "Landscape framing, 2.39:1 widescreen.",
    "16:9": "Landscape framing, 16:9.",
    "9:16": "Portrait framing, 9:16 vertical.",
    "1:1": "Square framing, 1:1.",
    "4:5": "Portrait framing, 4:5.",
    "3:4": "Portrait framing, 3:4.",
    "3:2": "Landscape framing, 3:2.",
    "2:3": "Portrait framing, 2:3.",
}


def build_task(prompt, out, refs=(), roles="", aspect="21:9", style=True):
    out = C.abspath(out)
    parts = [
        "Use your built-in image_gen tool to generate exactly ONE image. Call it with only the prompt parameter (plus referenced_image_paths when reference files are listed below); the tool has no size, quality or aspect parameters, so never pass them. The tool saves its PNG under "
        "~/.codex/generated_images/<thread>/; copy the file produced by THIS call to this absolute path: "
        f"{out} (create parent folders if needed). Verify the file exists and is a valid PNG, then print one final line "
        f"exactly like: DONE {out} <width>x<height>. Do nothing else, do not ask questions, do not edit any other file.",
    ]
    if refs:
        lst = "\n".join(f"  {i + 1}) {C.abspath(r)}" for i, r in enumerate(refs))
        parts.append(
            "Pass these local files, in this order, through the tool's referenced_image_paths parameter:\n" + lst +
            "\nRoles of the references: " + (roles or "they define the exact identity (face, hair, build, wardrobe) that must be reproduced.") +
            " Keep the identity consistent with the references; only the prompt below may change pose, framing, lighting, "
            "setting or wardrobe when it explicitly says so."
        )
    body = []
    if style:
        body.append(REALISM)
    if aspect:
        body.append(ASPECT.get(aspect, f"Framing aspect ratio {aspect}."))
    body.append(prompt.strip())
    parts.append('IMAGE PROMPT (pass this text verbatim as the prompt):\n"""\n' + "\n\n".join(body) + '\n"""')
    return "\n\n".join(parts)


def run_one(job, timeout=900):
    out = C.abspath(job["out"])
    jid = job.get("id") or pathlib.Path(out).stem
    if not job.get("force") and os.path.exists(out) and os.path.getsize(out) > 0:
        C.log(TAG, f"{jid} 已存在，跳过")
        return {"id": jid, "out": out, "status": "exists"}
    task = build_task(job["prompt"], out, job.get("refs", []), job.get("roles", ""), job.get("aspect", "21:9"),
                      not job.get("no_style", False))
    pathlib.Path(out).parent.mkdir(parents=True, exist_ok=True)
    pathlib.Path(out + ".prompt.txt").write_text(task, encoding="utf-8")
    log = LOGS / f"{jid}.log"
    t0 = time.time()
    cmd = ["codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "-c", f"model_reasoning_effort=\"{os.environ.get('CODEX_EFFORT', 'low')}\"", "-C", str(C.ROOT), "-"]
    with open(log, "w", encoding="utf-8") as lf:
        try:
            subprocess.run(cmd, input=task, stdout=lf, stderr=subprocess.STDOUT, text=True, timeout=timeout)
        except subprocess.TimeoutExpired:
            C.log(TAG, f"{jid} ✗ 超时 {timeout}s")
            return {"id": jid, "out": out, "status": "timeout"}
    if os.path.exists(out) and os.path.getsize(out) > 0:
        size = ""
        try:
            from PIL import Image
            with Image.open(out) as im:
                size = f"{im.size[0]}x{im.size[1]}"
        except Exception:
            pass
        C.log(TAG, f"{jid} ✓ {size} {int(time.time() - t0)}s → {job['out']}")
        return {"id": jid, "out": out, "status": "done", "size": size}
    C.log(TAG, f"{jid} ✗ 无输出（见 {log}）")
    return {"id": jid, "out": out, "status": "failed", "log": str(log)}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--jobs")
    ap.add_argument("--prompt")
    ap.add_argument("--out")
    ap.add_argument("--id")
    ap.add_argument("--ref", action="append", default=[])
    ap.add_argument("--roles", default="")
    ap.add_argument("--aspect", default="21:9")
    ap.add_argument("--no-style", action="store_true")
    ap.add_argument("--force", action="store_true")
    ap.add_argument("--parallel", type=int, default=3)
    a = ap.parse_args()
    if a.jobs:
        jobs = C.read_json(C.abspath(a.jobs))
    else:
        if not (a.prompt and a.out):
            ap.error("需要 --prompt 和 --out，或 --jobs")
        jobs = [{"id": a.id, "out": a.out, "prompt": a.prompt, "refs": a.ref, "roles": a.roles,
                 "aspect": a.aspect, "no_style": a.no_style, "force": a.force}]
    with cf.ThreadPoolExecutor(max_workers=max(1, a.parallel)) as ex:
        res = list(ex.map(run_one, jobs))
    ok = sum(1 for r in res if r["status"] in ("done", "exists"))
    C.log(TAG, f"完成 {ok}/{len(res)}")
    print(json.dumps(res, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
