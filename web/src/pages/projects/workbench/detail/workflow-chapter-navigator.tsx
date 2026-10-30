import { useDeferredValue, useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Input } from "antd";
import { BookOpenText, ChevronDown, ChevronLeft, ChevronRight, Search } from "lucide-react";
import { useNavigate } from "react-router";

import { PaginationBar } from "@/components/layout/workspace-page";
import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import { getDramaProgress } from "@/services/api/project-drama";
import type { ProjectUnit } from "@/services/api/projects";

import { issueSegmentsByUnit } from "./drama-review-queue";
import { unitKindOf, unitsOfKind } from "./unit-numbering";

type Props = {
    projectId: string;
    units: ProjectUnit[];
    unitId?: string;
    stage?: string;
};

export function WorkflowChapterNavigator({ projectId, units, unitId, stage }: Props) {
    const navigate = useNavigate();
    const [open, setOpen] = useState(false);
    const [query, setQuery] = useState("");
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(100);
    const deferredQuery = useDeferredValue(query.trim().toLocaleLowerCase("zh-CN"));
    // 与制作台、总览同一份段级进度（同一个 key）：章节信息行带上「带问题的段 N」。
    const progressQuery = useQuery({ queryKey: ["project", projectId, "drama", "progress"], queryFn: async ({ signal }) => (await getDramaProgress(projectId, signal)).progress });
    const issuesByUnit = useMemo(() => issueSegmentsByUnit(progressQuery.data?.segments), [progressQuery.data?.segments]);
    // 只在当前单元同一类里导航和编号：剧集是「第 N 集」，不和原文章节混排。
    const kind = unitKindOf(units.find((unit) => unit.id === unitId) || units.slice().sort((left, right) => left.position - right.position)[0] || { kind: "chapter" });
    const { units: orderedUnits, noun, label } = useMemo(() => unitsOfKind(units, kind), [kind, units]);
    const currentIndex = Math.max(0, orderedUnits.findIndex((unit) => unit.id === unitId));
    const current = orderedUnits[currentIndex];
    const filteredUnits = useMemo(() => {
        if (!deferredQuery) return orderedUnits;
        const numericQuery = /^\d+$/.test(deferredQuery) ? deferredQuery.replace(/^0+/, "") || "0" : "";
        return orderedUnits.filter((unit, index) => (numericQuery && String(index + 1).startsWith(numericQuery)) || unit.title.toLocaleLowerCase("zh-CN").includes(deferredQuery));
    }, [deferredQuery, orderedUnits]);
    const pageCount = Math.max(1, Math.ceil(filteredUnits.length / pageSize));
    const safePage = Math.min(page, pageCount);
    const pagedUnits = filteredUnits.slice((safePage - 1) * pageSize, safePage * pageSize);

    useEffect(() => {
        if (page > pageCount) setPage(pageCount);
    }, [page, pageCount]);

    const openPicker = () => {
        setQuery("");
        setPage(Math.floor(currentIndex / pageSize) + 1);
        setOpen(true);
    };

    const goTo = (target?: ProjectUnit) => {
        if (!target) return;
        setOpen(false);
        navigate(`/projects/${projectId}/workflow/${target.id}/${stage || "video"}`);
    };

    if (!current) return null;
    return (
        <>
            <div className="workflow-chapter-navigator">
                <button type="button" disabled={currentIndex === 0} onClick={() => goTo(orderedUnits[currentIndex - 1])} aria-label={`上一${noun}`} title={`上一${noun}`}><ChevronLeft /></button>
                <button type="button" className="workflow-chapter-current" onClick={openPicker} aria-haspopup="dialog" aria-label={`定位${label}，当前第 ${currentIndex + 1} ${noun}`}>
                    <span>第 {currentIndex + 1} {noun}</span><strong>{current.title}</strong>{issuesByUnit[current.id] ? <span className="shrink-0 text-red-600 dark:text-red-400">带问题的段 {issuesByUnit[current.id]}</span> : null}<em>{currentIndex + 1}/{orderedUnits.length}</em><ChevronDown />
                </button>
                <button type="button" disabled={currentIndex >= orderedUnits.length - 1} onClick={() => goTo(orderedUnits[currentIndex + 1])} aria-label={`下一${noun}`} title={`下一${noun}`}><ChevronRight /></button>
            </div>
            <AppModal flush open={open} footer={null} title={null} className="workspace-modal workspace-modal-wide workflow-chapter-modal" onCancel={() => setOpen(false)}>
                <div className="workflow-chapter-modal-shell">
                    <header className="workflow-chapter-modal-head">
                        <div className="workflow-chapter-modal-title"><span><BookOpenText /></span><div><strong>定位{label}</strong><p>{kind === "episode" ? "快速搜索并跳转到需要制作的剧集" : "快速搜索并跳转到需要制作的剧情章节"}</p></div></div>
                        <div className="workflow-chapter-modal-current"><span>当前</span><strong>第 {currentIndex + 1} {noun}</strong></div>
                    </header>
                    <div className="workflow-chapter-modal-search">
                        <Input autoFocus allowClear size="large" prefix={<Search className="size-4" />} value={query} onChange={(event) => { setQuery(event.target.value); setPage(1); }} placeholder={`输入${label}序号或标题`} />
                        <span>{deferredQuery ? `找到 ${filteredUnits.length.toLocaleString("zh-CN")} ${noun}` : `共 ${orderedUnits.length.toLocaleString("zh-CN")} ${noun}`}</span>
                    </div>
                    <div className="workflow-chapter-list thin-scrollbar">
                        {pagedUnits.length ? <div className="workflow-chapter-grid">{pagedUnits.map((unit) => {
                        const index = orderedUnits.findIndex((item) => item.id === unit.id);
                        const selected = unit.id === current.id;
                        return <button key={unit.id} type="button" className={`workflow-chapter-item${selected ? " is-active" : ""}`} onClick={() => goTo(unit)}><span className="workflow-chapter-item-index">{String(index + 1).padStart(Math.max(2, String(orderedUnits.length).length), "0")}</span><span className="workflow-chapter-item-copy"><strong title={unit.title}>{unit.title}</strong><small>第 {index + 1} {noun} · {(unit.wordCount || 0).toLocaleString("zh-CN")} 字{issuesByUnit[unit.id] ? ` · 带问题的段 ${issuesByUnit[unit.id]}` : ""}</small></span>{selected ? <em>当前</em> : <ChevronRight />}</button>;
                        })}</div> : <div className="workflow-chapter-empty"><Search /><strong>没有匹配的{label}</strong><span>换一个{label}序号或标题试试</span></div>}
                    </div>
                    <footer className="workflow-chapter-modal-pagination"><PaginationBar alwaysShow current={safePage} pageSize={pageSize} total={filteredUnits.length} pageSizeOptions={[100, 200, 500]} itemLabel={noun} onChange={(nextPage, nextPageSize) => { setPage(nextPage); setPageSize(nextPageSize); }} /></footer>
                </div>
            </AppModal>
        </>
    );
}
