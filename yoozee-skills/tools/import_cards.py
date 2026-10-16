#!/usr/bin/env python3
"""把参考材料拆成知识卡（一次性导入，结果提交进 kb/）。

  python3 yoozee-skills/tools/import_cards.py \
      --storyboard backend/builtin/柚子团队专属-即梦叙事分镜/SKILL.md \
      --handbook  <AI角色表情、动作与镜头表演提示词手册.txt>

正文原样保留，只做三件事：按章节切开；超过 CARD_LIMIT 字的按空行再切成（一）（二）；每张卡开头加两行「来源 / 何时读」。
即梦文档里「单条 20～30 秒」「上限 30 秒」这类时长说法不改原文，但卡头注明以 kb-dialects › models.json 为准。
"""
import argparse
import pathlib
import re

ROOT = pathlib.Path(__file__).resolve().parent.parent
KB = ROOT / "kb"
CARD_LIMIT = 11000
DURATION_NOTE = "注意：原文里的单条时长（20～30 秒、上限 30 秒）只对 Seedance 2.5 成立；一段最长多少秒以 kb-dialects › models.json 里项目所选模型为准。"

# (包, 卡路径, 起始行前缀, 结束行前缀（不含）, 何时读, 是否带时长提示)
STORYBOARD_CARDS = [
    ("kb-director", "cards/场景调用索引.md", "核心工作流口诀", "0.1、即梦 / Seedance 官方格式校准", "拿到一场戏先查：这类戏该读哪几张卡", False),
    ("kb-dialects", "seedance-2.0/即梦粘贴格式.md", "0.1、即梦 / Seedance 官方格式校准", "一、第一原则：台词与主线", "编译 Seedance / 即梦提示词时读：双层输出与粘贴版结构", True),
    ("kb-director", "cards/第一原则与全局参数.md", "一、第一原则：台词与主线", "三、拆分逻辑：小节、镜头、台词与节奏", "改编前读：台词剧情不可改、视觉可补的边界，开工前要定的全局参数", True),
    ("kb-director", "cards/分段拆分与镜头密度.md", "三、拆分逻辑：小节、镜头、台词与节奏", "四、每个小节必须输出的基本格式", "分段时读：按什么切段、一段几个镜头、台词时长怎么估、头尾安全区、段尾钩子", True),
    ("kb-director", "cards/分镜字段模板与写法.md", "四、每个小节必须输出的基本格式", "六、景别词库", "写镜头字段时读：每个字段写什么", True),
    ("kb-director", "cards/词库-景别机位构图焦距.md", "六、景别词库", "十、运镜方式完整词库", "选景别、机位、构图、焦距时查词", False),
    ("kb-director", "cards/词库-运镜.md", "十、运镜方式完整词库", "十一、灯光与光影完整词库", "选运镜时查词", False),
    ("kb-director", "cards/词库-灯光声音转场.md", "十一、灯光与光影完整词库", "十四、动作与表演词库", "写灯光、环境声、转场时查词", False),
    ("kb-performance", "cards/表演词库-情绪转可见表演.md", "十四、动作与表演词库", "十五、好莱坞导演视听语言知识库", "把「悲伤、愤怒」这类抽象情绪写成可见表演时读", False),
    ("kb-director", "cards/导演大脑-信息等级与镜头主语.md", "十五、好莱坞导演视听语言知识库", "15.1 镜头语言第一判断", "每场戏拆镜前先读：剧情功能、信息等级、镜头主语、观众信息量、结尾钩子", False),
    ("kb-director", "cards/镜头第一判断与对话戏.md", "15.1 镜头语言第一判断", "15.2A 高级分镜视听策略库", "对话、争吵、告白、审讯戏读", False),
    ("kb-director", "cards/高级视听策略.md", "15.2A 高级分镜视听策略库", "15.2B 导演思维分镜决策库", "要让一段有「导演感」时读：信息、情绪、潜台词策略", False),
    ("kb-director", "cards/导演思维决策矩阵.md", "15.2B 导演思维分镜决策库", "15.3 打斗戏怎么拍", "拿不准某个镜头怎么拍时读；伏笔、隐藏观察者、监控物件也在这里", False),
    ("kb-director", "cards/类型戏-打斗追逐悬疑.md", "15.3 打斗戏怎么拍", "15.6 信息揭示、身份揭露和反转怎么拍", "打斗、追逐、悬疑恐惧戏读", False),
    ("kb-director", "cards/类型戏-揭示反转工艺情感.md", "15.6 信息揭示、身份揭露和反转怎么拍", "15.8 希区柯克变焦", "身份揭露、反转、工艺制作、情感戏读", False),
    ("kb-director", "cards/特殊镜头与类型模板.md", "15.8 希区柯克变焦", "15.12 从剧本表达意图到视听语言的转换表", "要用特殊镜头（变焦、荷兰角、主观镜头）或套类型模板时读", False),
    ("kb-director", "cards/意图到视听语言转换表.md", "15.12 从剧本表达意图到视听语言的转换表", "十六、连续性、调度、3D动漫与生成稳定性知识库", "知道想表达什么、不知道怎么拍时查表", False),
    ("kb-continuity", "cards/轴线与连续锁定.md", "十六、连续性、调度、3D动漫与生成稳定性知识库", "16.2 人物调度 Blocking 知识库", "同一场景连续几段、正反打时读：轴线、动作和道具状态、场景锚点锁定", False),
    ("kb-continuity", "cards/人物调度与动画表演.md", "16.2 人物调度 Blocking 知识库", "16.4 镜头取舍与输出降噪规则", "安排人物走位、多人站位、3D 动画表演时读", False),
    ("kb-director", "cards/镜头取舍与节奏曲线.md", "16.4 镜头取舍与输出降噪规则", "16.6 类型片专项补充", "一段镜头太多、太拖或节奏平时读", False),
    ("kb-continuity", "cards/类型片补充与场景空间.md", "16.6 类型片专项补充", "16.8 AI视频生成风险控制知识库", "画空间图、核场景空间连续时读", False),
    ("kb-continuity", "cards/生成风险与素材引用.md", "16.8 AI视频生成风险控制知识库", "16.10 最终自检流程", "提交生成前读：容易崩的写法、@素材引用、首尾帧规则", True),
    ("kb-continuity", "cards/自检流程与评分.md", "16.10 最终自检流程", "16.13 景别节奏、相邻景别禁忌与反审美疲劳规则", "分段表定稿前自检", True),
    ("kb-director", "cards/景别节奏与相邻景别禁忌.md", "16.13 景别节奏、相邻景别禁忌与反审美疲劳规则", "十七、输出语言与格式硬规则", "排一段内的景别顺序时读：相邻景别不硬切", False),
    ("kb-dialects", "seedance-2.0/格式硬规则.md", "十七、输出语言与格式硬规则", "十八、标准输出示例", "编译即梦粘贴版前读", True),
    ("kb-dialects", "seedance-2.0/标准输出示例.md", "十八、标准输出示例", "十九、最终任务指令", "想看即梦粘贴版的颗粒度时读", True),
    ("kb-dialects", "seedance-2.0/最终任务指令.md", "十九、最终任务指令", "二十、常见错误修正表与最终粘贴模板", "只要即梦粘贴提示词（不走完整制作线）时读", True),
    ("kb-continuity", "cards/常见错误修正表.md", "二十、常见错误修正表与最终粘贴模板", "二十一、AI可理解性自检与导演统筹清单", "分段表定稿前、审片发现穿帮时对照", False),
    ("kb-continuity", "cards/AI可理解性自检清单.md", "二十一、AI可理解性自检与导演统筹清单", None, "分段表定稿前最后一遍自检", False),
]

