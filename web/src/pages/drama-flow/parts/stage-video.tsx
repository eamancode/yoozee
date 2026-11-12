import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { App, Button } from "antd";
import { Copy, RotateCw } from "lucide-react";

import { DramaTakeCompare, useInvalidateDrama } from "@/pages/projects/workbench/detail/drama-panels";
import { assetPreviewUrl } from "@/pages/projects/workbench/detail/workflow-shot-references";
import { compileProjectShot, pickShotArtifact, type DramaSegmentProgress } from "@/services/api/project-drama";
import type { ProjectAsset, ShotArtifact } from "@/services/api/projects";
import { resourceFileUrl } from "@/services/api/resources";
import type { GenerationTask } from "@/services/api/task-center";
import { getWallet } from "@/services/api/wallet";

import type { FlowData } from "../hooks/use-flow-data";
import { useModelNames } from "../hooks/use-model-names";
import { costSummary, costSummaryText, creditsText, noCost, shotCosts } from "../model/costs";
import { durationText, runRow } from "../model/runs";
import { queryReadState, readValue, type ReadState } from "../model/read-state";
import type { FlowStageView } from "../model/stages";
import type { ShotVideoStep } from "../model/video-steps";
import { DetailPane, PaneSection, ReadError, StageLayout, SummaryRow } from "./layout";
import { secondsText } from "./stage-shots";

type Chip = { text: string; tone?: "ok" | "run" | "bad" | "warn" };

export function videoChipOf(segment: DramaSegmentProgress, step: ShotVideoStep | undefined, videoRead: ReadState = "ready"): Chip {
    // 出片链没读到时，没出过片的段不知道是在排队、失败还是没开始，不能写「还没出」。
    if (videoRead !== "ready" && !segment.generated) return videoRead === "loading" ? { text: "正在读出片状态…" } : { text: "出片状态没读到", tone: "warn" };
    if (step?.state === "running") return { text: "生成中", tone: "run" };
    if (step?.state === "waiting") return { text: "排队中", tone: "run" };
    if (step?.state === "failed") return { text: "失败", tone: "bad" };
    if (segment.generated) return { text: segment.takes > 1 ? `已出 ${segment.takes} 条` : "已出片", tone: "ok" };
    if (step?.state === "cancelled") return { text: "已停止", tone: "warn" };
    return { text: "还没出" };
}

/** 首帧从哪来：链里截了上一段的尾帧就是「上一段尾帧」，否则按参考图和提示词出。 */
function firstFrameText(step: ShotVideoStep | undefined) {
    if (!step) return "按参考图和提示词";
    if (step.step.sourceShotId || step.step.frameResourceId) return "上一段的尾帧";
    return "按参考图和提示词";
}

/**
 * 出片：一段一张卡，左边是成片要用的那条视频（或失败原因），中间是这一段的内容和参考，右边是模型、规格和花费。
 * 点一段在右边看按现在的镜头和资产编译出的提示词和参考（不是历史视频当时的输入）、所有候选和每次运行。
 * 出这一段、重出、失败后重新出，都先打开花费预览（写明这次的范围和预估花费），确认后才提交。
 */
