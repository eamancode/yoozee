#!/usr/bin/env python3
"""验证器（规则即代码）：对文本产物按 rules/*.json 逐条检查，error 阻断、warn 提示。
  python3 {{TOOLS}}/check.py brief    [需求.json]
  python3 {{TOOLS}}/check.py story    [故事/故事.json]
  python3 {{TOOLS}}/check.py script   [剧本/剧本.json]
  python3 {{TOOLS}}/check.py bible    [剧本/资产圣经.json] [--stage skeleton|assets|shots]
  python3 {{TOOLS}}/check.py shots    [剧本/镜头表.json]
  python3 {{TOOLS}}/check.py segments [剧本/分段.json]
  python3 {{TOOLS}}/check.py analysis 参考/拆解/xxx.json
  python3 {{TOOLS}}/check.py all                          项目里有什么查什么
选项：--json 机器可读输出；--evidence 把结果记成一条 evidence（生成/证据.jsonl）；--project 指定项目根。
有 error 时退出码 2。相关文件（剧本、资产圣经、需求）按项目默认路径自动读，读不到就跳过依赖它的规则并说明。
柚子后端的 check_*.go 实现同一组规则 id，两边共用 tests/fixtures/check-*.json。"""
import argparse
import json
import math
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import common as C  # noqa: E402

DEFAULTS = {
    "brief": "需求.json", "story": "故事/故事.json", "script": "剧本/剧本.json", "bible": "剧本/资产圣经.json",
    "shots": "剧本/镜头表.json", "segments": "剧本/分段.json",
}
SIZES = ["大远景", "远景", "全景", "中全景", "中景", "中近景", "近景", "特写", "大特写"]
Z_OF_SIZE = {"大远景": "Z9", "远景": "Z8", "全景": "Z7", "中全景": "Z6", "中景": "Z5", "中近景": "Z4", "近景": "Z3", "特写": "Z2", "大特写": "Z1"}
WIDE_SIZES = {"大远景", "远景", "全景", "中全景", "中景"}
CLOSE_SIZES = {"特写", "大特写"}
ACTION_VERBS = ["走", "转", "起", "坐", "递", "拿", "抬", "放", "推", "拉", "倒", "摸", "敲", "点", "摔", "靠", "退", "上前", "低头", "抬头", "伸手", "握", "松", "翻", "撑", "靠近", "后退", "整理", "踱", "跑", "跳", "甩", "挥", "踢", "打", "拔", "收", "扶", "蹲", "站", "躺", "扑", "拽", "捶", "拍", "指", "擦", "撩", "挠", "搓", "掀", "开", "关", "系", "解", "穿", "脱", "喝", "吃", "写", "按", "滑", "撞", "挪", "插", "攥", "捏", "扣", "叠", "揣", "绕", "掏", "塞", "拭", "抹", "揉", "搂", "托", "举", "蹭", "挡", "探", "缩", "蜷", "撑住", "拨"]
DEFAULT_SPACE_POLICY = {  # 小墨第 9 条，通用默认；档案 space_policy 可覆盖
    "S1": {"max_size": "中全景", "no_moves": ["crane_up", "crane_down", "jib"], "no_y": ["Y7"], "no_sizes": ["大远景", "远景", "全景"]},
    "S2": {"max_size": "全景", "no_moves": ["crane_up", "crane_down", "jib"], "no_y": ["Y7"], "no_sizes": ["大远景", "远景"]},
    "S3": {"max_size": "远景", "no_sizes": ["大远景"]},
    "S4": {},
    "O1": {},
    "O2": {"no_sizes": ["大远景"]},
    "X": {"max_size": "中全景", "no_moves": ["truck", "pull", "orbit", "spiral", "zoom", "jib", "crane_up", "crane_down"], "no_sizes": ["大远景", "远景", "全景"]},
}
MOVE_AXIS = {"push": "z", "pull": "z", "zoom": "z", "dolly_zoom": "z", "crane_up": "y", "crane_down": "y", "truck": "x", "track": "x", "pan": "rot", "tilt": "rot", "roll": "rot", "orbit": "x", "spiral": "xy", "handheld_micro": "", "handheld": "", "handheld_violent": "", "rack_focus": "", "fixed": "", "snorricam": "", "jib": "xy", "fpv": "xy", "one_take": ""}
HEAVY = {"dolly_zoom", "orbit", "spiral", "snorricam", "fpv", "jib", "crane_up", "crane_down", "roll"}
BGM_WORDS = ["配乐", "BGM", "背景音乐", "弦乐", "钢琴声", "鼓点", "音乐卡点", "紧张弦乐", "温暖钢琴", "史诗鼓点"]
PEOPLE_WORDS = ["人物", "角色", "书生", "女子", "老者", "侠客", "路人", "百姓", "修士", "弟子", "人影", "人形", "身影", "背影", "剪影", "有人", "一人", "一群人", "无面人", "NPC", "群演", "龙套", "站着", "坐着", "走过", "倚靠"]
GENERIC_EMOTION = ["惊讶", "悲伤", "愤怒", "紧张", "害怕", "震惊", "开心", "难过", "焦虑"]
REDLINE_PATTERNS = [r"意识到", r"领会到", r"内心涌起", r"心想", r"感到[一阵很十分]", r"（其实", r"\(其实"]


class Report:
    def __init__(self, kind):
        self.kind = kind
        self.items = []
        self.skipped = []

    def add(self, rule, message, level=None):
        r = RULES[self.kind].get(rule) if self.kind in RULES else None
        lvl = level or (r["level"] if r else "warn")
        self.items.append({"level": lvl, "rule": rule, "message": message})

    def skip(self, rule, why):
        self.skipped.append({"rule": rule, "why": why})

    @property
    def errors(self):
        return [i for i in self.items if i["level"] == "error"]

    def to_dict(self):
        return {"kind": self.kind, "errors": len(self.errors), "warnings": len(self.items) - len(self.errors), "items": self.items, "skipped": self.skipped}

    def print(self):
        for i in self.items:
            print(("✗ " if i["level"] == "error" else "⚠ ") + f"[{i['rule']}] {i['message']}")
        for s in self.skipped:
            print(f"· 跳过 {s['rule']}：{s['why']}")
        if not self.items:
            print(f"✓ {self.kind} 无问题")
        else:
            print(f"{self.kind}：{len(self.errors)} 个 error，{len(self.items) - len(self.errors)} 个 warn")


RULES = {}


def load_rules():
    for k in ("brief", "story", "script", "bible", "shots", "segments", "compile", "analysis"):
        try:
            RULES[k] = C.rules(k)
        except SystemExit:
            RULES[k] = {}


def load_optional(path):
    p = C.abspath(path)
    return C.read_json(p) if os.path.exists(p) else None


def script_lines(script):
    """{line_id: beat}（kind=line/vo）。"""
    out = {}
    for sc in script.get("scenes", []):
        for b in sc.get("beats", []):
            if b.get("kind") in ("line", "vo"):
                out[b["id"]] = b
    return out


def scene_map(script):
    return {sc["id"]: sc for sc in script.get("scenes", [])}


