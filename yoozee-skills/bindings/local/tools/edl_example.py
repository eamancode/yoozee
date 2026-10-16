"""剪辑表示例（给 assemble.py 用；真的剪辑表放 剪辑/剪辑表.py）。画面按顺序首尾相接；额外音轨按落点摆；SUBS 只用来算对白时段（压配乐），不烧字幕。

整片开关：LOOK = "film"（真人剧胶片链：去噪、柔化、压高光、降饱和、暗角）| "clean"（只防 AI 味，不动颜色）| "none" | 自定义 ffmpeg 滤镜串；
        TARGET_I 成片响度（必填，按需求文档的平台定，如 YouTube、B 站 -14）；SIZE、FRAME 画幅（SIZE 如 "2560x1440"，不写就跟第一条视频素材走；FRAME "16:9" 放进 16:9 画框上下留黑，"none" 原画幅）；DUCK_DB 对白时配乐让多少。都按需求文档定。
V 里一条：dict(src=路径或 "BLACK"/"WHITE"/"TITLE:文字|副标题" 或 .png（静态图，zoom=推镜比例）, in=秒, out=秒,
            grade="night2|day|warm2|cool2|call|none"（预设调色，想要别的写 fx=任意 ffmpeg 滤镜串）, matte=False（这一段不走 LOOK）,
            a=原声音量(0 不要), clarity=False（原声不走对白链）, fi/fo=淡入淡出秒, fcolor="white"（淡入淡出的颜色）, freeze=定格秒,
            crop=(左,上,宽比例), delogo=[(左,上,宽,高)], live=True/False（强制按实拍或图文处理）, rev=("room"|"courtyard"|"hall", 湿声 dB，如 -14)（台词太干、像录音棚时给一点空间感）, id=备注)
    片名卡想要别的字体和样式：自己渲染一张 png 当静态图放进来。
A 里一条：dict(src=路径, at=("V", 第几条, 偏移秒), in=源入点, dur=时长, gain=dB, fi/fo, loop=True,
            kind="music|amb|sfx|voice", norm=目标 LUFS(None 不归一), fx="phone", clarity=True, raw=True（不走柔化链，音乐要亮的时候用）,
            duck=True(对白时让路), hits=[(偏移, dB), ...](同一样本落多次), env=[(秒, dB), ...](情绪包络), rev=(空间, 湿声 dB))
"""
LOOK = "film"
V, A, SUBS, DUCK_EXTRA = [], [], [], []
DUCK_DB = -8.0      # 对白时配乐让多少
TARGET_I = None     # 成片响度，必填：按需求文档的平台定，如 YouTube、B 站 -14；不填总装不跑
SIZE, FRAME = None, "none"   # 画幅：SIZE 不写就跟第一条视频素材走；FRAME "16:9" 留黑边，"none" 原画幅
GEN, GF, BED, MU, FO = "生成/", "生成/图文/", "素材库/声音床/", "剪辑/配乐/", "剪辑/音效/"


def v(src, i, o, **kw):
    V.append(dict(src=src, **{"in": i, "out": o}, **kw)); return len(V) - 1


def a(src, at, **kw):
    A.append(dict(src=src, at=at, **kw))


def sub(idx, s, e, text):
    SUBS.append(("V", idx, s, e, text))


def bed(name, at, dur, gain=0.0, fi=0.8, fo=1.0):
    a(BED + name + ".wav", at, dur=dur, loop=True, kind="amb", norm=-42, gain=gain, fi=fi, fo=fo)


def sfx(name, at, gain=0.0, **kw):
    a(FO + name + ".mp3", at, kind="sfx", norm=-22, gain=gain, **kw)


# ---------------- 示例（虚构）：一场戏的写法。写真内容时删掉。
# x = v(GEN + "S01_v2.mp4", 0.3, 2.6, grade="night2", fi=1.0, id="走廊远景")
# bed("corridor_night", ("V", x, 0), dur=19.5, fi=1.5)
# a(MU + "sel_intro.mp3", ("V", x, 0), dur=16.0, kind="music", norm=-20, gain=-11, fi=2.0, fo=3.0, duck=True)
# x = v(GEN + "S02_v1.mp4", 5.4, 9.1, grade="night2", id="七床的人呢")
# sub(x, 0.2, 3.5, "七床？……七床的人呢？")
# x = v("WHITE", 0, 0.3, id="白光")
# v("TITLE:片名", 0, 2.4, fi=0.6, fo=0.7)