export function StageVideo({
    data,
    stage,
    selectedId,
    canGenerate,
    stoppingChainId,
    onSelect,
    onGenerate,
    onStopChain,
    onRetryAll,
    onStopAll,
}: {
    data: FlowData;
    stage?: FlowStageView;
    selectedId?: string;
    canGenerate: boolean;
    stoppingChainId?: string;
    onSelect: (shotId?: string) => void;
    onGenerate: (shotId: string) => void;
    onStopChain: (chainId: string) => void;
    onRetryAll: () => void;
    onStopAll: () => void;
}) {
    const { project, unit, unitId, unitSegments: progress, steps, workspace, workspaceQuery, pipelineQuery, videoModel, progressRead } = data;
    // 段和出片状态来自进度接口，候选和花费来自当前集的 workspace、出片链来自链接口：哪一个没读到，就把依赖它的数字写成「没读到」。
    const progressUnread = progressRead !== "ready";
    // 生成中、排队、失败要进度和这一集的出片链都读到才算得出来。
    const videoRead = data.chainReadOf(unitId);
    const workspaceRead = queryReadState(workspaceQuery);
    // 模型只显示给人看的名字；提交和报价仍用原 ID（videoModel 原样传给花费预览）。
    const models = useModelNames(true);
    const videoModelName = models.nameOf(videoModel, "还没配好");
    const projectId = project?.id || "";
    const walletQuery = useQuery({ queryKey: ["wallet", "summary"], queryFn: () => getWallet(1, 1) });
    const videoTasks = useMemo(() => (workspace?.tasks || []).filter((task) => task.clientContext?.shotId), [workspace?.tasks]);
    const costs = useMemo(() => shotCosts(videoTasks), [videoTasks]);
    const takesByShot = useMemo(() => {
        const map = new Map<string, ShotArtifact[]>();
        for (const artifact of workspace?.shotArtifacts || []) {
            if (artifact.type !== "video" || !artifact.resourceId || (artifact.status !== "ready" && artifact.status !== "stale")) continue;
            map.set(artifact.shotId, [...(map.get(artifact.shotId) || []), artifact]);
        }
        for (const list of map.values()) list.sort((left, right) => right.version - left.version);
        return map;
    }, [workspace?.shotArtifacts]);
    const focusRef = useRef<HTMLElement | null>(null);
    // 函数体要带大括号：新版 Chrome 的 scrollIntoView 返回 Promise，箭头函数隐式返回它，React 会把它当清理函数调用而崩溃。
    useEffect(() => {
        focusRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
    }, [selectedId]);

    const states = progress.map((segment) => steps.get(segment.shotId)?.state);
    const failed = states.filter((state) => state === "failed").length;
    const running = states.filter((state) => state === "running").length;
    const waiting = states.filter((state) => state === "waiting").length;
    const done = progress.filter((item) => item.generated).length;
    const notYet = progress.filter((item, index) => !item.generated && !states[index]).length;
    const selected = progress.find((item) => item.shotId === selectedId);
    const unitSpent = costSummaryText(workspaceRead, costSummary(videoTasks));

    const pane = selected ? (
        <VideoPane
            key={selected.shotId}
            projectId={projectId}
            segment={selected}
            step={steps.get(selected.shotId)}
            takes={takesByShot.get(selected.shotId) || []}
            tasks={videoTasks.filter((task) => task.clientContext?.shotId === selected.shotId)}
            assets={workspace?.assets || []}
            onRefresh={() => workspaceQuery.refetch()}
            onClose={() => onSelect(undefined)}
        />
    ) : undefined;

    return (
        <StageLayout pane={pane}>
            <div className="drama-flow-work-head">
                <div>
                    <h2>出片{unit?.title ? ` · ${unit.title}` : ""}</h2>
                    <p>一段分镜出一条视频。失败的在那张卡上「重新出」，出过的可以看片、挑片、重出；每次提交前都先看这次要出的范围和花费。</p>
                </div>
                <div className="drama-flow-work-actions">
                    {failed > 1 ? (
                        <Button icon={<RotateCw className="size-3.5" />} onClick={onRetryAll}>
                            重新出失败的（先看花费）
                        </Button>
                    ) : null}
                    {running + waiting ? <Button onClick={onStopAll}>停止出片</Button> : null}
                </div>
            </div>
            <SummaryRow
                items={[
                    { label: "段", value: readValue(progressRead, progress.length) },
                    { label: "已出", value: readValue(progressRead, done), tone: "ok" },
                    { label: "生成中", value: readValue(videoRead, running), tone: videoRead === "ready" && running ? "run" : undefined },
                    { label: "排队", value: readValue(videoRead, waiting), tone: videoRead === "ready" && waiting ? "run" : undefined },
                    { label: "失败", value: readValue(videoRead, failed), tone: videoRead === "ready" && failed ? "bad" : undefined },
                    { label: "还没出", value: readValue(videoRead, notYet) },
                    { label: "本集已花", value: unitSpent },
                    { label: "余额", value: walletQuery.data ? creditsText(walletQuery.data.account.availableMicrocredits) : walletQuery.isError ? "没读到" : "…" },
                    { label: "视频模型", value: videoModelName.text, tone: videoModel ? videoModelName.tone : "bad" },
                ]}
            />
            {running + waiting + failed ? (
                <div className="drama-flow-queue" role="list" aria-label="出片队列">
                    {progress.map((segment, index) => {
                        const state = states[index];
                        if (!state || state === "succeeded" || state === "cancelled") return null;
                        return (
                            <button key={segment.shotId} type="button" role="listitem" data-state={state} onClick={() => onSelect(segment.shotId)}>
                                {segment.segmentId} · {state === "failed" ? "失败" : state === "running" ? "生成中" : "排队"}
                            </button>
                        );
                    })}
                </div>
            ) : null}
            {pipelineQuery.isError ? <ReadError what="出片进度" onRetry={() => void pipelineQuery.refetch()} /> : null}
            {workspaceQuery.isError ? <ReadError what="这一集的候选视频和花费" onRetry={() => void workspaceQuery.refetch()} /> : null}
            {!progressUnread && videoRead === "error" ? <ReadError what="这一集的出片状态（生成中、排队、失败）" onRetry={() => void data.refreshVideo()} /> : null}
            {!canGenerate && progress.length ? (
                <div className="drama-flow-note" data-tone="warn">
                    还没配好可用的视频模型，先到设置里配好再出片。
                </div>
            ) : null}
            {progressUnread ? null : progress.length ? (
                <div className="drama-flow-vcards">
                    {progress.map((segment) => {
                        const step = steps.get(segment.shotId);
                        const chip = videoChipOf(segment, step, videoRead);
                        const active = step?.state === "running" || step?.state === "waiting";
                        const isFailed = step?.state === "failed";
                        const takes = takesByShot.get(segment.shotId) || [];
                        const chosen = takes.find((item) => item.id === segment.selectedArtifactId) || takes.find((item) => item.selected) || takes[0];
                        const blocked = segment.overLimit || (segment.issues ?? 0) > 0;
                        const isSelected = segment.shotId === selectedId;
                        const cost = costs.get(segment.shotId);
                        return (
                            <article key={segment.shotId} ref={isSelected ? (node) => void (focusRef.current = node) : undefined} className="drama-flow-vcard" data-bad={isFailed || undefined} aria-current={isSelected || undefined}>
                                <div className="drama-flow-vcard-media">
                                    {chosen?.resourceId ? (
                                        <video className="drama-flow-video" controls preload="metadata" src={resourceFileUrl(chosen.resourceId)} />
                                    ) : (
                                        <div className="drama-flow-vcard-empty" data-tone={chip.tone}>
                                            {isFailed ? step?.error || "生成失败，没写原因" : chip.text}
                                        </div>
                                    )}
                                </div>
                                <div className="drama-flow-vcard-main">
                                    <button type="button" className="drama-flow-vcard-title" onClick={() => onSelect(isSelected ? undefined : segment.shotId)}>
                                        <span className="drama-flow-seg-no">{segment.segmentId}</span>
                                        <strong>{segment.title || segment.segmentId}</strong>
                                    </button>
                                    <span className="drama-flow-muted">
                                        {secondsText(segment.durationMs)}
                                        {segment.hasDialogue ? " · 有台词" : ""}
                                        {step ? ` · 参考 ${step.step.referenceCount} 张` : ""}
                                        {` · 首帧：${firstFrameText(step)}`}
                                    </span>
                                    {isFailed && chosen ? <span className="drama-flow-asset-error">最近一次失败：{step?.error || "没写原因"}</span> : null}
                                    {blocked ? <span className="drama-flow-asset-error">分镜有问题没改完，先去「分镜」里改</span> : null}
                                    {step?.step.referenceWarning ? <span className="drama-flow-muted">{step.step.referenceWarning}</span> : null}
                                </div>
                                <div className="drama-flow-vcard-side">
                                    <span className="drama-flow-chip" data-tone={chip.tone}>
                                        {chip.text}
                                    </span>
                                    <span className="drama-flow-muted">{step?.step.model ? models.nameOf(step.step.model, "").text : videoModelName.text}</span>
                                    {step ? <span className="drama-flow-muted">生成 {step.step.generationSeconds} 秒档</span> : null}
                                    <span className="drama-flow-muted">
                                        候选 {readValue(workspaceRead, takes.length)}
                                        {takes.length > 1 ? (segment.selectedArtifactId ? " · 已挑" : " · 待挑") : ""}
                                    </span>
                                    <span className="drama-flow-muted">花费 {costSummaryText(workspaceRead, cost ?? noCost())}</span>
                                    <div className="drama-flow-work-actions">
                                        {isFailed ? (
                                            <Button size="small" type="primary" icon={<RotateCw className="size-3.5" />} disabled={!canGenerate || blocked} onClick={() => onGenerate(segment.shotId)}>
                                                重新出
                                            </Button>
                                        ) : active && step ? (
                                            <Button size="small" loading={stoppingChainId === step.chainId} onClick={() => onStopChain(step.chainId)}>
                                                停止
                                            </Button>
                                        ) : (
                                            // 出片状态没读到时不知道这一段是不是正在出，先不让提交，免得重复出。
                                            <Button size="small" disabled={!canGenerate || blocked || videoRead !== "ready"} onClick={() => onGenerate(segment.shotId)}>
                                                {segment.generated ? "重出" : "出这一段"}
                                            </Button>
                                        )}
                                        <Button size="small" type="text" aria-expanded={isSelected} onClick={() => onSelect(isSelected ? undefined : segment.shotId)}>
                                            {takes.length > 1 && !segment.selectedArtifactId ? "挑片" : "详情"}
                                        </Button>
                                    </div>
                                </div>
                            </article>
                        );
                    })}
                </div>
            ) : (
                <div className="drama-flow-empty">
                    <strong>{stage?.state === "pending" ? "还没到出片这一步" : "这里还没有可以出片的分镜"}</strong>
                    <span>分镜写好后，在这里一段一段出视频。</span>
                </div>
            )}
        </StageLayout>
    );
}