def est_seconds(shot, lines_by_id):
    """估时：台词按字速（常规 4.5 字/秒，喊叫 6，哽咽 / 犹豫 3，旁白 3.5）+ 动作最短时长 + 表情读秒。"""
    if shot.get("est"):
        return float(shot["est"])
    t = 0.0
    for ln in shot.get("lines", []):
        beat = lines_by_id.get(ln.get("line_id"))
        text = beat["text"] if beat else ""
        delivery = (ln.get("delivery") or "")
        rate = 4.5
        if any(w in delivery for w in ("喊", "吼", "急", "命令")):
            rate = 6.0
        elif any(w in delivery for w in ("哽咽", "犹豫", "撒娇", "告白", "哭")):
            rate = 3.0
        elif beat and beat.get("kind") == "vo":
            rate = 3.5
        n = len(re.sub(r"[\s，。！？、“”…—,.!?]", "", text))
        t += max(0.8, n / rate) + 0.3
    verbs = sum(1 for v in ACTION_VERBS if v in (shot.get("action") or ""))
    t += 1.0 + min(verbs, 4) * 0.6
    if shot.get("performance", {}).get("beats"):
        t += 0.5
    if shot.get("fight"):
        t = max(t, 1.5)
    return round(max(1.0, t), 1)


def has_action_verb(text):
    return any(v in (text or "") for v in ACTION_VERBS)


# ---------------------------------------------------------------- brief

def validate_contract(kind, data, R):
    """按 contracts/<kind>.schema.json 校验实例；缺 jsonschema 模块就跳过。违反记 R-SCHEMA（warn）。"""
    try:
        import jsonschema
    except Exception:
        R.skip("R-SCHEMA", "没有 jsonschema 模块")
        return
    name = {"shots": "shotlist", "segments": "segment"}.get(kind, kind)
    path = C.runtime_file("contracts", f"{name}.schema.json")
    if not path:
        return
    schema = json.loads(open(path, encoding="utf-8").read())
    inst = data if kind != "segments" else data
    v = jsonschema.Draft202012Validator(schema) if hasattr(jsonschema, "Draft202012Validator") else jsonschema.Draft7Validator(schema)
    items = inst if kind == "segments" else [inst]
    n = 0
    for it in items:
        for err in sorted(v.iter_errors(it), key=lambda e: list(e.path))[:20]:
            where = "/".join(str(x) for x in err.path) or "(根)"
            R.add("R-SCHEMA", f"{kind} 不符合契约：{where}：{err.message[:120]}", "warn")
            n += 1
            if n >= 20:
                return


def check_brief(brief, ctx):
    R = Report("brief")
    missing = [k for k in ("purpose", "platform", "duration", "taste", "budget", "acceptance") if not brief.get(k)]
    bad = [k for k in ("purpose", "platform", "duration", "taste", "budget", "acceptance") if brief.get(k) and brief[k].get("status") not in ("已确认", "默认，已授权")]
    if missing or bad:
        R.add("R-BRIEF-001", f"六项事实未问透：缺 {missing or '无'}；状态未确认 {bad or '无'}")
    ids = [m["id"] for m in C.models_json()["models"]]
    if brief.get("video_model") not in ids:
        R.add("R-BRIEF-002", f"video_model={brief.get('video_model')} 不在 models.json：{ids}")
    try:
        C.profile(brief.get("style_profile", ""))
    except SystemExit:
        R.add("R-BRIEF-003", f"style_profile={brief.get('style_profile')} 不存在")
    if brief.get("script_mode") == "adapt" and ctx.get("story") is not None and not ctx["story"].get("fact_ledger"):
        R.add("R-BRIEF-004", "改编模式但故事档没有 fact_ledger（事实账本）")
    if brief.get("duanju_mode") and ctx.get("story") is not None and ctx["story"].get("mode") != "duanju_fast":
        R.add("R-BRIEF-004", "需求里有速产参数，但故事档不是 duanju_fast 模式")
    for q in brief.get("questions", []):
        if q.get("blocking") and q.get("status") in ("待答", "", None):
            R.add("R-BRIEF-005", f"{q['id']} 挡开工且未答：{q['question']}")
    return R


# ---------------------------------------------------------------- story

def check_story(story, ctx):
    R = Report("story")
    mode = story.get("mode")
    st = story.get("structure") or {}
    if mode == "five_stage" and not st.get("slots"):
        R.add("R-STORY-001", "five_stage 模式没有结构对照表")
    for s in st.get("slots", []):
        if s.get("status") == "missing":
            R.add("R-STORY-001", f"功能位「{s['slot']}」缺位（❌）")
        if s.get("status") == "weak" and not s.get("reason"):
            R.add("R-STORY-002", f"功能位「{s['slot']}」偏弱但没写保留理由")
    if mode == "five_stage" and not (story.get("treatment") or {}).get("aftertaste"):
        R.add("R-STORY-003", "treatment.aftertaste（余味）为空")
    if mode == "duanju_fast":
        d = story.get("duanju") or {}
        miss = [k for k in ("hook", "cast_cards", "engines", "sections", "outline") if not d.get(k)]
        if miss:
            R.add("R-STORY-004", f"速产五项产出缺：{miss}")
        b = ctx.get("brief") or {}
        if (b.get("duanju_mode") or {}).get("paywall") and not d.get("paywalls"):
            R.add("R-STORY-004", "付费模式但没有卡点（paywalls）")
    for p in story.get("plants", []):
        if not p.get("payoff") and p.get("status") != "open":
            R.add("R-STORY-005", f"伏笔 {p['id']} 没有回收位，也没标 open")
    if story.get("grid25"):
        empties = [g["no"] for g in story["grid25"] if not g.get("task")]
        if empties:
            R.add("R-STORY-006", f"25 格里无任务的格：{empties}")
    if mode == "adapt":
        fl = story.get("fact_ledger")
        if not fl:
            R.add("R-STORY-007", "改编模式没有 fact_ledger")
        else:
            for fx in fl.get("fixes", []):
                if not fx.get("tag"):
                    R.add("R-STORY-007", f"修复项没有来源标记：{fx.get('problem')}")
                if re.search(r"新增(一句|对白|台词)", fx.get("fix", "")):
                    R.add("R-STORY-007", f"修复项用了新增对白：{fx.get('fix')}")
    return R


# ---------------------------------------------------------------- script

