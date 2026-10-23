#!/usr/bin/env python3
"""yoozee 1.0 skills 打包脚本：一份源文件，打出四种包。

  python3 yoozee-skills/build.py            自检 → dist/claude-code、dist/codex、dist/standalone/*.zip，并写柚子内置技能 backend/builtin/
  python3 yoozee-skills/build.py --check    只自检，不写文件
  python3 yoozee-skills/build.py --no-yozi  不写 backend/builtin（只打对外包）
  python3 yoozee-skills/build.py --package yz-chapter-shots
                                            把流程技能包 packages/<id> 打成 dist/packages/<id>-<version>.zip（清单、files 列出的文件、
                                            内容哈希），先跑一遍与线上同一套的静态校验；不写 backend/builtin

流程技能包（packages/<id>）原样上传，不做占位符替换；默认构建把它同步成柚子内置种子 backend/builtin/<id>（只在首次安装时导入）。

自检：SKILL.md 字数上限、描述长度、引用的知识卡存在、占位符全部替换、契约是合法 JSON Schema、样例分段符合契约、方言编译对照测试。
"""
import argparse
import datetime
import hashlib
import json
import pathlib
import re
import shutil
import subprocess
import sys
import zipfile

SRC = pathlib.Path(__file__).resolve().parent
REPO = SRC.parent
DIST = SRC / "dist"
BUILTIN = REPO / "backend" / "builtin"
PACKAGES = SRC / "packages"
# 线上静态校验用到的步骤表、风格档案和点名规则，由后端 go test ./internal/drama -run TestStepPackageRulesExported -update 生成。
PACKAGE_RULES = REPO / "backend" / "internal" / "drama" / "contracts" / "step-packages.json"
VERSION = (SRC / "VERSION").read_text(encoding="utf-8").strip()
RELEASED = datetime.datetime(2026, 10, 15, tzinfo=datetime.timezone.utc)

SKILL_BODY_LIMIT = 6000        # yz-* 正文（不含 frontmatter）
CARD_LIMIT = 12000             # 每张知识卡
DESCRIPTION_LIMIT = 500        # 柚子技能简介上限（rune）
NAME_LIMIT = 80

TARGETS = {
    "claude-code": {
        "SKILLS": ".claude/skills",
        "TOOLS": ".claude/yoozee/tools",
        "RUNTIME": "`.claude/yoozee/RUNTIME.md`",
        "RUNTIME_PATH": "`.claude/yoozee/RUNTIME.md`",
    },
    "codex": {
        "SKILLS": ".agents/skills",
        "TOOLS": "yoozee/tools",
        "RUNTIME": "`yoozee/RUNTIME.md`",
        "RUNTIME_PATH": "`yoozee/RUNTIME.md`",
    },
    "yozi": {
        "SKILLS": "",
        "TOOLS": "",
        "RUNTIME": "技能 yz-producer 的 `RUNTIME.md`（用 skill_read_file 读）",
        "RUNTIME_PATH": "技能 yz-producer 的 `RUNTIME.md`",
    },
}

TOKEN = re.compile(r"\{\{(SKILLS|TOOLS|RUNTIME|RUNTIME_PATH|CARDS)\}\}")
CARD_REF = re.compile(r"(kb-[a-z]+) › ([^\s（）。、，；：「」]+?\.(?:md|json))")


def read(p):
    return pathlib.Path(p).read_text(encoding="utf-8")


def split_frontmatter(text):
    if not text.startswith("---\n"):
        raise SystemExit("SKILL.md 缺少 frontmatter")
    head, body = text[4:].split("\n---\n", 1)
    meta, block = {}, None
    for line in head.splitlines():
        if not line.strip():
            continue
        if line.startswith("  ") and block:
            k, v = line.strip().split(":", 1)
            meta[block][k.strip()] = v.strip()
            continue
        k, v = line.split(":", 1)
        k, v = k.strip(), v.strip()
        if v == "":
            block = k
            meta[k] = {}
        else:
            block = None
            meta[k] = v
    return meta, body.lstrip("\n")