SEPARATOR = re.compile(r"^=+\s*$")


def find(lines, prefix, after):
    for i in range(after, len(lines)):
        if lines[i].startswith(prefix):
            return i
    raise SystemExit(f"找不到章节：{prefix}")


def strip_separators(chunk):
    while chunk and (not chunk[0].strip() or SEPARATOR.match(chunk[0])):
        chunk = chunk[1:]
    # 原文末尾「👇以下是我要你改编的原文」是粘贴用的引导语，不属于知识
    while chunk and (not chunk[-1].strip() or SEPARATOR.match(chunk[-1]) or chunk[-1].startswith("👇")):
        chunk = chunk[:-1]
    return chunk


def split(text, limit=CARD_LIMIT):
    if len(text) <= limit:
        return [text]
    parts, cur = [], []
    size = 0
    for para in re.split(r"(\n\s*\n)", text):
        if size + len(para) > limit and cur:
            parts.append("".join(cur).strip())
            cur, size = [], 0
        cur.append(para)
        size += len(para)
    if cur:
        parts.append("".join(cur).strip())
    # 单段超长时按行硬切
    out = []
    for p in parts:
        while len(p) > limit:
            cut = p.rfind("\n", 0, limit)
            cut = cut if cut > limit // 2 else limit
            out.append(p[:cut].strip())
            p = p[cut:]
        out.append(p.strip())
    return [p for p in out if p]


