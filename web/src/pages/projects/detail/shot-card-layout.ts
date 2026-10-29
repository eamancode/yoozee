import { dramaShotGenreLabel, type DramaLine, type DramaSectionField, type DramaSectionOutput, type DramaSegment, type DramaShot, type DramaShotGenreId, type DramaSubShot } from "@/services/api/drama";

/**
 * 分镜卡按戏类版式显示（M8 卡 G）：同一套统一镜头表，按小节的戏类排成技能原本的分镜卡样子——
 * 文戏 15 是 7.3 的「片段 + S1… 一行一镜」，文戏 30 是即梦的「小节头 + 镜头N 逐字段」，武戏是 v7 的「镜 N｜1 秒 1 镜 + P/R/L/F」。
 * 每张卡的内容取镜头表（逐张编辑改的就是它，改完版式跟着变）和它的 extras（技能原生、镜头表没有对应字段的标签）；
 * 小节级的原样字段（7.3 的要素提取与站位、即梦的小节头尾、武戏的核心观看体验）和整节原文取 sections_json。只管显示，不改数据。
 */

export type CardLayoutGenre = DramaShotGenreId | "legacy";

export type CardField = { label: string; value: string };
/** 一个标签；level 是武戏 P/R/L/F 的档位（1–5，战损写成 L1→L2 时取后一个），着色用。 */
export type CardTag = { label: string; value: string; level?: number };
export type CardLine = { speaker: string; text: string; note?: string };

export type ShotCardModel = {
    /** 技能原生的镜头号：7.3「S1」、即梦「镜头1」、武戏「镜 1」。 */
    code: string;
    /** 段内时间：7.3「0.5s-3.5s」、即梦「0-5秒」、武戏「0-1s」。 */
    span: string;
    /** 镜头语言：7.3 一行开头的景别、机位运镜；武戏「景别 / 焦段 / 运镜」；即梦没有（在字段里）。 */
    camera: string;
    /** 7.3 一行里镜头语言之后的画面动作（武戏、即梦为空，在字段里）。 */
    body: string;
    fields: CardField[];
    tags: CardTag[];
    lines: CardLine[];
};

export type SectionLayoutModel = {
    genre: CardLayoutGenre;
    /** 版式说明：「文戏 15 · 7.3 片段」。 */
    label: string;
    /** 技能原生的小节标题：「片段 2（时长：12秒）」「小节1：七床空了」「【核心观看体验】…」；旧数据为空。 */
    title: string;
    head: CardField[];
    tail: CardField[];
    cards: ShotCardModel[];
    /** 这一节生成时的原样输出（含 BGM 等不进镜头表的内容）；没存时为空。 */
    raw: string;
    /** 旧数据：段不是按小节生成的，或这一节的原样输出没存下，按统一镜头表显示。 */
    legacy: boolean;
};

const LAYOUT_LABELS: Record<CardLayoutGenre, string> = {
    wenxi15: "文戏 15 · 7.3 片段",
    wenxi30: "文戏 30 · 即梦分镜",
    wuxi: "武戏 · v7 一秒一镜",
    legacy: "统一镜头表",
};

/** 即梦小节尾的字段（小节约束、三个连续性总结……）；其余小节级字段是小节头。粘贴提示词不显示：视频提示词从镜头表编译，不用它。 */
const WENXI30_TAIL = new Set(["小节约束", "小节人物场景站位标注", "小节动作连续性总结", "小节场景连续性总结", "小节声音连续性总结", "台词核对摘要", "自检摘要"]);
const WENXI30_HIDDEN = new Set(["小节", "即梦粘贴提示词"]);
/** 7.3 片段尾的字段（其余是片段头）。 */
const WENXI15_TAIL = ["最后一镜结束站位", "下一片段继承点"];
/** 武戏 extras 里在卡上单列成标签的四项（其余进字段）。 */
const WUXI_TAGS = ["力度", "受击", "战损", "闪帧"];
const EMPTY_VALUES = new Set(["无", "无。", "—", "-", "不再给"]);

