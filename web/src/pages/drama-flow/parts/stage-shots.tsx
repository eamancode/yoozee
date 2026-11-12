import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Segmented } from "antd";

import { dramaSegmentsKey, DramaSegmentCard, useInvalidateDrama } from "@/pages/projects/workbench/detail/drama-panels";
import type { SegmentAsset } from "@/pages/projects/workbench/detail/shot-card-layout";
import { unitEpisodeNumber } from "@/pages/projects/workbench/detail/unit-numbering";
import { assetPreviewUrl } from "@/pages/projects/workbench/detail/workflow-shot-references";
import { listDramaSegments, type DramaSegmentProgress, type DramaSegmentView, type DramaSubShot } from "@/services/api/project-drama";
import type { ProjectAsset, ProjectUnitWorkspace } from "@/services/api/projects";

import type { FlowData } from "../hooks/use-flow-data";
import { useStoryboardRun, type StoryboardRun } from "../hooks/use-storyboard-run";
import type { FlowStageView } from "../model/stages";
import { DetailPane, ReadError, StageLayout, SummaryRow } from "./layout";

export { unitWorkspaceKey } from "../hooks/use-flow-data";

export const secondsText = (ms: number) => `${Math.round(ms / 100) / 10} 秒`;
const DENSITY_KEY = "drama-flow:shots-density";

/** 这一段绑了哪些资产：段的资产引用 + 段 refs 里写的资产版本，都对回资产（和旧工作台同一套口径）。 */
export function boundAssetsOf(workspace: ProjectUnitWorkspace | undefined, segment: DramaSegmentView): SegmentAsset[] {
    if (!workspace) return [];
    const byVersion = new Map(workspace.assets.filter((asset) => asset.primaryVersionId).map((asset) => [asset.primaryVersionId as string, asset]));
    const byId = new Map<string, SegmentAsset>();
    const add = (asset: ProjectAsset | undefined, versionId: string, style = false) => {
        if (!asset || !versionId) return;
        const kind: SegmentAsset["kind"] = style || asset.category === "palette" ? "palette" : asset.category === "character" ? "character" : asset.category === "prop" ? "prop" : asset.category === "environment" ? "scene" : "other";
        const current = byId.get(asset.id) || { id: asset.id, title: asset.title, kind, versionIds: [], preview: assetPreviewUrl(asset), code: asset.palette?.code };
        if (!current.versionIds.includes(versionId)) current.versionIds.push(versionId);
        byId.set(asset.id, current);
    };
    for (const reference of workspace.shotReferences || []) {
        if (reference.shotId !== segment.shotId || reference.status !== "linked") continue;
        add(reference.asset || byVersion.get(reference.assetVersionId), reference.assetVersionId, reference.role === "style");
    }
    for (const ref of segment.spec.refs || []) add(byVersion.get(ref.file), ref.file);
    return Array.from(byId.values());
}

/** 镜头时长：下一镜的起点减这一镜的起点，最后一镜用段长收尾。 */
export function shotSeconds(shots: DramaSubShot[], index: number, segmentSeconds: number) {
    const end = index + 1 < shots.length ? shots[index + 1].at : segmentSeconds;
    return Math.max(0, Math.round((end - shots[index].at) * 10) / 10);
}

