import { useMemo, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input } from "antd";
import { CheckCircle2, Circle, CircleAlert, Download, ShieldCheck } from "lucide-react";

import { createDramaEvidence, downloadDramaDelivery, framesShotArtifact, getDramaProgress, listDramaEvidence, parseEvidenceMetrics, pickShotArtifact, runDramaGate, transcribeShotArtifact, type DramaEvidence, type DramaEvidenceKind, type DramaGateCheck, type DramaProgress, type DramaSegmentProgress } from "@/services/api/drama";
import type { ShotArtifact } from "@/services/api/projects";
import { resourceFileUrl } from "@/services/api/resources";
import { waitForGenerationTask } from "@/services/api/task-center";
import type { ReferenceImage } from "@/types/image";

import { dramaAuthLabel } from "../drama-model";

export const dramaProgressKey = (projectId: string) => ["project", projectId, "drama", "progress"] as const;
export const dramaEvidenceKey = (projectId: string) => ["project", projectId, "drama", "evidence"] as const;

export function useDramaProgress(projectId: string) {
    return useQuery({ queryKey: dramaProgressKey(projectId), queryFn: async ({ signal }) => (await getDramaProgress(projectId, signal)).progress });
}

/** 刷新进度与证据：生成、过审、选中、交付之后都要调用。 */
export function useInvalidateDrama(projectId: string) {
    const queryClient = useQueryClient();
    return () => Promise.all([
        queryClient.invalidateQueries({ queryKey: dramaProgressKey(projectId) }),
        queryClient.invalidateQueries({ queryKey: dramaEvidenceKey(projectId) }),
    ]);
}

const SEGMENT_CELLS: Array<{ key: keyof Pick<DramaSegmentProgress, "script" | "refs" | "generated" | "reviewed" | "inCut" | "accepted">; label: string }> = [
    { key: "script", label: "剧本" },
    { key: "refs", label: "参考" },
    { key: "generated", label: "生成" },
    { key: "reviewed", label: "过审" },
    { key: "inCut", label: "入片" },
    { key: "accepted", label: "验收" },
];

const EVIDENCE_LABELS: Record<string, string> = { faces: "人脸核对", frames: "抽帧核对", transcript: "台词转写" };

