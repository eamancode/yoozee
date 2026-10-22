"""公共工具：读 .env、日志、ffprobe、JSON 落盘。所有脚本共用。"""
import datetime
import json
import os
import pathlib
import subprocess
import sys

_RUNTIME = pathlib.Path(__file__).resolve().parent.parent   # 运行时目录：.claude/yoozee（Claude Code）或 yoozee（Codex）
ROOT = pathlib.Path(os.environ["YOOZEE_ROOT"]).resolve() if os.environ.get("YOOZEE_ROOT") else (
    _RUNTIME.parent.parent if _RUNTIME.parent.name == ".claude" else _RUNTIME.parent)  # 项目根
ENV_FILE = ROOT / ".env"
LOG_DIR = ROOT / "logs"
try:
    LOG_DIR.mkdir(exist_ok=True)
except OSError:
    pass


def load_env(path=ENV_FILE):
    if not path.exists():
        return
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        os.environ.setdefault(k.strip(), v.strip().strip('"').strip("'"))


load_env()


def need(key):
    v = os.environ.get(key)
    if not v:
        sys.exit(f"[config] 缺少 {key}：写入 {ENV_FILE}（一行 {key}=xxx）或 export 后重试")
    return v


def log(tag, msg):
    line = f"{datetime.datetime.now().strftime('%H:%M:%S')} [{tag}] {msg}"
    print(line, flush=True)
    with open(LOG_DIR / f"{tag}.log", "a", encoding="utf-8") as f:
        f.write(line + "\n")


def ffprobe(path):
    """返回 {duration, width, height, fps, has_audio}；失败返回 {}。"""
    try:
        out = subprocess.run(
            ["ffprobe", "-v", "error", "-print_format", "json", "-show_format", "-show_streams", str(path)],
            capture_output=True, text=True, timeout=60).stdout
        d = json.loads(out or "{}")
    except Exception:
        return {}
    info = {"duration": round(float(d.get("format", {}).get("duration", 0) or 0), 3)}
    for s in d.get("streams", []):
        if s.get("codec_type") == "video":
            info.update(width=s.get("width"), height=s.get("height"), fps=s.get("r_frame_rate"))
        if s.get("codec_type") == "audio":
            info.update(has_audio=True, sample_rate=s.get("sample_rate"), channels=s.get("channels"))
    return info


def write_json(path, obj):
    path = pathlib.Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(obj, f, ensure_ascii=False, indent=2)


def read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def abspath(p):
    p = pathlib.Path(p)
    return str(p if p.is_absolute() else ROOT / p)


_SRC = _RUNTIME.parent.parent if (_RUNTIME.parent.parent / "kb" / "kb-dialects" / "models.json").exists() else None  # 源码树（yoozee-skills/）里直接跑时


def runtime_file(*parts):
    """运行时目录里的数据文件（profiles/、rules/、contracts/）；源码树里直接跑时回到 yoozee-skills/ 下同名目录。"""
    for base in (_RUNTIME, _SRC):
        if base is not None:
            p = base.joinpath(*parts)
            if p.exists():
                return p
    sys.exit(f"[config] 找不到 {'/'.join(parts)}：先把 yoozee 技能包装进项目")


def models_json():
    """项目可选的生成模型（kb-dialects 知识包里的 models.json）。Claude Code 装在 .claude/skills，Codex 装在 .agents/skills。"""
    bases = [ROOT / ".claude" / "skills", ROOT / ".agents" / "skills", _RUNTIME.parent / "skills"]
    if _SRC is not None:
        bases.append(_SRC / "kb")
    for base in bases:
        p = base / "kb-dialects" / "models.json"
        if p.exists():
            return read_json(p)
    sys.exit("[config] 找不到 kb-dialects/models.json：先把 yoozee 技能包装进项目")


def kb_file(pkg, *parts):
    """知识包里的文件（词库 JSONL 等）。"""
    bases = [ROOT / ".claude" / "skills", ROOT / ".agents" / "skills", _RUNTIME.parent / "skills"]
    if _SRC is not None:
        bases.append(_SRC / "kb")
    for base in bases:
        p = base.joinpath(pkg, *parts)
        if p.exists():
            return p
    return None


