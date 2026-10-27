import type { DramaScene } from "@/services/api/drama";

/** 场次在正文里的定位：第一个场次头的文字，和它是正文里同样文字的第几个（从 0 数）；没有场次头（未命名场、整章一场）时为空。 */
export type SceneAnchor = { text: string; occurrence: number };

function compact(text: string) {
    return text.replace(/^#+\s*/, "").replace(/\s+/g, "");
}

export function sceneAnchor(scenes: DramaScene[], index: number): SceneAnchor | undefined {
    const head = scenes[index]?.sourceHeadings[0];
    if (!head) return undefined;
    const text = compact(head.text);
    let occurrence = 0;
    for (const scene of scenes) {
        for (const source of scene.sourceHeadings) {
            if (source.line >= head.line) return { text, occurrence };
            if (compact(source.text) === text) occurrence += 1;
        }
    }
    return { text, occurrence };
}

/** 在编辑器的文本块里找场次头：texts 是各文本块的文字（按文档顺序），返回第几个文本块；找不到返回 -1。 */
export function sceneAnchorBlock(texts: string[], anchor: SceneAnchor) {
    let seen = 0;
    for (let i = 0; i < texts.length; i += 1) {
        if (compact(texts[i]) !== anchor.text) continue;
        if (seen === anchor.occurrence) return i;
        seen += 1;
    }
    return -1;
}

/** 场次的地点和时间：「病房走廊 · 夜」，时间有变化时「电梯口 · 夜→晨」；读不出地点时用场次头。 */
export function sceneLabel(scene: DramaScene) {
    const parts = [scene.location || scene.heading, scene.times.join("→")].filter(Boolean);
    return parts.join(" · ");
}
