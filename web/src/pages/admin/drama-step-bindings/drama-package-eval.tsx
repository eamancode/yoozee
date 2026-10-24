import { Button, Input } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useState } from "react";

import { AppModal } from "@/components/ui/product/app-modal";
import type { DramaEvalChapterResult, DramaGateCheck, DramaPackageVersion, DramaStepEval, DramaStepEvalStatus } from "@/services/api/admin-drama-packages";
import { AdminDataTable, AdminStatusBadge, type AdminStatusTone } from "../components/admin-ui";

const EVAL_BADGES: Record<DramaStepEvalStatus, { label: string; tone: AdminStatusTone }> = {
    queued: { label: "评测排队", tone: "info" },
    running: { label: "评测中", tone: "info" },
    passed: { label: "评测通过", tone: "success" },
    failed: { label: "未通过", tone: "error" },
    error: { label: "评测出错", tone: "warning" },
};

export function evalInFlight(evaluation?: DramaStepEval) {
    return evaluation?.status === "queued" || evaluation?.status === "running";
}

/** 有评测且没通过（含还在跑、出错）的版本要写明原因才能启用；没评测过的历史版本（回退）直接启用。 */
export function evalNeedsForce(version: DramaPackageVersion) {
    return Boolean(version.eval) && version.eval?.status !== "passed";
}

function percent(value: number) {
    return `${Math.round(value * 1000) / 10}%`;
}

function gateValue(check: DramaGateCheck) {
    switch (check.metric) {
        case "parseRate":
        case "specRate":
        case "dialogueCoverage":
            return `${percent(check.value)} / 需 ≥ ${percent(check.limit)}`;
        case "durationRatio":
            return `${check.value.toFixed(2)} 倍 / 上限 ${check.limit} 倍`;
        default:
            return `${Math.round(check.value * 100) / 100} / 上限 ${Math.round(check.limit * 100) / 100}`;
    }
}

/** 版本列表里的评测一格：状态、进度、门槛逐项结论、启用记录。 */
export function DramaPackageEvalCell({ version, onDetail }: { version: DramaPackageVersion; onDetail: (evaluation: DramaStepEval) => void }) {
    const evaluation = version.eval;
    if (!evaluation) return <span className="text-xs text-foreground/45">{version.origin === "package" ? "未评测" : "--"}</span>;
    const badge = EVAL_BADGES[evaluation.status];
    return (
        <div className="grid gap-1 text-xs">
            <div className="flex flex-wrap items-center gap-1.5">
                <AdminStatusBadge label={badge.label} tone={badge.tone} />
                {evalInFlight(evaluation) ? (
                    <span className="text-foreground/55">
                        {evaluation.tasksDone}/{evaluation.tasksTotal} 个任务
                    </span>
                ) : null}
                {evaluation.activation === "auto" ? <AdminStatusBadge label="已自动启用" tone="success" /> : null}
                {evaluation.activation === "forced" ? <AdminStatusBadge label="强制启用" tone="warning" title={evaluation.activationReason} /> : null}
            </div>
            {evaluation.gate ? (
                <ul className="grid gap-0.5" aria-label="门槛结论">
                    {evaluation.gate.checks.map((check) => (
                        <li key={check.metric} className={check.passed ? "text-foreground/60" : "text-destructive"} title={check.detail}>
                            {check.passed ? "✓" : "✗"} {check.label}：{gateValue(check)}
                        </li>
                    ))}
                </ul>
            ) : null}
            {evaluation.activation === "forced" && evaluation.activationReason ? <div className="text-foreground/60">原因：{evaluation.activationReason}</div> : null}
            {evaluation.error ? <div className="text-foreground/60">{evaluation.error}</div> : null}
            <div className="text-foreground/45">
                {evaluation.chapters.map((chapter) => chapter.label).join("、")} · {evaluation.model}
            </div>
            {evaluation.result ? (
                <Button size="small" type="link" className="justify-self-start px-0" onClick={() => onDetail(evaluation)}>
                    逐章对比
                </Button>
            ) : null}
        </div>
    );
}

type ChapterRow = { key: string; chapter: string; candidate?: DramaEvalChapterResult; baseline?: DramaEvalChapterResult };

