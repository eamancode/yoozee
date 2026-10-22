import type { StoryboardRow } from "@/types/canvas";
import type { DramaLine, DramaSegment, DramaSubShot } from "@/services/api/drama";
import type { ProjectDetail, ProjectShot, ShotRevision } from "@/services/api/projects";

import { storyboardRowsToProjectShots, type ProjectShotReplacementInput } from "./chapter-storyboard-production";

/**
 * 把章节分镜行按生成模型的单段上限装成「段」（一段 = 一次生成，段内多镜头），
 * 每段写成契约 segment 存进版本的 spec，台词拆成 lines，绑定的资产写进 refs。
 */
export function storyboardRowsToSegments(rows: StoryboardRow[], detail: ProjectDetail, sceneTitle: string, maxSeconds: number): ProjectShotReplacementInput[] {
    const shots = storyboardRowsToProjectShots(rows, detail);
    const roleByVersionId = new Map(detail.assets.flatMap((asset) => asset.primaryVersionId ? [[asset.primaryVersionId, refRole(asset.category)] as const] : []));
    const titleByVersionId = new Map(detail.assets.flatMap((asset) => asset.primaryVersionId ? [[asset.primaryVersionId, asset.title] as const] : []));
    const groups: Array<Array<{ row: StoryboardRow; shot: ProjectShotReplacementInput; seconds: number }>> = [];
    let current: Array<{ row: StoryboardRow; shot: ProjectShotReplacementInput; seconds: number }> = [];
    let total = 0;
    rows.forEach((row, index) => {
        const seconds = Math.min(maxSeconds, Math.max(1, Math.round(Number(row.durationSeconds) || 1)));
        if (current.length && total + seconds > maxSeconds) {
            groups.push(current);
            current = [];
            total = 0;
        }
        current.push({ row, shot: shots[index], seconds });
        total += seconds;
    });
    if (current.length) groups.push(current);
    return groups.map((group, index) => {
        const id = `S${String(index + 1).padStart(2, "0")}`;
        const versionIds = Array.from(new Set(group.flatMap((item) => item.shot.assetVersionIds)));
        let at = 0;
        const subShots: DramaSubShot[] = group.map(({ row, seconds }) => {
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
        const dur = at;
        const segment: DramaSegment = {
            id,
            scene: sceneTitle.trim() || "一",
            title: firstSentence(group[0].row.plotDescription) || id,
            dur,
            mode: versionIds.length ? "lock_identity" : "text_only",
            ...(versionIds.length ? { refs: versionIds.map((versionId) => ({ file: versionId, role: roleByVersionId.get(versionId) || "frame", note: titleByVersionId.get(versionId) || "" })) } : {}),
            shots: subShots,
            ...(group.map(({ row }) => row.audioEffects.trim()).filter(Boolean).length ? { sound: group.map(({ row }) => row.audioEffects.trim()).filter(Boolean).join("；") } : {}),
            ...(group[group.length - 1].row.continuityOut.trim() ? { rules: group[group.length - 1].row.continuityOut.trim() } : {}),
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
                videoPrompt: group.map(({ shot }) => shot.revision.videoPrompt || "").filter(Boolean).join("\n"),
                imagePrompt: group[0].shot.revision.imagePrompt || "",
                negativePrompt: group[0].shot.revision.negativePrompt || "",
                spec: segment as unknown as Record<string, unknown>,
            },
        };
    });
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

function parseDialogue(value: string): DramaLine[] {
    return value.split(/\n+/).map((line) => line.trim()).filter(Boolean).map((line) => {
        const match = /^([^：:]{1,12})[：:]\s*(.+)$/.exec(line);
        if (!match) return { speaker: "旁白", text: stripQuotes(line), vo: true };
        const speaker = match[1].replace(/[（(].*?[）)]/g, "").trim();
        const vo = /画外|旁白|OS|VO/i.test(match[1]);
        return { speaker: speaker || "旁白", text: stripQuotes(match[2]), ...(vo ? { vo: true } : {}) };
    });
}

function stripQuotes(value: string) {
    return value.trim().replace(/^[“「"]|[”」"]$/g, "").trim();
}

function normalizeLine(value: string) {
    return value.replace(/[\s，。！？、…,.!?—-]/g, "");
}

function firstSentence(value: string) {
    return value.trim().split(/[。！？\n]/)[0].slice(0, 16);
}

function refRole(category: string) {
    if (category === "character") return "identity";
    if (category === "environment" || category === "scene" || category === "location") return "location";
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
