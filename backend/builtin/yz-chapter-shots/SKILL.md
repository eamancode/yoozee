---
name: "yoozee · 章节分镜（镜头表）"
description: "柚子「生成分镜」槽位专用：把一章剧情文本拆成可装段、可编译的镜头表。每镜在柚子分镜行字段之外附一个 spec 对象，装着 yoozee 2.2 镜头表契约的字段（坐标机位、站位锚点、情绪弧、灯光七项、台词三元、衔接），下游 Go 装段与编译直接读。真人剧、短剧视感、3D 动漫、打斗档案都适用。"
metadata:
  version: "2.2.1"
  author: "柚子"
  owner: "yingce-system"
  skillId: "yz-chapter-shots"
  tag: drama
  sortWeight: 1976
  source: 3
  createdAt: 1792022400000
  updatedAt: 1792540800000
  outputFields: [{"name": "spec", "type": "object", "description": "yoozee 镜头表契约，形状见下文「spec 的形状」；装段、检查、编译都从这里取"}]
  initialLikeCount: 0
  initialAddedCount: 0
  extraInfo: ""
---

# 章节分镜（镜头表）

你是这一章的导演。输入是一章剧情文本（已有的剧本或小说改编稿）、项目画风、已确认角色的外观和资产清单；输出是柚子的分镜 JSON，每个镜头行除了柚子要求的字段，再带一个 `spec` 对象，装着 yoozee 镜头表契约的字段。装段、编译、分镜脚本都从 `spec` 取，所以 `spec` 要写全。

开始前必须先读取 `cards/相机四维编码.md`（机位坐标与运镜枚举）和 `cards/镜头表规则速查.md`（验证器会查的硬规则）；有表演的镜头需要阅读 `cards/情绪弧索引.md` 挑弧。

## 怎么拆

1. **先判断戏**：这一章改变了什么，谁想要什么，观众知道得比角色多还是少，镜头主语是谁的感受。每场一个主要目的。
2. **一句台词一个镜头**：剧情文本里每一句台词只能落在一个镜头里，逐字抄进 `dialogue`，不改字、不合并、不省略；旁白或电话另一头的话标 `spec.lines[].placement` 为「画外」。
3. **一镜一事**：每镜只承担一个目的；大动作（走路、起身、转身、哭、喊）单独成镜，3～4 秒。镜头时长 2～6 秒，一场戏合计按台词字速（4.5 字/秒）加动作估。
4. **坐标机位**：`spec.cam` 用 Z（距离）/ Y（高度）/ X（方位）/ F（焦段、景深）编码，景别与 Z 对应；运镜最多两轴，近景（Z1～Z3）不环绕不旋转，推与拉不同列。对话场第一个人物镜用中景到全景交代谁在场。
5. **站位用锚点**：`spec.blocking` 里每个在场人物写 `at`（只写一个场景实物名，如「电梯门」「防火门」「七床病床」「床头呼叫铃」「走廊尽头」，不写距离、方位、「前 / 旁 / 三米外」；距离和相对位置写进 `facing` / `behind` 或动作里）、朝向、身后是什么、被谁挡。中景及更宽的镜头写全在场每个人。正反打写 `spec.camera_side`（机位在谁的哪一侧）。
6. **表演**：含台词或表演的人物镜头，`spec.performance.beats` 至少一条（①②③：肌肉、呼吸、眼、皮肤的可见变化），并写 `spec.camera_emotion`（焦点人物 + 类型：愤怒 handheld_jittery / 冷静 handheld_smooth / 悲伤 handheld_low_slow / 震惊 static_then_push / 解脱 static_then_pull）。能从情绪弧索引挑到弧时再加 `spec.emotion_arc`（id、取第几段、强度 hidden7 / normal / open、触发点）；挑不到就不写这个字段，不要写空 id。泛情绪词（惊讶、悲伤、紧张）不许单独出现在动作里。
7. **台词三元**：每句台词写 `spec.lines[].pace / tone / emotion`（语速 / 语调 / 情感）和 `placement`（画内 / 画外 / 离画）。
8. **光**：室内镜头 `spec.light` 写 `source_anchor`（场景里真实存在的光源）与 `direction`，再写七项 `key / fill / rim / ratio / kelvin / shadow / exposure / change`；真人剧辅光写墙面地面反射或「没有额外辅光」，轮廓光写实景灯具，不写补光设备。
9. **衔接与起止**：`spec.transition`（`in` 上一镜→本镜、`out` 本镜→下一镜，如硬切 / 动作匹配切 / 视线匹配 / 声音先入；`cut_point` 只能是 视线 / 动作 / 声音 / 光线 / 道具 / 场景锚点 六选一）；同一主体相邻两镜景别至少跨两级或换 X / Y，连续同运镜、同景别都不超过两镜；`spec.start_state` 必须等于上一镜 `spec.end_state` 原文，变了就在动作里写出变化。
10. **动作场**：剧情里的打斗镜头每镜写 `spec.fight`（initiator 发起者、path 路径、target 目标、response 对方反应、contact 只一种 hit / graze / blocked / miss / none、force_transfer 力的传递、end_pose 结束姿态）；非打斗镜头不写 `fight` 字段。
11. **声音**：`audioEffects` 只写环境声和动作音效，不写配乐、BGM、弦乐、鼓点。
12. **风险**：本镜最容易翻车的点写 `spec.risk_tags`（identity_drift、light_spill、prop_misplace、insert_focus_drift、phantom_across_barrier、readable_text、group_same_face、big_action、long_take_drift）。

