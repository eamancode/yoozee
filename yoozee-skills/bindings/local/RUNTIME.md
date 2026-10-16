# 本机运行时（Claude Code / Codex）

技能里的能力端口在本机怎么做。脚本都在 `{{TOOLS}}/`，在项目根目录下运行。
本机要有：ffmpeg / ffprobe、Python 3 加 numpy 和 Pillow、本地 `whisper` 命令、一种中文字体。生成模型的密钥只写在项目根目录的 `.env`（脚本用 `common.need()` 读），不进命令行、日志和提示词；交付时提醒用户删掉。

## 项目文件结构

```
项目根目录/
├── 需求文档.md          契约 brief 的 Markdown 版（模板：yz-brief/templates/需求文档.md）；「生成模型：」一行决定段长上限
├── 剧本/                剧本定稿、分段表.md（进度表 + 每段一个 JSON 块）、连戏表.md
├── 素材库/              人物、声音、场景、道具、构图（名字_变体.png / .wav）
├── 生成/                每段的候选 <段>_vK.mp4、证据 <段>_vK_sheet.jpg / _stt.txt / _faces.jpg、<段>_报数.txt
├── 剪辑/                剪辑表.py、缓存、草稿、sync.json
├── final/               一个成片 + 自审.md；_旧版_vN
├── 交付/                交付包
├── 反馈/                待处理.md：带版本、时间码、截帧的看片反馈
├── 工具档案/            本片用到的模型档案（从 kb-dialects 拷来）
└── .env                 密钥，做完删
```

## 读知识卡

「kb-director › cards/xxx.md」= 打开 `{{SKILLS}}/kb-director/cards/xxx.md`。

## 端口绑定

| 端口 | 本机做法 |
| --- | --- |
| `progress.read` | `python3 {{TOOLS}}/segments.py status`（数分段表六格）；终态看 需求文档.md、连戏表的校验列、final/ |
| `brief.read` / `brief.write` | 读写 `需求文档.md` |
| `segments.read` / `segments.write` | 读写 `剧本/分段表.md`（每段 JSON 块照契约 segment）；`python3 {{TOOLS}}/segments.py list` 列段并标出超过模型上限的段 |
| `bible.read` / `bible.write` | 读写 `剧本/连戏表.md` |
| `assets.check` | 对照连戏表逐项 `ls`；分段表每段 refs 指向的文件都要存在（`segments.py jobs` 会报缺素材） |
| `image.generate` | 图片适配（默认 `{{TOOLS}}/adapters/image_codex.py`）；换模型按 `{{TOOLS}}/adapters/适配规格.md` 现写 |
| `image.crop_sheet` | `python3 {{TOOLS}}/crop_sheet.py 素材库/人物/名字_三视图.png` |
| `voice.design` / `voice.speak` | 配音适配（默认 `{{TOOLS}}/adapters/tts_minimax.py design / speak`） |
| `prompt.compile` | `python3 {{TOOLS}}/compile.py S03`（不给 --model 就读需求文档的生成模型）；`compile.py all` 检查整张表 |
| `cost.estimate` | `python3 {{TOOLS}}/segments.py jobs --adapter <视频适配> --ids … --n 候选数 --out 生成/jobs.json` 打印秒数和估价；没有适配的模型按 models.json 的 `price_per_sec_usd` 手算 |
| `spend.approve` | 直接问用户，给出段数、秒数、估价、预算余量 |
| `video.generate` | `python3 {{TOOLS}}/segments.py run --adapter <视频适配> --jobs 生成/jobs.json`；长任务用 `{{TOOLS}}/bg.sh 名字 命令…` 后台跑，已存在的文件跳过。Seedance 两家还没有本机适配：先用 yz-calibrate 照适配规格写一个，或把 compile.py 的粘贴版交给用户在即梦里出 |
| `media.frames` | `python3 {{TOOLS}}/qa.py sheet 文件` |
| `media.faces` | `python3 {{TOOLS}}/qa.py faces spec.json`（要先看拼图定脸的位置） |
| `media.transcribe` | `python3 {{TOOLS}}/qa.py stt 文件 --expect 台词`（输出字错率）；第二把尺子 `{{TOOLS}}/adapters/stt_elevenlabs.py` |
| `media.levels` | `python3 {{TOOLS}}/qa.py levels 文件` |
| `media.voice` | `python3 {{TOOLS}}/voice_qa.py 文件 --text 台词 [--ref 样本.wav]` |
| 一段候选的证据一次出齐 | `python3 {{TOOLS}}/review_seg.py S03` |
| `evidence.write` | 证据文件放 `生成/<段>_vK_*`，结论写进分段表「过审」格（✓ 或 ✗ + 原因） |
| `take.pick` | 指标表 `python3 {{TOOLS}}/pick.py takes 生成/S03_v*.mp4`；选中写进分段表「生成」格：候选 N 选 vK + 一句理由 |
| `cut.assemble` | 剪辑表 `剪辑/剪辑表.py`（写法见 `{{TOOLS}}/edl_example.py`），`python3 {{TOOLS}}/assemble.py --edl 剪辑/剪辑表.py --out final/片名.mp4 [--draft]` |
| `sound.music` / `sound.sfx` | `{{TOOLS}}/adapters/audio_elevenlabs.py music / sfx / score`；房间声床 `python3 {{TOOLS}}/roomtone.py beds.json --out 素材库/声音床` |
| 程序渲染的屏幕图文 | `python3 {{TOOLS}}/gfx.py chat / card spec.json out.mp4` |
| `gate.check` | `python3 {{TOOLS}}/gate.py final/片名.mp4`，不过退出码 2 |
| `delivery.package` | 把 final/ 的成片和自审、剧本/ 的分段表和连戏表、素材库清单、生成/ 下每段的提示词 JSON 打成 `交付/片名_vN.zip`；包里不放 .env |
| `feedback.read` | 读 `反馈/待处理.md` |
| `ask.user` | 直接在对话里问 |

## 帮手

- **审片员**（yz-inspector）：只读，抽帧、截脸、听写、量数，返回带时间码的问题清单。每段过审前、成片自审、返修核实时派。
- **探路员**（yz-scout）：查模型接口、版本、价格、额度，能试就真试。校准新模型、算预算时派。

## 规矩

- 长任务后台跑、能续跑：产出先查磁盘有没有，有就跳过；结果以磁盘为准，不以任务通知为准。
- 可逆的直接做；花钱超出预算、删用户的文件、对外发布、用他的账号，这四种先问。