/** 项目概览里的「终态」面板：一句进度、8 个可验证终态和每段六格证据。 */
export function DramaOverviewPanel({ projectId }: { projectId: string }) {
    const query = useDramaProgress(projectId);
    const [expanded, setExpanded] = useState(false);
    const progress = query.data;
    if (query.isLoading) return <section className="rounded-xl border border-border/70 bg-surface p-5 text-xs text-foreground/45">正在读取段级进度…</section>;
    if (query.isError || !progress) return <section className="rounded-xl border border-border/70 bg-surface p-5 text-xs text-foreground/55">段级进度读取失败：{query.error instanceof Error ? query.error.message : "未知错误"} <Button size="small" type="link" onClick={() => void query.refetch()}>重试</Button></section>;
    const segments = expanded ? progress.segments : progress.segments.slice(0, 12);
    return (
        <section className="rounded-xl border border-border/70 bg-surface p-5">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                    <div className="text-[var(--fs-micro)] font-medium uppercase tracking-[.18em] text-[var(--workspace-accent)]">yoozee 1.0 · 段级进度</div>
                    <h2 className="mt-1.5 text-base font-semibold">{progress.line || "还没有分段"}</h2>
                    {progress.next ? <p className="mt-1 text-xs leading-5 text-foreground/50">下一步：{progress.next.label} · {progress.next.detail}（{progress.next.skill}）</p> : <p className="mt-1 text-xs text-foreground/50">8 个终态全部达成。</p>}
                </div>
                <DramaModelChips progress={progress} />
            </div>
            <ol className="mt-4 grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
                {progress.endStates.map((state, index) => (
                    <li key={state.key} className={`rounded-lg border px-3 py-2.5 ${state.done ? "border-border/50 bg-surface-active" : progress.next?.key === state.key ? "border-[var(--workspace-accent)]/50" : "border-border/70"}`}>
                        <div className="flex items-center gap-1.5 text-xs font-medium">{state.done ? <CheckCircle2 className="size-3.5 text-[var(--workspace-accent)]" /> : <Circle className="size-3.5 text-foreground/30" />}<span className="text-foreground/40">{index + 1}</span><span className="truncate">{state.label}</span></div>
                        <p className="mt-1 line-clamp-2 text-[var(--fs-label)] leading-5 text-foreground/50">{state.detail}</p>
                        <span className="mt-1 block text-[var(--fs-tiny)] text-foreground/35">{state.skill}</span>
                    </li>
                ))}
            </ol>
            {progress.segments.length ? (
                <div className="mt-4 overflow-x-auto">
                    <table className="w-full min-w-[640px] text-left text-xs">
                        <thead className="text-[var(--fs-tiny)] text-foreground/40"><tr><th className="py-1.5 pr-2 font-medium">段</th><th className="py-1.5 pr-2 font-medium">时长</th>{SEGMENT_CELLS.map((cell) => <th key={cell.key} className="py-1.5 pr-2 text-center font-medium">{cell.label}</th>)}<th className="py-1.5 font-medium">候选 / 缺口</th></tr></thead>
                        <tbody>
                            {segments.map((segment) => (
                                <tr key={segment.shotId} className="border-t border-border/50">
                                    <td className="max-w-56 py-1.5 pr-2"><span className="font-medium">{segment.segmentId}</span> <span className="text-foreground/50">{segment.unitTitle ? `${segment.unitTitle} · ` : ""}{segment.title}</span></td>
                                    <td className={`py-1.5 pr-2 tabular-nums ${segment.overLimit ? "text-amber-600 dark:text-amber-400" : "text-foreground/60"}`}>{(segment.durationMs / 1000).toFixed(1)}s{segment.overLimit ? " 超长" : ""}</td>
                                    {SEGMENT_CELLS.map((cell) => <td key={cell.key} className="py-1.5 pr-2 text-center">{segment[cell.key] ? <CheckCircle2 className="inline size-3.5 text-[var(--workspace-accent)]" aria-label="已完成" /> : <Circle className="inline size-3.5 text-foreground/25" aria-label="未完成" />}</td>)}
                                    <td className="py-1.5 text-foreground/50">{segment.takes} 条{segment.missingEvidence?.length ? ` · 缺${segment.missingEvidence.map((kind) => EVIDENCE_LABELS[kind] || kind).join("、")}` : ""}</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                    {progress.segments.length > 12 ? <Button className="mt-2" size="small" type="link" onClick={() => setExpanded((value) => !value)}>{expanded ? "收起" : `展开全部 ${progress.segments.length} 段`}</Button> : null}
                </div>
            ) : null}
        </section>
    );
}

export function DramaModelChips({ progress }: { progress: DramaProgress }) {
    return (
        <div className="flex flex-wrap gap-1.5 text-[var(--fs-label)]">
            <span className="rounded bg-foreground/10 px-2 py-0.5 text-foreground/65">{progress.model.label} · 每段最长 {progress.model.duration.max} 秒</span>
            <span className="rounded bg-foreground/10 px-2 py-0.5 text-foreground/65">{dramaAuthLabel(progress.authMode)}</span>
            {!progress.model.calibrated ? <span className="rounded bg-amber-500/10 px-2 py-0.5 text-amber-600 dark:text-amber-400">档案未校准</span> : null}
            {progress.counts.overLimit ? <span className="rounded bg-amber-500/10 px-2 py-0.5 text-amber-600 dark:text-amber-400">{progress.counts.overLimit} 段超长</span> : null}
        </div>
    );
}

/** 交付阶段：成片门禁（同步、黑场/静音、响度、真峰值）和交付包下载。 */
export function DramaDeliveryPanel({ projectId, projectName }: { projectId: string; projectName: string }) {
    const { message } = App.useApp();
    const invalidate = useInvalidateDrama(projectId);
    const progressQuery = useDramaProgress(projectId);
    const evidenceQuery = useQuery({ queryKey: dramaEvidenceKey(projectId), queryFn: async ({ signal }) => (await listDramaEvidence(projectId, signal)).evidence });
    const lastGate = useMemo(() => (evidenceQuery.data || []).filter((item) => item.kind === "gate").sort((left, right) => right.createdAt.localeCompare(left.createdAt))[0], [evidenceQuery.data]);
    const gateChecks = useMemo(() => {
        if (!lastGate) return [] as DramaGateCheck[];
        const checks = parseEvidenceMetrics(lastGate).checks;
        return Array.isArray(checks) ? (checks as DramaGateCheck[]) : [];
    }, [lastGate]);
    const gateMutation = useMutation({
        mutationFn: () => runDramaGate(projectId, {}),
        onSuccess: ({ gate }) => { void invalidate(); message[gate.report.passed ? "success" : "warning"](gate.report.passed ? "成片门禁通过" : "成片门禁未通过，看下面的明细"); },
        onError: (error) => message.error(error instanceof Error ? error.message : "门禁检查失败"),
    });
    const deliveryMutation = useMutation({
        mutationFn: () => downloadDramaDelivery(projectId, `${projectName}-交付包.zip`),
        onSuccess: (fileName) => { void invalidate(); message.success(`已下载 ${fileName}`); },
        onError: (error) => message.error(error instanceof Error ? error.message : "交付包生成失败"),
    });
    return (
        <div className="mt-5 rounded-xl border border-border/70 bg-surface p-5">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                    <h3 className="text-sm font-semibold">成片门禁与交付包</h3>
                    <p className="mt-1 text-xs leading-5 text-foreground/48">门禁检查最近一次剪辑导出的成片：音画同步、黑场与静音、响度（目标 ±1 LUFS）和真峰值。交付包含需求文档、连戏表、分段表、生成参数、资产清单、证据、进度、验收单和字幕；不含任何密钥。</p>
                </div>
                {progressQuery.data ? <DramaModelChips progress={progressQuery.data} /> : null}
            </div>
            <div className="mt-4 flex flex-wrap gap-2">
                <Button icon={<ShieldCheck className="size-3.5" />} loading={gateMutation.isPending} onClick={() => gateMutation.mutate()}>{lastGate ? "重新检查成片" : "检查成片"}</Button>
                <Button type="primary" icon={<Download className="size-3.5" />} loading={deliveryMutation.isPending} onClick={() => deliveryMutation.mutate()}>下载交付包</Button>
            </div>
            {lastGate ? (
                <div className="mt-4">
                    <div className="flex items-center gap-1.5 text-xs font-medium">{lastGate.passed ? <CheckCircle2 className="size-3.5 text-[var(--workspace-accent)]" /> : <CircleAlert className="size-3.5 text-amber-500" />}<span>{lastGate.passed ? "最近一次门禁通过" : "最近一次门禁未通过"}</span><span className="font-normal text-foreground/40">{new Date(lastGate.createdAt).toLocaleString("zh-CN")}</span></div>
                    <ul className="mt-2 grid gap-1 text-[var(--fs-label)] leading-5 sm:grid-cols-2">
                        {gateChecks.map((check) => <li key={check.name} className={check.ok ? "text-foreground/55" : "text-amber-600 dark:text-amber-400"}>{check.ok ? "✓" : "✗"} {check.name}：{check.detail}</li>)}
                    </ul>
                </div>
            ) : null}
        </div>
    );
}

function latestOf(items: DramaEvidence[], kind: DramaEvidenceKind) {
    return items.filter((item) => item.kind === kind).sort((left, right) => right.createdAt.localeCompare(left.createdAt))[0];
}

/**
 * 一条视频候选的证据面板：抽帧、人脸（人判）、台词转写（有台词才要），三项都过才能选定。
 * 判人脸、看抽帧必须由人在这里点；Agent 看不到画面时不替人判。
 */
export function DramaTakeEvidencePanel({ projectId, shotId, artifact, characterImages, onPicked }: { projectId: string; shotId: string; artifact: ShotArtifact; characterImages: ReferenceImage[]; onPicked: () => Promise<unknown> | void }) {
    const { message } = App.useApp();
    const invalidate = useInvalidateDrama(projectId);
    const progressQuery = useDramaProgress(projectId);
    const evidenceQuery = useQuery({ queryKey: dramaEvidenceKey(projectId), queryFn: async ({ signal }) => (await listDramaEvidence(projectId, signal)).evidence });
    const [reason, setReason] = useState("");
    const segment = progressQuery.data?.segments.find((item) => item.shotId === shotId);
    const hasDialogue = Boolean(segment?.hasDialogue);
    const items = useMemo(() => (evidenceQuery.data || []).filter((item) => item.artifactId === artifact.id), [artifact.id, evidenceQuery.data]);
    const frames = latestOf(items, "frames");
    const contactSheet = items.filter((item) => item.kind === "frames" && item.resourceId).sort((left, right) => right.createdAt.localeCompare(left.createdAt))[0];
    const faces = latestOf(items, "faces");
    const transcript = latestOf(items, "transcript");
    const required: Array<{ kind: DramaEvidenceKind; label: string; evidence?: DramaEvidence }> = [
        { kind: "frames", label: "抽帧", evidence: frames },
        { kind: "faces", label: "人脸", evidence: faces },
        ...(hasDialogue ? [{ kind: "transcript" as const, label: "台词", evidence: transcript }] : []),
    ];
    const missing = required.filter((item) => item.evidence?.passed !== true);
    const onError = (fallback: string) => (error: unknown) => message.error(error instanceof Error ? error.message : fallback);
    const framesMutation = useMutation({ mutationFn: () => framesShotArtifact(projectId, artifact.id), onSuccess: () => void invalidate(), onError: onError("抽帧失败") });
    const markMutation = useMutation({
        mutationFn: (input: { kind: DramaEvidenceKind; passed: boolean; note: string }) => createDramaEvidence(projectId, { artifactId: artifact.id, ...input }),
        onSuccess: () => void invalidate(),
        onError: onError("证据保存失败"),
    });
    const transcribeMutation = useMutation({
        mutationFn: async () => {
            const { task } = await transcribeShotArtifact(projectId, artifact.id);
            await waitForGenerationTask(task.id, { initialTask: task });
            return createDramaEvidence(projectId, { artifactId: artifact.id, kind: "transcript", taskId: task.id });
        },
        onSuccess: ({ evidence }) => { void invalidate(); const metrics = parseEvidenceMetrics(evidence); message[evidence.passed ? "success" : "warning"](`台词字错率 ${typeof metrics.cer === "number" ? `${(metrics.cer * 100).toFixed(1)}%` : "—"}${evidence.passed ? "，通过" : "，未通过"}`); },
        onError: onError("转写失败"),
    });
    const pickMutation = useMutation({
        mutationFn: () => pickShotArtifact(projectId, artifact.id, reason.trim()),
        onSuccess: async () => { setReason(""); await invalidate(); await onPicked(); message.success("已选定这条候选"); },
        onError: onError("选定失败"),
    });
    const transcriptMetrics = transcript ? parseEvidenceMetrics(transcript) : {};
    return (
        <div className="mt-3 rounded-lg border border-border/70 p-3 text-xs">
            <div className="flex items-center justify-between gap-2"><span className="font-medium">候选证据 · v{artifact.version}</span>{artifact.selected ? <span className="rounded bg-foreground/10 px-1.5 py-0.5 text-[var(--fs-tiny)] text-foreground/60">已选定</span> : null}</div>
            <div className="mt-2 space-y-2">
                <EvidenceRow label="抽帧" evidence={frames} pending={frames && frames.passed === null ? "待人看" : undefined}>
                    <Button size="small" loading={framesMutation.isPending} onClick={() => framesMutation.mutate()}>{contactSheet ? "重新抽帧" : "出抽帧拼图"}</Button>
                    {contactSheet ? <><Button size="small" onClick={() => markMutation.mutate({ kind: "frames", passed: true, note: "人看过抽帧：无穿帮、无多余肢体和文字" })}>画面没问题</Button><Button size="small" danger onClick={() => markMutation.mutate({ kind: "frames", passed: false, note: "人看过抽帧：有问题" })}>有问题</Button></> : null}
                </EvidenceRow>
                {contactSheet?.resourceId ? <img className="w-full rounded-md bg-foreground/5" src={resourceFileUrl(contactSheet.resourceId)} alt="候选抽帧拼图" /> : null}
                <EvidenceRow label="人脸" evidence={faces}>
                    <Button size="small" disabled={!contactSheet} onClick={() => markMutation.mutate({ kind: "faces", passed: true, note: "人对照定妆照：同一个人" })}>同一个人</Button>
                    <Button size="small" danger disabled={!contactSheet} onClick={() => markMutation.mutate({ kind: "faces", passed: false, note: "人对照定妆照：不是同一个人" })}>不是</Button>
                </EvidenceRow>
                {contactSheet && characterImages.length ? <div className="flex gap-1.5 overflow-x-auto">{characterImages.map((image) => image.url ? <img key={image.id} className="h-16 shrink-0 rounded bg-foreground/5 object-cover" src={image.url} alt={`${image.name}参考`} title={image.name} /> : null)}</div> : null}
                {hasDialogue ? (
                    <EvidenceRow label="台词" evidence={transcript} detail={typeof transcriptMetrics.cer === "number" ? `字错率 ${((transcriptMetrics.cer as number) * 100).toFixed(1)}%` : undefined}>
                        <Button size="small" loading={transcribeMutation.isPending} onClick={() => transcribeMutation.mutate()}>{transcript ? "重新转写" : "转写并核对"}</Button>
                    </EvidenceRow>
                ) : null}
            </div>
            <div className="mt-3 flex gap-2">
                <Input size="small" value={reason} onChange={(event) => setReason(event.target.value)} placeholder="选它的理由（一句话）" maxLength={200} />
                <Button size="small" type="primary" disabled={Boolean(missing.length) || !reason.trim() || artifact.status !== "ready"} loading={pickMutation.isPending} onClick={() => pickMutation.mutate()}>选定</Button>
            </div>
            {missing.length ? <p className="mt-1.5 text-[var(--fs-tiny)] text-foreground/45">还缺：{missing.map((item) => item.label).join("、")}（三项都过才能选定）</p> : null}
        </div>
    );
}

function EvidenceRow({ label, evidence, pending, detail, children }: { label: string; evidence?: DramaEvidence; pending?: string; detail?: string; children: ReactNode }) {
    const state = pending || (evidence?.passed === true ? "通过" : evidence?.passed === false ? "未通过" : "未做");
    const tone = evidence?.passed === true ? "text-[var(--workspace-accent)]" : evidence?.passed === false ? "text-amber-600 dark:text-amber-400" : "text-foreground/40";
    return <div className="flex flex-wrap items-center gap-1.5"><span className="w-8 shrink-0 font-medium text-foreground/60">{label}</span><span className={`w-12 shrink-0 ${tone}`}>{state}</span>{detail ? <span className="text-foreground/45">{detail}</span> : null}<span className="ml-auto flex flex-wrap gap-1">{children}</span></div>;
}
