"""把 yoozee 测试项目的 剧本.md 第 1 集转成章节正文（与七床 chapter.txt 同一写法）。

用法：python script_to_chapter.py <剧本.md> <输出 chapter.txt>
"""
import re
import sys

src, out = sys.argv[1], sys.argv[2]
text = open(src, encoding="utf-8").read().replace("\r\n", "\n")
scene_re = re.compile(r"^## (E01S\d+) (.+?)[　 ]+〔\w+〕\s*$")
beat_re = re.compile(r"^- (E01S\d+B\d+) 〔(\w+)〕(.+?)(?:\s+`[^`]*`)?(?:[　 ]+→.*)?$")
line_re = re.compile(r"^- \*\*(E01S\d+L\d+)\*\* ([^：]+)：“(.*)”(?:\s+`[^`]*`)?(?:[　 ]+→.*)?$")
blocks = []
current = None
for raw in text.split("\n"):
    m = scene_re.match(raw)
    if m:
        current = ["## " + m.group(2).strip(), ""]
        blocks.append(current)
        continue
    if raw.startswith("## "):
        current = None
        continue
    if current is None:
        continue
    m = line_re.match(raw)
    if m:
        current.append(f"{m.group(2).strip()}：{m.group(3).strip()}")
        continue
    m = beat_re.match(raw)
    if m:
        kind, body = m.group(2), m.group(3).strip()
        if kind == "screen_text":
            current.append(f"（画面文字：{body}）")
        elif kind == "sound":
            current.append(f"（音效：{body}）")
        else:
            current.append(f"（{body}）")
        continue
    if raw.startswith("- "):
        raise SystemExit(f"认不出的行：{raw}")
if not blocks:
    raise SystemExit("没有第 1 集的场")
open(out, "w", encoding="utf-8", newline="\n").write("\n\n".join("\n".join(b).rstrip() for b in blocks) + "\n")