NUMERALS = "一二三四五六七八九十"


def write_card(package, rel, body, source, when, note):
    pieces = split(body)
    written = []
    for k, piece in enumerate(pieces):
        path = pathlib.Path(rel)
        if len(pieces) > 1:
            path = path.with_name(f"{path.stem}-{k + 1}{path.suffix}")  # go:embed 不接受全角括号
        head = [f"> 来源：{source}", f"> 何时读：{when}"]
        if note:
            head.append(f"> {DURATION_NOTE}")
        target = KB / package / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text("\n".join(head) + "\n\n" + piece.strip() + "\n", encoding="utf-8")
        written.append(f"{package}/{path}  {len(piece)} 字")
    return written


def import_storyboard(path):
    text = pathlib.Path(path).read_text(encoding="utf-8")
    text = text.split("\n---\n", 1)[1] if text.startswith("---") else text
    lines = text.splitlines()
    # 目录里也有同名条目（带「：说明」），正文标题是独占一行的那一个
    first_real = next(i for i, ln in enumerate(lines) if ln.strip() == "0.1、即梦 / Seedance 官方格式校准")
    out = []
    for package, rel, start, end, when, note in STORYBOARD_CARDS:
        after = 0 if start == "核心工作流口诀" else first_real
        s = find(lines, start, after)
        e = find(lines, end, s + 1) if end else len(lines)
        chunk = strip_separators(lines[s:e])
        title = chunk[0].strip()
        out += write_card(package, rel, "\n".join(chunk), f"【柚子团队专属】即梦叙事分镜 · {title}", when, note)
    return out


HANDBOOK_PARTS = [
    ("cards/情绪时间轴.md", "第一部分  动态情绪与微表情表演", "第二部分  表情控制词典", "要写几秒内的情绪变化（悲伤、爱意、笑意、冷意、恐惧）时读，按时间轴写表演"),
    ("cards/表情控制词典.md", "第二部分  表情控制词典", "第三部分  动作、道具与双人互动", "要给起始帧或单镜头找精确表情词（中英对照）时读"),
    ("cards/动作道具与双人互动.md", "第三部分  动作、道具与双人互动", None, "写身体动作、道具使用、双人互动和镜头承接时读"),
]


def import_handbook(path):
    lines = pathlib.Path(path).read_text(encoding="utf-8").splitlines()
    body_start = find(lines, "第一部分  动态情绪与微表情表演", 0)
    out = []
    for rel, start, end, when in HANDBOOK_PARTS:
        s = find(lines, start, body_start)
        e = find(lines, end, s + 1) if end else len(lines)
        out += write_card("kb-performance", rel, "\n".join(strip_separators(lines[s:e])), f"AI 角色表情、动作与镜头表演提示词手册 · {lines[s].strip()}", when, False)
    return out


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--storyboard")
    ap.add_argument("--handbook")
    a = ap.parse_args()
    done = []
    if a.storyboard:
        done += import_storyboard(a.storyboard)
    if a.handbook:
        done += import_handbook(a.handbook)
    print("\n".join(done))
