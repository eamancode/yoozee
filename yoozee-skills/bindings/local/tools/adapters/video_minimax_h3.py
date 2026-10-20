#!/usr/bin/env python3
"""视频适配样例：MiniMax H3（v2 接口，音画一体）。能力声明 + 中性分段 → H3 方言 + 提交/查询/下载。2026-10 实测，详见 kb-dialects › minimax-h3/档案.md。
单跑：python3 {{TOOLS}}/adapters/video_minimax_h3.py --jobs 生成/jobs.json [--poll 20]   或 --fetch 任务号:路径 补下载
"""
import argparse
import base64
import json
import mimetypes
import os
import pathlib
import sys
import time

import requests

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import common as C  # noqa: E402
import compile as P  # noqa: E402  段级声音和约束的取法与方言编译一致（段没写时从镜头取）

TAG = "video_minimax_h3"
CAPABILITIES = {
    "model": "minimax-h3",  # models.json 里的 id；segments.py 用它核对需求文档的生成模型，不一致就不出任务
    "modes": {"lock_identity": "参考模式：reference_image（≤9，人物干净参考图 + 空景图/机位背景图）+ reference_audio（≤3，合计 ≤15 秒）；画面稳、音色锁得住，但场景参考会被当成每个机位的背景贴",
              "lock_first_frame": "首帧模式：first_frame 一张图；构图光线照图，但只锁第一帧的脸，人一动脸就变；不能同时挂任何参考"},
    "exclusive": True, "duration": {"min": 4, "max": 15, "step": 1}, "resolutions": ["768P", "2K"],
    "ref_audio_total_sec": 15, "ref_images_max": 9, "price_per_sec": {"768P": 0.08, "2K": 0.13}, "audio_in_video": True,
    "ref_image_fee": {"free": 5, "per_image": 0.04},  # 2026-10-09 文档：第 6 张参考图起每张 0.04 美元
    "ratios": ["21:9", "16:9", "4:3", "1:1", "3:4", "9:16"],  # 参考模式按需求文档「平台与画幅」的比例传（段里写了 ratio 以段为准）；首帧模式比例跟图走
    "output": {"768P": "21:9 1536x672（首帧 2.39:1 1568x672）；16:9 1344x768", "2K": "21:9 2944x1248；16:9 2560x1440", "fps": 24, "audio": "32kHz AAC"},
}
_T = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
_read = lambda n: open(os.path.join(_T, n), encoding="utf-8").read().strip() + " "
STYLE, ACTING = _read("video_style.txt"), _read("acting.txt")  # 通用提示词块在 tools/，不属于这家模型。收音方式不写进提示词：会让台词干、贴耳、字缝掉成死寂
ROLE_TEXT = {"identity": "is the exact identity reference of {note}: match the face, hairstyle and distinguishing marks exactly and wear exactly what the reference wears; ignore its plain grey background",
             "location": "is the location reference: keep its layout, materials, colours and light; it contains no people. {note}",
             "plate": "is the background for one camera side: {note}",
             "prop": "is the exact prop: {note}",
             "frame": "is a composition reference only (framing, light, seating); the faces must follow the identity references, not this picture. {note}"}


def _t(sec):
    return f"{int(sec // 60):02d}:{sec % 60:06.3f}"