def packages():
    """所有包：(类型, 名字, 源目录)。"""
    out = []
    for d in sorted((SRC / "skills").iterdir()):
        if (d / "SKILL.md").exists():
            out.append(("skill", d.name, d))
    for d in sorted((SRC / "kb").iterdir()):
        if (d / "SKILL.md").exists():
            out.append(("kb", d.name, d))
    return out


def card_when(path):
    for line in read(path).splitlines()[:4]:
        if line.startswith("> 何时读："):
            return line[len("> 何时读："):].strip()
    return ""


def card_index(pkg_dir):
    rows = ["| 卡片 | 何时读 |", "| --- | --- |"]
    for p in sorted(pkg_dir.rglob("*")):
        if p.is_dir() or p.name == "SKILL.md":
            continue
        rel = p.relative_to(pkg_dir).as_posix()
        when = card_when(p) if p.suffix == ".md" else ("可选生成模型的边界：一段最长几秒、参考数量、提示词上限" if p.name == "models.json" else "")
        rows.append(f"| `{rel}` | {when} |")
    return "\n".join(rows)


def render(text, target, pkg_dir=None):
    values = dict(TARGETS[target])
    if pkg_dir is not None and "{{CARDS}}" in text:
        values["CARDS"] = card_index(pkg_dir)
    out = TOKEN.sub(lambda m: values.get(m.group(1), m.group(0)), text)
    if target == "yozi":
        out = out.replace("`/` ", "").replace("``", "")
    return out


# ---------------------------------------------------------------- 自检

def check():
    problems = []
    names = set()
    for kind, name, d in packages():
        meta, body = split_frontmatter(read(d / "SKILL.md"))
        names.add(name)
        if meta.get("name") != name:
            problems.append(f"{name}: frontmatter name 与目录名不一致")
        desc = meta.get("description", "")
        if not desc or len(desc) > DESCRIPTION_LIMIT:
            problems.append(f"{name}: description 为空或超过 {DESCRIPTION_LIMIT} 字（{len(desc)}）")
        if len(meta.get("title", "")) > NAME_LIMIT:
            problems.append(f"{name}: title 超过 {NAME_LIMIT} 字")
        if kind == "skill" and len(body) > SKILL_BODY_LIMIT:
            problems.append(f"{name}: 正文 {len(body)} 字，超过 {SKILL_BODY_LIMIT}")
        if kind == "kb":
            for card in d.rglob("*.md"):
                if card.name != "SKILL.md" and len(read(card)) > CARD_LIMIT + 400:
                    problems.append(f"{name}: 卡片 {card.relative_to(d)} 超过 {CARD_LIMIT} 字")
        texts = [read(p) for p in d.rglob("*.md")]
        for text in texts:
            for pkg, rel in CARD_REF.findall(text):
                if "{" in rel or "xxx" in rel:
                    continue
                if not (SRC / "kb" / pkg / rel).exists():
                    problems.append(f"{name}: 引用了不存在的卡片 {pkg} › {rel}")
    for text in [read(p) for p in (SRC / "bindings").rglob("*.md")]:
        for pkg, rel in CARD_REF.findall(text):
            if "xxx" not in rel and not (SRC / "kb" / pkg / rel).exists():
                problems.append(f"bindings: 引用了不存在的卡片 {pkg} › {rel}")
    # 契约与样例
    try:
        import jsonschema
    except ImportError:
        jsonschema = None
    schemas = {}
    for p in sorted((SRC / "contracts").glob("*.schema.json")):
        try:
            schemas[p.stem.split(".")[0]] = json.loads(read(p))
        except json.JSONDecodeError as e:
            problems.append(f"契约 {p.name} 不是合法 JSON：{e}")
            continue
        if jsonschema:
            try:
                jsonschema.Draft202012Validator.check_schema(schemas[p.stem.split(".")[0]])
            except jsonschema.SchemaError as e:
                problems.append(f"契约 {p.name} 不是合法 JSON Schema：{e.message}")
    if jsonschema and "segment" in schemas:
        v = jsonschema.Draft202012Validator(schemas["segment"])
        for p in sorted((SRC / "tests" / "fixtures").glob("segment-*.json")):
            for err in v.iter_errors(json.loads(read(p))):
                problems.append(f"样例 {p.name} 不符合契约 segment：{err.message}")
    models = json.loads(read(SRC / "kb" / "kb-dialects" / "models.json"))
    ids = [m["id"] for m in models["models"]]
    if models["default"] not in ids:
        problems.append("models.json 的 default 不在模型列表里")
    for m in models["models"]:
        d = m.get("duration", {})
        if not (isinstance(d.get("min"), int) and isinstance(d.get("max"), int) and d["min"] <= d["max"]):
            problems.append(f"models.json {m.get('id')}: duration 无效")
        for f in ("档案.md", "方言卡.md"):
            if not (SRC / "kb" / "kb-dialects" / m["id"] / f).exists():
                problems.append(f"kb-dialects/{m['id']}/{f} 不存在")
    r = subprocess.run([sys.executable, str(SRC / "tests" / "test_compile.py")], capture_output=True, text=True)
    if r.returncode:
        problems.append("方言编译对照测试没过：\n" + r.stdout + r.stderr)
    problems += check_flow_packages()
    return problems