def check_script(script, ctx):
    R = Report("script")
    seen = {}
    brief = ctx.get("brief") or {}
    duanju = bool(brief.get("duanju_mode")) or (ctx.get("story") or {}).get("mode") == "duanju_fast"
    stext = {t["text"] for t in script.get("screen_text", [])}
    plants = {p["id"] for p in (ctx.get("story") or {}).get("plants", [])} | {p["id"] for p in (ctx.get("bible") or {}).get("plants", [])}
    for sc in script.get("scenes", []):
        if not sc.get("heading"):
            R.add("R-SCRIPT-004", f"{sc['id']} 没有 heading")
        if not sc.get("characters"):
            R.add("R-SCRIPT-004", f"{sc['id']} 没有 characters")
        if sc.get("kind") == "action":
            f = sc.get("fight") or {}
            if not f.get("phase_plan") or not f.get("sides"):
                R.add("R-SCRIPT-006", f"{sc['id']} 是动作场但缺 fight.phase_plan 或 fight.sides")
        has_jump = any(b.get("kind") == "time_jump" for b in sc.get("beats", []))
        if has_jump and not sc.get("time_jump"):
            R.add("R-SCRIPT-005", f"{sc['id']} 有时间跳跃节拍但没写 scene.time_jump")
        for b in sc.get("beats", []):
            bid = b["id"]
            if bid in seen:
                R.add("R-SCRIPT-001", f"节拍 id 重复：{bid}（{seen[bid]} 与 {sc['id']}）")
            seen[bid] = sc["id"]
            if not bid.startswith(sc["id"]):
                R.add("R-SCRIPT-001", f"节拍 {bid} 不在它的场 {sc['id']} 前缀下")
            if b.get("kind") in ("line", "vo"):
                if not b.get("speaker") or not b.get("text", "").strip():
                    R.add("R-SCRIPT-002", f"{bid} 台词缺 speaker 或 text")
                if duanju:
                    n = len(re.sub(r"[\s，。！？、“”…—,.!?]", "", b.get("text", "")))
                    if n > 15:
                        R.add("R-SCRIPT-007", f"{bid} 台词 {n} 字，速产单句应 ≤15 字")
                if re.search(r"我(其实)?是.{1,8}的(人|女儿|儿子|继承人|老板)", b.get("text", "")):
                    R.add("R-SCRIPT-003", f"{bid} 台词疑似解释设定：{b['text'][:30]}")
            if b.get("kind") == "action":
                for pat in REDLINE_PATTERNS:
                    if re.search(pat, b.get("text", "")):
                        R.add("R-SCRIPT-003", f"{bid} 动作里有不可拍的心理描写 / 括号暗示：{b['text'][:40]}")
                        break
            if b.get("kind") == "screen_text" and b.get("text") not in stext:
                R.add("R-SCRIPT-008", f"{bid} 画面文字未逐字登记到 script.screen_text：{b['text'][:30]}")
            if b.get("plant_id") and plants and b["plant_id"] not in plants:
                R.add("R-SCRIPT-009", f"{bid} 的伏笔 {b['plant_id']} 未在 story / bible 登记")
    return R


# ---------------------------------------------------------------- bible

def tier_requirements(prof, tier):
    return (prof.get("asset_tier_policy") or {}).get(tier, [])


def check_bible(bible, ctx, stage="skeleton"):
    R = Report("bible")
    prof = ctx.get("profile") or {}
    script = ctx.get("script")
    brief = ctx.get("brief") or {}
    for ch in bible.get("characters", []):
        if not (ch.get("evidence") or ch.get("derived") or ch.get("proposal")):
            R.add("R-BIBLE-002", f"人物「{ch['name']}」没有原文依据，也没有推导 / 设计建议标记")
        if ch.get("speaks") and not (ch.get("voice_profile") or {}).get("timbre"):
            R.add("R-BIBLE-011", f"说话角色「{ch['name']}」没有文字音色设定（voice_profile.timbre），台词四元的音色取不到")
        tier = ch.get("tier") or "support"
        for v in ch.get("variants", []):
            if v.get("kind") == "costume" and not v.get("trigger_beat"):
                R.add("R-BIBLE-003", f"「{ch['name']}」的服装变体「{v['label']}」没有剧本节拍证据")
            if re.search(r"湿|战损|血|脏|破损", v.get("label", "")) and v.get("kind") == "costume":
                R.add("R-BIBLE-003", f"「{ch['name']}」的「{v['label']}」是演出状态不是变体：归镜头 state")
            if stage == "assets":
                reqs = tier_requirements(prof, tier)
                crops = len(v.get("crops") or [])
                lock = len(v.get("lock_card") or [])
                anchors = len(ch.get("anchors") or [])
                for req in reqs:
                    if req.startswith("crops>=") and crops < int(req[7:]):
                        R.add("R-BIBLE-001", f"「{ch['name']}·{v['label']}」({tier}) 裁图 {crops} 张，少于 {req[7:]}")
                    if req.startswith("lock_card>=") and lock < int(req[11:]):
                        R.add("R-BIBLE-001", f"「{ch['name']}·{v['label']}」锁定卡 {lock} 条，少于 {req[11:]}")
                    if req.startswith("anchors>=") and anchors < int(req[9:]):
                        R.add("R-BIBLE-001", f"「{ch['name']}」身份锚点 {anchors} 个，少于 {req[9:]}")
                    if req == "voice_if_speaks" and ch.get("speaks") and not v.get("voice"):
                        R.add("R-BIBLE-001", f"「{ch['name']}·{v['label']}」说话但没有音色参考")
                    if req == "group_variation" and not ch.get("group_variation") and re.search(r"群|们|众|兵|卫|追兵|村民", ch["name"]):
                        R.add("R-BIBLE-010", f"群体角色「{ch['name']}」没有差异化锚点")
                if not v.get("checked"):
                    R.add("R-BIBLE-001", f"「{ch['name']}·{v['label']}」还没过门禁（checked）")
        if (prof.get("rules") or {}).get("group_variation_required") and re.search(r"群|们|众|追兵|村民|士兵|侍卫", ch["name"]) and not ch.get("group_variation"):
            R.add("R-BIBLE-010", f"群体角色「{ch['name']}」没有 group_variation")
    for pr in bible.get("props", []):
        if not pr.get("evidence"):
            R.add("R-BIBLE-002", f"道具「{pr['name']}」没有原文依据标记")
    for lo in bible.get("locations", []):
        if not lo.get("space_type"):
            R.add("R-BIBLE-004", f"场景「{lo['name']}」没有 space_type")
        texts = [lo.get("plain_view", ""), lo.get("fixtures", "")] + [v.get("desc", "") for v in lo.get("versions", [])]
        hits = sorted({w for t in texts for w in PEOPLE_WORDS if w in t})
        if hits:
            R.add("R-BIBLE-005", f"场景「{lo['name']}」描述含人词 {hits}：场景图是空舞台，重写")
        if not lo.get("light_source") or len(lo.get("anchors") or []) < 2:
            R.add("R-BIBLE-006", f"场景「{lo['name']}」缺 light_source 或锚点不足 2 个")
        if not lo.get("evidence"):
            R.add("R-BIBLE-002", f"场景「{lo['name']}」没有原文依据标记")
        if stage == "assets" and not lo.get("no_people_checked"):
            R.add("R-BIBLE-005", f"场景「{lo['name']}」未做禁人物自检（no_people_checked）")
    if script is not None:
        maps = {m["scene"]: m for m in bible.get("spatial_maps", [])}
        for sc in script.get("scenes", []):
            if len(sc.get("characters", [])) >= 2:
                m = maps.get(sc["id"])
                if not m:
                    R.add("R-BIBLE-007", f"{sc['id']} 有 {len(sc['characters'])} 人同场但没有空间图")
                    continue
                pos = m.get("positions", [])
                who = {p["who"] for p in pos}
                missing = [c for c in sc["characters"] if c not in who]
                if missing:
                    R.add("R-BIBLE-007", f"{sc['id']} 空间图缺人物位置：{missing}")
                if not any(p.get("meters") for p in pos) or not any(p.get("facing") for p in pos):
                    R.add("R-BIBLE-007", f"{sc['id']} 空间图没有米数或朝向")
                if stage == "shots" and brief.get("auth_mode") == "step" and not m.get("approved"):
                    R.add("R-BIBLE-008", f"{sc['id']} 空间图未经用户确认（每步确认档）")
    else:
        R.skip("R-BIBLE-007", "读不到剧本，无法判断哪些场两人以上")
    for coll, key in (("characters", None), ("locations", None), ("props", None)):
        for item in bible.get(coll, []):
            files = []
            if coll == "characters":
                for v in item.get("variants", []):
                    files += [v.get("sheet"), v.get("voice"), v.get("voice_short")] + list(v.get("crops") or [])
            elif coll == "locations":
                files += [item.get("plate"), item.get("side_a"), item.get("side_b")] + list((item.get("views") or {}).values())
            else:
                files += [item.get("image")] + [s.get("image") for s in item.get("states", [])]
            for f in files:
                if f and not f.startswith("asset:") and not os.path.exists(C.abspath(f)):
                    R.add("R-BIBLE-009", f"「{item['name']}」指向的文件不存在：{f}")
    return R


