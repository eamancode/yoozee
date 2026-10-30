import type { VideoCapabilityConfig } from "@/lib/model-capabilities";
import type { DramaLine, DramaSegment } from "@/services/api/project-drama";
import type { ProjectShot, ShotRevision } from "@/services/api/projects";

/** 段长目标：一场戏装成 15–25 秒的段，不贴着模型上限装满。 */
export const DEFAULT_SEGMENT_TARGET = { min: 15, max: 25 };

/** 对话框里的段长目标：按一段上限截断，上限不到目标下限时（如单段 15 秒）只说一个数。 */
export function segmentTargetLabel(maxSeconds: number) {
    const low = Math.min(DEFAULT_SEGMENT_TARGET.min, maxSeconds);
    const high = Math.min(DEFAULT_SEGMENT_TARGET.max, maxSeconds);
    return low === high ? `${high} 秒` : `${low}–${high} 秒`;
}

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
            submitSeconds: (seconds) => (seconds > max ? undefined : Math.max(min, min + Math.ceil((seconds - min) / step) * step)),
            label: min === max ? `${min} 秒` : `${min}–${max} 秒`,
        };
    }
    return { maxSeconds: familyMax, minSeconds: 1, submitSeconds: (seconds) => (seconds > familyMax ? undefined : Math.max(1, Math.ceil(seconds))), label: `最长 ${familyMax} 秒` };
}

const SCENE_CATEGORIES = new Set(["scene", "environment", "location"]);

/** 台词覆盖率：章节里引号中的台词，有多少原样出现在本章各段的 lines 里。 */
export function chapterDialogueCoverage(sourceText: string, shots: ProjectShot[], revisions: ShotRevision[]) {
    const quoted = Array.from(sourceText.matchAll(/[“「"]([^”」"]{2,})[”」"]/g), (match) => normalizeLine(match[1])).filter(Boolean);
    const expected = Array.from(new Set(quoted));
    const revisionById = new Map(revisions.map((revision) => [revision.id, revision]));
    const spoken = shots
        .flatMap((shot) => {
            const spec = parseSpec(revisionById.get(shot.currentRevisionId || "")?.specJson || "");
            return (spec?.shots || []).flatMap((sub) => (sub.lines || []).map((line) => normalizeLine(line.text)));
        })
        .join("\n");
    const covered = expected.filter((line) => spoken.includes(line));
    return { expected: expected.length, covered: covered.length, missing: expected.filter((line) => !spoken.includes(line)) };
}

// 模型常把「没有台词」写成「无」「（无）」「-」：不能当成一句旁白，也不能当成接话。
const NO_DIALOGUE = /^[（(]?\s*(无|无台词|没有台词|none|n\/a|-+|—+)\s*[）)]?$/i;

/** 一行一句：「说话人：台词」；说话人里带「画外 / 旁白」算画外音，没有说话人的算旁白。 */
export function parseDialogue(value: string): DramaLine[] {
    return value
        .split(/\n+/)
        .map((line) => line.trim())
        .filter((line) => line && !NO_DIALOGUE.test(line))
        .map((line) => {
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
    return value
        .trim()
        .replace(/^[“「"]|[”」"]$/g, "")
        .trim();
}

function normalizeLine(value: string) {
    return value.replace(/[\s，。！？、…,.!?—-]/g, "");
}

export function firstSentence(value: string) {
    return value
        .trim()
        .split(/[。！？\n]/)[0]
        .slice(0, 16);
}

/** 资产分类 → 段参考的用途（identity 人物、location 场景、prop 道具，其余只作构图参考）。 */
export function segmentRefRole(category: string) {
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