# ---------------------------------------------------------------- 流程技能包
# 静态校验与后端 internal/drama/step_package.go 是同一套规则；两边共用 tests/packages/cases.json。

def package_rules():
    return json.loads(read(PACKAGE_RULES))


def js_len(text):
    """JS 的 string.length（UTF-16 码元），线上按它算字数。"""
    return len(text.encode("utf-16-le")) // 2


def _problem(rule, message, path=""):
    out = {"rule": rule, "message": message}
    if path:
        out["path"] = path
    return out


def _clean_path(p):
    if not isinstance(p, str) or not p or "\\" in p or "\x00" in p or p.startswith("/"):
        return False
    return all(part not in ("", ".", "..") for part in p.split("/"))


MANIFEST_FIELDS = {"id": str, "step": str, "contract": str, "profiles": list, "version": str, "files": list, "eval": list}


def parse_manifest(raw, rules):
    """读 package.json：字段齐全、类型对、没有多余字段。与 drama.ParseStepPackageManifest 一致。"""
    mf = rules["manifestFile"]

    def bad(msg):
        return _problem("manifest", msg, mf)

    if raw is None or not raw.strip():
        return None, [bad(f"包里没有 {mf}")]
    try:
        m = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as e:
        return None, [bad(f"{mf} 不是合法的清单：{e}")]
    if not isinstance(m, dict):
        return None, [bad(f"{mf} 不是合法的清单：顶层要是对象")]
    for k, v in m.items():
        if k not in MANIFEST_FIELDS:
            return None, [bad(f"{mf} 不是合法的清单：未知字段 {k}")]
        t = MANIFEST_FIELDS[k]
        if v is not None and (not isinstance(v, t) or (t is list and not all(isinstance(x, str) for x in v))):
            return None, [bad(f"{mf} 不是合法的清单：{k} 类型不对")]
    m = {k: (m[k] if m.get(k) is not None else ([] if t is list else "")) for k, t in MANIFEST_FIELDS.items()}
    out = []
    if not re.match(rules["idPattern"], m["id"]):
        out.append(bad(f"id「{m['id']}」只能是小写字母、数字和连字符，1～36 个字符"))
    if not m["step"].strip():
        out.append(bad("缺少 step"))
    if not re.match(rules["contractPattern"], m["contract"]):
        out.append(bad(f"contract「{m['contract']}」要写成「步骤@版本号」"))
    if not re.match(rules["versionPattern"], m["version"]):
        out.append(bad(f"version「{m['version']}」只能是字母、数字和 . + -，最长 64 个字符"))
    if not m["profiles"]:
        out.append(bad("profiles 至少写一个风格档案"))
    if len(set(m["profiles"])) != len(m["profiles"]):
        out.append(bad("profiles 里有重复"))
    if not m["files"]:
        out.append(bad("files 不能为空"))
    for f in m["files"]:
        if not _clean_path(f):
            out.append(bad(f"files 里的「{f}」不是包内的相对路径"))
        elif f in (mf, rules["hashFile"]):
            out.append(bad(f"files 不用列 {f}"))
    if len(set(m["files"])) != len(m["files"]):
        out.append(bad("files 里有重复"))
    if m["files"] and "SKILL.md" not in m["files"]:
        out.append(bad("files 里没有 SKILL.md"))
    if any(not e.strip() for e in m["eval"]):
        out.append(bad("eval 里有空的章节 ID"))
    return m, out