def build_job(seg, resolution="768P", n=1, ratio=None):
    """中性分段 → H3 任务。方言：integrated_multimodal_description 里 [Shot k] At mm:ss.mmm，说话人 (S1)，台词 <d>[Chinese] …</d>，参考 <Picture k>；再加 overall_soundscape 和 non_diegetic_music。
    ratio 是需求文档的画幅；参考模式没有画幅、或画幅 H3 不支持时抛 ValueError。"""
    mode = seg.get("mode", "lock_identity")
    job = {"id": seg["id"], "out": f"生成/{seg['id']}.mp4", "model": "MiniMax-H3", "duration": int(seg["dur"]), "resolution": resolution, "n": n}
    rules = P.segment_rules(seg)
    head = STYLE + ACTING + (rules + " " if rules else "")
    body = "integrated_multimodal_description:\n"
    speakers = []
    if mode == "lock_first_frame":
        job["first_frame"] = seg["first_frame"]; head += "Keep the exact framing, faces, wardrobe and lighting of the first frame. "
        if seg.get("last_frame"):  # 首尾帧：文档 2026-10-09 确认 H3 支持 first_frame + last_frame
            job["last_frame"] = seg["last_frame"]; head += "The video must end exactly on the last frame. "
    else:
        imgs, roles = [], []
        for i, r in enumerate(seg.get("refs", []), 1):
            imgs.append(r["file"]); roles.append(f"<Picture {i}> " + ROLE_TEXT.get(r.get("role", "frame"), "{note}").format(note=r.get("note", "")))
        for k, v in enumerate(seg.get("voices", []), 1):
            speakers.append(v["character"]); roles.append(f"<Audio {k}> gives only the sound of the voice (timbre) of {v['character']} (S{k})")
        if roles:
            body += "Reference roles: " + "; ".join(roles) + "."
            body += (" How each line is delivered (volume, pitch, emotion, pace, breath) comes only from the acting described in the scene, never from the audio references; do not reuse any words from them." if speakers else "") + "\n"
        ratio = seg.get("ratio") or ratio
        if not ratio:
            raise ValueError(f"{seg['id']} 没有画幅：在需求文档「平台与画幅」一行写明比例（如 9:16）")
        if ratio not in CAPABILITIES["ratios"]:
            raise ValueError(f"{seg['id']} 的画幅 {ratio} MiniMax-H3 不支持；可选：{'、'.join(CAPABILITIES['ratios'])}")
        job.update(ratio=ratio, ref_images=imgs, ref_audio=[v["file"] for v in seg.get("voices", [])])
    for k, sh in enumerate(seg["shots"], 1):
        line = (head if k == 1 else "") + sh["text"]
        for ln in sh.get("lines", []):
            s_idx = speakers.index(ln["speaker"]) + 1 if ln["speaker"] in speakers else 1
            vo = " [off-screen voiceover]" if ln.get("vo") else ""  # 画外音（内心独白、旁白）：嘴不动
            how = f" {ln['delivery'].strip().rstrip('.,')}" if ln.get("delivery") else ""  # 这一句的表演状态，紧贴台词写（R-008：语气由表演状态决定）
            line += f"{vo} (S{s_idx}),{how}:\n<d>[{seg.get('lang', 'Chinese')}] {ln['text']}</d>" if how else f"{vo} (S{s_idx}):\n<d>[{seg.get('lang', 'Chinese')}] {ln['text']}</d>"
        body += (f"[Shot {k}] " if k == 1 else f"[Shot {k}] At {_t(sh.get('at', 0))}, ") + line + "\n"
    body += "\noverall_soundscape: " + (P.segment_sound(seg, "; ") or "quiet room tone") + "\n\nnon_diegetic_music: N/A"
    job["prompt"] = body
    return job


def job_inputs(job):
    return [p for p in job.get("ref_images", []) + job.get("ref_audio", []) + [job[k] for k in ("first_frame", "last_frame") if job.get(k)]]
HOSTS = ["https://api.minimax.io", "https://api.minimaxi.com"]
DONE = {"succeeded", "success"}
FAILED = {"failed", "fail", "cancelled", "canceled"}