# ---------------------------------------------------------------- shots

def cam_x(sh):
    return sh.get("cam", {}).get("x")


def cam_y(sh):
    return sh.get("cam", {}).get("y")


def is_normal(sh):
    f = sh.get("cam", {}).get("f") or {}
    moves = set(sh.get("move", []))
    return cam_y(sh) in ("Y3", "Y4", "Y5") and cam_x(sh) in ("X1",) and not f.get("geometry") == "dutch" and moves <= {"fixed", "push", "pull", "truck", "pan", "tilt", "track"} and not (moves & {"handheld", "handheld_violent"})


def check_shots(shotlist, ctx):
    R = Report("shots")
    prof = ctx.get("profile") or {}
    rules_p = prof.get("rules") or {}
    quotas = prof.get("quotas") or {}
    script = ctx.get("script")
    bible = ctx.get("bible") or {}
    shots = shotlist.get("shots", [])
    lines_by_id = script_lines(script) if script else {}
    scenes = scene_map(script) if script else {}
    # 镜头表写了 episode 时只对那一集的台词与伏笔算覆盖（多集项目一集一张镜头表）
    ep = shotlist.get("episode")
    ep_prefix = f"E{int(ep):02d}" if isinstance(ep, int) else None
    if ep_prefix:
        lines_by_id = {k: v for k, v in lines_by_id.items() if k.startswith(ep_prefix)}
    # 覆盖
    if script is not None:
        used = {}
        for sh in shots:
            for ln in sh.get("lines", []):
                used.setdefault(ln.get("line_id"), []).append(sh["id"])
        for lid in lines_by_id:
            n = len(used.get(lid, []))
            if n == 0:
                R.add("R-SHOT-001", f"台词 {lid} 没有落在任何镜头：「{lines_by_id[lid]['text'][:20]}」")
            elif n > 1:
                R.add("R-SHOT-001", f"台词 {lid} 落在 {n} 个镜头：{used[lid]}")
        for lid, where in used.items():
            if lid not in lines_by_id:
                R.add("R-SHOT-001", f"镜头 {where} 引用了剧本里不存在的台词 id {lid}")
    else:
        R.skip("R-SHOT-001", "读不到剧本")
    # 逐镜
    max_axes = rules_p.get("max_move_axes", 2)
    vfx_ids = load_vfx_ids(prof)
    prev = None
    same_move_run = same_size_run = 1
    arcs_by_id = C.vocab_by_id("kb-performance", "vocab/emotion-arcs.jsonl")
    martial_ids = set(C.vocab_by_id("kb-action", "vocab/martial.jsonl"))
    acts_by_id = C.vocab_by_id("kb-performance", "vocab/action-patterns.jsonl")
    shots_by_id = {x["id"]: x for x in shots}
    for sh in shots:
        cam = sh.get("cam", {})
        if Z_OF_SIZE.get(sh.get("size")) and cam.get("z") and Z_OF_SIZE[sh["size"]] != cam["z"]:
            R.add("R-SHOT-002", f"{sh['id']} 景别「{sh['size']}」与 cam.z={cam['z']} 不一致（应为 {Z_OF_SIZE[sh['size']]}）")
        moves = sh.get("move", [])
        axes = {MOVE_AXIS.get(m, m) for m in moves if MOVE_AXIS.get(m, m)}
        n_axes = sum(len(a) if a in ("xy", "xyz") else 1 for a in axes if a != "rot") + (1 if "rot" in axes else 0)
        if n_axes > max_axes:
            R.add("R-SHOT-003", f"{sh['id']} 运镜 {moves} 涉及 {n_axes} 轴，超过档案上限 {max_axes}")
        if "fixed" in moves and len(moves) > 1:
            R.add("R-SHOT-003", f"{sh['id']} fixed 不能与其他运镜同列：{moves}")
        if rules_p.get("no_rotation_at_z1_z3", True) and cam.get("z") in ("Z1", "Z2", "Z3") and ({"orbit", "spiral", "roll"} & set(moves)):
            R.add("R-SHOT-004", f"{sh['id']} 近景（{cam['z']}）做大旋转 {moves}，容易崩脸")
        if {"push", "pull"} <= set(moves):
            R.add("R-SHOT-005", f"{sh['id']} 推进与拉远同时出现")
        if rules_p.get("one_take_allowed") is False and "one_take" in moves:
            R.add("R-SHOT-016", f"{sh['id']} 档案禁一镜到底，改多镜切换或微运镜")
        blob = " ".join(str(sh.get(k, "")) for k in ("sfx", "voice", "action"))
        hits = [w for w in BGM_WORDS if w in blob]
        if hits:
            R.add("R-SHOT-017", f"{sh['id']} 出现配乐词 {hits}")
        if re.search(r"招牌|字幕|写着|纸条上|屏幕上的字|文字", sh.get("action", "")) and script is not None and not script.get("screen_text"):
            R.add("R-SHOT-018", f"{sh['id']} 画面可能出现可读文字，但剧本没有登记 screen_text")
        sc = scenes.get(sh.get("scene")) if scenes else None
        est = est_seconds(sh, lines_by_id)
        if sh.get("lines") and est > rules_p.get("standing_talk_max_sec", 3) and not has_action_verb(sh.get("action")):
            R.add("R-SHOT-010", f"{sh['id']} 含台词、估时 {est} 秒却没有可见动作：加动作或切镜")
        indoor = False
        if sc and "内" in (sc.get("heading") or "")[:3]:
            indoor = True
        loc = next((l for l in bible.get("locations", []) if sc and l["name"] == sc.get("location_ref")), None) if sc else None
        if loc and str(loc.get("space_type", "")).startswith("S"):
            indoor = True
        if indoor and rules_p.get("indoor_light_anchor_required", True) and not (sh.get("light") or {}).get("source_anchor"):
            R.add("R-SHOT-011", f"{sh['id']} 室内镜头没有光源锚点")
        if prof.get("light_policy") == "practicals_only" and (sh.get("light") or {}).get("policy") == "stylized":
            R.add("R-SHOT-011", f"{sh['id']} 档案只许实景光，但 light.policy=stylized")
        if sc and sh.get("blocking"):
            smap = next((m for m in bible.get("spatial_maps", []) if m["scene"] == sc["id"]), None)
            anchors = set()
            if smap:
                anchors |= {p.get("at") for p in smap.get("positions", [])}
            if loc:
                anchors |= set(loc.get("anchors") or [])
            if anchors:
                bad = [b.get("at") for b in sh["blocking"] if b.get("at") not in anchors]
                if bad:
                    R.add("R-SHOT-012", f"{sh['id']} blocking 用了空间图里没有的锚点：{bad}")
        if sc and sh.get("subject") == "person" and sh.get("size") in WIDE_SIZES and len(sc.get("characters", [])) >= 2:
            who = {b.get("who") for b in sh.get("blocking", [])}
            missing = [c for c in sc["characters"] if c not in who]
            if missing:
                R.add("R-SHOT-013", f"{sh['id']} A 档景别没写全在场人物位置：{missing}")
        if prev and prev.get("scene") == sh.get("scene"):
            if prev.get("subject") == "person" and sh.get("subject") == "person" and prev.get("focus") == sh.get("focus") and prev.get("end_state") and sh.get("start_state") and prev["end_state"].strip() != sh["start_state"].strip() and not re.search(r"(从|由).{0,12}(变|改|起身|转身|走到|移到)", sh.get("action", "")):
                R.add("R-SHOT-014", f"{sh['id']} start_state 与上一镜 end_state 不一致，且 action 没写出变化")
            same_subject = sh.get("focus") == prev.get("focus") or (not sh.get("focus") and not prev.get("focus"))
            if same_subject and sh.get("size") in SIZES and prev.get("size") in SIZES:
                gap = abs(SIZES.index(sh["size"]) - SIZES.index(prev["size"]))
                angle_changed = cam_x(sh) != cam_x(prev) or cam_y(sh) != cam_y(prev)
                if gap < rules_p.get("adjacent_size_min_gap", 2) and not angle_changed and sh.get("subject") == prev.get("subject") == "person":
                    R.add("R-SHOT-007", f"{sh['id']} 与上一镜同主体、景别只差 {gap} 级且机位未变：跳切风险")
            same_move_run = same_move_run + 1 if sorted(sh.get("move", [])) == sorted(prev.get("move", [])) else 1
            same_size_run = same_size_run + 1 if sh.get("size") == prev.get("size") else 1
            if same_move_run > quotas.get("consecutive_same_move_max", 99) and not (set(sh.get("move", [])) == {"handheld_violent"}):
                R.add("R-SHOT-008", f"{sh['id']} 连续 {same_move_run} 镜同一运镜 {sh.get('move')}")
            if same_size_run > quotas.get("consecutive_same_size_max", 99):
                R.add("R-SHOT-009", f"{sh['id']} 连续 {same_size_run} 镜同一景别 {sh.get('size')}")
        else:
            same_move_run = same_size_run = 1
            first_person = next((x for x in shots if x.get("scene") == sh.get("scene") and x.get("subject") == "person"), None)
            if sc and sc.get("kind", "dialogue") == "dialogue" and rules_p.get("dialogue_open_size") and first_person is sh and sh.get("size") not in rules_p["dialogue_open_size"]:
                R.add("R-SHOT-021", f"{sh['id']} 对话场第一个人物镜景别「{sh.get('size')}」不在档案允许的 {rules_p['dialogue_open_size']}（先交代谁在场）")
        if sc and sc.get("kind") == "action":
            f = sh.get("fight")
            if not f or f.get("contact") not in ("hit", "graze", "blocked", "miss", "none"):
                R.add("R-SHOT-019", f"{sh['id']} 动作场镜头缺因果动作单元或接触判定")
        for v in sh.get("vfx", []):
            if vfx_ids and v["id"] not in vfx_ids:
                R.add("R-SHOT-020", f"{sh['id']} 特效 {v['id']} 不在词库里")
        if prof.get("camera_follows_emotion") and sh.get("subject") == "person" and sh.get("performance") and not sh.get("camera_emotion"):
            R.add("R-SHOT-022", f"{sh['id']} 有表演但没写 camera_emotion（档案要求摄影机跟情绪）")
        if loc:
            pol = (prof.get("space_policy") or DEFAULT_SPACE_POLICY).get(loc.get("space_type")) or {}
            if pol.get("max_size") and sh.get("size") in SIZES and SIZES.index(sh["size"]) < SIZES.index(pol["max_size"]):
                R.add("R-SHOT-023", f"{sh['id']} 场景 {loc['space_type']} 最大景别 {pol['max_size']}，用了 {sh['size']}")
            if pol.get("no") and (set(pol["no"]) & set(moves)):
                R.add("R-SHOT-023", f"{sh['id']} 场景 {loc['space_type']} 禁用运镜 {sorted(set(pol['no']) & set(moves))}")
            if pol.get("no_birdseye") and cam.get("y") == "Y7":
                R.add("R-SHOT-023", f"{sh['id']} 室内普通层高禁鸟瞰（Y7）")
        if prof.get("performance_grain") == "micro" and sh.get("performance") is not None and not sh["performance"].get("beats"):
            R.add("R-SHOT-024", f"{sh['id']} 档案要求微表演节拍，performance.beats 为空")
        ge = [w for w in GENERIC_EMOTION if w in (sh.get("action") or "")]
        if ge and not (sh.get("performance") or {}).get("beats"):
            R.add("R-SHOT-024", f"{sh['id']} action 里有泛情绪词 {ge} 而无具体可见细节")
        # 2.1：情绪弧 / 动作模式 / 衔接 / 台词三元 / 灯光七项
        f_ = sh.get("fight") or {}
        if f_.get("martial_ids"):
            bad_m = [i for i in f_["martial_ids"] if i not in martial_ids]
            if bad_m:
                R.add("R-SHOT-020", f"{sh['id']} fight.martial_ids 里 {bad_m} 不在武学词库")
        arc = sh.get("emotion_arc")
        if arc:
            a = arcs_by_id.get(arc.get("id"))
            if not a:
                R.add("R-SHOT-025", f"{sh['id']} 情绪弧 {arc.get('id')} 不在词库里")
            else:
                n = len(a.get("phases") or [])
                pf, pt = arc.get("phase_from"), arc.get("phase_to")
                if (pf and pf > n) or (pt and pt > n) or (pf and pt and pf > pt):
                    R.add("R-SHOT-025", f"{sh['id']} 情绪弧 {arc['id']} 只有 {n} 段，phase_from/to={pf}/{pt} 不合法")
            if arc.get("continues"):
                src = shots_by_id.get(arc["continues"])
                order = [x["id"] for x in shots]
                if not src or src.get("scene") != sh.get("scene") or src.get("focus") != sh.get("focus") or not src.get("emotion_arc") or order.index(src["id"]) >= order.index(sh["id"]):
                    R.add("R-SHOT-025", f"{sh['id']} continues={arc['continues']} 不是同场同 focus 且带弧的镜头")
        elif prof.get("emotion_arc_required") and sh.get("subject") == "person" and (sh.get("lines") or sh.get("performance")) and not (sh.get("performance") or {}).get("beats"):
            R.add("R-SHOT-026", f"{sh['id']} 含台词或表演但没有 emotion_arc，也没有手写 performance.beats")
        ap = sh.get("action_pattern")
        if ap:
            pat = acts_by_id.get(ap.get("id"))
            if not pat:
                R.add("R-SHOT-028", f"{sh['id']} 动作模式 {ap.get('id')} 不在词库里")
            elif pat.get("two_person") and len(sh.get("blocking") or []) < 2:
                R.add("R-SHOT-028", f"{sh['id']} 双人动作模式 {ap['id']} 用在只有 {len(sh.get('blocking') or [])} 人站位的镜头")
        tr = sh.get("transition") or {}
        if tr.get("in") and prev and prev.get("scene") == sh.get("scene"):
            same_subject = sh.get("focus") == prev.get("focus")
            gap = abs(SIZES.index(sh["size"]) - SIZES.index(prev["size"])) if sh.get("size") in SIZES and prev.get("size") in SIZES else 9
            angle_changed = cam_x(sh) != cam_x(prev) or cam_y(sh) != cam_y(prev)
            if "硬切" in tr["in"] and same_subject and gap < 2 and not angle_changed:
                R.add("R-SHOT-027", f"{sh['id']} 衔接写了硬切，但与上一镜同主体、景别只差 {gap} 级且机位未变，是跳切")
            if "动作匹配" in tr["in"] and not has_action_verb(prev.get("action") or ""):
                R.add("R-SHOT-027", f"{sh['id']} 衔接写了动作匹配切，但上一镜 action 里没有动作动词可接")
        for ln in sh.get("lines", []) or []:
            trip = [bool(ln.get(k)) for k in ("pace", "tone", "emotion")]
            if any(trip) and not all(trip):
                R.add("R-SHOT-029", f"{sh['id']} 台词 {ln.get('line_id')} 的三元（语速 / 语调 / 情感）没写齐，编译会退回 delivery")
        if prof.get("light_policy") == "practicals_only":
            lt = sh.get("light") or {}
            bad = [k for k in ("fill", "rim", "key") if any(w in re.sub(r"(无|没有|不|不加|不用)(额外)?(补光|反光板|柔光箱|灯板)", "", lt.get(k) or "") for w in ("补光", "反光板", "柔光箱", "霓虹补", "LED 板", "LED板", "灯板"))]
            if bad:
                R.add("R-SHOT-030", f"{sh['id']} 实景光档案下 light.{'/'.join(bad)} 写了补光设备")
        prev = sh
    # 伏笔
    if bible.get("plants"):
        for p in bible["plants"]:
            if ep_prefix and not str(p.get("scene", "")).startswith(ep_prefix):
                continue
            if p.get("scene") and not any(sh.get("plant_id") == p["id"] for sh in shots if sh.get("scene") == p["scene"]):
                R.add("R-SHOT-015", f"伏笔 {p['id']}（{p['what']}）在 {p['scene']} 没有镜头承担")
    # 配额
    n = len(shots) or 1
    def ratio(pred):
        return sum(1 for sh in shots if pred(sh)) / n
    checks = []
    if "normal_max" in quotas:
        checks.append(("常规镜头占比", ratio(is_normal), None, quotas["normal_max"]))
    if "side45_min" in quotas or "side45_max" in quotas:
        checks.append(("侧 45 占比", ratio(lambda s: cam_x(s) == "X2"), quotas.get("side45_min"), quotas.get("side45_max")))
    if "side90_max" in quotas:
        checks.append(("侧面占比", ratio(lambda s: cam_x(s) == "X3"), None, quotas["side90_max"]))
    if "fixed_max" in quotas:
        checks.append(("固定镜头占比", ratio(lambda s: s.get("move") == ["fixed"]), None, quotas["fixed_max"]))
    if "closeup_max" in quotas:
        checks.append(("人物特写占比", ratio(lambda s: s.get("size") in CLOSE_SIZES and s.get("subject") == "person"), None, quotas["closeup_max"]))
    if "non_person_min" in quotas or "non_person_max" in quotas:
        checks.append(("非人物镜头占比", ratio(lambda s: s.get("subject") == "non_person"), quotas.get("non_person_min"), quotas.get("non_person_max")))
    for name, val, lo, hi in checks:
        if hi is not None and val > hi + 1e-9:
            R.add("R-SHOT-006", f"{name} {val:.0%} 超过档案上限 {hi:.0%}", "warn")
        if lo is not None and val < lo - 1e-9 and n >= 8:
            R.add("R-SHOT-006", f"{name} {val:.0%} 低于档案下限 {lo:.0%}", "warn")
    counts = {
        "dutch_max": sum(1 for s in shots if (s.get("cam", {}).get("f") or {}).get("geometry") == "dutch"),
        "extreme_low_max": sum(1 for s in shots if cam_y(s) == "Y1"),
        "extreme_high_max": sum(1 for s in shots if cam_y(s) == "Y7"),
    }
    runs, last = 0, False
    for s in shots:
        cur = "handheld_violent" in s.get("move", [])
        if cur and not last:
            runs += 1
        last = cur
    counts["shake_violent_max"] = runs
    labels = {"dutch_max": "荷兰角", "extreme_low_max": "大仰角 / 虫视", "extreme_high_max": "大俯角 / 鸟瞰", "shake_violent_max": "手持剧烈晃动段落"}
    for k, v in counts.items():
        if k in quotas and v > quotas[k]:
            R.add("R-SHOT-006", f"{labels[k]} 用了 {v} 次，档案上限 {quotas[k]}", "error")
    heavy = quotas.get("heavy_moves") or {}
    for mv, cap in heavy.items():
        if mv == "fisheye":
            used = sum(1 for s in shots if (s.get("cam", {}).get("f") or {}).get("distortion") == "fisheye")
        elif mv == "pov":
            used = sum(1 for s in shots if (s.get("cam", {}).get("f") or {}).get("identity") == "pov")
        else:
            used = sum(1 for s in shots if mv in s.get("move", []))
        if used > cap:
            R.add("R-SHOT-006", f"重技法 {mv} 用了 {used} 次，档案上限 {cap}", "error")
    if "min_angles" in quotas and n >= 12:
        angles = {(cam_x(s), cam_y(s)) for s in shots}
        if len(angles) < quotas["min_angles"]:
            R.add("R-SHOT-006", f"机位种类 {len(angles)}，少于档案要求 {quotas['min_angles']}", "warn")
    if "min_moves" in quotas and n >= 12:
        mv = {m for s in shots for m in s.get("move", [])}
        if len(mv) < quotas["min_moves"]:
            R.add("R-SHOT-006", f"运镜种类 {len(mv)}，少于档案要求 {quotas['min_moves']}", "warn")
    if "group_wide_min" in quotas and script is not None:
        for sc in script.get("scenes", []):
            if len(sc.get("characters", [])) >= 3:
                mine = [s for s in shots if s.get("scene") == sc["id"]]
                if mine:
                    wide = sum(1 for s in mine if s.get("size") in WIDE_SIZES and len(s.get("blocking", [])) >= 2) / len(mine)
                    if wide < quotas["group_wide_min"]:
                        R.add("R-SHOT-006", f"{sc['id']} 三人以上场 A 档群像占比 {wide:.0%}，低于 {quotas['group_wide_min']:.0%}", "warn")
    return R