def _text_file(p, rules):
    if p.startswith("scripts/") or p.startswith("assets/"):
        return False
    ext = pathlib.PurePosixPath(p).suffix.lower()
    return ext != "" and ext in rules["textExtensions"]


def check_step_package(m, files, rules):
    """对照线上步骤表查包；files 是 {路径: bytes}。与 drama.CheckStepPackage 一致。"""
    out = []
    steps = {s["id"]: s for s in rules["steps"]}
    step = steps.get(m["step"])
    if step is None:
        out.append(_problem("step", f"step「{m['step']}」不在线上步骤表里（现有：{'、'.join(steps)}）"))
    elif m["contract"] != step["contract"]:
        out.append(_problem("contract", f"清单声明满足契约 {m['contract']}，线上这一步的契约是 {step['contract']}"))
    for pid in m["profiles"]:
        if pid not in rules["profiles"]:
            out.append(_problem("profiles", f"风格档案「{pid}」不存在（已知：{'、'.join(rules['profiles'])}）"))
    listed = set(m["files"])
    for f in m["files"]:
        if f not in files:
            out.append(_problem("files", f"files 列了 {f}，包里没有这个文件", f))
    for p in sorted(files):
        if p not in (rules["manifestFile"], rules["hashFile"]) and p not in listed:
            out.append(_problem("files", f"包里的 {p} 没写进 files", p))
    if "SKILL.md" not in files or "SKILL.md" not in listed:
        return out
    try:
        entry = files["SKILL.md"].decode("utf-8")
    except UnicodeDecodeError:
        out.append(_problem("entry", "SKILL.md 不是 UTF-8 文本", "SKILL.md"))
        return out
    required_re = re.compile(rules["requiredReading"])
    ref_re = re.compile(rules["cardReference"], re.ASCII)
    missing = []
    for line in entry.split("\n"):
        for ref in ref_re.findall(line):
            ref = ref[2:] if ref.startswith("./") else ref
            if ref in listed or ref in missing or not required_re.search(ref + " " + line):
                continue
            missing.append(ref)
    for ref in missing:
        out.append(_problem("required_card", f"SKILL.md 点名必读 {ref}，但 files 里没有", ref))
    if step is None:
        return out
    cards = []
    for p in m["files"]:
        if p == "SKILL.md" or p not in files or not _text_file(p, rules):
            continue
        try:
            content = files[p].decode("utf-8")
        except UnicodeDecodeError:
            continue
        index = entry.find(p)
        if index < 0:
            continue
        line_start = entry.rfind("\n", 0, index) + 1
        line_end = entry.find("\n", index)
        line = entry[line_start:] if line_end < 0 else entry[line_start:line_end]
        cards.append({"path": p, "chars": js_len(content), "index": index, "required": bool(required_re.search(p + " " + line))})
    required = sorted((c for c in cards if c["required"]), key=lambda c: c["index"])
    optional = sorted((c for c in cards if not c["required"]), key=lambda c: -c["chars"])
    if len(required) > step["skillLinkedFiles"]:
        out.append(_problem("budget", f"SKILL.md 点名必读 {len(required)} 张卡，这一步最多注入 {step['skillLinkedFiles']} 张，多出的读不到"))
    chosen = (required + optional)[:step["skillLinkedFiles"]]
    total = js_len(entry) + sum(c["chars"] for c in chosen)
    if total > step["skillBudgetChars"]:
        parts = [f"SKILL.md {js_len(entry)} 字"] + [f"{c['path']} {c['chars']} 字" for c in chosen]
        out.append(_problem("budget", f"最多注入 {total} 字（{' + '.join(parts)}），超过这一步的上限 {step['skillBudgetChars']} 字，超出部分会被截断"))
    return out


def package_problems(manifest_raw, files, rules=None):
    """上传时的顺序：清单不合格就不往下查。"""
    rules = rules or package_rules()
    m, problems = parse_manifest(manifest_raw, rules)
    if problems:
        return m, problems
    return m, check_step_package(m, files, rules)


