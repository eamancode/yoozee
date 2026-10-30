import assert from "node:assert/strict";
import test from "node:test";

import type { DramaSectionOutput, DramaSegment, DramaShot, DramaSubShot } from "@/services/api/project-drama";

import { cardBoundAssets, sectionLayout, sectionLayoutBadge, sectionOutputFor, type SegmentAsset } from "./shot-card-layout";

const output = (patch: Partial<DramaSectionOutput>): DramaSectionOutput => ({
    id: "E01S01-P1",
    scene: "E01S01",
    heading: "内·护士站·夜",
    no: 1,
    genre: "wenxi15",
    lines: [2, 6],
    lineIds: [],
    est: 12,
    raw: "",
    stats: { blocks: 1, shots: 4, lines: 1 },
    shots: [],
    ...patch,
});

const shotsBy = (shots: DramaShot[]) => (sub: DramaSubShot) => shots.find((shot) => shot.id === sub.shot_id);

const wenxi15Spec: DramaSegment = {
    id: "S01",
    scene: "E01S01",
    dur: 12,
    mode: "lock_identity",
    section: "E01S01-P1",
    genre: "wenxi15",
    shots: [
        { at: 0, size: "全景", camera: "平视，大景深", text: "编译后的画面句", shot_id: "E01S01-01" },
        { at: 3.5, size: "中近景", camera: "过肩，浅景深", text: "编译后的画面句", shot_id: "E01S01-02", lines: [{ speaker: "林小满", text: "七床的人呢？", vo: true }] },
    ],
};
const wenxi15Shots: DramaShot[] = [
    { id: "E01S01-01", scene: "E01S01", action: "林小满转身看向门口。", extras: { 风格: "冷峻悬疑", 核心情绪: "不安" } },
    { id: "E01S01-02", scene: "E01S01", action: "林小满抬头，握紧病历夹。", extras: { 风格: "冷峻悬疑", 核心情绪: "紧张" }, blocking: [{ who: "林小满·淋湿", at: "护士站" }] },
];
const wenxi15Raw = "## 片段 3（时长：12秒）\n\n**要素提取** [角色：林小满；BGM背景音乐：低音钢琴]\n\nS1，0s-3.5s，全景、平视。林小满转身。风格：冷峻悬疑。核心情绪：不安。";

test("文戏 15 按 7.3 片段排：片段标题、要素提取（含 BGM）在头，结束站位在尾，S1 一行一镜带风格和核心情绪", () => {
    const section = output({
        raw: wenxi15Raw,
        fields: [
            { name: "要素提取", value: "[角色：林小满；场景：护士站；BGM背景音乐：低音钢琴]" },
            { name: "当前空间站位", value: "[林小满站在护士站后]" },
            { name: "最后一镜结束站位", value: "[11s-12s，林小满停在门口]" },
            { name: "下一片段继承点", value: "[病历夹、日光灯]" },
        ],
    });
    const layout = sectionLayout(wenxi15Spec, section, shotsBy(wenxi15Shots));
    assert.equal(layout.legacy, false);
    assert.equal(layout.genre, "wenxi15");
    assert.equal(layout.title, "片段 3（时长：12秒）");
    assert.deepEqual(
        layout.head.map((field) => field.label),
        ["要素提取", "当前空间站位"],
    );
    assert.match(layout.head[0].value, /BGM背景音乐：低音钢琴/);
    assert.deepEqual(
        layout.tail.map((field) => field.label),
        ["最后一镜结束站位", "下一片段继承点"],
    );
    assert.equal(layout.raw, wenxi15Raw);
    const [first, second] = layout.cards;
    assert.equal(first.code, "S1");
    assert.equal(first.span, "0s-3.5s");
    assert.equal(first.camera, "全景、平视、大景深");
    assert.equal(first.body, "林小满转身看向门口。");
    assert.deepEqual(first.tags, [
        { label: "风格", value: "冷峻悬疑" },
        { label: "核心情绪", value: "不安" },
    ]);
    assert.equal(second.span, "3.5s-12s");
    assert.deepEqual(second.lines, [{ speaker: "林小满", text: "七床的人呢？", note: "画外" }]);
    assert.equal(sectionLayoutBadge(layout), "文戏 15");
});