def load_vfx_ids(prof):
    ids = set()
    for pkg, rel in (("kb-action", "vocab/vfx.jsonl"),):
        p = C.kb_file(pkg, *rel.split("/"))
        if p:
            ids |= {json.loads(l)["id"] for l in open(p, encoding="utf-8") if l.strip()}
    pack = (prof.get("vfx_policy") or {}).get("ip_pack")
    if pack:
        p = C.kb_file(*pack.split("/"))
        if p:
            ids |= {json.loads(l)["id"] for l in open(p, encoding="utf-8") if l.strip()}
    return ids


# ---------------------------------------------------------------- segments

def check_segments(segs, ctx):
    R = Report("segments")
    m = ctx.get("model") or C.model(C.project_model())
    shotlist = ctx.get("shots")
    script = ctx.get("script")
    brief = ctx.get("brief") or {}
    lo, hi = m["duration"]["min"], m["duration"]["max"]
    shots_by_id = {s["id"]: s for s in (shotlist or {}).get("shots", [])}
    order = [s["id"] for s in (shotlist or {}).get("shots", [])]
    covered = []
    scenes = scene_map(script) if script else {}
    by_scene = {}
    for seg in segs:
        dur = float(seg.get("dur", 0))
        if dur > hi:
            R.add("R-SEG-001", f"{seg['id']} 时长 {dur} 秒超过 {m['label']} 上限 {hi} 秒")
        elif dur < lo:
            R.add("R-SEG-001", f"{seg['id']} 时长 {dur} 秒低于下限 {lo} 秒（按 {lo} 秒出再剪）", "warn")
        ids = seg.get("shot_ids") or []
        if shotlist is not None:
            sc_ids = {shots_by_id[i]["scene"] for i in ids if i in shots_by_id}
            if len(sc_ids) > 1:
                R.add("R-SEG-002", f"{seg['id']} 跨场：{sorted(sc_ids)}")
            idx = [order.index(i) for i in ids if i in order]
            if idx and idx != list(range(idx[0], idx[0] + len(idx))):
                R.add("R-SEG-003", f"{seg['id']} 的 shot_ids 在镜头表里不连续")
            unknown = [i for i in ids if i not in shots_by_id]
            if unknown:
                R.add("R-SEG-003", f"{seg['id']} 引用了镜头表里没有的镜头 {unknown}")
            covered += ids
        refs = seg.get("refs", [])
        imgs = [r for r in refs if r.get("role") != "motion"]
        vids = [r for r in refs if r.get("role") == "motion"]
        auds = seg.get("voices", [])
        lim = m.get("refs", {})
        for name, have, cap in (("参考图", len(imgs), lim.get("images")), ("参考视频", len(vids), lim.get("videos")), ("参考音频", len(auds), lim.get("audios"))):
            if cap is not None and have > cap:
                R.add("R-SEG-005", f"{seg['id']} {name} {have} 个，超过上限 {cap}")
        total_cap = m.get("files_total") or lim.get("total")
        if total_cap is not None and len(imgs) + len(vids) + len(auds) > total_cap:
            R.add("R-SEG-005", f"{seg['id']} 参考合计 {len(imgs) + len(vids) + len(auds)} 个，超过上限 {total_cap}")
        if seg.get("mode") == "lock_first_frame" and (refs or auds):
            ex = m.get("frames_exclusive_with_refs")
            R.add("R-SEG-006", f"{seg['id']} 锁首帧又挂了参考（{m['label']} 互斥{'已知' if ex else '未校准'}）", "error" if ex else "warn")
        sc = scenes.get(seg.get("scene")) or next((v for v in scenes.values() if v.get("heading") == seg.get("scene")), None)
        if sc and sc.get("time_jump") and not seg.get("story_span"):
            R.add("R-SEG-007", f"{seg['id']} 所在场有时间跳跃但没写 story_span")
        tags = {}
        for h in seg.get("handles", []):
            kind = re.sub(r"\d+$", "", h["tag"])
            tags.setdefault(kind, []).append(int(re.sub(r"\D", "", h["tag"]) or 0))
        for kind, nums in tags.items():
            if sorted(nums) != list(range(1, len(nums) + 1)):
                R.add("R-SEG-009", f"{seg['id']} 句柄 {kind} 编号不连续：{nums}")
        by_scene.setdefault(seg.get("scene"), []).append(seg)
    if shotlist is not None:
        missing = [i for i in order if i not in covered]
        dup = sorted({i for i in covered if covered.count(i) > 1})
        if missing:
            R.add("R-SEG-003", f"镜头未被任何段覆盖：{missing}")
        if dup:
            R.add("R-SEG-003", f"镜头被多段重复覆盖：{dup}")
    for scene, group in by_scene.items():
        if len(group) > 1:
            total = sum(float(s.get("dur", 0)) for s in group)
            if total <= hi:
                R.add("R-SEG-011", f"场「{scene}」合计 {total} 秒可装一段，却拆成 {len(group)} 段")
            for prev, cur in zip(group, group[1:]):
                br = cur.get("continuity_bridge")
                if not br:
                    R.add("R-SEG-010", f"{cur['id']} 是同场后段但没有一致性桥")
                    continue
                if br.get("prev_segment") != prev["id"]:
                    R.add("R-SEG-012", f"{cur['id']} 桥指向 {br.get('prev_segment')}，前段其实是 {prev['id']}")
                prev_files = [r["file"] for r in prev.get("refs", []) if r.get("role") != "motion"]
                if br.get("shared_refs") != prev_files:
                    R.add("R-SEG-012", f"{cur['id']} 桥的 shared_refs 与前段参考不同或顺序不同")
                prev_end = (prev.get("shots") or [{}])[-1].get("end_state") or prev.get("rules") or ""
                if br.get("handoff_state", "").strip() != prev_end.strip():
                    R.add("R-SEG-012", f"{cur['id']} 桥的 handoff_state 与前段末镜 end_state 不同")
                if shotlist is not None and prev.get("shot_ids") and cur.get("shot_ids"):
                    a, b = shots_by_id.get(prev["shot_ids"][-1]), shots_by_id.get(cur["shot_ids"][0])
                    if a and b and a.get("lines") and b.get("lines") and not has_action_verb(a.get("action")):
                        R.add("R-SEG-004", f"{prev['id']}→{cur['id']} 切在两句接话之间")
    # 目标片长：多集项目按本批段所属的集读 episodes[].target_seconds；否则读 duration.value 里的「数字 + 单位」。
    # 75% 是「分段表按片长的 75% 写、剪辑再压」的生成余量；按单集 target_seconds 比。
    secs = None
    eps = {str(e.get("no")): e.get("target_seconds") for e in (brief or {}).get("episodes", []) if e.get("target_seconds")}
    ep_nos = {m.group(1) for s in segs for m in [re.match(r"E(\d{2})", str(s.get("scene", "")))] if m}
    if eps and ep_nos:
        secs = sum(float(eps[str(int(n))]) for n in ep_nos if str(int(n)) in eps) or None
    if secs is None:
        target = brief.get("duration", {}).get("value") if brief else None
        mt = re.search(r"(\d+(?:\.\d+)?)\s*(分钟|分|秒|s|min)", str(target)) if target else None
        if mt:
            secs = float(mt.group(1)) * (60 if mt.group(2) in ("分钟", "分", "min") else 1)
    if secs:
        total = sum(float(s.get("dur", 0)) for s in segs)
        if total > secs * 1.1 + 1e-6:
            R.add("R-SEG-008", f"分段合计 {total:.0f} 秒，超过目标片长 {secs:.0f} 秒的 10% 余量（{secs * 1.1:.0f} 秒）：剪辑会压不下去，并镜或删镜")
        elif total < secs * 0.6 - 1e-6:
            R.add("R-SEG-008", f"分段合计 {total:.0f} 秒，不足目标片长 {secs:.0f} 秒的 60%：戏不够，补镜头或场")
    return R


