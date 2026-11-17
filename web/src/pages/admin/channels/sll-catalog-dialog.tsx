import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Input } from "antd";
import { useState } from "react";

import { AdminModal } from "../ui/overlays";
import { getSLLCatalog, refreshSLLMetrics, syncSLLCatalog, type SLLCapabilities, type SLLCatalogRow, type SLLCatalogView, type SLLMetric } from "@/services/api/admin-sll-catalog";

function localTime(value?: string | null) {
    return value && !value.startsWith("0001-") ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "—";
}

function durationText(cap: SLLCapabilities) {
    if (cap.durations?.length) return cap.durations.join(" / ") + " 秒";
    if (cap.durationMin && cap.durationMax) return cap.durationMin === cap.durationMax ? `${cap.durationMin} 秒` : `${cap.durationMin}–${cap.durationMax} 秒`;
    return "待核实";
}

function rateText(metric: SLLMetric) {
    const rate = metric.rate != null ? `${metric.rate}%` : "";
    switch (metric.state) {
        case "value": return rate || "无统计";
        case "unavailable": return "供应商无统计";
        case "fetch_failed": return rate ? `采集失败，上次 ${rate}` : "采集失败";
        default: return "尚未采集";
    }
}

function referenceLimit(value?: number | null) {
    return value == null ? "待核实" : value === 0 ? "不支持" : `最多 ${value} 个`;
}

function SLLModelDetail({ row }: { row: SLLCatalogRow }) {
    const cap = row.capabilities;
    const metric = row.metric;
    return (
        <details className="border-b border-[var(--admin-border)] py-2">
            <summary className="cursor-pointer break-words">
                <strong>{row.displayName}</strong> · {row.modelId} · {row.available ? (row.enabled ? "已启用" : "已停用") : "已排除"}
                <span className="ml-2">{rateText(metric)}{metric.stale ? "（已过期）" : ""}</span>
            </summary>
            <div className="mt-2 space-y-1 pl-4 text-sm">
                {row.reason && <p>{row.reason}</p>}
                {row.description && <p>供应商说明 · {row.description}</p>}
                <p>上游人民币成本（{row.group} 分组，倍率 {row.groupRatio}） · {(row.tiers || []).map((tier) => `${tier.resolution} ¥${tier.costMicroCny / 1_000_000}/条`).join("、") || "无合格价档"}</p>
                <p>时长 {durationText(cap)} · 画幅 {cap.aspectRatios?.join(" / ") || "待核实"}</p>
                <p>图片 {referenceLimit(cap.maxImages)}{cap.minImages ? `，至少 ${cap.minImages} 张` : ""} · 视频 {referenceLimit(cap.maxVideos)} · 音频 {referenceLimit(cap.maxAudios)}</p>
                <p>参考总数 {referenceLimit(cap.maxReferences)} · 单条参考视频时长 {cap.maxVideoSeconds == null ? "待核实" : `≤ ${cap.maxVideoSeconds} 秒`} · 同步生成声音 {cap.generateAudio == null ? "待核实" : cap.generateAudio ? "支持" : "不支持"}</p>
                <p>官方模型级成功率（近 {metric.windowHours || 24} 小时） · {rateText(metric)}{metric.stale ? " · 已过期" : ""} · 样本数 {metric.sampleCount ?? "未公开"}</p>
                <p>统计来源 {metric.source || "SLL 公开成功率"} · 采集时间 {localTime(metric.collectedAt)} · 最近尝试 {localTime(metric.attemptedAt)}</p>
            </div>
        </details>
    );
}

