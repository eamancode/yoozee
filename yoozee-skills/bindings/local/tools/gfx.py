#!/usr/bin/env python3
"""屏幕图文（默认做法，可改）：聊天记录、通知卡等，做成"实拍屏幕特写"的插入镜头。字由程序排，不交给视频模型。
命令行：
  python3 {{TOOLS}}/gfx.py chat spec.json out.mp4     spec: {"theme":"dark|light","clock":"22:40","title":"夜班群（5）","dur":3.0,"size":[2560,1072],
        "steps":[[0,[消息...]],[1.2,[更多消息...]]], "typing":{"text":"七床的人呢？","start":0.2,"per_char":0.2,"send":1.9} 可选, "focus_last":2, "zoom":1.12, "rot":-1.2, "tilt":0.05}
        消息：{"who":"妈|爸|me|system","text":"...","image":"路径","time":"22:38"}；头像放 素材库/头像/{妈,爸,me}.png，没有就画一个。
  python3 {{TOOLS}}/gfx.py card spec.json out.mp4     spec: {"title":"住院部 · 护士站","sub":"七床","big":"已出院","accent":[52,58,66],"bg":[238,239,241],"dur":3.0,"night":true,"push":0.12}

设计原则：
- 界面是虚构的通用样式，不仿任何真实 App 的品牌（无 logo、无专有配色）。
- 文字由程序排版，保证中文清晰准确；视频模型不负责生成文字。
- 插入镜头要像实拍：轻微透视、焦外虚化、屏幕辉光、颗粒、缓慢推镜和极轻微手持。
"""
import math
import os
import subprocess
import sys

import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageFont

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import common as C  # noqa: E402

import glob as _glob
_cands = _glob.glob("/System/Library/AssetsV2/com_apple_MobileAsset_Font*/*/AssetData/PingFang.ttc") + ["/System/Library/Fonts/PingFang.ttc", "/System/Library/Fonts/Hiragino Sans GB.ttc", "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"]
PINGFANG = next((f for f in _cands if os.path.exists(f)), None)  # 中文字体：苹方 → 冬青 → Noto，都没有就报错说清
SONGTI = "/System/Library/Fonts/Supplemental/Songti.ttc"
EMOJI = "/System/Library/Fonts/Apple Color Emoji.ttc"
EMOJI_CHARS = set("👍🙂😊❤️🌹🎉👌🙏")
PF = {"regular": 3, "medium": 7, "semibold": 11, "light": 15}
ST = {"black": 0, "bold": 1, "light": 3, "regular": 6}
_cache = {}


def font(size, weight="regular", family="pingfang"):
    key = (family, weight, size)
    if key not in _cache:
        if family == "songti":
            _cache[key] = ImageFont.truetype(SONGTI, size, index=ST[weight])
        else:
            if PINGFANG is None:
                raise SystemExit("没有找到中文字体（苹方 / 冬青 / Noto CJK），装一个再渲染屏幕图文")
            try:
                _cache[key] = ImageFont.truetype(PINGFANG, size, index=PF[weight] if "PingFang" in PINGFANG else 0)
            except OSError:
                _cache[key] = ImageFont.truetype(PINGFANG, size)
    return _cache[key]


def text_w(d, s, f):
    return d.textlength(s, font=f)


NO_LINE_START = set("，。、！？；：,.!?;:）)》」』”’…—")


def wrap(d, s, f, maxw):
    """按字符折行（中文没有空格），遵守避头标点：标点不放行首，必要时把上一个字一起带到下一行。"""
    lines, cur = [], ""
    for ch in s:
        if ch == "\n":
            lines.append(cur)
            cur = ""
            continue
        if text_w(d, cur + ch, f) > maxw and cur:
            if ch in NO_LINE_START:
                if len(cur) >= 2:
                    lines.append(cur[:-1])
                    cur = cur[-1] + ch
                else:
                    cur += ch
            else:
                lines.append(cur)
                cur = ch
        else:
            cur += ch
    if cur:
        lines.append(cur)
    return lines or [""]


