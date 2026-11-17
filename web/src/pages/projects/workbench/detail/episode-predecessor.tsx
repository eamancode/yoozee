import type { OntologyDocument } from "@/services/api/project-drama";
import type { ProjectUnit } from "@/services/api/projects";

import { unitsOfKind } from "./unit-numbering";

type Props = {
    units: Pick<ProjectUnit, "id" | "kind" | "position" | "title">[];
    unitId: string;
    handoffs: Pick<OntologyDocument, "unitId" | "version" | "content">[];
};

export function EpisodePredecessor({ units, unitId, handoffs }: Props) {
    const episodes = unitsOfKind(units, "episode").units;
    const index = episodes.findIndex((unit) => unit.id === unitId);
    if (index < 0) return null;
    if (index === 0) return <p>当前为首集，无前序剧集。</p>;

    const previous = episodes[index - 1];
    const handoff = handoffs.find((doc) => doc.unitId === previous.id);
    return (
        <section aria-label="前序剧集">
            <h3 className="font-medium">当前前序 · 第 {index} 集《{previous.title}》</h3>
            {handoff?.content.trim() ? (
                <details className="mt-2">
                    <summary className="cursor-pointer text-sm">查看前序交接卡（第 {handoff.version} 版）</summary>
                    <blockquote className="mt-2 whitespace-pre-wrap break-words border-l-2 border-border pl-3">{handoff.content}</blockquote>
                </details>
            ) : <p className="mt-1 text-sm">前序交接卡待更新</p>}
        </section>
    );
}