# ---------------------------------------------------------------- analysis

def check_analysis(an, ctx):
    R = Report("analysis")
    ph = an.get("phases", [])
    dur = (an.get("source") or {}).get("duration")
    if ph:
        if abs(ph[0]["from"]) > 0.11:
            R.add("R-ANALYSIS-001", f"首段从 {ph[0]['from']} 秒开始，不是 0")
        for a, b in zip(ph, ph[1:]):
            if abs(a["to"] - b["from"]) > 0.11:
                R.add("R-ANALYSIS-001", f"{a['to']}→{b['from']} 之间有时间空洞或重叠")
        if dur and abs(ph[-1]["to"] - dur) > 0.11:
            R.add("R-ANALYSIS-001", f"末段到 {ph[-1]['to']} 秒，视频总长 {dur} 秒")
    hits = [p for p in ph if p.get("contact") == "hit"]
    ka = an.get("key_actions", [])
    for k in ka:
        if len(k.get("contact_evidence") or []) < 2 and k.get("confidence") == "high":
            R.add("R-ANALYSIS-002", f"关键动作「{k['name']}」证据不足两项却标 high")
    if hits and not ka:
        R.add("R-ANALYSIS-002", f"有 {len(hits)} 个命中阶段但没有逐帧关键动作记录")
    if (an.get("source") or {}).get("has_cuts") and not any(c.get("cut") for c in an.get("camera", [])):
        R.add("R-ANALYSIS-003", "标了有切镜但 camera 里没有切镜时间")
    for a in an.get("actors", []):
        if re.search(r"左边|右边|左侧|右侧", a.get("desc", "")) and len(a.get("desc", "")) < 12:
            R.add("R-ANALYSIS-004", f"{a['label']} 的身份标签只写了位置：{a['desc']}")
    if (an.get("source") or {}).get("limits") and not (an.get("source") or {}).get("precision"):
        R.add("R-ANALYSIS-005", "写了能力限制但没写分析精度")
    return R