test("文戏 30 按即梦分镜排：小节标题，小节头在前、小节尾单列，镜头N 逐字段，台词带语速语调情感，粘贴提示词不显示", () => {
    const spec: DramaSegment = {
        id: "S02",
        scene: "E01S02",
        dur: 24,
        mode: "text_only",
        section: "E01S02-P1",
        genre: "wenxi30",
        shots: [{ at: 0, size: "中景", camera: "平视，固定", text: "画面", shot_id: "E01S02-01", light_full: "主光：顶部日光灯", lines: [{ speaker: "林小满", text: "七床？", pace: "中偏慢", tone: "尾音上扬", emotion: "疑惑" }] }],
    };
    const shots: DramaShot[] = [
        {
            id: "E01S02-01",
            scene: "E01S02",
            purpose: "建立走廊与人物。",
            action: "林小满抱着病历夹走来。",
            camera_side: "机位在走廊中线",
            extras: { 场景锚点: "日光灯一字排开", 运动方向: "由远及近", 动作连续: "开始时在走廊尽头；结束时停在门口" },
            blocking: [{ who: "林小满", at: "走廊中央", facing: "面向镜头" }],
            performance: { beats: ["眉头微皱", "嘴唇抿紧"] },
            transition: { out: "硬切，衔接点为视线" },
            start_state: "走廊空荡",
            end_state: "停在七床门口",
            sfx: "脚步声",
        },
    ];
    const section = output({
        id: "E01S02-P1",
        genre: "wenxi30",
        raw: "小节2：七床空了\n生成时长：0-24秒",
        fields: [
            { name: "小节", value: "七床空了" },
            { name: "生成时长", value: "0-24秒" },
            { name: "剧情功能", value: "发现空床" },
            { name: "小节约束", value: "无字幕" },
            { name: "即梦粘贴提示词", value: "以林小满为主体……" },
        ],
    });
    const layout = sectionLayout(spec, section, shotsBy(shots));
    assert.equal(layout.title, "小节2：七床空了");
    assert.deepEqual(
        layout.head.map((field) => field.label),
        ["生成时长", "剧情功能"],
    );
    assert.deepEqual(
        layout.tail.map((field) => field.label),
        ["小节约束"],
    );
    const card = layout.cards[0];
    assert.equal(card.code, "镜头1");
    assert.equal(card.span, "0-24秒");
    const field = (label: string) => card.fields.find((item) => item.label === label)?.value;
    assert.equal(field("镜头目的"), "建立走廊与人物。");
    assert.equal(field("镜头人物站位"), "林小满 走廊中央，面向镜头");
    assert.equal(field("镜头场景锚点"), "日光灯一字排开");
    assert.equal(field("镜头运动方向"), "由远及近");
    assert.equal(field("表情"), "眉头微皱；嘴唇抿紧");
    assert.equal(field("灯光与光影"), "主光：顶部日光灯");
    assert.equal(field("衔接"), "硬切，衔接点为视线");
    assert.equal(field("结束画面"), "停在七床门口");
    assert.deepEqual(card.lines, [{ speaker: "林小满", text: "七床？", note: "【语速（中偏慢）+语调（尾音上扬）+情感（疑惑）】" }]);
    assert.equal(sectionLayoutBadge(layout), "文戏 30");
});

test("武戏按 v7 排：核心观看体验做标题，镜 N 一秒一镜，P/R/L/F 单列成带档位的标签，空值不显示", () => {
    const spec: DramaSegment = {
        id: "S03",
        scene: "E01S02",
        dur: 2,
        mode: "lock_identity",
        section: "E01S02-P2",
        genre: "wuxi",
        shots: [
            { at: 0, size: "全景", camera: "推镜，24mm", text: "", shot_id: "E01S02-05", extras: { 核心观看体验: "以伤换位", 力度: "P1 轻·试探", 受击: "无", 战损: "L1", 摄影机目标: "阿坤", 站位: "阿坤 画右前景·面向左；周岸 画左中景" } },
            { at: 1, size: "中景", camera: "甩镜，35mm", text: "", shot_id: "E01S02-06", extras: { 力度: "P3 重·格挡", 战损: "L1→L2", 闪帧: "F2 黑闪 2 帧", 下一镜连接: "WHIP MATCH" } },
        ],
    };
    const shots: DramaShot[] = [
        { id: "E01S02-05", scene: "E01S02", action: "阿坤撞开防火门冲出→举刀扑向林小满" },
        { id: "E01S02-06", scene: "E01S02", action: "周岸右前臂下压格挡→刀身被压偏", performance: { beats: ["周岸 咬牙", "不再给"] } },
    ];
    const layout = sectionLayout(spec, output({ id: "E01S02-P2", genre: "wuxi", fields: [{ name: "核心观看体验", value: "以伤换位 + 窄走廊近身刀战" }] }), shotsBy(shots));
    assert.equal(layout.title, "【核心观看体验】以伤换位 + 窄走廊近身刀战");
    const [first, second] = layout.cards;
    assert.equal(first.code, "镜 1");
    assert.equal(first.span, "0-1s");
    assert.equal(first.camera, "全景 / 推镜 / 24mm");
    assert.deepEqual(first.tags, [
        { label: "力度", value: "P1 轻·试探", level: 1 },
        { label: "战损", value: "L1", level: 1 },
    ]);
    assert.equal(first.fields.find((field) => field.label === "动作链")?.value, "阿坤撞开防火门冲出→举刀扑向林小满");
    assert.equal(first.fields.find((field) => field.label === "摄影机目标")?.value, "阿坤");
    assert.deepEqual(
        second.tags.map((tag) => [tag.label, tag.level]),
        [
            ["力度", 3],
            ["战损", 2],
            ["闪帧", 2],
        ],
    );
    assert.equal(second.fields.find((field) => field.label === "表情")?.value, "周岸 咬牙");
    assert.equal(second.fields.find((field) => field.label === "下一镜连接")?.value, "WHIP MATCH");
    assert.equal(sectionLayoutBadge(layout), "武戏");
});