def content_hash(files):
    """与后端 contentHashAndSize 一致：按路径排序，逐个写 路径\\0内容\\0 的 sha256。"""
    h = hashlib.sha256()
    for p in sorted(files):
        h.update(p.encode("utf-8") + b"\0" + files[p] + b"\0")
    return h.hexdigest()


def read_package_dir(d):
    return {p.relative_to(d).as_posix(): p.read_bytes() for p in sorted(d.rglob("*"))
            if p.is_file() and "__pycache__" not in p.parts and p.name != ".DS_Store"}


def _zip_info(name):
    info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
    info.compress_type = zipfile.ZIP_DEFLATED
    info.external_attr = 0o644 << 16
    return info


def build_package(pid):
    d = PACKAGES / pid
    if not (d / "package.json").exists():
        raise SystemExit(f"没有流程技能包 packages/{pid}（要有 package.json）")
    rules = package_rules()
    files = read_package_dir(d)
    m, problems = package_problems(files.get(rules["manifestFile"]), files, rules)
    if not problems and m["id"] != pid:
        problems.append(_problem("manifest", f"清单 id「{m['id']}」与目录名 {pid} 不一致", rules["manifestFile"]))
    if problems:
        print(f"流程技能包 {pid} 静态校验没过：")
        print("\n".join(f"✗ [{p['rule']}] {p['message']}" for p in problems))
        sys.exit(2)
    payload = {p: files[p] for p in m["files"]}
    payload[rules["manifestFile"]] = files[rules["manifestFile"]]
    digest = {
        "algorithm": "sha256：按路径排序，逐个写 路径\\0内容\\0",
        "contentHash": content_hash(payload),
        "files": {p: hashlib.sha256(payload[p]).hexdigest() for p in sorted(payload)},
    }
    out = DIST / "packages" / f"{pid}-{m['version']}.zip"
    out.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
        for p in sorted(payload):
            z.writestr(_zip_info(p), payload[p])
        z.writestr(_zip_info(rules["hashFile"]), json.dumps(digest, ensure_ascii=False, indent=2) + "\n")
    print(f"✓ 静态校验通过：{pid} {m['version']}（步骤 {m['step']}，契约 {m['contract']}，{len(m['files'])} 个文件）")
    shown = out.relative_to(REPO).as_posix() if out.is_relative_to(REPO) else out
    print(f"✓ {shown}  内容哈希 {digest['contentHash']}")
    return out


def flow_packages():
    if not PACKAGES.exists():
        return []
    return sorted(d for d in PACKAGES.iterdir() if (d / "package.json").exists())


def check_flow_packages():
    """每个流程技能包都能过静态校验，且与内置种子 backend/builtin/<id> 逐字一致。"""
    problems = []
    rules = package_rules()
    for d in flow_packages():
        files = read_package_dir(d)
        _, found = package_problems(files.get(rules["manifestFile"]), files, rules)
        problems += [f"packages/{d.name}: [{p['rule']}] {p['message']}" for p in found]
        seed = BUILTIN / d.name
        if seed.exists() and read_package_dir(seed) != files:
            problems.append(f"packages/{d.name}: 与内置种子 backend/builtin/{d.name} 不一致，跑一次 build.py 同步")
    return problems


def sync_flow_package_seeds():
    names = []
    for d in flow_packages():
        seed = BUILTIN / d.name
        if seed.exists():
            shutil.rmtree(seed)
        shutil.copytree(d, seed, ignore=shutil.ignore_patterns("__pycache__", ".DS_Store"))
        names.append(d.name)
    return names


# ---------------------------------------------------------------- 打包

def copy_tree(src, dst, target, skip=()):
    for p in sorted(src.rglob("*")):
        if p.is_dir() or "__pycache__" in p.parts or p.name in skip:
            continue
        out = dst / p.relative_to(src)
        out.parent.mkdir(parents=True, exist_ok=True)
        if p.suffix in (".md", ".py", ".sh", ".txt", ".json", ".yaml"):
            text = render(read(p), target, src if p.name == "SKILL.md" else None)
            leftover = TOKEN.findall(text)
            if leftover:
                raise SystemExit(f"{p}: 占位符没替换完：{leftover}")
            out.write_text(text, encoding="utf-8", newline="\n")
            if p.suffix in (".py", ".sh"):
                out.chmod(0o755)
        else:
            shutil.copy2(p, out)