function VideoPane({
    projectId,
    segment,
    step,
    takes,
    tasks,
    assets,
    onRefresh,
    onClose,
}: {
    projectId: string;
    segment: DramaSegmentProgress;
    step?: ShotVideoStep;
    takes: ShotArtifact[];
    tasks: GenerationTask[];
    assets: ProjectAsset[];
    onRefresh: () => Promise<unknown>;
    onClose: () => void;
}) {
    const { message } = App.useApp();
    const invalidateDrama = useInvalidateDrama(projectId);
    const chosen = takes.find((item) => item.id === segment.selectedArtifactId) || takes.find((item) => item.selected);
    const [viewing, setViewing] = useState<string | undefined>(chosen?.id || takes[0]?.id);
    const shown = takes.find((item) => item.id === viewing) || chosen || takes[0];
    const compileQuery = useQuery({
        queryKey: ["project", projectId, "drama", "compile", segment.shotId, "flow"],
        queryFn: async () => (await compileProjectShot(projectId, segment.shotId)).compiled,
        staleTime: 30_000,
    });
    const compiled = compileQuery.data?.result;
    const pick = useMutation({
        mutationFn: (artifactId: string) => pickShotArtifact(projectId, artifactId, "在短剧新工作流里挑片"),
        onSuccess: async () => {
            await Promise.all([onRefresh(), invalidateDrama()]);
            message.success("成片会用这一条");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "挑片没成功"),
    });
    const models = useModelNames(true);
    const copy = (text: string) =>
        navigator.clipboard.writeText(text).then(
            () => message.success("已复制"),
            () => message.error("复制没成功"),
        );
    return (
        <DetailPane title={`${segment.segmentId} ${segment.title}`.trim()} subtitle={`${secondsText(segment.durationMs)}${step?.step.model ? ` · ${models.nameOf(step.step.model, "").text}` : ""}`} onClose={onClose}>
            {shown?.resourceId ? <video key={shown.id} className="drama-flow-video" controls preload="metadata" src={resourceFileUrl(shown.resourceId)} /> : null}
            {step?.state === "failed" ? (
                <PaneSection label="失败原因">
                    <p data-tone="bad">{step.error || "没写原因"}</p>
                </PaneSection>
            ) : null}
            <PaneSection label={`候选（${takes.length}）`} empty="还没出过">
                {takes.length ? (
                    <div className="drama-flow-takes">
                        {takes.map((take) => (
                            <div key={take.id} className="drama-flow-take" data-active={take.id === shown?.id || undefined}>
                                <Button size="small" type="text" aria-pressed={take.id === shown?.id} onClick={() => setViewing(take.id)}>
                                    第 {take.version} 条{take.status === "stale" ? "（改动前）" : ""}
                                </Button>
                                {take.id === chosen?.id ? (
                                    <span className="drama-flow-chip" data-tone="ok">
                                        成片用这条
                                    </span>
                                ) : (
                                    <Button size="small" loading={pick.isPending && pick.variables === take.id} disabled={pick.isPending} onClick={() => pick.mutate(take.id)}>
                                        用这条
                                    </Button>
                                )}
                            </div>
                        ))}
                    </div>
                ) : undefined}
            </PaneSection>
            {takes.length > 1 ? <DramaTakeCompare projectId={projectId} shotId={segment.shotId} artifacts={takes} activeId={shown?.id} onOpen={setViewing} /> : null}
            <p className="drama-flow-muted">下面的提示词和参考是按现在的镜头和资产重新编译的，下一次出这一段会用它；已经出好的视频当时用的输入可能不同。</p>
            <PaneSection label={compiled ? `当前编译的提示词 · ${compiled.chars} 字` : "当前编译的提示词"} empty={compileQuery.isError ? "提示词没读到" : "正在组提示词…"}>
                {compiled ? (
                    <>
                        <pre className="drama-flow-stream">{compiled.prompt}</pre>
                        <Button size="small" icon={<Copy className="size-3.5" />} onClick={() => void copy(compiled.prompt)}>
                            复制
                        </Button>
                    </>
                ) : undefined}
            </PaneSection>
            <PaneSection label={`当前编译的参考（${compiled?.files.length ?? 0}）`} empty={compiled ? "这一段不带参考图" : compileQuery.isError ? "参考没读到" : "…"}>
                {compiled?.files.length ? (
                    <div className="drama-flow-seg-assets">
                        {compiled.files.map((file) => {
                            const asset = assets.find((item) => item.primaryVersionId === file.file);
                            const preview = asset ? assetPreviewUrl(asset) : "";
                            return (
                                <span key={`${file.tag}-${file.file}`} title={`${file.tag} · ${file.role}`}>
                                    {preview ? <img src={preview} alt="" loading="lazy" /> : null}
                                    {file.tag} · {asset?.title || file.role}
                                </span>
                            );
                        })}
                    </div>
                ) : undefined}
            </PaneSection>
            {compiled?.issues.length || compiled?.warnings?.length ? (
                <PaneSection label="提示词检查">
                    <ul className="drama-flow-issues">
                        {compiled.issues.map((issue, index) => (
                            <li key={`i-${index}`} data-level={issue.level}>
                                <strong>{issue.level === "error" ? "错误" : "提醒"}</strong>
                                <span>{issue.message}</span>
                            </li>
                        ))}
                        {(compiled.warnings || []).map((warning, index) => (
                            <li key={`w-${index}`} data-level="warn">
                                <strong>提醒</strong>
                                <span>{warning}</span>
                            </li>
                        ))}
                    </ul>
                </PaneSection>
            ) : null}
            <PaneSection label={`运行记录（${tasks.length}）`} empty="还没有运行">
                {tasks.length ? (
                    <ul className="drama-flow-runs-list">
                        {tasks.map((task) => {
                            const row = runRow(task);
                            return (
                                <li key={task.id} data-status={row.status}>
                                    <div className="drama-flow-runs-line">
                                        <strong>{row.step}</strong>
                                        <span className="drama-flow-chip" data-tone={row.status === "succeeded" ? "ok" : row.status === "failed" ? "bad" : row.active ? "run" : undefined}>
                                            {row.statusLabel}
                                        </span>
                                    </div>
                                    <div className="drama-flow-runs-meta">
                                        <span>{new Date(row.createdAt).toLocaleString("zh-CN", { hour12: false })}</span>
                                        {row.model ? <span>{models.nameOf(row.model, "").text}</span> : null}
                                        {row.seconds !== undefined ? <span>用时 {durationText(row.seconds)}</span> : null}
                                        <span>{row.cost === undefined ? "没有计费记录" : creditsText(row.cost)}</span>
                                    </div>
                                    {row.error ? (
                                        <p className="drama-flow-runs-error">
                                            {row.error}
                                            <Button size="small" type="link" onClick={() => void copy(row.error || "")}>
                                                复制
                                            </Button>
                                        </p>
                                    ) : null}
                                </li>
                            );
                        })}
                    </ul>
                ) : undefined}
            </PaneSection>
        </DetailPane>
    );
}