test("原样输出缺失（卡 F 之前的段、没存下、戏类对不上）标旧数据、按统一镜头表显示；镜头表还没读到时不急着标旧数据", () => {
    const legacySpec: DramaSegment = { id: "S01", scene: "E01S01", dur: 8, mode: "text_only", shots: [{ at: 0, size: "中景", camera: "平视", text: "林小满走进病房" }] };
    const legacy = sectionLayout(legacySpec, undefined, () => undefined);
    assert.equal(legacy.legacy, true);
    assert.equal(sectionLayoutBadge(legacy), "旧数据");
    assert.equal(legacy.cards[0].body, "林小满走进病房");
    assert.equal(legacy.cards[0].span, "0–8s");
    assert.equal(sectionLayout(wenxi15Spec, undefined, shotsBy(wenxi15Shots)).legacy, true);
    assert.equal(sectionOutputFor(wenxi15Spec, [output({ genre: "wuxi" })]), undefined);
    assert.ok(sectionOutputFor(wenxi15Spec, [output({})]));
    const pending = sectionLayout(wenxi15Spec, undefined, shotsBy(wenxi15Shots), false);
    assert.equal(pending.legacy, false);
    assert.equal(pending.title, "片段 1（时长：12秒）");
});

test("卡下的绑定资产按这一镜实际的参考（refs）列，不按名字：写「小满」也显示绑的淋湿子卡，子卡没图退回主卡时显示主卡", () => {
    const assets: SegmentAsset[] = [
        { id: "palette", title: "E02S01 色卡", kind: "palette", versionIds: ["v-palette"], preview: "", code: "E02S01" },
        { id: "wet", title: "林小满·淋湿", kind: "character", versionIds: ["v-wet"], preview: "/wet.png" },
        { id: "lin", title: "林小满", kind: "character", versionIds: ["v-lin"], preview: "/lin.png" },
        { id: "zhou", title: "周岸", kind: "character", versionIds: ["v-zhou"], preview: "" },
        { id: "chart", title: "病历夹", kind: "prop", versionIds: ["v-chart"], preview: "" },
        { id: "station", title: "护士站", kind: "scene", versionIds: ["v-station"], preview: "" },
    ];
    const sub = (text: string, refs?: string[]): DramaSubShot => ({ at: 0, text, ...(refs ? { refs } : {}) });
    const spec = (shots: DramaSubShot[], files: string[]): Pick<DramaSegment, "shots" | "refs"> => ({ shots, refs: files.map((file) => ({ file, role: "identity" })) });
    const ids = (list: SegmentAsset[]) => list.map((asset) => asset.id);

    // 名字写成「小满」、绑定是淋湿子卡：显示淋湿；文字里提到病历夹但没绑也不列；写错名字的周岸按绑定仍列出。
    const wetShot = sub("小满抬头，握紧病历夹，阿岸在身后", ["v-wet", "v-station", "v-zhou"]);
    const segment = spec([wetShot, sub("空镜", ["v-station"])], ["v-wet", "v-zhou", "v-station"]);
    assert.deepEqual(ids(cardBoundAssets(wetShot, segment, assets)), ["wet", "zhou", "station", "palette"]);
    assert.deepEqual(ids(cardBoundAssets(segment.shots[1], segment, assets)), ["station", "palette"]);

    // 子卡没图、装段退回主卡：refs 是主卡的版本，卡下显示主卡，不显示淋湿。
    const fallback = sub("林小满·淋湿抬头", ["v-lin"]);
    assert.deepEqual(ids(cardBoundAssets(fallback, spec([fallback], ["v-lin"]), assets)), ["lin", "palette"]);

    // 在制作台取消了绑定（不在段 refs 里了）的不再列；给整段补绑、没分到任何一镜的每张都列。
    const edited = spec([wetShot, sub("空镜", ["v-station"])], ["v-wet", "v-station", "v-chart"]);
    assert.deepEqual(ids(cardBoundAssets(wetShot, edited, assets)), ["wet", "chart", "station", "palette"]);
    assert.deepEqual(ids(cardBoundAssets(edited.shots[1], edited, assets)), ["chart", "station", "palette"]);

    // 旧段的镜头没有 refs：这一段绑的资产每张都列。
    const legacy = sub("林小满走进病房");
    assert.deepEqual(ids(cardBoundAssets(legacy, spec([legacy], ["v-lin"]), assets)), ["wet", "lin", "zhou", "chart", "station", "palette"]);
});