class MiniMax:
    def __init__(self):
        self.key = C.need("MINIMAX_API_KEY")
        h = os.environ.get("MINIMAX_API_HOST", "").rstrip("/")
        self.hosts = [h] if h else HOSTS[:]
        self.host = self.hosts[0]
        self.locked = bool(h)
        self.upload_purpose = os.environ.get("MINIMAX_UPLOAD_PURPOSE", "video_generation_input")
        self._uploads = {}

    # ---------- 基础请求 ----------
    def _hdr(self, json_ct=True):
        h = {"Authorization": f"Bearer {self.key}"}
        if json_ct:
            h["Content-Type"] = "application/json"
        return h

    def _switch_host(self, why):
        alt = [x for x in self.hosts if x != self.host]
        if self.locked or not alt:
            return False
        C.log(TAG, f"{self.host} {why}，改用 {alt[0]}")
        self.host, self.locked = alt[0], True
        return True

    def _req(self, method, path, **kw):
        err = None
        for attempt in range(8):
            try:
                r = requests.request(method, self.host + path, timeout=300, **kw)
            except requests.RequestException as e:
                err = f"网络异常 {e}"
                time.sleep(5 * (attempt + 1))
                continue
            if r.status_code in (401, 403) and self._switch_host(f"鉴权失败 HTTP {r.status_code}"):
                continue
            if r.status_code == 429 or r.status_code >= 500:
                err = f"HTTP {r.status_code}: {r.text[:200]}"
                wait = min(120, 15 * (attempt + 1))
                C.log(TAG, f"{err}，{wait}s 后重试")
                time.sleep(wait)
                continue
            return r
        raise RuntimeError(f"{method} {path} 失败：{err}")

    @staticmethod
    def _check(r, what):
        try:
            d = r.json()
        except ValueError:
            raise RuntimeError(f"{what} 返回非 JSON：HTTP {r.status_code} {r.text[:300]}")
        br = d.get("base_resp") or {}
        if r.status_code != 200 or br.get("status_code", 0) != 0:
            raise RuntimeError(f"{what} 失败：HTTP {r.status_code} {json.dumps(d, ensure_ascii=False)[:500]}")
        return d

    # ---------- 素材 ----------
    @staticmethod
    def data_uri(path):
        path = C.abspath(path)
        ext = os.path.splitext(path)[1].lower().lstrip(".")
        # H3 只认 data:image/<png|jpeg|webp>、data:audio/<wav|mp3>、data:video/<mp4|mov>；Python mimetypes 给的 audio/x-wav 会被拒（错误码 2013）
        fixed = {"wav": "audio/wav", "mp3": "audio/mp3", "png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg",
                 "webp": "image/webp", "mp4": "video/mp4", "mov": "video/mov"}
        mime = fixed.get(ext) or mimetypes.guess_type(path)[0] or "application/octet-stream"
        return f"data:{mime};base64," + base64.b64encode(pathlib.Path(path).read_bytes()).decode()

    def upload(self, path, purpose=None):
        path = C.abspath(path)
        key = (path, purpose)
        if key in self._uploads:
            return self._uploads[key]
        purpose = purpose or self.upload_purpose
        with open(path, "rb") as f:
            r = self._req("POST", "/v1/files/upload", headers=self._hdr(json_ct=False),
                          data={"purpose": purpose}, files={"file": (os.path.basename(path), f)})
        d = self._check(r, f"上传 {os.path.basename(path)}")
        fid = (d.get("file") or {}).get("file_id") or d.get("file_id")
        if not fid:
            raise RuntimeError(f"上传未返回 file_id：{json.dumps(d, ensure_ascii=False)[:300]}")
        C.log(TAG, f"上传 {os.path.basename(path)} → file_id {fid}")
        self._uploads[key] = f"mm_file://{fid}"
        return self._uploads[key]

    def media_url(self, path, kind):
        """图片走 base64；音频 <10MB 走 base64，否则上传；视频一律上传（MINIMAX_INLINE_MEDIA=1 可强制全部 base64）。"""
        if str(path).startswith(("http://", "https://", "mm_file://", "data:")):
            return path
        size = os.path.getsize(C.abspath(path))
        inline = os.environ.get("MINIMAX_INLINE_MEDIA") == "1"
        if kind == "image" or inline or (kind == "audio" and size < 10 * 1024 * 1024):
            return self.data_uri(path)
        return self.upload(path)

    def build_content(self, prompt, first=None, last=None, ref_images=(), ref_videos=(), ref_audio=()):
        if (first or last) and (ref_images or ref_videos or ref_audio):
            raise ValueError("first/last_frame 与 reference_* 互斥，不能同时给")
        content = [{"type": "text", "text": prompt}]
        if first:
            content.append({"type": "image_url", "image_url": {"url": self.media_url(first, "image")}, "role": "first_frame"})
        if last:
            content.append({"type": "image_url", "image_url": {"url": self.media_url(last, "image")}, "role": "last_frame"})
        for p in ref_images:
            content.append({"type": "image_url", "image_url": {"url": self.media_url(p, "image")}, "role": "reference_image"})
        for p in ref_videos:
            content.append({"type": "video_url", "video_url": {"url": self.media_url(p, "video")}, "role": "reference_video"})
        for p in ref_audio:
            content.append({"type": "audio_url", "audio_url": {"url": self.media_url(p, "audio")}, "role": "reference_audio"})
        return content

    # ---------- 任务 ----------
    def submit(self, model, content, duration, resolution, ratio=None, expand=None):
        body = {"model": model, "content": content, "duration": int(duration), "resolution": resolution}
        if ratio:
            body["ratio"] = ratio
        if expand:
            body["extra"] = {"prompt_expansion_mode": expand}
        r = self._req("POST", "/v2/video_generation", headers=self._hdr(), data=json.dumps(body))
        d = self._check(r, "提交任务")
        tid = d.get("task_id") or (d.get("task") or {}).get("task_id") or (d.get("task") or {}).get("id")
        if not tid:
            raise RuntimeError(f"未返回 task_id：{json.dumps(d, ensure_ascii=False)[:300]}")
        return tid, d

    def query(self, task_id):
        r = self._req("GET", f"/v2/query/video_generation/{task_id}", headers=self._hdr())
        d = self._check(r, f"查询 {task_id}")
        t = d.get("task") if isinstance(d.get("task"), dict) else d
        status = str(t.get("status") or d.get("status") or "").lower()
        url = None
        c = t.get("content")
        if isinstance(c, dict):
            url = c.get("url") or c.get("video_url")
        elif isinstance(c, list):
            for it in c:
                if isinstance(it, dict):
                    vu = it.get("video_url")
                    u = it.get("url") or (vu.get("url") if isinstance(vu, dict) else vu)
                    if u:
                        url = u
                        break
        url = url or t.get("video_url") or d.get("video_url")
        file_id = t.get("file_id") or d.get("file_id")
        return status, url, file_id, d

    def download(self, url, file_id, out, task_id=None, tries=4):
        """下载成片。链接限时、阿里云 OSS 偶尔读超时：失败就重新查询拿新链接再下，最多 tries 次。"""
        out = pathlib.Path(C.abspath(out))
        out.parent.mkdir(parents=True, exist_ok=True)
        last = None
        for k in range(tries):
            if (not url or k > 0) and task_id:
                try:
                    _, url2, fid2, _ = self.query(task_id)
                    url, file_id = url2 or url, fid2 or file_id
                except Exception as e:
                    last = e
            if not url and file_id:
                r = self._req("GET", f"/v1/files/retrieve?file_id={file_id}", headers=self._hdr())
                d = self._check(r, "取文件地址")
                url = (d.get("file") or {}).get("download_url") or d.get("download_url")
            if not url:
                last = RuntimeError("没有可下载的地址")
                continue
            tmp = out.with_suffix(out.suffix + ".part")
            try:
                with requests.get(url, stream=True, timeout=(30, 120)) as r:
                    r.raise_for_status()
                    with open(tmp, "wb") as f:
                        for chunk in r.iter_content(1 << 20):
                            f.write(chunk)
                tmp.replace(out)
                return str(out)
            except Exception as e:
                last = e
                C.log(TAG, f"下载第 {k + 1} 次失败：{str(e)[:120]}，{10 * (k + 1)}s 后重试")
                time.sleep(10 * (k + 1))
        raise RuntimeError(f"下载失败：{last}")


