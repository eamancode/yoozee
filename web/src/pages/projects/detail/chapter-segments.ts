import type { VideoCapabilityConfig } from "@/lib/model-capabilities";
import type { StoryboardRow } from "@/types/canvas";
import type { DramaLine, DramaSegment, DramaSubShot } from "@/services/api/drama";
import type { ProjectAsset, ProjectDetail, ProjectShot, ShotRevision } from "@/services/api/projects";

import { storyboardRowsToProjectShots, type ProjectShotReplacementInput } from "./chapter-storyboard-production";

/** 段长目标：一场戏装成 15–25 秒的段，不贴着模型上限装满。 */
export const DEFAULT_SEGMENT_TARGET = { min: 15, max: 25 };

/** 段长规则：项目生成模型一段的上限，叠加所选视频渠道实际能提交的时长。 */
export type SegmentDurationRule = {
    /** 一段内容最长多少秒。 */
    maxSeconds: number;
    /** 渠道能提交的最短秒数。 */
    minSeconds: number;
    /** 内容 n 秒时实际提交的段长（向上取到渠道档位）；超过上限或没有合法档位时为 undefined。 */
    submitSeconds: (seconds: number) => number | undefined;
    /** 给用户看的时长规则，如「15/30 秒」「5–15 秒」。 */
    label: string;
    /** 渠道规则和项目生成模型冲突、一段都交不了时的说明。 */
    problem?: string;
};

/**
 * 段长规则：familyMaxSeconds 是项目生成模型一段的上限（models.json duration.max），
 * channel 是所选视频渠道的 capabilityConfig.video.duration。档位制（enum）向上取到合法档位，
 * 按秒（range）的不足最短秒数时取最短秒数。
 */
export function segmentDurationRule(familyMaxSeconds: number, channel?: VideoCapabilityConfig["duration"], familyLabel = "项目生成模型"): SegmentDurationRule {
    const familyMax = familyMaxSeconds > 0 ? familyMaxSeconds : 15;
    if (channel?.selection === "enum") {
        const values = Array.from(new Set((channel.values || []).filter((value) => value > 0))).sort((left, right) => left - right);
        const allowed = values.filter((value) => value <= familyMax);
        if (!allowed.length) {
            return {
                maxSeconds: familyMax,
                minSeconds: 1,
                submitSeconds: () => undefined,
                label: values.length ? `${values.join("/")} 秒` : "未声明时长",
                problem: values.length ? `所选视频渠道只能生成 ${values.join("/")} 秒，超过${familyLabel}一段的上限 ${familyMax} 秒` : "所选视频渠道没有声明可选时长",
            };
        }
        return { maxSeconds: allowed[allowed.length - 1], minSeconds: allowed[0], submitSeconds: (seconds) => allowed.find((value) => value >= seconds), label: `${allowed.join("/")} 秒` };
    }
    if (channel?.selection === "range") {
        const min = Math.max(1, channel.min || 1);
        const step = Math.max(1, channel.step || 1);
        const top = Math.min(familyMax, channel.max || familyMax);
        if (top < min) {
            return { maxSeconds: familyMax, minSeconds: min, submitSeconds: () => undefined, label: `最短 ${min} 秒`, problem: `所选视频渠道最短 ${min} 秒，超过${familyLabel}一段的上限 ${familyMax} 秒` };
        }
        const max = min + Math.floor((top - min) / step) * step;
        return {
            maxSeconds: max,
            minSeconds: min,
            submitSeconds: (seconds) => seconds > max ? undefined : Math.max(min, min + Math.ceil((seconds - min) / step) * step),
            label: min === max ? `${min} 秒` : `${min}–${max} 秒`,
        };
    }
    return { maxSeconds: familyMax, minSeconds: 1, submitSeconds: (seconds) => seconds > familyMax ? undefined : Math.max(1, Math.ceil(seconds)), label: `最长 ${familyMax} 秒` };
}

export type SegmentPlanOptions = {
    /** 分镜行都没绑场景时，整章按一场处理，场景名用章节标题。 */
    chapterTitle: string;
    rule: SegmentDurationRule;
    /** 段长目标区间（秒），默认 15–25；上限仍是 rule.maxSeconds。 */
    target?: { min: number; max: number };
    /** 每段参考图上限（项目生成模型的 refs.images）。 */
    maxRefs?: number;
};

export type SegmentPlan = { shots: ProjectShotReplacementInput[]; notes: string[] };

type PlannedRow = { row: StoryboardRow; shot: ProjectShotReplacementInput; seconds: number };
type PlannedScene = { name: string; rows: PlannedRow[]; versionIds: string[] };