/** 段内秒数：去掉多余的 0（3.0 → 3，0.50 → 0.5）。 */
export function formatSeconds(value: number) {
    return String(Math.round(value * 10) / 10);
}

function present(value?: string) {
    const text = (value || "").trim();
    return text && !EMPTY_VALUES.has(text) ? text : "";
}

function fields(pairs: Array<[string, string | undefined]>): CardField[] {
    return pairs.flatMap(([label, value]) => (present(value) ? [{ label, value: present(value) }] : []));
}

function joinWords(parts: Array<string | undefined>, sep: string) {
    return parts
        .map((part) => (part || "").trim())
        .filter(Boolean)
        .join(sep);
}

/** 统一镜头表里一镜的站位：「谁 在哪，面向…，背后…」逐人一句。 */
function blockingText(shot?: DramaShot) {
    return (shot?.blocking || []).map((item) => joinWords([`${item.who}${item.at ? ` ${item.at}` : ""}`, item.facing, item.behind && `背后是${item.behind.replace(/^背后(是)?/, "")}`], "，")).join("；");
}

function cardLines(sub: DramaSubShot, withDelivery: boolean): CardLine[] {
    return (sub.lines || []).map((line: DramaLine) => {
        const delivery = withDelivery ? joinWords([line.pace && `语速（${line.pace}）`, line.tone && `语调（${line.tone}）`, line.emotion && `情感（${line.emotion}）`], "+") : "";
        return { speaker: line.speaker, text: line.text, note: joinWords([line.vo ? "画外" : "", delivery && `【${delivery}】`], " ") || undefined };
    });
}

function levelOf(value: string) {
    const levels = Array.from(value.matchAll(/[PRLF]([1-5])/g), (match) => Number(match[1]));
    return levels.length ? levels[levels.length - 1] : undefined;
}

function rawHeading(raw: string, pattern: RegExp) {
    for (const line of raw.split("\n")) {
        const match = pattern.exec(line.trim());
        if (match) return match;
    }
    return undefined;
}

function sectionField(section: DramaSectionOutput | undefined, name: string) {
    return section?.fields?.find((field: DramaSectionField) => field.name === name)?.value?.trim() || "";
}

/** 一节对应的原样输出：小节编号和戏类都对得上才算（改过戏类、按新编号重切后旧的不算）。 */
export function sectionOutputFor(spec: DramaSegment, sections?: DramaSectionOutput[]) {
    if (!spec.section) return undefined;
    return sections?.find((item) => item.id === spec.section && item.genre === spec.genre);
}

/**
 * 一段（= 一节）的版式。sectionKnown 为 false 表示还不知道有没有原样输出（镜头表在读、是草稿）：按戏类版式显示，不标旧数据。
 * shotOf 取这张卡在镜头表里的镜头（逐张编辑改的就是它）；取不到时用段里的镜头（编译后的文字）。
 */
