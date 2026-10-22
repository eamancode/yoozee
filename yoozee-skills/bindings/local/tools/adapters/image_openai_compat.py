#!/usr/bin/env python3
"""图片适配：OpenAI 兼容的 /v1/images/generations 接口（任何中转站）。密钥只从 .env 读：IMAGE_API_KEY、IMAGE_API_BASE（如 https://host/v1）、IMAGE_API_MODEL。
单张： python3 {{TOOLS}}/adapters/image_openai_compat.py --prompt "..." --out 素材库/人物/A_三视图.png [--size 1792x1024] [--aspect 16:9] [--no-style]
批量： python3 {{TOOLS}}/adapters/image_openai_compat.py --jobs 素材库/jobs.json [--parallel 2]
       jobs.json = [{"id":"A_三视图","out":"...","prompt":"...","aspect":"16:9","size":"1792x1024","no_style":false}]
探测： python3 {{TOOLS}}/adapters/image_openai_compat.py --probe        （一张最小图，打印接口返回的字段与尺寸，不存文件）
已存在且非空的输出文件跳过（能续跑）。真人剧默认前置写实约束块（{{TOOLS}}/realism.txt），--no-style 关闭。
"""
import argparse
import base64
import concurrent.futures as cf
import json
import os
import pathlib
import sys
import time
import urllib.error
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import common as C  # noqa: E402

TAG = "image_openai"
REALISM = open(os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "realism.txt"), encoding="utf-8").read().strip()
# gpt-image 系接口只接受 1024x1024 / 1024x1536 / 1536x1024（实测 paipu 中转 lec-ac-image-2-5-sunburst：1792 与 720p 系都报 size is not supported）；
# 16:9 / 9:16 先出 3:2 / 2:3，交付前按需裁边。换中转站先 --probe。
ASPECT_SIZE = {"16:9": "1536x1024", "9:16": "1024x1536", "3:2": "1536x1024", "2:3": "1024x1536", "1:1": "1024x1024"}
CAPABILITIES = {"kind": "image", "endpoint": "images/generations", "sizes": list(dict.fromkeys(ASPECT_SIZE.values())), "refs": False}


def request_image(prompt, size, n=1, timeout=300):
    base = C.need("IMAGE_API_BASE").rstrip("/")
    key = C.need("IMAGE_API_KEY")
    model = C.need("IMAGE_API_MODEL")
    body = {"model": model, "prompt": prompt, "n": n, "size": size}
    req = urllib.request.Request(base + "/images/generations", data=json.dumps(body).encode("utf-8"), method="POST",
                                 headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"})
    last = None
    for attempt in range(4):
        try:
            with urllib.request.urlopen(req, timeout=timeout) as r:
                return json.loads(r.read().decode("utf-8"))
        except urllib.error.HTTPError as e:
            text = e.read().decode("utf-8", "replace")[:600]
            last = f"HTTP {e.code}: {text}"
            if e.code in (400, 401, 403, 404, 422):
                break
        except Exception as e:  # 网络抖动重试
            last = str(e)
        time.sleep(3 * (attempt + 1))
    raise RuntimeError(last)


def save_item(item, out):
    out = pathlib.Path(out)
    out.parent.mkdir(parents=True, exist_ok=True)
    if item.get("b64_json"):
        out.write_bytes(base64.b64decode(item["b64_json"]))
    elif item.get("url"):
        with urllib.request.urlopen(item["url"], timeout=300) as r:
            out.write_bytes(r.read())
    else:
        raise RuntimeError(f"返回里既没有 b64_json 也没有 url：{list(item)}")
    return out


def run_one(job):
    out = C.abspath(job["out"])
    jid = job.get("id") or pathlib.Path(out).stem
    if not job.get("force") and os.path.exists(out) and os.path.getsize(out) > 0:
        C.log(TAG, f"{jid} 已存在，跳过")
        return {"id": jid, "out": out, "status": "exists"}
    prompt = job["prompt"].strip()
    if not job.get("no_style", False):
        prompt = REALISM + "\n\n" + prompt
    size = job.get("size") or ASPECT_SIZE.get(job.get("aspect", "16:9"), "1024x1024")
    pathlib.Path(out + ".prompt.txt").write_text(prompt, encoding="utf-8")
    t0 = time.time()
    try:
        data = request_image(prompt, size)
        items = data.get("data") or []
        if not items:
            raise RuntimeError(f"返回无 data：{json.dumps(data, ensure_ascii=False)[:300]}")
        save_item(items[0], out)
        C.log(TAG, f"{jid} ✓ {size} {time.time() - t0:.0f}s → {os.path.relpath(out, C.ROOT)}")
        return {"id": jid, "out": out, "status": "ok", "size": size, "seconds": round(time.time() - t0)}
    except Exception as e:
        C.log(TAG, f"{jid} ✗ {e}")
        return {"id": jid, "out": out, "status": "error", "error": str(e)[:300]}


def probe():
    data = request_image("a plain grey studio backdrop, empty, soft even light", "1024x1024", timeout=180)
    items = data.get("data") or []
    print("返回顶层字段：", sorted(data.keys()))
    if items:
        it = items[0]
        print("data[0] 字段：", sorted(it.keys()))
        if it.get("url"):
            print("url 形式；示例：", it["url"][:80] + "…")
        if it.get("b64_json"):
            print("b64 形式，长度", len(it["b64_json"]))
    else:
        print(json.dumps(data, ensure_ascii=False)[:500])


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--prompt")
    ap.add_argument("--out")
    ap.add_argument("--size")
    ap.add_argument("--aspect", default="16:9")
    ap.add_argument("--no-style", action="store_true")
    ap.add_argument("--jobs")
    ap.add_argument("--parallel", type=int, default=2)
    ap.add_argument("--probe", action="store_true")
    a = ap.parse_args()
    if a.probe:
        probe()
        return
    if a.jobs:
        jobs = C.read_json(C.abspath(a.jobs))
    else:
        if not (a.prompt and a.out):
            sys.exit("要么 --prompt + --out，要么 --jobs，要么 --probe")
        jobs = [{"id": pathlib.Path(a.out).stem, "out": a.out, "prompt": a.prompt, "aspect": a.aspect, "size": a.size, "no_style": a.no_style}]
    with cf.ThreadPoolExecutor(max_workers=max(1, a.parallel)) as ex:
        results = list(ex.map(run_one, jobs))
    bad = [r for r in results if r["status"] == "error"]
    print(json.dumps(results, ensure_ascii=False, indent=1))
    sys.exit(1 if bad else 0)


if __name__ == "__main__":
    main()
