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


def models_json():
    """项目可选的生成模型（kb-dialects 知识包里的 models.json）。Claude Code 装在 .claude/skills，Codex 装在 .agents/skills。"""
    for base in (ROOT / ".claude" / "skills", ROOT / ".agents" / "skills", _RUNTIME.parent / "skills"):
        p = base / "kb-dialects" / "models.json"
        if p.exists():
            return read_json(p)
    sys.exit("[config] 找不到 kb-dialects/models.json：先把 yoozee 技能包装进项目")


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
    return models_json()["default"]
