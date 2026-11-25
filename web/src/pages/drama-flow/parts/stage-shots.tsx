import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Segmented } from "antd";

import { dramaSegmentsKey, DramaSegmentCard, useInvalidateDrama } from "@/pages/projects/workbench/detail/drama-panels";
import { unitEpisodeNumber } from "@/pages/projects/workbench/detail/unit-numbering";
import { projectWorkflow } from "@/lib/project-workflow";
import { DirectorSetup } from "@/pages/projects/workbench/detail/sss-project-panels";
import { listDramaSegments, type DramaSegmentProgress, type DramaSegmentView, type DramaSubShot } from "@/services/api/project-drama";

import type { FlowData } from "../hooks/use-flow-data";
import { useStoryboardRun, type StoryboardRun } from "../hooks/use-storyboard-run";
import { directorPhaseText, directorRoundsText, directorRunView } from "../model/director-run";
import { durationText } from "../model/runs";
import { boundAssetsOf, type UnitBinding } from "../model/shot-bindings";
import type { FlowStageView } from "../model/stages";
import { DetailPane, ReadError, StageLayout, SummaryRow } from "./layout";
import { modelSummaryItem } from "./model-card";
import { ShotBindingEditor } from "./shot-binding-editor";
import { DeleteSegmentButton } from "./delete-segment-button";
import { SceneReferenceReviewDialog } from "./scene-reference-review";

export { unitWorkspaceKey } from "../hooks/use-flow-data";

export const secondsText = (ms: number) => `${Math.round(ms / 100) / 10} 秒`;
const DENSITY_KEY = "drama-flow:shots-density";

export { boundAssetsOf };

/** 镜头时长：下一镜的起点减这一镜的起点，最后一镜用段长收尾。 */
export function shotSeconds(shots: DramaSubShot[], index: number, segmentSeconds: number) {
    const end = index + 1 < shots.length ? shots[index + 1].at : segmentSeconds;
    return Math.max(0, Math.round((end - shots[index].at) * 10) / 10);
}