const SCENE_CATEGORIES = new Set(["scene", "environment", "location"]);
const LOCKED_CUT = 1000;
const NEUTRAL_CUT = 4;

/**
 * 把章节分镜行装成「段」（一段 = 一次生成，段内多镜头）：先按绑定的场景资产切成场，
 * 每场内按段长目标挑切点——优先景别跳切和台词开头，不在两句接话之间切；段长按渠道档位取合法值。
 * 同一场的各段共用这一场所有镜头绑定资产的并集作为参考。
 */
export function storyboardRowsToSegments(rows: StoryboardRow[], detail: ProjectDetail, options: SegmentPlanOptions): SegmentPlan {
    const shots = storyboardRowsToProjectShots(rows, detail);
    const { rule } = options;
    const assetByVersionId = new Map(detail.assets.flatMap((asset) => asset.primaryVersionId ? [[asset.primaryVersionId, asset] as const] : []));
    const targetMax = Math.min(options.target?.max ?? DEFAULT_SEGMENT_TARGET.max, rule.maxSeconds);
    const target = { min: Math.min(options.target?.min ?? DEFAULT_SEGMENT_TARGET.min, targetMax), max: targetMax };
    // 渠道规则和项目模型冲突时仍按内容时长装段（后端只认项目模型上限），但要明确告诉用户这个渠道交不了。
    const submit = (seconds: number) => rule.problem ? (seconds <= rule.maxSeconds ? Math.max(1, Math.ceil(seconds)) : undefined) : rule.submitSeconds(seconds);
    const notes: string[] = [];
    if (rule.problem) notes.push(`${rule.problem}：段按内容时长写入，这个渠道提交不了，先换视频渠道或改项目生成模型`);

    const scenes = groupRowsByScene(rows, shots, detail.assets, options.chapterTitle, rule.maxSeconds);
    const groups: Array<{ scene: PlannedScene; rows: PlannedRow[]; refs: string[]; lockedCutBefore: boolean }> = [];
    for (const scene of scenes) {
        const refs = orderedRefs(scene.versionIds, assetByVersionId);
        if (options.maxRefs && refs.length > options.maxRefs) notes.push(`「${scene.name}」绑定了 ${refs.length} 个资产，一段最多带 ${options.maxRefs} 个参考，只带前 ${options.maxRefs} 个`);
        const sceneRefs = options.maxRefs ? refs.slice(0, options.maxRefs) : refs;
        splitScene(scene.rows, submit, rule.maxSeconds, target).forEach((range, index) => {
            groups.push({ scene, rows: scene.rows.slice(range.start, range.end), refs: sceneRefs, lockedCutBefore: index > 0 && range.locked });
        });
    }

    const padded: string[] = [];
    const result = groups.map((group, index) => {
        const id = segmentId(index);
        if (group.lockedCutBefore) notes.push(`${segmentId(index - 1)} 和 ${id} 之间拆开了一段连续对话：这段对话超过 ${rule.maxSeconds} 秒，一段放不下`);
        let at = 0;
        const subShots: DramaSubShot[] = group.rows.map(({ row, seconds }) => {
            const shot: DramaSubShot = {
                at,
                ...(row.shotSize.trim() ? { size: row.shotSize.trim() } : {}),
                ...([row.camera, row.motion].some((value) => value.trim()) ? { camera: [row.camera.trim(), row.motion.trim()].filter(Boolean).join("，") } : {}),
                text: [row.plotDescription.trim(), row.performanceBlocking.trim()].filter(Boolean).join("。") || row.videoMotionPrompt.trim() || "（待补充画面）",
            };
            const lines = parseDialogue(row.dialogue);
            if (lines.length) shot.lines = lines;
            at += seconds;
            return shot;
        });
        const content = at;
        const dur = submit(content) ?? content;
        if (dur > content) padded.push(`${id} 内容 ${content} 秒按 ${dur} 秒生成`);
        const versionIds = group.refs;
        const sounds = group.rows.map(({ row }) => row.audioEffects.trim()).filter(Boolean);
        const segment: DramaSegment = {
            id,
            scene: group.scene.name,
            title: firstSentence(group.rows[0].row.plotDescription) || id,
            dur,
            mode: versionIds.length ? "lock_identity" : "text_only",
            ...(versionIds.length ? { refs: versionIds.map((versionId) => ({ file: versionId, role: refRole(assetByVersionId.get(versionId)?.category || ""), note: assetByVersionId.get(versionId)?.title || "" })) } : {}),
            shots: subShots,
            ...(sounds.length ? { sound: sounds.join("；") } : {}),
            ...(group.rows[group.rows.length - 1].row.continuityOut.trim() ? { rules: group.rows[group.rows.length - 1].row.continuityOut.trim() } : {}),
        };
        const description = subShots.map((shot) => shot.text).join("；");
        return {
            title: `${id} ${segment.title}`,
            description,
            durationMs: dur * 1000,
            assetVersionIds: versionIds.slice(0, 6),
            revision: {
                plotDescription: description,
                dialogue: subShots.flatMap((shot) => shot.lines || []).map((line) => `${line.speaker}：${line.text}`).join("\n"),
                durationMs: dur * 1000,
                videoPrompt: group.rows.map(({ shot }) => shot.revision.videoPrompt || "").filter(Boolean).join("\n"),
                imagePrompt: group.rows[0].shot.revision.imagePrompt || "",
                negativePrompt: group.rows[0].shot.revision.negativePrompt || "",
                spec: segment as unknown as Record<string, unknown>,
            },
        };
    });
    if (padded.length) notes.push(`按渠道时长（${rule.label}）提交：${padded.join("，")}，剪辑时裁掉多出的部分`);
    return { shots: result, notes };
}