/** 分镜：一集的镜头表状态、汇总、每段的镜头和绑定资产都直接显示；点一段在右侧就地改（拆段、合段、改镜头都在那里）。 */
export function StageShots({ data, stage, selectedId, onSelect }: { data: FlowData; stage?: FlowStageView; selectedId?: string; onSelect: (shotId?: string) => void }) {
    const { project, unit, unitId, workspace, workspaceQuery, pipelineQuery, unitSegments, allUnits, segmentRule } = data;
    // 段本身来自分段接口；超时长、问题、参考这些标记来自进度接口：哪个没读到就写「没读到」，不按 0 算。
    const segmentsUnread = (pending: boolean) => (pending ? "…" : "没读到");
    const projectId = project?.id || "";
    const run = useStoryboardRun(data);
    const invalidateDrama = useInvalidateDrama(projectId);
    const segmentsQuery = useQuery({ queryKey: dramaSegmentsKey(projectId, unitId), queryFn: async ({ signal }) => (await listDramaSegments(projectId, unitId, signal)).segments, enabled: Boolean(projectId && unitId) });
    const [density, setDensity] = useState<"full" | "compact">(() => (localStorage.getItem(DENSITY_KEY) === "compact" ? "compact" : "full"));
    const episode = useMemo(() => unitEpisodeNumber(allUnits, unitId), [allUnits, unitId]);
    const progressByShot = useMemo(() => new Map(unitSegments.map((item) => [item.shotId, item])), [unitSegments]);
    const staleShots = useMemo(() => new Set((workspace?.shotArtifacts || []).filter((artifact) => artifact.type === "video" && artifact.status === "stale").map((artifact) => artifact.shotId)), [workspace?.shotArtifacts]);
    const takeCounts = useMemo(() => {
        const counts: Record<string, number> = {};
        for (const artifact of workspace?.shotArtifacts || []) if (artifact.unitId === unitId) counts[artifact.shotId] = (counts[artifact.shotId] || 0) + 1;
        return counts;
    }, [workspace?.shotArtifacts, unitId]);
    const busyShotIds = useMemo(() => new Set((workspace?.tasks || []).flatMap((task) => (task.clientContext?.shotId && (task.status === "queued" || task.status === "running") ? [task.clientContext.shotId] : []))), [workspace?.tasks]);
    const focusRef = useRef<HTMLElement | null>(null);
    // 函数体要带大括号：新版 Chrome 的 scrollIntoView 返回 Promise，箭头函数隐式返回它，React 会把它当清理函数调用而崩溃。
    useEffect(() => {
        focusRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
    }, [selectedId, segmentsQuery.data]);

    const segments = segmentsQuery.data || [];
    const selected = segments.find((segment) => segment.shotId === selectedId);
    const totalMs = segments.reduce((sum, item) => sum + item.durationMs, 0);
    const shotCount = segments.reduce((sum, item) => sum + item.spec.shots.length, 0);
    const overLimit = unitSegments.filter((item) => item.overLimit).length;
    const withIssues = unitSegments.filter((item) => (item.issues ?? 0) > 0).length;
    const noRefs = unitSegments.filter((item) => !item.refs).length;
    const genreRule = segmentRule ? { maxSeconds: segmentRule.maxSeconds, minSeconds: segmentRule.minSeconds } : undefined;

    const pane = selected ? (
        <DetailPane
            title={`${selected.spec.id} ${selected.title || selected.spec.title || ""}`.trim()}
            subtitle={`${secondsText(selected.durationMs)} · ${selected.spec.shots.length} 个镜头 · 场 ${selected.spec.scene}`}
            onClose={() => onSelect(undefined)}
        >
            <DramaSegmentCard
                projectId={projectId}
                unitId={unitId}
                shotId={selected.shotId}
                revisionId={selected.revisionId}
                specJson={JSON.stringify(selected.spec)}
                durationRule={segmentRule}
                genreRule={genreRule}
                takeCounts={takeCounts}
                busyShotIds={busyShotIds}
                episode={episode}
                boundAssets={boundAssetsOf(workspace, selected)}
                onEdited={async (selectShotId) => {
                    if (selectShotId && selectShotId !== selected.shotId) onSelect(selectShotId);
                    await Promise.all([workspaceQuery.refetch(), invalidateDrama(), segmentsQuery.refetch()]);
                }}
            />
        </DetailPane>
    ) : undefined;

    return (
        <StageLayout pane={pane}>
            <div className="drama-flow-work-head">
                <div>
                    <h2>分镜{unit?.title ? ` · ${unit.title}` : ""}</h2>
                    <p>一段出一条视频。每段的镜头、台词、绑定的角色场景道具都在下面；点一段在右边直接改。</p>
                </div>
                <div className="drama-flow-work-actions">
                    <Segmented<"full" | "compact">
                        size="small"
                        value={density}
                        onChange={(value) => {
                            setDensity(value);
                            localStorage.setItem(DENSITY_KEY, value);
                        }}
                        options={[
                            { value: "full", label: "完整" },
                            { value: "compact", label: "紧凑" },
                        ]}
                    />
                </div>
            </div>
            <ShotlistBar run={run} segmentCount={segments.length} />
            <SummaryRow
                items={[
                    { label: "段", value: segmentsQuery.data && !segmentsQuery.isError ? segments.length : segmentsUnread(segmentsQuery.isLoading) },
                    { label: "镜头", value: segmentsQuery.data && !segmentsQuery.isError ? shotCount : segmentsUnread(segmentsQuery.isLoading) },
                    { label: "总时长", value: segmentsQuery.data && !segmentsQuery.isError ? secondsText(totalMs) : segmentsUnread(segmentsQuery.isLoading) },
                    { label: "单段上限", value: segmentRule ? `${segmentRule.maxSeconds} 秒` : "没读到" },
                    { label: "超时长", value: pipelineQuery.isError || !pipelineQuery.data ? segmentsUnread(pipelineQuery.isLoading) : overLimit, tone: overLimit ? "bad" : undefined },
                    { label: "有问题", value: pipelineQuery.isError || !pipelineQuery.data ? segmentsUnread(pipelineQuery.isLoading) : withIssues, tone: withIssues ? "bad" : undefined },
                    { label: "没参考", value: pipelineQuery.isError || !pipelineQuery.data ? segmentsUnread(pipelineQuery.isLoading) : noRefs, tone: noRefs ? "warn" : undefined },
                ]}
            />
            {segmentsQuery.isError ? <ReadError what="分镜" onRetry={() => void segmentsQuery.refetch()} /> : null}
            {pipelineQuery.isError ? <ReadError what="每段的检查标记（超时长、问题、参考）" onRetry={() => void pipelineQuery.refetch()} /> : null}
            {workspaceQuery.isError ? <ReadError what="每段绑定的资产和出片状态" onRetry={() => void workspaceQuery.refetch()} /> : null}
            {segmentsQuery.isError ? null : segments.length ? (
                <div className="drama-flow-segments" data-density={density}>
                    {segments.map((segment, index) => (
                        <SegmentBlock
                            key={segment.shotId}
                            ref={segment.shotId === selectedId ? (node) => void (focusRef.current = node) : undefined}
                            index={index}
                            segment={segment}
                            info={progressByShot.get(segment.shotId)}
                            assets={boundAssetsOf(workspace, segment)}
                            stale={staleShots.has(segment.shotId)}
                            busy={busyShotIds.has(segment.shotId)}
                            compact={density === "compact"}
                            selected={segment.shotId === selectedId}
                            onSelect={() => onSelect(segment.shotId === selectedId ? undefined : segment.shotId)}
                        />
                    ))}
                </div>
            ) : segmentsQuery.isLoading ? (
                <div className="drama-flow-empty">
                    <strong>正在读分镜…</strong>
                </div>
            ) : (
                <div className="drama-flow-empty">
                    <strong>{stage?.state === "running" || run.running ? "正在写分镜" : "这一集还没有分镜"}</strong>
                    <span>{stage?.state === "running" || run.running ? "写好一节出现一节，不用守着。" : "定妆做完后会自动开始；也可以点上面的「生成这一集分镜」。"}</span>
                </div>
            )}
        </StageLayout>
    );
}

