import type { DramaScript, DramaShot, DramaShotIssue, DramaShotLine } from "@/services/api/drama";

/** 台词表里的一句（kind 为 line / vo），分镜卡选台词用。 */
export type ScriptLineOption = { id: string; scene: string; speaker: string; text: string; vo: boolean };

/** 检查问题所在的场，按出现顺序去重：「重新生成问题场」只重写这几场。整章级、认不出场的问题不算。 */
export function issueScenes(issues: DramaShotIssue[] = []) {
    return Array.from(new Set(issues.map((issue) => issue.scene || "").filter(Boolean)));
}

/** 检查问题所在的小节，按出现顺序去重：「重新生成问题小节」只重写这几节。不是按小节生成的镜头表、整章级问题不算。 */
export function issueSections(issues: DramaShotIssue[] = []) {
    return Array.from(new Set(issues.map((issue) => issue.section || "").filter(Boolean)));
}

/** 挂在这一镜（镜头表 id）上的问题。 */
export function shotIssues(issues: DramaShotIssue[] = [], shotId?: string) {
    return shotId ? issues.filter((issue) => issue.shots?.includes(shotId)) : [];
}

/** 台词表里的台词，按剧本顺序。 */
export function scriptLineOptions(script?: DramaScript): ScriptLineOption[] {
    return (script?.scenes || []).flatMap((scene) => scene.beats.filter((beat) => beat.kind === "line" || beat.kind === "vo").map((beat) => ({ id: beat.id, scene: scene.id, speaker: beat.speaker || "", text: beat.text, vo: beat.kind === "vo" })));
}

/** 快捷修复按钮上的字；没有快捷修复的问题返回 undefined（只显示文字）。 */
export function issueFixLabel(issue: DramaShotIssue) {
    if (issue.fix === "add_line") return "补进这个镜头";
    if (issue.fix === "keep_line") return "只保留在这个镜头";
    return undefined;
}

/**
 * 台词类问题的一键修复，返回要保存的镜头（只返回改了的）：add_line 把这句补进 shotId 这一镜（已经在就不动）；
 * keep_line 只留在 shotId 这一镜，问题列出的其余镜头去掉这句。
 */
export function issueFixShots(shots: DramaShot[], issue: DramaShotIssue, shotId: string): DramaShot[] {
    const lineId = issue.lineId;
    if (!lineId || !issue.fix) return [];
    const has = (shot: DramaShot) => (shot.lines || []).some((line) => line.line_id === lineId);
    if (issue.fix === "add_line") {
        const target = shots.find((shot) => shot.id === shotId);
        if (!target || has(target)) return [];
        return [{ ...target, lines: [...(target.lines || []), { line_id: lineId }] }];
    }
    return shots
        .filter((shot) => shot.id !== shotId && issue.shots?.includes(shot.id) && has(shot))
        .map((shot) => {
            const lines = (shot.lines || []).filter((line: DramaShotLine) => line.line_id !== lineId);
            const next: DramaShot = { ...shot, lines };
            if (!lines.length) delete next.lines;
            return next;
        });
}