/** 一行镜头属于哪一场：绑定里优先级最高的场景资产；没有就沿用上一场，开头没有就取后面第一场。 */
function groupRowsByScene(rows: StoryboardRow[], shots: ProjectShotReplacementInput[], assets: ProjectAsset[], chapterTitle: string, maxSeconds: number): PlannedScene[] {
    const assetById = new Map(assets.map((asset) => [asset.id, asset]));
    const own = rows.map((row) => {
        const binding = row.assetBindings.slice().sort((left, right) => right.priority - left.priority).find((item) => {
            const asset = assetById.get(item.nodeId);
            return asset && (SCENE_CATEGORIES.has(asset.category) || (item.role === "environment" && asset.category !== "character"));
        });
        const asset = binding ? assetById.get(binding.nodeId) : undefined;
        return asset ? { key: asset.id, name: asset.title } : undefined;
    });
    const fallback = { key: "", name: chapterTitle.trim() || "一" };
    let current = own.find(Boolean) || fallback;
    const scenes: PlannedScene[] = [];
    let lastKey: string | undefined;
    rows.forEach((row, index) => {
        current = own[index] || current;
        const planned = { row, shot: shots[index], seconds: Math.min(maxSeconds, Math.max(1, Math.round(Number(row.durationSeconds) || 1))) };
        if (lastKey === current.key) {
            const scene = scenes[scenes.length - 1];
            scene.rows.push(planned);
            scene.versionIds.push(...planned.shot.assetVersionIds);
        } else {
            scenes.push({ name: current.name, rows: [planned], versionIds: [...planned.shot.assetVersionIds] });
        }
        lastKey = current.key;
    });
    return scenes;
}

/**
 * 一场之内挑切点（动态规划）：每段不超过上限且能按渠道档位提交；
 * 代价 = 偏离段长目标的秒数 + 向上取档多出的秒数 + 切点代价（接话之间 1000、景别跳切或台词开头 0、其余 4）。
 */
function splitScene(rows: PlannedRow[], submit: (seconds: number) => number | undefined, maxSeconds: number, target: { min: number; max: number }) {
    const prefix = [0];
    for (const item of rows) prefix.push(prefix[prefix.length - 1] + item.seconds);
    const best = new Array<number>(rows.length + 1).fill(Number.POSITIVE_INFINITY);
    const from = new Array<number>(rows.length + 1).fill(-1);
    best[0] = 0;
    for (let end = 1; end <= rows.length; end += 1) {
        for (let start = end - 1; start >= 0; start -= 1) {
            const seconds = prefix[end] - prefix[start];
            if (seconds > maxSeconds) break;
            const submitted = submit(seconds);
            if (submitted === undefined || !Number.isFinite(best[start])) continue;
            const deviation = seconds < target.min ? target.min - seconds : seconds > target.max ? seconds - target.max : 0;
            const cost = best[start] + deviation + (submitted - seconds) + (start > 0 ? cutCost(rows[start - 1].row, rows[start].row) : 0);
            if (cost < best[end]) {
                best[end] = cost;
                from[end] = start;
            }
        }
    }
    const ranges: Array<{ start: number; end: number; locked: boolean }> = [];
    for (let end = rows.length; end > 0; end = from[end]) {
        const start = from[end];
        if (start < 0) throw new Error("分镜行装段失败：有镜头超过一段上限");
        ranges.unshift({ start, end, locked: start > 0 && cutCost(rows[start - 1].row, rows[start].row) >= LOCKED_CUT });
    }
    return ranges;
}

