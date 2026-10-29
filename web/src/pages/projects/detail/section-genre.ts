import { dramaShotGenreLabel, type DramaSegmentView } from "@/services/api/drama";

/** 小节改戏类的目标：文戏节改为武戏，武戏节改回文戏（文戏按项目视频模型单段上限选 15 / 30，由服务端定）。 */
export type SectionGenreTarget = "wuxi" | "wenxi";

export function sectionGenreTarget(genre?: string): SectionGenreTarget {
    return genre === "wuxi" ? "wenxi" : "wuxi";
}

/**
 * 这次改戏类会覆盖的段：改为武戏只动这一节；改回文戏时同一场里与它相连的一串武戏节一起回到文戏（它们是一起按武戏切出来的）。
 * 段按分段列表顺序；不是按小节装的段（没有 section）不参与。
 */
export function sectionGenreSpan(list: DramaSegmentView[], index: number): DramaSegmentView[] {
    const current = list[index];
    if (!current?.spec.section) return [];
    if (current.spec.genre !== "wuxi") return [current];
    const sameRun = (view?: DramaSegmentView) => Boolean(view?.spec.section) && view?.spec.genre === "wuxi" && view?.spec.scene === current.spec.scene;
    let start = index;
    let end = index;
    while (start > 0 && sameRun(list[start - 1])) start--;
    while (end < list.length - 1 && sameRun(list[end + 1])) end++;
    return list.slice(start, end + 1);
}

/** 改戏类确认框：写明会覆盖哪几段、按什么口径重切、下游产物会过期（有生成结果的段点出数量）。 */
export function sectionGenreConfirm(span: DramaSegmentView[], target: SectionGenreTarget, maxSeconds: number | undefined, takesOf: (shotId: string) => number) {
    const sections = span.map((view) => view.spec.section || view.spec.id);
    const segments = span.map((view) => view.spec.id);
    const from = dramaShotGenreLabel(span[0]?.spec.genre);
    const limit = maxSeconds ? `每节不超过 ${maxSeconds} 秒` : "每节不超过项目视频模型的单段上限";
    const takes = span.reduce((sum, view) => sum + takesOf(view.shotId), 0);
    const lines =
        target === "wuxi"
            ? [
                  `${sections.join("、")}（${from}）这一节的剧本范围按武戏口径重新切成 1～N 个武戏小节（N 秒 N 镜，${limit}），用武戏技能重写，直接覆盖现在的文戏版。`,
                  `段 ${segments.join("、")} 会换成武戏内容，它的分镜图、动作预演和视频会标成过期；同一场里切法跟着变的节一起重写，其余小节的段和产物不动。`,
              ]
            : [
                  `${sections.join("、")}（武戏）${span.length > 1 ? "是一起按武戏切出来的，" : ""}一起改回文戏：这段剧本范围按文戏口径（项目视频模型单段上限 15 秒用文戏 15，30 秒用文戏 30）重切重写，直接覆盖现在的武戏版。`,
                  `段 ${segments.join("、")} 会换成文戏内容，它们的分镜图、动作预演和视频会标成过期（多出来的武戏段删掉）；其余小节的段和产物不动。`,
              ];
    if (takes) lines.push(`这些段已有 ${takes} 个生成结果（分镜图、预演或视频），覆盖后它们对应改动前的内容，需要重新生成。`);
    lines.push("会建一个生成分段任务（只重写受影响的小节），写完自动重检、装段。");
    return {
        title: target === "wuxi" ? `把 ${sections.join("、")} 改为武戏？` : `把 ${sections.join("、")} 改回文戏？`,
        lines,
        okText: target === "wuxi" ? "改为武戏并重写" : "改回文戏并重写",
    };
}