def skill_markdown(meta, body, target, pkg_dir):
    body = render(body, target, pkg_dir)
    if TOKEN.findall(body):
        raise SystemExit(f"{meta['name']}: 占位符没替换完")
    if target == "yozi":
        created = int(RELEASED.timestamp() * 1000)
        weight = meta.get("yozi", {}).get("sortWeight", "1900")
        head = [
            "---",
            f'name: "{meta["title"]}"',
            f'description: "{meta["description"]}"',
            "metadata:",
            f'  version: "{VERSION}"',
            '  author: "柚子"',
            '  owner: "yingce-system"',
            f'  skillId: "{meta["name"]}"',
            "  tag: drama",
            f"  sortWeight: {weight}",
            "  source: 3",
            f"  createdAt: {created}",
            f"  updatedAt: {created}",
            "  initialLikeCount: 0",
            "  initialAddedCount: 0",
            '  extraInfo: ""',
            "---",
        ]
    else:
        head = ["---", f"name: {meta['name']}", f"description: {meta['description']}", "---"]
    return "\n".join(head) + "\n\n" + body


def build_skills(dst_skills, target):
    for kind, name, d in packages():
        meta, body = split_frontmatter(read(d / "SKILL.md"))
        out = dst_skills / name
        if out.exists():
            shutil.rmtree(out)
        copy_tree(d, out, target, skip=("SKILL.md",))
        out.mkdir(parents=True, exist_ok=True)
        (out / "SKILL.md").write_text(skill_markdown(meta, body, target, d), encoding="utf-8", newline="\n")
        if target == "codex":
            ag = out / "agents"
            ag.mkdir(exist_ok=True)
            short = meta["description"].split("。")[0][:80]
            (ag / "openai.yaml").write_text(
                "interface:\n"
                f'  display_name: "{meta["title"]}"\n'
                f'  short_description: "{short}"\n'
                + (f'  default_prompt: "使用 ${name}，读进度表，做下一件能做的事。"\n' if name == "yz-producer" else ""),
                encoding="utf-8", newline="\n")


def runtime_dir(dst, target):
    copy_tree(SRC / "bindings" / "local" / "tools", dst / "tools", target)
    shutil.copytree(SRC / "contracts", dst / "contracts", dirs_exist_ok=True)
    (dst / "RUNTIME.md").write_text(render(read(SRC / "bindings" / "local" / "RUNTIME.md"), target), encoding="utf-8", newline="\n")
    (dst / "ports.md").write_text(read(SRC / "ports.md"), encoding="utf-8", newline="\n")


def build_claude():
    root = DIST / "claude-code"
    if root.exists():
        shutil.rmtree(root)
    build_skills(root / ".claude" / "skills", "claude-code")
    runtime_dir(root / ".claude" / "yoozee", "claude-code")
    copy_tree(SRC / "bindings" / "local" / "agents", root / ".claude" / "agents", "claude-code")
    copy_tree(SRC / "bindings" / "local" / "hooks", root / ".claude" / "hooks", "claude-code")
    shutil.copy2(SRC / "bindings" / "local" / "settings.json", root / ".claude" / "settings.yoozee.json")
    (root / "CLAUDE.yoozee.md").write_text(render(read(SRC / "bindings" / "local" / "PROJECT.md"), "claude-code"), encoding="utf-8", newline="\n")
    return root


def build_codex():
    root = DIST / "codex"
    if root.exists():
        shutil.rmtree(root)
    build_skills(root / ".agents" / "skills", "codex")
    runtime_dir(root / "yoozee", "codex")
    copy_tree(SRC / "bindings" / "local" / "agents", root / "yoozee" / "helpers", "codex")
    copy_tree(SRC / "bindings" / "local" / "hooks", root / "yoozee" / "hooks", "codex")
    (root / "AGENTS.yoozee.md").write_text(render(read(SRC / "bindings" / "local" / "PROJECT.md"), "codex")
                                           + "\n- 审片员、探路员的做法写在 `yoozee/helpers/`，需要时按其中说明自己做或开子任务。\n", encoding="utf-8", newline="\n")
    return root