# ---------------------------------------------------------------- main

def context(a):
    ctx = {"brief": load_optional(a.brief), "story": load_optional(a.story), "script": load_optional(a.script),
           "bible": load_optional(a.bible), "shots": load_optional(a.shots)}
    try:
        ctx["profile"] = C.profile(a.profile or (ctx["brief"] or {}).get("style_profile") or C.project_profile())
    except SystemExit:
        ctx["profile"] = {}
    try:
        ctx["model"] = C.model(a.model or (ctx["brief"] or {}).get("video_model") or C.project_model())
    except SystemExit:
        ctx["model"] = None
    return ctx


def write_evidence(report, a):
    path = C.ROOT / "生成" / "证据.jsonl"
    path.parent.mkdir(parents=True, exist_ok=True)
    rec = {"segment": report.kind, "kind": "check", "passed": not report.errors,
           "metrics": {"errors": len(report.errors), "warnings": len(report.items) - len(report.errors), "rules": sorted({i['rule'] for i in report.items})},
           "note": "; ".join(i["message"] for i in report.errors[:5]), "at": __import__("datetime").datetime.now().isoformat(timespec="seconds")}
    with open(path, "a", encoding="utf-8") as f:
        f.write(json.dumps(rec, ensure_ascii=False) + "\n")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("kind", choices=["brief", "story", "script", "bible", "shots", "segments", "analysis", "all"])
    ap.add_argument("path", nargs="?")
    ap.add_argument("--project", help="项目根目录（默认按运行时位置推断）")
    ap.add_argument("--stage", default="skeleton", choices=["skeleton", "assets", "shots"], help="bible：骨架 / 资产齐 / 进镜头表 三个阶段的门禁")
    ap.add_argument("--profile")
    ap.add_argument("--model")
    ap.add_argument("--brief", default=DEFAULTS["brief"])
    ap.add_argument("--story", default=DEFAULTS["story"])
    ap.add_argument("--script", default=DEFAULTS["script"])
    ap.add_argument("--bible", default=DEFAULTS["bible"])
    ap.add_argument("--shots", default=DEFAULTS["shots"])
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--evidence", action="store_true")
    a = ap.parse_args()
    if a.project:
        import pathlib
        C.ROOT = pathlib.Path(a.project).resolve()
    load_rules()
    ctx = context(a)
    kinds = ["brief", "story", "script", "bible", "shots", "segments"] if a.kind == "all" else [a.kind]
    reports = []
    for kind in kinds:
        path = a.path if a.kind != "all" and a.path else DEFAULTS.get(kind)
        if kind == "analysis":
            if not a.path:
                sys.exit("analysis 要给文件路径")
            data = C.read_json(C.abspath(a.path))
        else:
            ap_ = C.abspath(path)
            if not os.path.exists(ap_):
                if a.kind == "all":
                    continue
                sys.exit(f"找不到 {path}")
            data = C.read_json(ap_)
        fn = {"brief": check_brief, "story": check_story, "script": check_script, "bible": lambda d, c: check_bible(d, c, a.stage),
              "shots": check_shots, "segments": check_segments, "analysis": check_analysis}[kind]
        rep = fn(data, ctx)
        validate_contract(kind, data, rep)
        reports.append(rep)
        if a.evidence:
            write_evidence(rep, a)
    if a.json:
        print(json.dumps([r.to_dict() for r in reports], ensure_ascii=False, indent=1))
    else:
        for r in reports:
            r.print()
    sys.exit(2 if any(r.errors for r in reports) else 0)


if __name__ == "__main__":
    main()
