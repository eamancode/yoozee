import { App, Button, Collapse, Input, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { FlaskConical } from "lucide-react";
import { useEffect, useState } from "react";

import { Select } from "@/components/ui/base/select";
import { listAdminDramaEvalRuns, runAdminDramaEval, type DramaCompileProfileVersion, type DramaEvalRow, type DramaEvalRun } from "@/services/api/admin-drama-compile-profiles";
import { listDramaModels, type DramaModel } from "@/services/api/drama";
import { AdminStatusBadge } from "../components/admin-ui";

/** 评测重跑（视频提示词阶段）：已存镜头表 → 装段 → 编译，对比候选与基线编译档案，不调模型、不改项目。 */
export function DramaEvalPanel({ profiles, activeVersion }: { profiles: DramaCompileProfileVersion[]; activeVersion: number }) {
    const { message } = App.useApp();
    const [projectId, setProjectId] = useState("");
    const [unitIds, setUnitIds] = useState("");
    const [candidate, setCandidate] = useState<number | null>(null);
    const [baseline, setBaseline] = useState<string>("active");
    const [model, setModel] = useState<string>("project");
    const [models, setModels] = useState<DramaModel[]>([]);
    const [running, setRunning] = useState(false);
    const [run, setRun] = useState<DramaEvalRun | null>(null);
    const [history, setHistory] = useState<DramaEvalRun[]>([]);

    useEffect(() => {
        void listDramaModels()
            .then((result) => setModels(result.models.models))
            .catch(() => setModels([]));
        void listAdminDramaEvalRuns()
            .then((result) => setHistory(result.runs))
            .catch(() => setHistory([]));
    }, []);

    const versionOptions = [{ label: "v0 内置默认", value: 0 }, ...profiles.map((item) => ({ label: `v${item.version} ${item.name}${item.enabled ? "（生效中）" : ""}`, value: item.version }))];

    const submit = async () => {
        if (!projectId.trim() || candidate === null) {
            message.warning("请填写项目 ID 并选择候选版本");
            return;
        }
        setRunning(true);
        try {
            const { run: result } = await runAdminDramaEval({
                projectId: projectId.trim(),
                unitIds: unitIds
                    .split(/[,，\s]+/)
                    .map((item) => item.trim())
                    .filter(Boolean),
                candidateVersion: candidate,
                ...(baseline === "active" ? {} : { baselineVersion: Number(baseline) }),
                ...(model === "project" ? {} : { model }),
            });
            setRun(result);
            setHistory((items) => [{ ...result, segments: undefined }, ...items].slice(0, 30));
        } catch (error) {
            message.error(error instanceof Error ? error.message : "评测失败");
        } finally {
            setRunning(false);
        }
    };

    return (
        <section className="mt-6 rounded-md border border-border p-4">
            <div className="mb-3 flex flex-wrap items-center gap-2">
                <FlaskConical className="size-4" />
                <h2 className="text-sm font-semibold">评测重跑 · 视频提示词</h2>
                <span className="text-xs text-foreground/50">用已存镜头表重新装段、编译，对比两个版本；不调模型、不改项目。先用「yoozee 实测·七床」第一章。</span>
            </div>
            <div className="grid gap-3 md:grid-cols-5">
                <Input aria-label="项目 ID" placeholder="项目 ID（项目页地址里的 ID）" value={projectId} onChange={(event) => setProjectId(event.target.value)} />
                <Input aria-label="章节 ID" placeholder="章节 ID，可多个；不填取有镜头表的第一章" value={unitIds} onChange={(event) => setUnitIds(event.target.value)} />
                <Select aria-label="候选版本" placeholder="候选版本" value={candidate ?? undefined} onChange={(value) => setCandidate(Number(value))} options={versionOptions} />
                <Select
                    aria-label="基线版本"
                    value={baseline}
                    onChange={setBaseline}
                    options={[{ label: `基线：当前生效（v${activeVersion}）`, value: "active" }, ...versionOptions.map((item) => ({ label: `基线：${item.label}`, value: String(item.value) }))]}
                />
                <Select aria-label="生成模型" value={model} onChange={setModel} options={[{ label: "模型：项目默认", value: "project" }, ...models.map((item) => ({ label: `模型：${item.label}`, value: item.id }))]} />
            </div>
            <div className="mt-3 flex justify-end">
                <Button type="primary" loading={running} onClick={() => void submit()}>
                    开始评测
                </Button>
            </div>
            {run ? <EvalResult run={run} /> : null}
            {history.length ? (
                <Collapse
                    className="mt-4"
                    size="small"
                    items={[
                        {
                            key: "history",
                            label: `最近的评测（${history.length}）`,
                            children: (
                                <div className="space-y-4">
                                    {history.map((item) => (
                                        <EvalTable key={item.id || item.createdAt} run={item} />
                                    ))}
                                </div>
                            ),
                        },
                    ]}
                />
            ) : null}
        </section>
    );
}

function EvalResult({ run }: { run: DramaEvalRun }) {
    const segments = run.segments || [];
    const changed = segments.filter((item) => item.candidate !== item.baseline).length;
    return (
        <div className="mt-4 space-y-3">
            <EvalTable run={run} />
            <Collapse
                size="small"
                items={[
                    {
                        key: "segments",
                        label: `逐段提示词（不同 ${changed} / ${segments.length}）`,
                        children: (
                            <div className="space-y-4">
                                {segments.map((item) => (
                                    <div key={`${item.unit}-${item.segment}`}>
                                        <div className="mb-1 flex items-center gap-2 text-xs font-medium">
                                            {item.unit} · {item.segment} · {item.seconds} 秒
                                            {item.candidate === item.baseline ? <AdminStatusBadge label="相同" /> : <AdminStatusBadge label={`字数 ${item.baselineChars} → ${item.candidateChars}`} tone="info" />}
                                        </div>
                                        <div className="grid gap-2 md:grid-cols-2">
                                            <pre className="thin-scrollbar max-h-72 overflow-auto whitespace-pre-wrap rounded-md bg-muted/50 p-2 text-xs leading-5">{item.baseline}</pre>
                                            <pre className="thin-scrollbar max-h-72 overflow-auto whitespace-pre-wrap rounded-md bg-muted/50 p-2 text-xs leading-5">{item.candidate}</pre>
                                        </div>
                                    </div>
                                ))}
                            </div>
                        ),
                    },
                    { key: "markdown", label: "Markdown 指标表", children: <pre className="thin-scrollbar max-h-72 overflow-auto whitespace-pre-wrap text-xs leading-5">{run.markdown}</pre> },
                ]}
            />
        </div>
    );
}

function EvalTable({ run }: { run: DramaEvalRun }) {
    const columns: ColumnsType<DramaEvalRow> = [
        { title: "章节", dataIndex: "unit" },
        { title: "档案", render: (_, row) => `v${row.version} ${row.versionName}` },
        { title: "检查 error / warn", align: "center", render: (_, row) => `${row.errors} / ${row.warnings}` },
        { title: "spec 写出率", align: "center", render: (_, row) => (row.specRate < 0 ? "—" : `${Math.round(row.specRate * 100)}%`) },
        { title: "段数", align: "center", render: (_, row) => row.failure || row.segments },
        { title: "总时长", align: "center", render: (_, row) => `${row.totalSeconds} 秒` },
        { title: "段时长", align: "center", render: (_, row) => `${row.minSeconds}～${row.maxSeconds} 秒` },
        { title: "字数 合计 / 平均 / 最长", align: "center", render: (_, row) => `${row.chars} / ${row.avgChars} / ${row.maxChars}` },
        { title: "超上限", dataIndex: "overLimit", align: "center" },
        { title: "超软预算", dataIndex: "overSoft", align: "center" },
        { title: "编译 warn", dataIndex: "compileWarns", align: "center" },
    ];
    return (
        <div>
            <div className="mb-1 text-xs text-foreground/55">
                {new Date(run.createdAt).toLocaleString("zh-CN", { hour12: false })} · {run.model} · 基线 v{run.baseline.version} {run.baseline.name} · 候选 v{run.candidate.version} {run.candidate.name}
            </div>
            <Table className="app-data-table" size="small" rowKey={(row) => `${row.unit}-${row.version}-${row.versionName}`} pagination={false} columns={columns} dataSource={run.rows} scroll={{ x: 960 }} />
        </div>
    );
}