INSTALL = """# 安装 yoozee {version} skills

## Claude Code
把 `claude-code/` 里的内容拷进你的项目根目录：
- `.claude/skills/`、`.claude/agents/`、`.claude/yoozee/`、`.claude/hooks/` 直接合并；
- `CLAUDE.yoozee.md` 的内容追加到项目的 `CLAUDE.md`；
- `.claude/settings.yoozee.json` 里的 hooks 合并进 `.claude/settings.json`（密钥守门）。

## Codex
把 `codex/` 里的内容拷进项目根目录：`.agents/skills/`、`yoozee/` 直接合并，`AGENTS.yoozee.md` 的内容追加到 `AGENTS.md`。

## 开始
在项目里说「用 yz-producer 带我把这个剧本做成短剧」，把剧本发给它。生成模型的密钥写进项目根目录的 `.env`，不要写进对话和命令行。

本机需要 ffmpeg、Python 3（numpy、Pillow）、本地 whisper 命令和一种中文字体。
"""


def build_standalone(claude_root, codex_root):
    out = DIST / "standalone"
    out.mkdir(parents=True, exist_ok=True)
    zpath = out / f"yoozee-skills-{VERSION}.zip"
    prefix = f"yoozee-skills-{VERSION}"
    with zipfile.ZipFile(zpath, "w", zipfile.ZIP_DEFLATED) as z:
        for base, label in ((claude_root, "claude-code"), (codex_root, "codex")):
            for p in sorted(base.rglob("*")):
                if p.is_file():
                    z.write(p, f"{prefix}/{label}/{p.relative_to(base).as_posix()}")
        for name in ("README.md", "SOURCES.md", "CHANGELOG.md", "VERSION"):
            z.write(SRC / name, f"{prefix}/{name}")
        z.write(REPO / "LICENSE", f"{prefix}/LICENSE")
        z.writestr(f"{prefix}/INSTALL.md", INSTALL.format(version=VERSION))
    with zipfile.ZipFile(zpath) as z:
        bad = [n for n in z.namelist() if re.search(r"(^|/)\.env($|\.)|bindings/yozi", n)]
        if bad:
            raise SystemExit(f"对外包里不该有：{bad}")
    return zpath


def build_yozi():
    names = [name for _, name, _ in packages()]
    for name in names:
        if (BUILTIN / name).exists():
            shutil.rmtree(BUILTIN / name)
    build_skills(BUILTIN, "yozi")
    producer = BUILTIN / "yz-producer"
    (producer / "RUNTIME.md").write_text(read(SRC / "bindings" / "yozi" / "RUNTIME.md"), encoding="utf-8", newline="\n")
    (producer / "ports.md").write_text(read(SRC / "ports.md"), encoding="utf-8", newline="\n")
    shutil.copytree(SRC / "contracts", producer / "contracts", dirs_exist_ok=True)
    blocks = BUILTIN / "kb-dialects" / "blocks"
    blocks.mkdir(parents=True, exist_ok=True)
    for f in ("video_style.txt", "acting.txt", "video_style.zh.txt", "acting.zh.txt"):
        shutil.copy2(SRC / "bindings" / "local" / "tools" / f, blocks / f)
    return names


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true")
    ap.add_argument("--no-yozi", action="store_true")
    ap.add_argument("--package", metavar="ID", help="只构建一个流程技能包 packages/<ID>")
    a = ap.parse_args()
    if a.package:
        build_package(a.package)
        return
    problems = check()
    if problems:
        print("自检没过：")
        print("\n".join("✗ " + p for p in problems))
        sys.exit(2)
    print(f"✓ 自检通过（{len(packages())} 个包）")
    if a.check:
        return
    claude_root = build_claude()
    codex_root = build_codex()
    zpath = build_standalone(claude_root, codex_root)
    print(f"✓ {claude_root.relative_to(REPO)}\n✓ {codex_root.relative_to(REPO)}\n✓ {zpath.relative_to(REPO)}")
    if not a.no_yozi:
        names = build_yozi()
        print(f"✓ backend/builtin：{'、'.join(names)}")
        seeds = sync_flow_package_seeds()
        if seeds:
            print(f"✓ backend/builtin 流程技能包种子：{'、'.join(seeds)}")


if __name__ == "__main__":
    main()