export function sectionLayout(spec: DramaSegment, section: DramaSectionOutput | undefined, shotOf: (sub: DramaSubShot) => DramaShot | undefined, sectionKnown = true): SectionLayoutModel {
    const genre: CardLayoutGenre = spec.section && spec.genre ? spec.genre : "legacy";
    const raw = section?.raw?.trim() || "";
    const legacy = genre === "legacy" || (sectionKnown && !section);
    const ends = spec.shots.map((_, index) => spec.shots[index + 1]?.at ?? spec.dur);
    const base = { genre: legacy ? ("legacy" as const) : genre, label: LAYOUT_LABELS[legacy ? "legacy" : genre], raw, legacy };
    if (legacy) return { ...base, title: "", head: [], tail: [], cards: spec.shots.map((sub, index) => legacyCard(sub, index, ends[index])) };
    const cards = spec.shots.map((sub, index) => {
        const shot = shotOf(sub);
        if (genre === "wenxi15") return wenxi15Card(sub, shot, index, ends[index]);
        if (genre === "wenxi30") return wenxi30Card(sub, shot, index, ends[index]);
        return wuxiCard(sub, shot, index, ends[index]);
    });
    if (genre === "wenxi15") {
        const header = rawHeading(raw, /片段\s*(\d+)\s*[（(]\s*时长/);
        const tailOf = (field: DramaSectionField) => WENXI15_TAIL.some((name) => field.name.startsWith(name));
        return {
            ...base,
            title: `片段 ${header?.[1] || section?.no || 1}（时长：${formatSeconds(spec.dur)}秒）`,
            head: fields((section?.fields || []).filter((field) => !tailOf(field)).map((field) => [field.name, field.value])),
            tail: fields((section?.fields || []).filter(tailOf).map((field) => [field.name, field.value])),
            cards,
        };
    }
    if (genre === "wenxi30") {
        const header = rawHeading(raw, /^[#*\s]*小节\s*(\d+)\s*[：:]\s*(.*?)\**$/);
        const name = sectionField(section, "小节") || header?.[2] || "";
        const all = (section?.fields || []).filter((field) => !WENXI30_HIDDEN.has(field.name));
        return {
            ...base,
            title: `小节${header?.[1] || section?.no || 1}${name ? `：${name}` : ""}`,
            head: fields(all.filter((field) => !WENXI30_TAIL.has(field.name)).map((field) => [field.name, field.value])),
            tail: fields(all.filter((field) => WENXI30_TAIL.has(field.name)).map((field) => [field.name, field.value])),
            cards,
        };
    }
    const experience = sectionField(section, "核心观看体验") || present((spec.shots[0]?.extras || shotOf(spec.shots[0])?.extras || {})["核心观看体验"]);
    return { ...base, title: experience ? `【核心观看体验】${experience}` : "", head: [], tail: [], cards };
}

function legacyCard(sub: DramaSubShot, index: number, end: number): ShotCardModel {
    return { code: String(index + 1), span: `${formatSeconds(sub.at)}–${formatSeconds(end)}s`, camera: joinWords([sub.size, sub.camera], " · "), body: sub.text, fields: [], tags: [], lines: cardLines(sub, false) };
}

/** 7.3：S1，0.5s-3.5s，景别、运镜。画面动作。风格：X。核心情绪：Y。角色（台词）：“…” */
function wenxi15Card(sub: DramaSubShot, shot: DramaShot | undefined, index: number, end: number): ShotCardModel {
    const extras = shot?.extras || {};
    return {
        code: `S${index + 1}`,
        span: `${formatSeconds(sub.at)}s-${formatSeconds(end)}s`,
        camera: joinWords([sub.size || shot?.size, ...(sub.camera || "").split("，")], "、"),
        body: present(shot?.action) || sub.text,
        fields: [],
        tags: fields([
            ["风格", extras["风格"]],
            ["核心情绪", extras["核心情绪"]],
        ]),
        lines: cardLines(sub, false),
    };
}

/** 即梦：镜头N（a-b秒）下逐字段写，字段顺序照技能。 */
function wenxi30Card(sub: DramaSubShot, shot: DramaShot | undefined, index: number, end: number): ShotCardModel {
    const extras = shot?.extras || {};
    return {
        code: `镜头${index + 1}`,
        span: `${formatSeconds(sub.at)}-${formatSeconds(end)}秒`,
        camera: "",
        body: "",
        fields: fields([
            ["镜头目的", shot?.purpose || sub.purpose],
            ["镜头人物站位", blockingText(shot) || sub.positions],
            ["镜头场景锚点", extras["场景锚点"] || sub.anchors],
            ["镜头机位坐标", shot?.camera_side],
            ["镜头运动方向", extras["运动方向"]],
            ["镜头动作连续", extras["动作连续"]],
            ["景别与镜头", joinWords([sub.size || shot?.size, sub.camera], "，")],
            ["构图", shot?.composition || sub.composition],
            ["动作", shot?.action],
            ["表情", performanceOf(shot) || sub.expression],
            ["画外音", shot?.vo || sub.vo],
            ["声音", shot?.sfx || sub.sound],
            ["灯光与光影", sub.light_full],
            ["衔接", shot?.transition?.out || sub.transition?.out],
            ["起始画面", shot?.start_state || sub.start_state],
            ["结束画面", shot?.end_state || sub.end_state],
        ]),
        tags: [],
        lines: cardLines(sub, true),
    };
}

/** v7：镜 N｜a-bs，景别 / 焦段 / 运镜，摄影机目标、站位、动作链、P/R/L/F、特效、环境反馈、表情、台词、光影、下一镜连接。 */
function wuxiCard(sub: DramaSubShot, shot: DramaShot | undefined, index: number, end: number): ShotCardModel {
    const extras = { ...(shot?.extras || {}), ...(sub.extras || {}) };
    return {
        code: `镜 ${index + 1}`,
        span: `${formatSeconds(sub.at)}-${formatSeconds(end)}s`,
        camera: joinWords([sub.size || shot?.size, ...(sub.camera || "").split("，")], " / "),
        body: "",
        fields: fields([
            ["摄影机目标", extras["摄影机目标"]],
            ["站位", extras["站位"] || blockingText(shot) || sub.positions],
            ["动作链", shot?.action],
            ["特效", extras["特效"]],
            ["环境反馈", extras["环境反馈"]],
            ["表情", performanceOf(shot) || sub.expression],
            ["光影", shot?.light?.key || shot?.light?.source_anchor || sub.light_full],
            ["下一镜连接", extras["下一镜连接"] || sub.transition?.out],
            ["剧本底", extras["剧本底"]],
        ]),
        tags: WUXI_TAGS.flatMap((label) => {
            const value = present(extras[label]);
            return value ? [{ label, value, level: levelOf(value) }] : [];
        }),
        lines: cardLines(sub, false),
    };
}

function performanceOf(shot?: DramaShot) {
    return (shot?.performance?.beats || []).filter((beat) => present(beat)).join("；");
}

/** 版式名（段卡头上用）：戏类名，旧数据写「旧数据」。 */
export function sectionLayoutBadge(layout: Pick<SectionLayoutModel, "genre" | "legacy">) {
    return layout.legacy ? "旧数据" : dramaShotGenreLabel(layout.genre);
}

/**
 * 一段绑的一项资产（工作台按这一段的资产引用算好）：person 是角色卡对应的人（子卡是主卡名），code 是色卡的场编号，
 * preview 是缩略图地址（没有图时为空，显示占位）。
 */
export type SegmentAsset = { id: string; title: string; kind: "character" | "prop" | "scene" | "palette" | "other"; person: string; preview: string; code?: string };

const ASSET_ORDER: Record<SegmentAsset["kind"], number> = { character: 0, prop: 1, scene: 2, palette: 3, other: 4 };

/**
 * 一张卡下面的绑定资产：角色只列这一镜在场的（站位、焦点、摄影机目标、台词说话人、画面里写到的；主卡名、子卡名、「主卡名·任意」都算同一个人），
 * 道具只列这一镜写到的，场景和本场色卡整段共用、每张都列。资产都来自这一段的绑定，不另猜。
 */
export function cardBoundAssets(sub: DramaSubShot, shot: DramaShot | undefined, assets: SegmentAsset[]) {
    const names = new Set<string>();
    const addName = (name?: string) => {
        const value = (name || "").trim();
        if (!value) return;
        names.add(value);
        names.add(value.split("·")[0].trim());
    };
    for (const item of shot?.blocking || []) addName(item.who);
    addName(shot?.focus);
    addName((shot?.extras || sub.extras || {})["摄影机目标"]?.split(/[\s·，,]/)[0]);
    for (const line of sub.lines || []) addName(line.speaker);
    const text = [shot?.action, sub.text, sub.positions, ...Object.values({ ...(shot?.extras || {}), ...(sub.extras || {}) })].filter(Boolean).join(" ");
    const has = (asset: SegmentAsset) => {
        if (asset.kind === "scene" || asset.kind === "palette") return true;
        if (asset.kind === "character") return names.has(asset.title) || names.has(asset.person) || text.includes(asset.title) || text.includes(asset.person);
        return text.includes(asset.title);
    };
    return assets.filter(has).sort((left, right) => ASSET_ORDER[left.kind] - ASSET_ORDER[right.kind]);
}