function chapterCell(result?: DramaEvalChapterResult) {
    if (!result) return <span className="text-foreground/45">--</span>;
    if (!result.parsed)
        return (
            <span title={result.failure}>
                {result.truncated ? "截断" : "解析失败"}：{result.failure || "--"}
            </span>
        );
    return (
        <div className="grid gap-0.5">
            <span>
                spec {result.specRows}/{result.rows} 行 · 台词 {result.linesExact}/{result.linesTotal}
            </span>
            <span>
                error {result.errors} · warn {result.warnings}
            </span>
            <span>
                估时 {Math.round(result.estSeconds)} 秒{result.targetSeconds ? ` / 每集 ${result.targetSeconds} 秒` : ""} · {result.segments} 段 {Math.round(result.segmentSeconds)} 秒
            </span>
        </div>
    );
}

/** 规则编号上候选与对照条数不同的地方（看 warn 多在哪条规则）。 */
function ruleDiff(row: ChapterRow) {
    const rules = new Set([...Object.keys(row.candidate?.rules || {}), ...Object.keys(row.baseline?.rules || {})]);
    const diffs = [...rules].sort().flatMap((rule) => {
        const a = row.candidate?.rules?.[rule] || 0;
        const b = row.baseline?.rules?.[rule] || 0;
        return a === b ? [] : [`${rule} ${b}→${a}`];
    });
    return diffs.length ? diffs.join("、") : "相同";
}

/** 评测的逐章对比：这个版本与当时启用版本在同一组章节上的结果。 */
export function DramaPackageEvalDetail({ evaluation, onClose }: { evaluation: DramaStepEval | null; onClose: () => void }) {
    const rows: ChapterRow[] = (evaluation?.chapters || []).map((chapter) => ({
        key: chapter.id,
        chapter: chapter.label,
        candidate: evaluation?.result?.candidate.find((item) => item.chapter === chapter.id),
        baseline: evaluation?.result?.baseline.find((item) => item.chapter === chapter.id),
    }));
    const columns: ColumnsType<ChapterRow> = [
        { title: "章节", dataIndex: "chapter", width: 200 },
        { title: "这个版本", width: 260, render: (_, row) => <div className="text-xs">{chapterCell(row.candidate)}</div> },
        { title: "当时启用的版本", width: 260, render: (_, row) => <div className="text-xs">{chapterCell(row.baseline)}</div> },
        { title: "检查规则差异（对照→这个版本）", render: (_, row) => <span className="text-xs">{ruleDiff(row)}</span> },
    ];
    return (
        <AppModal open={Boolean(evaluation)} onCancel={onClose} footer={null} width={1080} title="评测逐章对比">
            {evaluation?.result?.baselineRunId ? <p className="mb-2 text-xs text-foreground/55">对照复用了启用版本上一次评测的结果。</p> : null}
            <AdminDataTable table={{ className: "app-data-table", size: "small", rowKey: "key", pagination: false, columns, dataSource: rows, scroll: { x: 960 } }} />
        </AppModal>
    );
}

/** 强制启用：评测没通过（或没跑完）的版本，管理员写明原因后启用，原因进审计日志。 */
export function DramaPackageForceActivate({ version, loading, onCancel, onConfirm }: { version: DramaPackageVersion | null; loading: boolean; onCancel: () => void; onConfirm: (reason: string) => void }) {
    const [reason, setReason] = useState("");
    const status = version?.eval ? EVAL_BADGES[version.eval.status].label : "";
    return (
        <AppModal
            open={Boolean(version)}
            title={`强制启用 ${version?.versionLabel || ""}`}
            okText="强制启用"
            cancelText="取消"
            okButtonProps={{ danger: true, disabled: !reason.trim(), loading }}
            onCancel={() => {
                setReason("");
                onCancel();
            }}
            onOk={() => onConfirm(reason.trim())}
            afterClose={() => setReason("")}
        >
            <p className="mb-2 text-sm text-foreground/70">这个版本的评测状态是「{status}」，没有过自动启用门槛。所有项目下次运行这一步就会用它；原因会记进审计日志。</p>
            <Input.TextArea value={reason} maxLength={500} showCount autoSize={{ minRows: 3 }} onChange={(event) => setReason(event.target.value)} placeholder="写明为什么跳过门槛启用（必填）" aria-label="强制启用的原因" />
        </AppModal>
    );
}