/** 分镜：一集的镜头表状态、汇总、每段的镜头和绑定资产都直接显示；点一段在右侧就地改（拆段、合段、改镜头都在那里）。 */
export function StageShots({ data, stage, selectedId, onSelect }: { data: FlowData; stage?: FlowStageView; selectedId?: string; onSelect: (shotId?: string) => void }) {
    const { project, unit, unitId, workspace, workspaceQuery, pipelineQuery, unitSegments, allUnits, segmentRule, stepModels } = data;
    // 段本身来自分段接口；超时长、问题、参考这些标记来自进度接口：哪个没读到就写「没读到」，不按 0 算。
    const segmentsUnread = (pending: boolean) => (pending ? "…" : "没读到");
    const projectId = project?.id || "";
    const run = useStoryboardRun(data);
    const director = project ? projectWorkflow(project.workflowId).storyboardMode !== "" : false;
    const invalidateDrama = useInvalidateDrama(projectId);
    const segmentsQuery = useQuery({ queryKey: dramaSegmentsKey(projectId, unitId), queryFn: async ({ signal }) => (await listDramaSegments(projectId, unitId, signal)).segments, enabled: Boolean(projectId && unitId) });
    const [density, setDensity] = useState<"full" | "compact">(() => (localStorage.getItem(DENSITY_KEY) === "compact" ? "compact" : "full"));
    const [editing, setEditing] = useState<{ unitId: string; shotId: string }>();
    const [reviewing, setReviewing] = useState<{ unitId: string; shotId: string }>();
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
    const bindingSegment = editing?.unitId === unitId ? segments.find((item) => item.shotId === editing.shotId) : undefined;
    const reviewSegment = reviewing?.unitId === unitId ? segments.find((item) => item.shotId === reviewing.shotId) : undefined;
    const pendingScenes = segments.filter((segment) => segment.sceneReview?.length);
    function openSceneReview(shotId: string) {
        setEditing(undefined);
        setReviewing({ unitId, shotId });
    }
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
                channelModel={data.videoModel}
                specJson={JSON.stringify(selected.spec)}
                durationRule={segmentRule}
                genreRule={genreRule}
                takeCounts={takeCounts}
                busyShotIds={busyShotIds}
                episode={episode}
                boundAssets={boundAssetsOf(workspace, selected)}
                onPickAssets={workspace && !workspaceQuery.isError ? () => setEditing({ unitId, shotId: selected.shotId }) : undefined}
                onEdited={async (selectShotId) => {
                    if (selectShotId && selectShotId !== selected.shotId) onSelect(selectShotId);
                    await Promise.all([workspaceQuery.refetch(), invalidateDrama(), segmentsQuery.refetch()]);
                }}
            />
        </DetailPane>
    ) : undefined;

    return (
        <StageLayout pane={pane}>
            {reviewSegment ? (
                <SceneReferenceReviewDialog key={reviewSegment.shotId} projectId={projectId} segment={reviewSegment} assets={data.assets} textModel={stepModels.text.effective} videoModel={data.videoModel} onClose={() => setReviewing(undefined)} />
            ) : null}
            {bindingSegment && workspace ? (
                <ShotBindingEditor
                    key={bindingSegment.shotId}
                    projectId={projectId}
                    segment={bindingSegment}
                    bindings={boundAssetsOf(workspace, bindingSegment)}
                    assets={data.assets}
                    assetsQuery={data.assetsQuery}
                    videoModel={data.videoModel}
                    onSceneReview={() => openSceneReview(bindingSegment.shotId)}
                    onClose={() => setEditing((current) => (current === editing ? undefined : current))}
                />
            ) : null}
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
            <ShotlistBar run={run} segmentCount={segments.length} director={director} />
            {project && projectWorkflow(project.workflowId).storyboardMode === "sss" ? <DirectorSetup projectId={project.id} tasks={data.projectTasks} textModel={stepModels.text} /> : null}
            <SummaryRow
                items={[
                    { label: "段", value: segmentsQuery.data && !segmentsQuery.isError ? segments.length : segmentsUnread(segmentsQuery.isLoading) },
                    { label: "镜头", value: segmentsQuery.data && !segmentsQuery.isError ? shotCount : segmentsUnread(segmentsQuery.isLoading) },
                    { label: "总时长", value: segmentsQuery.data && !segmentsQuery.isError ? secondsText(totalMs) : segmentsUnread(segmentsQuery.isLoading) },
                    { label: "单段上限", value: segmentRule ? `${segmentRule.maxSeconds} 秒` : "没读到" },
                    { label: "超时长", value: pipelineQuery.isError || !pipelineQuery.data ? segmentsUnread(pipelineQuery.isLoading) : overLimit, tone: overLimit ? "bad" : undefined },
                    { label: "有问题", value: pipelineQuery.isError || !pipelineQuery.data ? segmentsUnread(pipelineQuery.isLoading) : withIssues, tone: withIssues ? "bad" : undefined },
                    { label: "没参考", value: pipelineQuery.isError || !pipelineQuery.data ? segmentsUnread(pipelineQuery.isLoading) : noRefs, tone: noRefs ? "warn" : undefined },
                    modelSummaryItem(stepModels.text),
                ]}
            />
            {segmentsQuery.isError ? <ReadError what="分镜" onRetry={() => void segmentsQuery.refetch()} /> : null}
            {pipelineQuery.isError ? <ReadError what="每段的检查标记（超时长、问题、参考）" onRetry={() => void pipelineQuery.refetch()} /> : null}
            {workspaceQuery.isError ? <ReadError what="每段绑定的资产和出片状态" onRetry={() => void workspaceQuery.refetch()} /> : null}
            {pendingScenes.length > 0 ? (
                <details className="mb-3 rounded-md border border-border/60 p-3">
                    <summary>场景待核对 · {pendingScenes.length} 段</summary>
                    <ul className="mt-2 space-y-2">
                        {pendingScenes.map((segment) => (
                            <li key={segment.shotId}>
                                <Button size="small" onClick={() => openSceneReview(segment.shotId)}>
                                    {segment.spec.id} · 核对场景
                                </Button>
                                <span className="ml-2">{segment.sceneReview?.[0]}</span>
                            </li>
                        ))}
                    </ul>
                </details>
            ) : null}
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
                            onEditBindings={workspace && !workspaceQuery.isError ? () => setEditing({ unitId, shotId: segment.shotId }) : undefined}
                            stale={staleShots.has(segment.shotId)}
                            busy={busyShotIds.has(segment.shotId)}
                            compact={density === "compact"}
                            selected={segment.shotId === selectedId}
                            onSelect={() => onSelect(segment.shotId === selectedId ? undefined : segment.shotId)}
                            deleteAction={
                                <div className="flex flex-wrap items-center gap-1">
                                    <Button size="small" aria-label={`核对场景 ${segment.spec.id}`} onClick={() => openSceneReview(segment.shotId)}>
                                        核对场景
                                    </Button>
                                    <DeleteSegmentButton
                                        projectId={projectId}
                                        segment={segment}
                                        episode={episode}
                                        unitTitle={unit?.title}
                                        onDeleted={() => {
                                            if (segment.shotId === selectedId) onSelect(undefined);
                                        }}
                                    />
                                </div>
                            }
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
                    <span>
                        {stage?.state === "running" || run.running ? (director ? "导演分轮写，各轮已保存，全部校验后统一装段，装好之前这里是空的，不用守着。" : "写好一节出现一节，不用守着。") : "定妆做完后会自动开始；也可以点上面的「生成这一集分镜」。"}
                    </span>
                </div>
            )}
        </StageLayout>
    );
}