function ShotlistBar({ run, segmentCount }: { run: StoryboardRun; segmentCount: number }) {
    const { shotlist, running, stream, latest, problemSections, repack, generate, code } = run;
    const errors = shotlist?.check.errors ?? 0;
    const warnings = shotlist?.check.warnings ?? 0;
    const status = running
        ? `正在写分镜${stream.shotCount ? ` · 已出 ${stream.shotCount} 镜` : ""}`
        : !shotlist
          ? latest?.status === "failed"
              ? `上一次生成失败：${latest.error || "没写原因"}`
              : "还没有镜头表"
          : shotlist.draft
            ? `镜头表有 ${errors} 个问题，存成了草稿，还没装段`
            : `镜头表已装成 ${segmentCount} 段${shotlist.accepted ? "（带着问题装的，问题标在段上）" : ""}${warnings ? ` · ${warnings} 条提醒` : ""}`;
    return (
        <div className="drama-flow-note" data-tone={shotlist?.draft || latest?.status === "failed" ? "warn" : undefined}>
            <div className="drama-flow-bar-row">
                <span>
                    {status}
                    {shotlist ? ` · 第 ${shotlist.version} 版 · 风格档案 ${shotlist.shotlist.profile || "默认"}` : ""}
                </span>
                <div className="drama-flow-work-actions">
                    {shotlist?.draft && problemSections.length ? (
                        <Button size="small" type="primary" disabled={running} onClick={() => void generate(problemSections)}>
                            只重写有问题的 {problemSections.length} 节
                        </Button>
                    ) : null}
                    {shotlist?.draft ? (
                        <Button size="small" disabled={running} loading={repack.isPending && repack.variables?.accept} onClick={() => repack.mutate({ accept: true })}>
                            带着问题先装段
                        </Button>
                    ) : null}
                    {shotlist && !shotlist.draft ? (
                        <Button size="small" disabled={running} loading={repack.isPending && !repack.variables?.accept} onClick={() => repack.mutate({})}>
                            重新装段
                        </Button>
                    ) : null}
                    <Button size="small" loading={running} onClick={() => void generate()}>
                        {shotlist ? "整集重新生成" : "生成这一集分镜"}
                    </Button>
                </div>
            </div>
            {shotlist?.draft && shotlist.issues?.length ? (
                <ul className="drama-flow-issues">
                    {shotlist.issues.slice(0, 12).map((issue, index) => (
                        <li key={`${issue.rule}-${index}`} data-level={issue.level}>
                            <strong>{issue.section ? code(issue.section) : issue.scene ? code(issue.scene) : "镜头表"}</strong>
                            <span>{issue.message}</span>
                            <code>{issue.rule}</code>
                        </li>
                    ))}
                </ul>
            ) : null}
            {running && run.streamText ? (
                <details>
                    <summary>看模型正在写的内容</summary>
                    <pre className="drama-flow-stream">{run.streamText.slice(-4000)}</pre>
                </details>
            ) : null}
        </div>
    );
}