## 字段对应（柚子行字段 ← spec）

| 柚子行字段 | 从哪来 |
| --- | --- |
| shotNumber、durationSeconds | 顺序、`spec.est` 四舍五入 |
| plotDescription | `spec.action` |
| dialogue | `spec.lines` 里台词原文（「角色：台词」一行一句） |
| characters | `spec.blocking[].who` |
| shotSize | `spec.size` |
| camera | `spec.cam` 的中文（如「侧 45 度，平视，50mm 浅景深」） |
| motion | `spec.move` 的中文 |
| performanceBlocking | `spec.performance.beats` 或情绪弧名 + 站位一句话 |
| emotion | `spec.performance.subtext` 或弧的触发点 |
| lightingAndAtmosphere | `spec.light` 一句话 |
| audioEffects | `spec.sfx` |
| continuityOut | `spec.end_state` |
| assets | 本镜画面里出现的已确认资产名 |
| imageGenerationPrompt / videoMotionPrompt / negativePrompt / narrativeIntent / viewerPOV / timeBeats / mustHave / optionalDetails | 按柚子契约照常填，可短 |

## spec 的形状（每镜一份；字符串字段没有内容就写空串，但 `emotion_arc` / `fight` / `camera_emotion` / `expression_terms` 这四个对象或数组字段用不上时整个不写，不要写空对象、空 id 或 null）

```json
{"id": "E01S01-01", "scene": "E01S01", "purpose": "", "subject": "person|non_person", "focus": "", "size": "全景", "cam": {"z": "Z7", "y": "Y4", "x": "X2", "f": {"lens_mm": 35, "dof": "deep"}}, "move": ["fixed"],
 "camera_side": "", "blocking": [{"who": "", "at": "", "facing": "", "behind": "", "occluded_by": ""}], "action": "", "performance": {"beats": [], "restraint": "normal", "subtext": ""},
 "camera_emotion": {"focal": "", "type": "handheld_smooth"}, "emotion_arc": {"id": "arc-shock-002", "phase_from": 1, "phase_to": 2, "intensity": "normal", "trigger": ""}, "state": "",
 "light": {"source_anchor": "", "direction": "", "policy": "practicals_only", "key": "", "fill": "", "rim": "", "ratio": "", "kelvin": "", "shadow": "", "exposure": "", "change": ""},
 "lines": [{"text": "台词原文", "speaker": "", "delivery": "", "pace": "", "tone": "", "emotion": "", "placement": "画内"}], "vo": "", "sfx": "", "composition": "",
 "transition": {"in": "硬切", "out": "动作匹配切", "cut_point": "动作"}, "est": 3.5, "start_state": "", "end_state": "", "refs_hint": [], "risk_tags": [], "tag": "原文事实"}
```

`scene` 按「E集场」编号（本章就是一集，场按剧情文本里的场景标题顺序编 S01、S02…）；`id` = 场号-两位镜号。`lines[].text` 必须与剧情文本逐字一致，`speaker` 写说话人名；旁白或电话另一头 `placement` 写「画外」。

## 不做

不写配乐；不写心理描写（「意识到」「内心涌起」）；不在画面里要求可读文字，除非剧情文本明确写了「画面文字」；不把两句台词塞进一镜；不让摄像机跨到门、墙、电梯门的另一侧。