/**
 * 分镜状态条。导演分镜（SSS 电影 / 短剧、本体论）不写镜头表：服务端分轮写完直接装成段，单元卡、粘贴提示词和返修在「导演文档」里，
 * 所以 director 时按段数说状态，不提镜头表和装段。
 */
function ShotlistBar({ run, segmentCount, director }: { run: StoryboardRun; segmentCount: number; director: boolean }) {
    const { shotlist, running, stream, latest, problemSections, repack, generate, code } = run;
    const errors = shotlist?.check.errors ?? 0;
    const warnings = shotlist?.check.warnings ?? 0;
    // 导演分镜的轮次进度只读最新的分镜任务：刚点了生成、任务列表还没刷到新任务时（latest 不是进行中），不拿上一次的任务冒充。
    const latestActive = Boolean(latest && (latest.status === "queued" || latest.status === "running"));
    const now = useNow(director && running && latestActive);
    const streamTurn = stream.scenes.at(-1);
    const view = latest && (!running || latestActive) ? directorRunView(latest, { now, streamTurn: streamTurn ? { index: streamTurn.index, heading: streamTurn.heading } : undefined }) : undefined;
    const rounds = view ? directorRoundsText(view) : "";
    const checkpoint = latest?.dramaRounds;
    const resume =
        director && latest?.status === "failed" && checkpoint && !checkpoint.repair && checkpoint.completed > 0 && checkpoint.completed < checkpoint.total && checkpoint.turns.length >= checkpoint.completed
            ? { taskId: latest.id, from: checkpoint.completed + 1 }
            : undefined;
    const status = running
        ? director
            ? view?.current
                ? `导演正在写第 ${view.current.index} 轮：${view.current.label}${rounds ? ` · ${rounds}` : ""}${view.waitedSeconds !== undefined ? ` · 已等 ${durationText(view.waitedSeconds)}` : ""}。各轮写完就保存，全部校验后统一装段`
                : "导演正在分轮写分镜（立项、集设计、讲戏、切分、逐个单元写卡和编译、复看）。各轮写完就保存，全部校验后统一装段"
            : `正在写分镜${stream.shotCount ? ` · 已出 ${stream.shotCount} 镜` : ""}`
        : director
          ? latest?.status === "failed"
              ? `上一次生成失败${rounds ? `（${rounds}）` : ""}：${latest.error || "没写原因"}${segmentCount ? `；现有 ${segmentCount} 段没动` : ""}`
              : segmentCount
                ? `导演分镜已装成 ${segmentCount} 段；单元卡、粘贴提示词和返修在右上角「导演文档」`
                : "还没有分镜"
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
                        <Button size="small" type="primary" disabled={running} onClick={() => void generate({ onlySections: problemSections })}>
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
                    {resume ? (
                        <Button size="small" type="primary" loading={running} onClick={() => void generate({ resumeTaskId: resume.taskId })}>
                            从第 {resume.from} 轮继续
                        </Button>
                    ) : null}
                    <Button size="small" loading={running && !resume} disabled={running} onClick={() => void generate()}>
                        {shotlist || (director && segmentCount) || resume ? "整集重新生成" : "生成这一集分镜"}
                    </Button>
                </div>
            </div>
            {view?.phases.length && (running || latest?.status === "failed") ? (
                <ol className="drama-flow-director-phases" aria-label="导演分镜各轮">
                    {view.phases.map((phase) => (
                        <li key={phase.kind} className="drama-flow-chip" data-state={phase.state} data-tone={phase.state === "done" ? "ok" : phase.state === "current" ? "run" : undefined}>
                            {directorPhaseText(phase)}
                        </li>
                    ))}
                </ol>
            ) : null}
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

/** 进行中时每秒刷新一次「已等」；不在跑时不开定时器。 */
function useNow(active: boolean) {
    const [now, setNow] = useState(() => Date.now());
    useEffect(() => {
        if (!active) return;
        setNow(Date.now());
        const timer = window.setInterval(() => setNow(Date.now()), 1000);
        return () => window.clearInterval(timer);
    }, [active]);
    return now;
}

function SegmentBlock({
    ref,
    index,
    segment,
    info,
    assets,
    onEditBindings,
    stale,
    busy,
    compact,
    selected,
    onSelect,
    deleteAction,
}: {
    ref?: (node: HTMLElement | null) => void;
    index: number;
    segment: DramaSegmentView;
    info?: DramaSegmentProgress;
    assets: UnitBinding[];
    onEditBindings?: () => void;
    stale: boolean;
    busy: boolean;
    compact: boolean;
    selected: boolean;
    onSelect: () => void;
    deleteAction: ReactNode;
}) {
    const issues = info?.issues ?? segment.issues?.length ?? 0;
    const bad = Boolean(info?.overLimit) || issues > 0;
    const shots = segment.spec.shots;
    const segmentSeconds = segment.durationMs / 1000;
    const shown = compact ? shots.slice(0, 1) : shots;
    return (
        <article ref={ref} className="drama-flow-seg" data-bad={bad || undefined} aria-current={selected || undefined}>
            <header className="drama-flow-seg-header">
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
                {deleteAction}
            </header>
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
            <div className="drama-flow-seg-assets items-center" aria-label="绑定的资产">
                {assets.map((asset) => (
                    <span key={asset.id} title={`${asset.title} · ${asset.versionId}`} data-kind={asset.kind}>
                        {asset.preview ? <img src={asset.preview} alt="" loading="lazy" /> : null}
                        {asset.code || asset.title}
                        {asset.version ? ` · v${asset.version}` : ""}
                        {asset.sampleResourceId ? " · 有音样" : ""}
                    </span>
                ))}
                <Button size="small" disabled={!onEditBindings} className="ml-auto" aria-label={`编辑绑定 ${segment.spec.id || segment.title}`} onClick={onEditBindings}>
                    编辑绑定
                </Button>
            </div>
        </article>
    );
}
