import type { Editor } from "@tiptap/react";
import { useReducedMotion } from "motion/react";

import { Callout } from "@/components/ui/product/callout";
import { Tooltip } from "@/components/ui/base/tooltip";
import type { DramaScene, DramaScenes } from "@/services/api/drama";

import { sceneAnchor, sceneAnchorBlock, sceneLabel } from "./scene-anchor";

type ChapterSceneListProps = {
    /** 这一章（集）的场次表：按已保存的正文切。 */
    scenes?: DramaScenes;
    editor: Editor | null;
    /** 正文有未保存修改：场次表按已保存的正文切。 */
    dirty: boolean;
};

/** 章节页正文上方的场次表（同一集里连续同地点的场次头并成一场）：编号、地点、时间、行数，点一场滚到它的场次头。 */
export function ChapterSceneList({ scenes: data, editor, dirty }: ChapterSceneListProps) {
    const reducedMotion = useReducedMotion();
    // 没有场次头的原文（小说正文整章一场）不显示。
    if (!data?.scenes.some((scene) => scene.sourceHeadings.length > 0)) return null;

    const scrollTo = (index: number) => {
        if (!editor) return;
        const anchor = sceneAnchor(data.scenes, index);
        const positions: number[] = [];
        const texts: string[] = [];
        editor.state.doc.descendants((node, pos) => {
            if (!node.isTextblock) return true;
            positions.push(pos);
            texts.push(node.textContent);
            return false;
        });
        const block = anchor ? sceneAnchorBlock(texts, anchor) : 0;
        if (block < 0 || positions[block] === undefined) return;
        const dom = editor.view.nodeDOM(positions[block]);
        if (dom instanceof HTMLElement) dom.scrollIntoView({ block: "start", behavior: reducedMotion ? "auto" : "smooth" });
    };

    return (
        <div className="shrink-0 border-b border-border/70 px-4 py-2">
            {data.notice ? (
                <Callout tone="warning" title="场次规则已更新，建议重新生成" className="mb-2">
                    {data.notice.message}
                </Callout>
            ) : null}
            <div className="flex items-center gap-2">
                <span className="shrink-0 text-[var(--fs-tiny)] font-medium text-foreground/45">
                    场次 {data.scenes.length}
                    {dirty ? "（按已保存的正文）" : ""}
                </span>
                <ol className="thin-scrollbar flex min-w-0 flex-1 gap-1.5 overflow-x-auto pb-0.5" aria-label="场次表">
                    {data.scenes.map((scene, index) => (
                        <li key={scene.id} className="shrink-0">
                            <Tooltip title={sceneTooltip(scene)}>
                                <button
                                    type="button"
                                    onClick={() => scrollTo(index)}
                                    className="flex items-center gap-1.5 rounded-md border border-border/70 bg-foreground/[.025] px-2 py-1 text-[var(--fs-tiny)] text-foreground/70 hover:border-[var(--workspace-accent)] hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--workspace-accent)]"
                                    aria-label={`跳到 ${scene.id} ${scene.heading}`}
                                >
                                    <span className="font-mono tabular-nums text-foreground/45">{scene.id}</span>
                                    <span className="max-w-[12rem] truncate">{sceneLabel(scene)}</span>
                                    <span className="tabular-nums text-foreground/38">{scene.lineCount} 行</span>
                                </button>
                            </Tooltip>
                        </li>
                    ))}
                </ol>
            </div>
        </div>
    );
}

function sceneTooltip(scene: DramaScene) {
    const lines = [`${scene.heading}（第 ${scene.lines[0]}–${scene.lines[1]} 行）`];
    if (scene.sourceHeadings.length > 1) lines.push(`正文里连续 ${scene.sourceHeadings.length} 个同地点场次头并成这一场`);
    if (scene.shotScene !== scene.id) lines.push(`镜头表里写作 ${scene.shotScene}`);
    return lines.join("；");
}
