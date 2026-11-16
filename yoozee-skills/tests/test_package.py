#!/usr/bin/env python3
"""流程技能包静态校验：tests/packages/cases.json 里每个包给出的规则编号，与后端 drama.CheckStepPackage 一致
（backend/internal/drama/step_package_test.go 跑同一份样例）；packages/ 下的包都能过校验，构建出的 zip 带清单和内容哈希。
  python3 yoozee-skills/tests/test_package.py"""
import json
import hashlib
import pathlib
import sys
import tempfile
import zipfile

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path.insert(0, str(ROOT))
import build as B  # noqa: E402


def expand(value):
    return value if isinstance(value, str) else value["repeat"] * value["times"]


def test_sss_script_manuals():
    for kind in ("film", "drama"):
        director = B.BUILTIN / f"yz-sss-{kind}-director"
        script = (B.BUILTIN / f"yz-sss-{kind}-script" / "SKILL.md").read_bytes()
        for name in ("04_集设计.md", "05_台词手术.md"):
            assert (director / "手册" / name).read_bytes() in script
        for line in director.with_suffix(".SHA256SUMS").read_text(encoding="utf-8").splitlines():
            digest, path = line.split("  ", 1)
            assert hashlib.sha256((director / path).read_bytes()).hexdigest() == digest


def main():
    test_sss_script_manuals()
    rules = B.package_rules()
    failed = 0
    cases = json.loads((HERE / "packages" / "cases.json").read_text(encoding="utf-8"))["cases"]
    for c in cases:
        files = {p: expand(v).encode("utf-8") for p, v in c["files"].items()}
        manifest = json.dumps(c["manifest"], ensure_ascii=False).encode("utf-8")
        _, problems = B.package_problems(manifest, files, rules)
        got = sorted(p["rule"] for p in problems)
        if got != sorted(c["expect"]):
            failed += 1
            print(f"✗ {c['name']}：规则 {got}，期望 {sorted(c['expect'])}：{problems}")
        else:
            print(f"✓ {c['name']}")
    problems = B.check_flow_packages()
    if problems:
        failed += 1
        print("✗ packages/ 校验没过：\n" + "\n".join(problems))
    packages = B.flow_packages()
    names_seen = [d.name for d in packages]
    if len(names_seen) != len(set(names_seen)):
        failed += 1
        print(f"✗ 流程技能包枚举有重复：{names_seen}")
    # 写死：这些包的规范源在 backend/builtin，必须能被枚举、打包（不跟着 build.py 里的常量走，常量漏了这里要失败）。
    required = ("yz-ontology-director", "yz-sss-film-director", "yz-sss-drama-director", "yz-sss-film-script", "yz-sss-film-style", "yz-sss-drama-script", "yz-sss-drama-style")
    for pid in required:
        if pid not in names_seen or next(d for d in packages if d.name == pid) != B.BUILTIN / pid:
            failed += 1
            print(f"✗ {pid} 应从 backend/builtin 枚举到")
    before = {d.name: B.read_package_dir(d) for d in packages}
    with tempfile.TemporaryDirectory() as tmp:
        B.DIST = pathlib.Path(tmp)
        for d in packages:
            out = B.build_package(d.name)
            with zipfile.ZipFile(out) as z:
                names = set(z.namelist())
                digest = json.loads(z.read(rules["hashFile"]))
                payload = {n: z.read(n) for n in names if n != rules["hashFile"]}
            want = set(json.loads((d / "package.json").read_text(encoding="utf-8"))["files"]) | {rules["manifestFile"], rules["hashFile"]}
            if names != want or digest["contentHash"] != B.content_hash(payload):
                failed += 1
                print(f"✗ {d.name} 的 zip 内容或哈希不对：{sorted(names)}")
            source = B.read_package_dir(d)
            if any(payload[n] != source[n] for n in payload):
                failed += 1
                print(f"✗ {d.name} 的 zip 文件与规范源字节不一致")
    # 打包只读：规范源（含 backend/builtin 里的原样导入）一个字节都不变。
    if {d.name: B.read_package_dir(d) for d in packages} != before:
        failed += 1
        print("✗ 打包改动了规范源")
    if failed:
        sys.exit(1)
    print(f"✓ {len(cases)} 个样例与后端规则一致")


if __name__ == "__main__":
    main()