export function SLLCatalogResults({ view, keyword }: { view: SLLCatalogView; keyword: string }) {
    const term = keyword.trim().toLowerCase();
    const rows = (view.rows || []).filter((row) => `${row.displayName} ${row.modelId}`.toLowerCase().includes(term));
    const exclusions = view.excluded || [];
    const visibleExclusions = exclusions.filter((row) => `${row.modelId} ${row.reason} ${row.resolution || ""}`.toLowerCase().includes(term));
    return (
        <div className="space-y-2">
            {localTime(view.collectedAt) !== "—" && (
                <div className="text-sm">
                    <p>最近目录同步 {localTime(view.collectedAt)} · 分组 {view.group} · 新增 {view.added} · 更新 {view.updated} · 排除 {exclusions.length} 项</p>
                    <p className="break-all">价目来源 {view.source}{view.version ? ` · 来源版本 ${view.version}` : ""} · 供应商更新时间未公开</p>
                </div>
            )}
            <div className="max-h-[42vh] overflow-auto" aria-label="SLL 线路与规格">
                {rows.map((row) => <SLLModelDetail key={row.modelId} row={row} />)}
                {!rows.length && <p>{term ? "没有匹配的线路" : "当前没有已同步的线路"}</p>}
            </div>
            {exclusions.length > 0 && (
                <details>
                    <summary className="cursor-pointer">查看排除原因（{visibleExclusions.length}）</summary>
                    <ul className="max-h-48 overflow-auto text-sm">
                        {visibleExclusions.map((row, index) => <li key={`${row.modelId}-${row.resolution}-${index}`}>{row.modelId} {row.resolution} · {row.reason}</li>)}
                    </ul>
                </details>
            )}
        </div>
    );
}

export function SLLCatalogDialog({ channelId, onClose, onImported }: { channelId: string; onClose: () => void; onImported: () => Promise<void> }) {
    const client = useQueryClient();
    const key = ["admin", "sll-catalog", channelId];
    const query = useQuery({ queryKey: key, queryFn: ({ signal }) => getSLLCatalog(channelId, signal), refetchInterval: 60_000 });
    const [draft, setDraft] = useState<{ value: string; reconfirming: boolean } | null>(null);
    const [keyword, setKeyword] = useState("");
    const [working, setWorking] = useState<"sync" | "metrics" | null>(null);
    const [notice, setNotice] = useState("");
    const [error, setError] = useState("");
    const reconfirming = query.data?.needsGroupConfirmation === true;
    const confirmedGroup = reconfirming ? "" : (query.data?.group ?? "");
    const group = draft && draft.reconfirming === reconfirming ? draft.value : confirmedGroup;
    const run = async (action: "sync" | "metrics") => {
        setWorking(action);
        setError("");
        setNotice("");
        try {
            if (action === "sync") {
                const result = await syncSLLCatalog(channelId, group.trim());
                client.setQueryData(key, result);
                setDraft(null);
                setNotice("目录同步完成");
                await onImported();
            } else {
                await refreshSLLMetrics(channelId);
                setNotice("成功率采集完成");
            }
        } catch (cause) {
            setError(cause instanceof Error ? cause.message : "操作失败");
        } finally {
            await query.refetch();
            setWorking(null);
        }
    };
    return (
        <AdminModal open title="SLL 视频线路目录" width={920} footer={null} closable={!working} mask={{ closable: !working }} keyboard={!working} onCancel={onClose}>
            <div className="space-y-3">
                <p>按凭据的实际调用分组，同步 Seedance 2.5 单条成本不超过 ¥6 的规格。在渠道模型表中设置模型启停和面向用户的积分售价。</p>
                {reconfirming && <p role="status">渠道凭据已变更，请重新填写当前凭据使用的供应商分组。</p>}
                <div className="flex flex-wrap items-center gap-2">
                    <label htmlFor="sll-call-group">供应商调用分组</label>
                    <Input id="sll-call-group" className="max-w-44" placeholder="填写实际分组" maxLength={128} value={group} disabled={!!working} onChange={(event) => setDraft({ value: event.target.value, reconfirming })} />
                    <Button type="primary" loading={working === "sync"} disabled={!!working || !group.trim() || !query.data} onClick={() => void run("sync")}>同步公开价目</Button>
                    <Button loading={working === "metrics"} disabled={!!working || !query.data?.rows?.length} onClick={() => void run("metrics")}>刷新官方成功率</Button>
                </div>
                <p className="text-sm">成功率每小时采集，按上游模型 ID 关联。连续两个周期未更新时标为过期。</p>
                {(error || query.error) && <div role="alert">{error || query.error?.message}{query.error && <Button size="small" onClick={() => void query.refetch()}>重读目录</Button>}</div>}
                {notice && <p role="status">{notice}</p>}
                <Input aria-label="搜索 SLL 展示名或上游 ID" placeholder="搜索展示名或上游 ID" value={keyword} allowClear onChange={(event) => setKeyword(event.target.value)} />
                {query.data ? <SLLCatalogResults view={query.data} keyword={keyword} /> : query.isLoading && <p role="status">正在读取目录…</p>}
            </div>
        </AdminModal>
    );
}