function SegmentBlock({
    ref,
    index,
    segment,
    info,
    assets,
    stale,
    busy,
    compact,
    selected,
    onSelect,
}: {
    ref?: (node: HTMLElement | null) => void;
    index: number;
    segment: DramaSegmentView;
    info?: DramaSegmentProgress;
    assets: SegmentAsset[];
    stale: boolean;
    busy: boolean;
    compact: boolean;
    selected: boolean;
    onSelect: () => void;
}) {
    const issues = info?.issues ?? segment.issues?.length ?? 0;
    const bad = Boolean(info?.overLimit) || issues > 0;
    const shots = segment.spec.shots;
    const segmentSeconds = segment.durationMs / 1000;
    const shown = compact ? shots.slice(0, 1) : shots;
    return (
        <article ref={ref} className="drama-flow-seg" data-bad={bad || undefined} aria-current={selected || undefined}>
            <button type="button" className="drama-flow-seg-head" aria-expanded={selected} onClick={onSelect}>
                <span className="drama-flow-seg-no">{segment.spec.id || `第 ${index + 1} 段`}</span>
                <strong>{segment.title || segment.spec.title || `第 ${index + 1} 段`}</strong>
                <span className="drama-flow-seg-meta">
                    {secondsText(segment.durationMs)} · {shots.length} 镜 · 场 {segment.spec.scene}
                </span>
                <span className="drama-flow-seg-tags">
                    {info?.overLimit ? (
                        <span className="drama-flow-chip" data-tone="bad">
                            超时长
                        </span>
                    ) : null}
                    {issues ? (
                        <span className="drama-flow-chip" data-tone="bad">
                            {issues} 处问题
                        </span>
                    ) : null}
                    {info && !info.refs ? (
                        <span className="drama-flow-chip" data-tone="warn">
                            没参考
                        </span>
                    ) : null}
                    {stale ? (
                        <span className="drama-flow-chip" data-tone="warn">
                            出片后改过
                        </span>
                    ) : null}
                    {busy ? (
                        <span className="drama-flow-chip" data-tone="run">
                            生成中
                        </span>
                    ) : info?.generated ? (
                        <span className="drama-flow-chip" data-tone="ok">
                            已出片
                        </span>
                    ) : null}
                </span>
            </button>
            <table className="drama-flow-shot-table">
                <tbody>
                    {shown.map((shot, shotIndex) => (
                        <tr key={shot.shot_id || shotIndex}>
                            <td className="drama-flow-shot-no">{shotIndex + 1}</td>
                            <td className="drama-flow-shot-cam">
                                {[shot.size, shot.camera].filter(Boolean).join(" · ") || "—"}
                                <em>{shotSeconds(shots, shotIndex, segmentSeconds)} 秒</em>
                            </td>
                            <td>
                                <p>{shot.text}</p>
                                {shot.lines?.length ? (
                                    <ul className="drama-flow-lines">
                                        {shot.lines.map((line, lineIndex) => (
                                            <li key={line.line_id || lineIndex}>
                                                <b>
                                                    {line.speaker}
                                                    {line.vo ? "（画外）" : ""}
                                                </b>
                                                {line.delivery ? <i>{line.delivery}</i> : null}
                                                {line.text}
                                            </li>
                                        ))}
                                    </ul>
                                ) : null}
                            </td>
                        </tr>
                    ))}
                    {compact && shots.length > 1 ? (
                        <tr>
                            <td colSpan={3} className="drama-flow-muted">
                                还有 {shots.length - 1} 个镜头，切到「完整」或点开这一段看
                            </td>
                        </tr>
                    ) : null}
                </tbody>
            </table>
            {assets.length ? (
                <div className="drama-flow-seg-assets" aria-label="绑定的资产">
                    {assets.map((asset) => (
                        <span key={asset.id} title={asset.title} data-kind={asset.kind}>
                            {asset.preview ? <img src={asset.preview} alt="" loading="lazy" /> : null}
                            {asset.code || asset.title}
                        </span>
                    ))}
                </div>
            ) : null}
        </article>
    );
}