def draw_text(im, d, xy, text, f, fill):
    """画一行字；遇到表情符号用彩色表情字体单独画（PIL 不会自动换字体）。"""
    x, y = xy
    size = f.size
    for ch in text:
        if ch in EMOJI_CHARS or ord(ch) > 0x1F000:
            ef = ImageFont.truetype(EMOJI, 160)
            e = Image.new("RGBA", (200, 200), (0, 0, 0, 0))
            ImageDraw.Draw(e).text((0, 0), ch, font=ef, embedded_color=True)
            e = e.crop(e.getbbox() or (0, 0, 160, 160))
            k = size * 1.05 / max(e.height, 1)
            e = e.resize((max(1, int(e.width * k)), max(1, int(e.height * k))), Image.LANCZOS)
            im.paste(e, (int(x), int(y + size * 0.12)), e)
            x += e.width + size * 0.08
        else:
            d.text((x, y), ch, font=f, fill=fill)
            x += d.textlength(ch, font=f)
    return x


def text_len(d, text, f):
    w = 0
    for ch in text:
        w += f.size * 1.13 if (ch in EMOJI_CHARS or ord(ch) > 0x1F000) else d.textlength(ch, font=f)
    return w


def rounded(d, box, r, fill):
    d.rounded_rectangle(box, radius=r, fill=fill)


# ------------------------------------------------------------------ 头像
AVATAR_FILES = {"rose": "素材库/头像/妈.png", "landscape": "素材库/头像/爸.png", "self": "素材库/头像/me.png"}