function cutCost(before: StoryboardRow, after: StoryboardRow) {
    const beforeSpeaks = parseDialogue(before.dialogue).length > 0;
    const afterSpeaks = parseDialogue(after.dialogue).length > 0;
    if (beforeSpeaks && afterSpeaks) return LOCKED_CUT;
    if (afterSpeaks) return 0;
    const left = shotSizeRank(before.shotSize);
    const right = shotSizeRank(after.shotSize);
    if (left && right && Math.abs(left - right) >= 2) return 0;
    return NEUTRAL_CUT;
}

const SHOT_SIZE_RANKS: Array<[string, number]> = [["大远景", 1], ["远景", 1], ["全景", 2], ["中近景", 4], ["中景", 3], ["近景", 5], ["大特写", 7], ["特写", 6]];

function shotSizeRank(value: string) {
    const text = value.trim();
    return SHOT_SIZE_RANKS.find(([label]) => text.includes(label))?.[1] || 0;
}

/** 参考按用途排：角色、场景、道具、其他；同类按出场顺序。 */
function orderedRefs(versionIds: string[], assetByVersionId: Map<string, ProjectAsset>) {
    const unique = Array.from(new Set(versionIds));
    const rank = (versionId: string) => ["identity", "location", "prop", "frame"].indexOf(refRole(assetByVersionId.get(versionId)?.category || ""));
    return unique.map((versionId, index) => ({ versionId, index })).sort((left, right) => rank(left.versionId) - rank(right.versionId) || left.index - right.index).map((item) => item.versionId);
}

function segmentId(index: number) {
    return `S${String(index + 1).padStart(2, "0")}`;
}

/** 台词覆盖率：章节里引号中的台词，有多少原样出现在本章各段的 lines 里。 */
export function chapterDialogueCoverage(sourceText: string, shots: ProjectShot[], revisions: ShotRevision[]) {
    const quoted = Array.from(sourceText.matchAll(/[“「"]([^”」"]{2,})[”」"]/g), (match) => normalizeLine(match[1])).filter(Boolean);
    const expected = Array.from(new Set(quoted));
    const revisionById = new Map(revisions.map((revision) => [revision.id, revision]));
    const spoken = shots.flatMap((shot) => {
        const spec = parseSpec(revisionById.get(shot.currentRevisionId || "")?.specJson || "");
        return (spec?.shots || []).flatMap((sub) => (sub.lines || []).map((line) => normalizeLine(line.text)));
    }).join("\n");
    const covered = expected.filter((line) => spoken.includes(line));
    return { expected: expected.length, covered: covered.length, missing: expected.filter((line) => !spoken.includes(line)) };
}

// 模型常把「没有台词」写成「无」「（无）」「-」：不能当成一句旁白，也不能当成接话。
const NO_DIALOGUE = /^[（(]?\s*(无|无台词|没有台词|none|n\/a|-+|—+)\s*[）)]?$/i;

/** 一行一句：「说话人：台词」；说话人里带「画外 / 旁白」算画外音，没有说话人的算旁白。 */
export function parseDialogue(value: string): DramaLine[] {
    return value.split(/\n+/).map((line) => line.trim()).filter((line) => line && !NO_DIALOGUE.test(line)).map((line) => {
        const match = /^([^：:]{1,12})[：:]\s*(.+)$/.exec(line);
        if (!match) return { speaker: "旁白", text: stripQuotes(line), vo: true };
        const speaker = match[1].replace(/[（(].*?[）)]/g, "").trim();
        const vo = /画外|旁白|OS|VO/i.test(match[1]);
        return { speaker: speaker || "旁白", text: stripQuotes(match[2]), ...(vo ? { vo: true } : {}) };
    });
}

/** parseDialogue 的反向：编辑框里每行一句。 */
export function formatDialogue(lines: DramaLine[] = []) {
    return lines.map((line) => `${line.speaker}${line.vo && line.speaker !== "旁白" ? "（画外）" : ""}：${line.text}`).join("\n");
}

function stripQuotes(value: string) {
    return value.trim().replace(/^[“「"]|[”」"]$/g, "").trim();
}

function normalizeLine(value: string) {
    return value.replace(/[\s，。！？、…,.!?—-]/g, "");
}

export function firstSentence(value: string) {
    return value.trim().split(/[。！？\n]/)[0].slice(0, 16);
}

function refRole(category: string) {
    if (category === "character") return "identity";
    if (SCENE_CATEGORIES.has(category)) return "location";
    if (category === "prop") return "prop";
    return "frame";
}

function parseSpec(specJson: string): DramaSegment | undefined {
    if (!specJson.trim()) return undefined;
    try {
        return JSON.parse(specJson) as DramaSegment;
    } catch {
        return undefined;
    }
}
