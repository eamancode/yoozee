import { useMemo } from "react";
import { Button } from "antd";
import { Link } from "react-router";

import { DramaDeliveryPanel, DramaFeedbackForm } from "@/pages/projects/workbench/detail/drama-panels";

import type { FlowData } from "../hooks/use-flow-data";
import type { FlowStageKey, FlowStageView } from "../model/stages";
import { ReadError, StageLayout, SummaryRow } from "./layout";
import { secondsText } from "./stage-shots";

/**
 * 成片：先看每一段进没进成片、缺哪几段，再检查、导出、下载交付包；看片意见对着某一段记下来。
 * 精剪仍在旧版剪辑页做，这里给入口。
 */
export function StageCut({ data, stage, onGo }: { data: FlowData; stage?: FlowStageView; onGo: (stage: FlowStageKey, target?: { ep?: string; item?: string }) => void }) {
    const { project, progress, segments, allUnits, pipelineQuery } = data;
    // 进度没读到（或刷新失败）时不显示任何计数，避免把「没读到」看成「0 段缺片」。
    const counts = pipelineQuery.isError ? undefined : progress?.counts;
    const unread = pipelineQuery.isError ? "没读到" : "…";
    const groups = useMemo(() => {
        const order = new Map(allUnits.map((unit, index) => [unit.id, index]));
        const byUnit = new Map<string, typeof segments>();
        for (const segment of segments) byUnit.set(segment.unitId, [...(byUnit.get(segment.unitId) || []), segment]);
        return [...byUnit.entries()].sort((left, right) => (order.get(left[0]) ?? 0) - (order.get(right[0]) ?? 0)).map(([unitId, list]) => ({ unitId, title: allUnits.find((unit) => unit.id === unitId)?.title || list[0]?.unitTitle || "", list }));
    }, [segments, allUnits]);
    if (!project) return null;
    const totalMs = segments.reduce((sum, item) => sum + item.durationMs, 0);
    const inCutMs = segments.filter((item) => item.inCut).reduce((sum, item) => sum + item.durationMs, 0);
    const missing = counts ? counts.segments - counts.generated : undefined;
    return (
        <StageLayout>
            <div className="drama-flow-work-head">
                <div>
                    <h2>成片</h2>
                    <p>每一段进没进成片、缺哪几段都在时间线上；检查、导出、下载交付包就在这一页。</p>
                </div>
                <div className="drama-flow-work-actions">
                    <Link to={`/projects/${encodeURIComponent(project.id)}/editor`}>
                        <Button>在旧版剪辑页精剪</Button>
                    </Link>
                </div>
            </div>
            <SummaryRow
                items={[
                    { label: "段", value: counts?.segments ?? unread },
                    { label: "已出片", value: counts?.generated ?? unread },
                    { label: "已入片", value: counts?.inCut ?? unread, tone: "ok" },
                    { label: "缺片", value: missing ?? unread, tone: missing ? "warn" : undefined },
                    { label: "已验收", value: counts?.accepted ?? unread },
                    { label: "入片时长", value: counts ? `${secondsText(inCutMs)} / ${secondsText(totalMs)}` : unread },
                ]}
            />
            {pipelineQuery.isError ? (
                <ReadError what="成片进度" onRetry={() => void pipelineQuery.refetch()} />
            ) : !counts ? (
                <p className="drama-flow-muted">正在读成片进度…</p>
            ) : !counts.generated ? (
                <div className="drama-flow-empty">
                    <strong>{stage?.state === "pending" ? "还没到成片这一步" : "还没有出好的视频"}</strong>
                    <span>先出片，出好的视频会自动排进成片。</span>
                    <Button onClick={() => onGo("video")}>去出片</Button>
                </div>
            ) : (
                <>
                    <div className="drama-flow-panel">
                        <div className="drama-flow-panel-head">
                            <h3>时间线</h3>
                            <span>块的宽度按时长；灰色是还没出片，点它去出这一段</span>
                        </div>
                        {groups.map((group) => (
                            <div key={group.unitId} className="drama-flow-timeline-row">
                                <span className="drama-flow-subhead">{group.title}</span>
                                <div className="drama-flow-timeline">
                                    {group.list.map((segment) => (
                                        <button
                                            key={segment.shotId}
                                            type="button"
                                            data-state={segment.inCut ? "in" : segment.generated ? "out" : "missing"}
                                            style={{ flexGrow: Math.max(1, Math.round(segment.durationMs / 1000)) }}
                                            title={`${segment.segmentId} ${segment.title} · ${secondsText(segment.durationMs)} · ${segment.inCut ? "已入片" : segment.generated ? "出了还没入片" : "还没出片"}${segment.takes > 1 && !segment.selectedArtifactId ? " · 候选待挑" : ""}`}
                                            onClick={() => onGo("video", { ep: segment.unitId, item: segment.shotId })}
                                        >
                                            {segment.segmentId}
                                        </button>
                                    ))}
                                </div>
                            </div>
                        ))}
                    </div>
                    <DramaDeliveryPanel projectId={project.id} projectName={project.name} />
                    <div className="drama-flow-panel">
                        <div className="drama-flow-panel-head">
                            <h3>看片意见</h3>
                            <span>对着某一段记一条，记进需求文档，重做那一段时按它改</span>
                        </div>
                        <DramaFeedbackForm projectId={project.id} segments={segments} />
                    </div>
                </>
            )}
        </StageLayout>
    );
}