def avatar(kind, size):
    """头像：优先用 素材库/头像/<发言人>.png（me.png 是主角自己）；没有就按名字画一个简单的（妈=花、爸=风景、其他=首字）。"""
    f = AVATAR_FILES.get(kind) or f"素材库/头像/{kind}.png"
    if kind not in ("self", "rose", "landscape"):
        kind = {"妈": "rose", "爸": "landscape"}.get(kind, "initial:" + str(kind)[:1])
    if f and os.path.exists(C.abspath(f)):
        ph = Image.open(C.abspath(f)).convert("RGB")
        side = min(ph.size)
        ph = ph.crop(((ph.width - side) // 2, (ph.height - side) // 2, (ph.width + side) // 2, (ph.height + side) // 2)).resize((size, size), Image.LANCZOS)
        mask = Image.new("L", (size, size), 0)
        ImageDraw.Draw(mask).rounded_rectangle([0, 0, size - 1, size - 1], radius=int(size * 0.14), fill=255)
        out = Image.new("RGBA", (size, size), (0, 0, 0, 0))
        out.paste(ph, (0, 0), mask)
        return out
    im = Image.new("RGB", (size, size), (200, 200, 200))
    d = ImageDraw.Draw(im)
    if kind == "rose":
        # 暖粉渐变背景 + 简化的花瓣圆环，远看像一张花的照片
        for y in range(size):
            t = y / size
            d.line([(0, y), (size, y)], fill=(int(70 + 40 * t), int(110 - 30 * t), int(60 - 20 * t)))
        cx, cy = size * 0.5, size * 0.52
        for k, (rr, col) in enumerate([(0.42, (196, 58, 92)), (0.32, (214, 76, 108)), (0.22, (232, 104, 130)), (0.12, (178, 40, 70))]):
            r = size * rr
            d.ellipse([cx - r, cy - r * 0.9, cx + r, cy + r * 0.9], fill=col)
        im = im.filter(ImageFilter.GaussianBlur(size * 0.012))
    elif kind == "landscape":
        for y in range(size):
            t = y / size
            d.line([(0, y), (size, y)], fill=(int(150 + 60 * t), int(190 + 30 * t), int(220 + 10 * t)))
        d.polygon([(0, size * 0.7), (size * 0.35, size * 0.35), (size * 0.6, size * 0.62), (size * 0.8, size * 0.45), (size, size * 0.66), (size, size), (0, size)], fill=(74, 104, 88))
        d.polygon([(0, size * 0.85), (size * 0.5, size * 0.66), (size, size * 0.86), (size, size), (0, size)], fill=(52, 80, 64))
        im = im.filter(ImageFilter.GaussianBlur(size * 0.01))
    elif kind == "self" or kind.startswith("initial:"):
        ch = "我" if kind == "self" else kind.split(":", 1)[1]
        d.rectangle([0, 0, size, size], fill=(58, 64, 74))
        f = font(int(size * 0.46), "medium")
        w = text_w(d, ch, f)
        d.text(((size - w) / 2, size * 0.2), ch, font=f, fill=(225, 228, 232))
    else:
        d.rectangle([0, 0, size, size], fill=(120, 140, 160))
    mask = Image.new("L", (size, size), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, size - 1, size - 1], radius=int(size * 0.14), fill=255)
    out = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    out.paste(im, (0, 0), mask)
    return out


# ------------------------------------------------------------------ 手机聊天界面
class Chat:
    """一屏聊天界面（虚构通用样式）。messages: [{"who": "妈"|"爸"|"me", "text": "...", "image": path|None, "time": "22:38"}]"""
    W = 1179
    BG = (237, 237, 237)
    BUBBLE_OTHER = (255, 255, 255)
    BUBBLE_ME = (184, 228, 201)
    INK = (25, 25, 25)
    GREY = (150, 150, 150)

    THEMES = {
        "light": {"BG": (237, 237, 237), "BAR": (237, 237, 237), "BUBBLE_OTHER": (255, 255, 255), "BUBBLE_ME": (184, 228, 201),
                  "INK": (25, 25, 25), "GREY": (150, 150, 150), "LINE": (215, 215, 215), "INPUT_BG": (246, 246, 246),
                  "INPUT": (255, 255, 255), "TAG": (220, 220, 220), "ICON": (80, 80, 80)},
        "dark": {"BG": (17, 17, 17), "BAR": (24, 24, 24), "BUBBLE_OTHER": (44, 44, 46), "BUBBLE_ME": (58, 120, 86),
                 "INK": (226, 226, 226), "GREY": (122, 122, 122), "LINE": (40, 40, 40), "INPUT_BG": (28, 28, 30),
                 "INPUT": (44, 44, 46), "TAG": (36, 36, 38), "ICON": (170, 170, 170)},
    }

    def __init__(self, title="群聊", clock="22:40", scale=1.0, theme="light"):
        self.title, self.clock, self.s = title, clock, scale
        t = self.THEMES[theme]
        self.BG, self.BAR, self.BUBBLE_OTHER, self.BUBBLE_ME = t["BG"], t["BAR"], t["BUBBLE_OTHER"], t["BUBBLE_ME"]
        self.INK, self.GREY, self.LINE, self.INPUT_BG = t["INK"], t["GREY"], t["LINE"], t["INPUT_BG"]
        self.INPUT, self.TAG, self.ICON = t["INPUT"], t["TAG"], t["ICON"]

    def px(self, v):
        return int(v * self.s)

    def render(self, messages, typing=None, height=2556, show_cursor=True, scroll=0):
        """返回 RGB 图。typing：输入框里正在打的字；scroll：消息区向上滚动的像素。"""
        W, H, px = self.W, height, self.px
        im = Image.new("RGB", (W, H), self.BG)
        d = ImageDraw.Draw(im)
        # 消息区（先画在长画布上，再按滚动裁切）
        top_bar, bottom_bar = px(250), px(190)
        area_h = H - top_bar - bottom_bar
        canvas, used = self._messages(messages)
        area = Image.new("RGB", (W, area_h), self.BG)
        if used <= area_h:
            # 内容不满一屏：从上往下排（和真实聊天软件一样）
            y0 = 0
            area.paste(canvas, (0, 0))
        else:
            # 超过一屏：最新消息贴底；scroll 往上翻看历史
            y0 = max(0, min(used - area_h - scroll, used - area_h))
            area.paste(canvas.crop((0, y0, W, y0 + area_h)), (0, 0))
        im.paste(area, (0, top_bar))
        # 每条消息在整张界面上的纵向位置（相对高度 0～1），以及输入框位置
        self.meta = {"msgs": [((top_bar + a - y0) / H, (top_bar + b - y0) / H) for a, b in self._spans],
                     "input": ((H - bottom_bar) / H, 1.0), "max_scroll": max(0, used - area_h)}
        # 顶部状态栏 + 标题栏
        d.rectangle([0, 0, W, top_bar], fill=self.BAR)
        d.text((px(90), px(40)), self.clock, font=font(px(46), "semibold"), fill=self.INK)
        # 电池与信号
        bx = W - px(150)
        d.rounded_rectangle([bx, px(52), bx + px(70), px(84)], radius=px(8), outline=self.INK, width=px(3))
        d.rectangle([bx + px(6), px(58), bx + px(52), px(78)], fill=self.INK)
        for i in range(4):
            h = px(10 + i * 7)
            x = W - px(300) + i * px(16)
            d.rectangle([x, px(84) - h, x + px(10), px(84)], fill=self.INK)
        tf = font(px(50), "medium")
        tw = text_w(d, self.title, tf)
        d.text(((W - tw) / 2, px(150)), self.title, font=tf, fill=self.INK)
        d.text((px(40), px(138)), "‹", font=font(px(80), "light"), fill=self.INK)
        d.text((W - px(110), px(130)), "···", font=font(px(56), "semibold"), fill=self.INK)
        d.line([(0, top_bar - 1), (W, top_bar - 1)], fill=self.LINE, width=px(2))
        # 底部输入栏
        yb = H - bottom_bar
        d.rectangle([0, yb, W, H], fill=self.INPUT_BG)
        d.line([(0, yb), (W, yb)], fill=self.LINE, width=px(2))
        box = [px(130), yb + px(28), W - px(250), yb + px(128)]
        rounded(d, box, px(18), self.INPUT)
        if typing is not None:
            tf2 = font(px(50))
            d.text((box[0] + px(26), box[1] + px(20)), typing, font=tf2, fill=self.INK)
            if show_cursor:
                cx = box[0] + px(26) + text_w(d, typing, tf2) + px(4)
                d.rectangle([cx, box[1] + px(20), cx + px(4), box[1] + px(84)], fill=(60, 120, 230))
            # 发送按钮
            sb = [W - px(225), yb + px(30), W - px(40), yb + px(126)]
            rounded(d, sb, px(16), (72, 160, 110) if typing else (90, 90, 90))
            f3 = font(px(44), "medium")
            sw = text_w(d, "发送", f3)
            d.text(((sb[0] + sb[2] - sw) / 2, sb[1] + px(20)), "发送", font=f3, fill=(255, 255, 255))
        else:
            # 语音按钮和加号的简化图形
            d.ellipse([px(30), yb + px(40), px(105), yb + px(115)], outline=self.ICON, width=px(4))
            d.ellipse([W - px(200), yb + px(40), W - px(125), yb + px(115)], outline=self.ICON, width=px(4))
            d.ellipse([W - px(105), yb + px(40), W - px(30), yb + px(115)], outline=self.ICON, width=px(4))
            d.line([(W - px(67), yb + px(55)), (W - px(67), yb + px(100))], fill=self.ICON, width=px(4))
            d.line([(W - px(90), yb + px(78)), (W - px(45), yb + px(78))], fill=self.ICON, width=px(4))
        return im

    def _messages(self, messages):
        W, px = self.W, self.px
        tmp = Image.new("RGB", (W, 20000), self.BG)
        d = ImageDraw.Draw(tmp)
        y = px(40)
        av = px(120)
        f = font(px(52))
        fname = font(px(36))
        ftime = font(px(36))
        maxw = W - px(2 * (40 + 120 + 24)) - px(60)
        self._spans = []
        for m in messages:
            y_start = y
            if m.get("time"):
                tw = text_w(d, m["time"], ftime)
                d.text(((W - tw) / 2, y), m["time"], font=ftime, fill=self.GREY)
                y += px(80)
            if m.get("who") == "system":
                tw = text_w(d, m["text"], ftime)
                rounded(d, [(W - tw) / 2 - px(20), y - px(6), (W + tw) / 2 + px(20), y + px(56)], px(10), self.TAG)
                d.text(((W - tw) / 2, y), m["text"], font=ftime, fill=self.GREY)
                y += px(100)
                self._spans.append((y_start, y))
                continue
            me = m.get("who") == "me"
            kind = "self" if me else m.get("who", "other")
            a = avatar(kind, av)
            ax = W - px(40) - av if me else px(40)
            tmp.paste(a, (ax, y), a)
            by = y
            if not me:
                d.text((ax + av + px(24), y - px(4)), m["who"], font=fname, fill=self.GREY)
                by = y + px(50)
            if m.get("image"):
                ph = Image.open(C.abspath(m["image"])).convert("RGB")
                tw_ = px(460)
                th_ = int(ph.height * tw_ / ph.width)
                ph = ph.resize((tw_, th_), Image.LANCZOS)
                mask = Image.new("L", ph.size, 0)
                ImageDraw.Draw(mask).rounded_rectangle([0, 0, tw_ - 1, th_ - 1], radius=px(16), fill=255)
                bx = ax - px(24) - tw_ if me else ax + av + px(24)
                tmp.paste(ph, (bx, by), mask)
                y = by + th_ + px(40)
                if m.get("text"):
                    # 图片后面跟一条文字气泡
                    by = y
                else:
                    self._spans.append((y_start, y))
                    continue
            lines = wrap(d, m.get("text", ""), f, maxw)
            lh = px(72)
            bw = max(text_len(d, ln, f) for ln in lines) + px(56)
            bh = lh * len(lines) + px(44)
            bx = ax - px(24) - bw if me else ax + av + px(24)
            col = self.BUBBLE_ME if me else self.BUBBLE_OTHER
            rounded(d, [bx, by, bx + bw, by + bh], px(18), col)
            # 小尖角
            tip_y = by + px(40)
            if me:
                d.polygon([(bx + bw, tip_y - px(12)), (bx + bw + px(14), tip_y), (bx + bw, tip_y + px(12))], fill=col)
            else:
                d.polygon([(bx, tip_y - px(12)), (bx - px(14), tip_y), (bx, tip_y + px(12))], fill=col)
            for i, ln in enumerate(lines):
                draw_text(tmp, d, (bx + px(28), by + px(20) + i * lh), ln, f, self.INK)
            y = max(by + bh, y + av) + px(44)
            self._spans.append((y_start, y))
        return tmp.crop((0, 0, W, y + px(20))), y + px(20)


# ------------------------------------------------------------------ 实拍屏幕质感
def macro_insert(ui, out_size=(2560, 1072), focus=(0.5, 0.5), zoom=1.0, tilt=0.06, rot=-1.2, blur_max=9.0,
                 focus_band=0.22, glow=0.18, black_lift=10, t=0.0, seed=0, warm=0.0):
    """把一张界面图做成"手机/显示器屏幕特写"的一帧。
    focus：界面上对焦点的相对坐标；zoom：相对"界面宽度铺满画面"的放大倍数；tilt：透视倾斜（上远下近）；
    rot：画面旋转角度；blur_max：焦外最大模糊半径；focus_band：清晰带占画面高度的比例。"""
    W, H = out_size
    ui = ui.convert("RGB")
    # 1+2. 只裁出对焦点周围需要的那块，再放大到画布（比整张放大快得多）
    scale = W * zoom / ui.width
    pad = 1.18
    cw, ch = int(W * pad), int(H * pad)
    sw, sh = cw / scale, ch / scale
    fx, fy = focus[0] * ui.width, focus[1] * ui.height
    box = (fx - sw / 2, fy - sh / 2, fx + sw / 2, fy + sh / 2)
    src = Image.new("RGB", (int(sw) + 2, int(sh) + 2), (8, 8, 10))
    ix0, iy0 = math.floor(box[0]), math.floor(box[1])
    src.paste(ui, (-ix0, -iy0))
    canvas = src.resize((cw, ch), Image.LANCZOS, box=(box[0] - ix0, box[1] - iy0, box[2] - ix0, box[3] - iy0))
    # 3. 旋转 + 透视（上边缩窄，像手机斜放在桌上）
    canvas = canvas.rotate(rot, resample=Image.BICUBIC, fillcolor=(8, 8, 10))
    if tilt:
        k = tilt
        src = [(0, 0), (cw, 0), (cw, ch), (0, ch)]
        dst = [(cw * k, 0), (cw * (1 - k), 0), (cw, ch), (0, ch)]
        coeffs = _persp_coeffs(dst, src)
        canvas = canvas.transform((cw, ch), Image.PERSPECTIVE, coeffs, Image.BICUBIC, fillcolor=(8, 8, 10))
    # 4. 裁回画面大小
    ox, oy = (cw - W) // 2, (ch - H) // 2
    frame = canvas.crop((ox, oy, ox + W, oy + H))
    # 5. 焦外：按到清晰带的距离混合模糊版本
    arr = np.asarray(frame).astype(np.float32)
    blurred = np.asarray(frame.filter(ImageFilter.GaussianBlur(blur_max))).astype(np.float32)
    ys = np.linspace(0, 1, H)[:, None]
    dist = np.clip((np.abs(ys - 0.5) - focus_band / 2) / (0.5 - focus_band / 2), 0, 1)
    m = (dist ** 1.4)[:, :, None]
    arr = arr * (1 - m) + blurred * m
    # 6. 屏幕辉光：亮部的大半径模糊叠加
    if glow:
        g = np.asarray(Image.fromarray(np.clip(arr, 0, 255).astype(np.uint8)).filter(ImageFilter.GaussianBlur(28))).astype(np.float32)
        arr = arr + glow * np.clip(g - 120, 0, 255)
    # 7. 屏幕在暗处：压一点对比、抬黑位、略偏冷或偏暖
    arr = arr * 0.9 + black_lift
    if warm:
        arr[:, :, 0] *= 1 + warm
        arr[:, :, 2] *= 1 - warm
    # 8. 暗角
    xx = np.linspace(-1, 1, W)[None, :]
    yy = np.linspace(-1, 1, H)[:, None]
    vig = 1 - 0.35 * np.clip((xx ** 2 * 0.6 + yy ** 2) - 0.15, 0, 1)
    arr *= vig[:, :, None]
    # 9. 颗粒
    rng = np.random.default_rng(seed)
    arr += rng.normal(0, 4.5, arr.shape[:2])[:, :, None]
    return Image.fromarray(np.clip(arr, 0, 255).astype(np.uint8))


def _persp_coeffs(pa, pb):
    A = []
    for (x, y), (X, Y) in zip(pa, pb):
        A.append([x, y, 1, 0, 0, 0, -X * x, -X * y])
        A.append([0, 0, 0, x, y, 1, -Y * x, -Y * y])
    A = np.array(A, dtype=np.float64)
    B = np.array([c for p in pb for c in p], dtype=np.float64)
    return np.linalg.solve(A, B).tolist()


# ------------------------------------------------------------------ 输出视频
class Writer:
    """把 PIL 帧流写成视频（ffmpeg 管道）。默认 24fps、高质量 H.264。"""

    def __init__(self, out, size, fps=24, crf=12):
        self.out = C.abspath(out)
        os.makedirs(os.path.dirname(self.out), exist_ok=True)
        self.p = subprocess.Popen(["ffmpeg", "-v", "error", "-y", "-f", "rawvideo", "-pix_fmt", "rgb24", "-s", f"{size[0]}x{size[1]}",
                                   "-r", str(fps), "-i", "-", "-c:v", "libx264", "-preset", "slow", "-crf", str(crf),
                                   "-pix_fmt", "yuv420p", "-movflags", "+faststart", self.out], stdin=subprocess.PIPE)
        self.size = size

    def add(self, im):
        if im.size != tuple(self.size):
            im = im.resize(self.size, Image.LANCZOS)
        self.p.stdin.write(im.convert("RGB").tobytes())

    def close(self):
        self.p.stdin.close()
        self.p.wait()
        return self.out


def ease(t):
    t = max(0.0, min(1.0, t))
    return t * t * (3 - 2 * t)


def drift(t, dur, amp=(0.004, 0.003), push=0.04):
    """慢推 + 极轻微手持：返回 (zoom 系数, dx, dy)（相对量）。"""
    z = 1 + push * ease(t / max(dur, 1e-6))
    dx = amp[0] * math.sin(t * 0.9 + 0.3) + amp[0] * 0.5 * math.sin(t * 2.3)
    dy = amp[1] * math.sin(t * 1.1 + 1.7) + amp[1] * 0.5 * math.sin(t * 2.9 + 0.5)
    return z, dx, dy


# ------------------------------------------------------------------ 命令行
def _ease(t):
    t = max(0.0, min(1.0, t)); return t * t * (3 - 2 * t)


def _chat(spec, out):
    size = tuple(spec.get("size", [2560, 1072])); dur = float(spec.get("dur", 3.0)); fps = 24
    steps = sorted(spec["steps"], key=lambda s: s[0]); typing = spec.get("typing")
    look = dict(glow=0.10, black_lift=4, blur_max=8, focus_band=0.36) if spec.get("theme", "dark") == "dark" else dict(glow=0.04, black_lift=8, blur_max=7, focus_band=0.40)
    cache = {}

    def ui_at(t):
        idx = max(i for i, (t0, _) in enumerate(steps) if t >= t0 or i == 0)
        ty = None
        if typing:
            k = int(max(0.0, t - typing.get("start", 0)) / typing.get("per_char", 0.2)); txt = typing["text"][:k]
            if typing.get("send") is not None and t >= typing["send"]:
                txt = ""
            if typing.get("delete") is not None and t >= typing["delete"]:
                txt = typing["text"][:max(0, len(typing["text"]) - int((t - typing["delete"]) / typing.get("del_per", 0.3)) - 1)]
            ty = (txt, (int(t * 2.2) % 2) == 0)
        key = (idx, ty)
        if key not in cache:
            c = Chat(title=spec.get("title", "群聊"), clock=spec.get("clock", "22:40"), theme=spec.get("theme", "dark"))
            cache[key] = (c.render(steps[idx][1], typing=None if ty is None else ty[0], show_cursor=True if ty is None else ty[1]), c.meta)
        return cache[key]
    w = Writer(out, size, fps=fps)
    for i in range(int(round(dur * fps))):
        t = i / fps; ui, meta = ui_at(t)
        spans = meta["msgs"][-int(spec.get("focus_last", 2)):] or [(0.4, 0.6)]
        fy = (spans[0][0] + spans[-1][1]) / 2 if not (typing and t < (typing.get("send") or 1e9)) else meta["input"][0] + 0.03
        z, dx, dy = drift(t, dur, amp=(0.0025, 0.002), push=spec.get("push", 0.05))
        w.add(macro_insert(ui, out_size=size, focus=(spec.get("fx", 0.47) + dx, fy + dy), zoom=spec.get("zoom", 1.12) * z, rot=spec.get("rot", -1.2), tilt=spec.get("tilt", 0.05), seed=i + 7, **look))
    w.close(); print(out)


def _card(spec, out):
    size = tuple(spec.get("size", [2560, 1072])); dur = float(spec.get("dur", 3.0)); fps = 24
    W, H = 1920, 1080
    accent = tuple(spec.get("accent", [52, 58, 66])); bg = tuple(spec.get("bg", [238, 239, 241]))  # 顶栏色和底色按这部片的界面定，默认中性灰
    im = Image.new("RGB", (W, H), bg); d = ImageDraw.Draw(im)
    d.rectangle([0, 0, W, 86], fill=accent); d.text((36, 22), spec.get("title", ""), font=font(36, "semibold"), fill=(255, 255, 255))
    rounded(d, [340, 160, W - 120, H - 120], 18, (255, 255, 255))
    d.text((400, 220), spec.get("sub", ""), font=font(34), fill=(90, 96, 106))
    big = spec.get("big", ""); f = font(96, "semibold"); d.text(((W - text_w(d, big, f)) / 2, 440), big, font=f, fill=(30, 33, 38))
    for i, ln in enumerate(spec.get("lines", [])):
        d.text((400, 600 + i * 60), ln, font=font(34), fill=(70, 76, 86))
    if spec.get("night"):
        im = Image.blend(im, Image.new("RGB", im.size, (40, 52, 70)), 0.10)
    look = dict(glow=0.09 if spec.get("night") else 0.05, black_lift=3 if spec.get("night") else 6, blur_max=6, focus_band=0.46, tilt=0.03)
    w = Writer(out, size, fps=fps)
    for i in range(int(round(dur * fps))):
        t = i / fps; z, dx, dy = drift(t, dur, amp=(0.0015, 0.001), push=spec.get("push", 0.12))
        w.add(macro_insert(im, out_size=size, focus=(0.455 + dx, 0.47 + dy), zoom=1.5 * z, rot=0.0, seed=i + 7, **look))
    w.close(); print(out)


if __name__ == "__main__":
    import json as _json
    if len(sys.argv) < 4 or sys.argv[1] not in ("chat", "card"):
        sys.exit("用法：gfx.py chat|card spec.json out.mp4")
    spec = _json.load(open(sys.argv[2], encoding="utf-8"))
    (_chat if sys.argv[1] == "chat" else _card)(spec, sys.argv[3])