_VOCAB_CACHE = {}


def vocab(pkg, rel):
    """知识包词库（JSONL）→ 列表；找不到返回空列表。rel 如 vocab/emotion-arcs.jsonl。"""
    key = (pkg, rel)
    if key not in _VOCAB_CACHE:
        p = kb_file(pkg, *rel.split("/"))
        items = []
        if p:
            with open(p, encoding="utf-8") as fh:
                items = [json.loads(ln) for ln in fh if ln.strip()]
        _VOCAB_CACHE[key] = items
    return _VOCAB_CACHE[key]


def vocab_by_id(pkg, rel):
    return {x["id"]: x for x in vocab(pkg, rel)}


def profile(profile_id):
    """风格档案（profiles/<id>.json），处理 extends 继承：子档案字段覆盖父档案，quotas / rules 逐键合并。"""
    data = read_json(runtime_file("profiles", f"{profile_id}.json"))
    if data.get("extends"):
        base = profile(data["extends"])
        merged = dict(base)
        for k, v in data.items():
            if k in ("quotas", "rules", "color", "lens_policy", "space_policy", "vfx_policy", "asset_tier_policy") and isinstance(v, dict) and isinstance(base.get(k), dict):
                merged[k] = {**base[k], **v}
            else:
                merged[k] = v
        return merged
    return data


def rules(kind):
    """规则表 rules/<kind>.json → {id: rule}。"""
    return {r["id"]: r for r in read_json(runtime_file("rules", f"{kind}.json"))}


def project_brief():
    """2.0 的需求文档 JSON（项目根目录 需求.json）；没有返回 None。"""
    p = ROOT / "需求.json"
    return read_json(p) if p.exists() else None


def model(model_id):
    data = models_json()
    for m in data["models"]:
        if m["id"] == model_id:
            return m
    sys.exit(f"[config] models.json 里没有模型 {model_id}；可选：{', '.join(m['id'] for m in data['models'])}")


def project_model():
    """需求文档里定的生成模型：读 需求文档.md 里「生成模型：」一行，或环境变量 YOOZEE_VIDEO_MODEL。"""
    if os.environ.get("YOOZEE_VIDEO_MODEL"):
        return os.environ["YOOZEE_VIDEO_MODEL"].strip()
    b = project_brief()
    if b and b.get("video_model"):
        return b["video_model"]
    brief = ROOT / "需求文档.md"
    if brief.exists():
        import re
        text = brief.read_text(encoding="utf-8")
        ids = [m["id"] for m in models_json()["models"]]
        hit = re.search(r"生成模型[：:]\s*([^\n]+)", text)
        if hit:
            line = hit.group(1).lower().replace(" ", "")
            for mid in sorted(ids, key=len, reverse=True):
                if mid.replace("-", "") in line.replace("-", "") or mid in line:
                    return mid
    data = models_json()
    return data.get("recommended") or data["default"]


def project_profile():
    """需求文档里定的风格档案 id；没有就 live-realism。"""
    if os.environ.get("YOOZEE_PROFILE"):
        return os.environ["YOOZEE_PROFILE"].strip()
    b = project_brief()
    return (b or {}).get("style_profile") or "live-realism"


def project_ratio():
    """需求文档里定的画幅：先读 需求.json 的 platform.value，再读 需求文档.md 里「画幅：」一行写的比例，如 9:16；没写比例返回 None。"""
    import re
    b = project_brief()
    if b and (b.get("platform") or {}).get("value"):
        ratio = re.search(r"(\d+(?:\.\d+)?)\s*[:：]\s*(\d+(?:\.\d+)?)", b["platform"]["value"])
        if ratio:
            return f"{ratio.group(1)}:{ratio.group(2)}"
    brief = ROOT / "需求文档.md"
    if not brief.exists():
        return None
    import re
    hit = re.search(r"画幅[：:]\s*([^\n]+)", brief.read_text(encoding="utf-8"))
    ratio = hit and re.search(r"(\d+(?:\.\d+)?)\s*[:：]\s*(\d+(?:\.\d+)?)", hit.group(1))
    return f"{ratio.group(1)}:{ratio.group(2)}" if ratio else None