def variant_path(out, k, n):
    if n <= 1:
        return out
    p = pathlib.Path(out)
    return str(p.with_name(f"{p.stem}_v{k + 1}{p.suffix}"))


def run_jobs(jobs, poll=15):
    mm = MiniMax()
    pending, results = {}, []
    for j in jobs:
        n = int(j.get("n", 1))
        for k in range(n):
            out = variant_path(j["out"], k, n)
            if not j.get("force") and os.path.exists(C.abspath(out)) and C.ffprobe(C.abspath(out)).get("duration", 0) > 0:
                C.log(TAG, f"{j['id']} 已存在，跳过 {out}")
                results.append({"id": j["id"], "out": out, "status": "exists"})
                continue
            try:
                content = mm.build_content(j["prompt"], j.get("first_frame"), j.get("last_frame"),
                                           j.get("ref_images", []), j.get("ref_videos", []), j.get("ref_audio", []))
                tid, resp = mm.submit(j.get("model", "MiniMax-H3"), content, j.get("duration", 6),
                                      j.get("resolution", "2K"), j.get("ratio"), j.get("expand"))
            except Exception as e:
                C.log(TAG, f"{j['id']} 提交失败：{e}")
                results.append({"id": j["id"], "out": out, "status": "submit_failed", "error": str(e)})
                continue
            C.log(TAG, f"{j['id']} 已提交 task={tid} → {out}")
            pending[tid] = {"job": j, "out": out, "t0": time.time()}
            C.write_json(C.abspath(out) + ".json", {"job": j, "task_id": tid, "submit_resp": resp, "status": "pending"})
            time.sleep(1.5)
    while pending:
        time.sleep(poll)
        for tid in list(pending):
            p = pending[tid]
            try:
                status, url, file_id, raw = mm.query(tid)
            except Exception as e:
                C.log(TAG, f"{p['job']['id']} 查询异常：{e}")
                continue
            meta_path = C.abspath(p["out"]) + ".json"
            if status in DONE:
                try:
                    path = mm.download(url, file_id, p["out"], task_id=tid)
                    info = C.ffprobe(path)
                    usage = (raw.get("task") or {}).get("usage") or {}
                    C.log(TAG, f"{p['job']['id']} ✓ {int(time.time() - p['t0'])}s {info.get('width')}x{info.get('height')} "
                               f"{info.get('duration')}s audio={info.get('has_audio', False)} 计费{usage.get('total_seconds', '?')}s → {p['out']}")
                    results.append({"id": p["job"]["id"], "out": p["out"], "status": "done", "info": info})
                    C.write_json(meta_path, {"job": p["job"], "task_id": tid, "status": "done", "info": info, "query_resp": raw})
                except Exception as e:
                    C.log(TAG, f"{p['job']['id']} 下载失败：{e}")
                    results.append({"id": p["job"]["id"], "out": p["out"], "status": "download_failed", "error": str(e)})
                del pending[tid]
            elif status in FAILED:
                C.log(TAG, f"{p['job']['id']} ✗ 失败：{json.dumps(raw, ensure_ascii=False)[:400]}")
                results.append({"id": p["job"]["id"], "out": p["out"], "status": "failed", "raw": raw})
                C.write_json(meta_path, {"job": p["job"], "task_id": tid, "status": "failed", "query_resp": raw})
                del pending[tid]
            else:
                C.log(TAG, f"{p['job']['id']} {status or 'pending'} {int(time.time() - p['t0'])}s")
    return results


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--jobs")
    ap.add_argument("--prompt")
    ap.add_argument("--out")
    ap.add_argument("--id", default="shot")
    ap.add_argument("--model", default="MiniMax-H3")
    ap.add_argument("--duration", type=int, default=6)
    ap.add_argument("--resolution", default="2K")
    ap.add_argument("--ratio")
    ap.add_argument("--expand", choices=["disabled", "balanced", "quality"])
    ap.add_argument("--first-frame")
    ap.add_argument("--last-frame")
    ap.add_argument("--ref-image", action="append", default=[])
    ap.add_argument("--ref-video", action="append", default=[])
    ap.add_argument("--ref-audio", action="append", default=[])
    ap.add_argument("--n", type=int, default=1)
    ap.add_argument("--force", action="store_true")
    ap.add_argument("--poll", type=int, default=15)
    ap.add_argument("--fetch", help="按任务号补下载：TASK_ID:输出路径")
    a = ap.parse_args()
    if a.fetch:
        tid, out = a.fetch.split(":", 1)
        mm = MiniMax()
        status, url, fid, raw = mm.query(tid)
        if status not in DONE:
            sys.exit(f"任务 {tid} 状态是 {status}，不能下载")
        path = mm.download(url, fid, out, task_id=tid)
        info = C.ffprobe(path)
        C.log(TAG, f"补下载 ✓ {tid} → {out} {info.get('width')}x{info.get('height')} {info.get('duration')}s")
        meta = C.abspath(out) + ".json"
        old_meta = C.read_json(meta) if os.path.exists(meta) else {}
        old_meta.update({"task_id": tid, "status": "done", "info": info, "query_resp": raw})
        C.write_json(meta, old_meta)
        return
    if a.jobs:
        jobs = C.read_json(C.abspath(a.jobs))
    else:
        if not (a.prompt and a.out):
            ap.error("需要 --prompt 和 --out，或 --jobs")
        jobs = [{"id": a.id, "out": a.out, "prompt": a.prompt, "model": a.model, "duration": a.duration,
                 "resolution": a.resolution, "ratio": a.ratio, "expand": a.expand, "first_frame": a.first_frame,
                 "last_frame": a.last_frame, "ref_images": a.ref_image, "ref_videos": a.ref_video,
                 "ref_audio": a.ref_audio, "n": a.n, "force": a.force}]
    res = run_jobs(jobs, a.poll)
    ok = sum(1 for r in res if r["status"] in ("done", "exists"))
    C.log(TAG, f"完成 {ok}/{len(res)}")
    print(json.dumps(res, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
