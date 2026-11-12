import { useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input } from "antd";
import { ArrowRight, Plus, Search } from "lucide-react";
import { Link, useNavigate } from "react-router";

import { CachedResourceImage } from "@/components/cached-resource-image";
import { resolveCanvasStylePreset, resolveProjectCanvasStyle } from "@/components/canvas/canvas-style-picker-modal";
import { CollectionGrid, PageHeader, WorkspacePage } from "@/components/layout/workspace-page";
import { DeleteButton } from "@/components/ui/base/buttons/delete-button";
import { Select } from "@/components/ui/base/select";
import { CollectionToolbar } from "@/components/layout/collection-toolbar";
import { WorkspaceErrorState, WorkspaceLoadingState, WorkspaceNoResultsState, WorkspaceState } from "@/components/layout/workspace-state";
import { MediaPlaceholder } from "@/components/ui/product/media-placeholder";
import { parseStyleProfile } from "@/lib/canvas/style-profile";
import { projectSummaryCompletion } from "@/lib/project-workbench";
import { projectVisualTypeLabel } from "@/lib/project-visual-type";
import { dramaRunModeLabel } from "@/pages/projects/workbench/drama-model";
import { sourceTypeLabel } from "@/pages/projects/workbench/detail/shared";
import { getDramaSummaries, type DramaProjectSummary } from "@/services/api/project-drama";
import { deleteProject, listProjects, type ProjectSummary } from "@/services/api/projects";
import { resourceFileUrl } from "@/services/api/resources";

import { useLoadMoreSentinel } from "./hooks/use-load-more-sentinel";
import { buildInbox, inboxCounts } from "./model/inbox";
import { flowPrimaryAction } from "./model/primary-action";
import { flowStageStates } from "./model/stages";
import { createSummaryBatcher } from "./model/summary-batcher";

import "./drama-flow.css";

type StatusFilter = "all" | "active" | "attention" | "done" | "archived";

const loadSummary = createSummaryBatcher<DramaProjectSummary>((ids) => getDramaSummaries(ids));
const summaryKey = (projectId: string) => ["drama-flow", "summary", projectId] as const;

/** 卡片上的状态：一句话、语气、要人处理几件事；失败的图和片都算要处理。 */
function summaryFlow(summary: DramaProjectSummary) {
    const counts = inboxCounts(buildInbox({ pipeline: summary.pipeline }));
    const must = counts.must + summary.failedImages + summary.failedVideos;
    return {
        must,
        flow: flowPrimaryAction({
            pipeline: summary.pipeline,
            segments: summary.counts.segments,
            generated: summary.counts.generated,
            inCut: summary.counts.inCut,
            mustHandle: must,
            toConfirm: counts.confirm,
            failedVideos: summary.failedVideos,
            runningVideos: summary.runningVideos,
        }),
    };
}
type SortKey = "updated" | "progress" | "name";

/** 短剧新工作流的项目列表：和「短剧制作」是同一批项目，只是每张卡片直接告诉你「现在该做什么」。 */
export default function DramaFlowListPage() {
    const { message } = App.useApp();
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const [keyword, setKeyword] = useState("");
    const [status, setStatus] = useState<StatusFilter>("all");
    const [sort, setSort] = useState<SortKey>("updated");

    const query = useInfiniteQuery({
        queryKey: ["projects", "paged"],
        queryFn: ({ pageParam }) => listProjects({ page: pageParam, pageSize: 50 }),
        initialPageParam: 1,
        getNextPageParam: (lastPage) => (lastPage.hasMore ? lastPage.page + 1 : undefined),
    });
    const removeMutation = useMutation({
        mutationFn: deleteProject,
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: ["projects"] });
            message.success("项目已删除");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "项目删除失败"),
    });

    const allProjects = useMemo(() => query.data?.pages.flatMap((page) => page.projects) ?? [], [query.data]);
    const totalProjectCount = query.data?.pages[0]?.total ?? allProjects.length;
    // 「等我处理」「已完成」要按进度筛，所以这两档把已加载项目的进度都读上（照样攒批）。
    const needAll = status === "attention" || status === "done";
    const summaryQueries = useQueries({ queries: allProjects.map((item) => ({ queryKey: summaryKey(item.project.id), queryFn: () => loadSummary(item.project.id), enabled: needAll, staleTime: 60_000 })) });
    const summaryState = summaryQueries.map((item) => item.dataUpdatedAt).join(",");
    const summaryFailed = needAll ? summaryQueries.filter((item) => item.isError) : [];
    const summaries = useMemo(() => new Map(allProjects.map((item, index) => [item.project.id, summaryQueries[index]?.data])), [allProjects, summaryState]); // eslint-disable-line react-hooks/exhaustive-deps
    const projects = useMemo(() => {
        const needle = keyword.trim().toLowerCase();
        const filtered = allProjects.filter((item) => {
            const archived = item.project.status === "archived";
            if (status === "archived" && !archived) return false;
            if (status !== "archived" && status !== "all" && archived) return false;
            if (needAll) {
                const summary = summaries.get(item.project.id);
                if (!summary) return false;
                const { must, flow } = summaryFlow(summary);
                if (status === "attention" && !must && flow.tone !== "attention" && flow.tone !== "ready") return false;
                if (status === "done" && flow.tone !== "done") return false;
            }
            if (!needle) return true;
            const style = resolveProjectCanvasStyle(item.project.stylePresetId, item.project.styleProfileJson);
            return [item.project.name, item.project.description, item.project.stylePresetId, style?.title].some((value) => (value || "").toLowerCase().includes(needle));
        });
        return [...filtered].sort((a, b) => {
            if (sort === "name") return a.project.name.localeCompare(b.project.name, "zh-CN");
            if (sort === "progress") return projectSummaryCompletion(b) - projectSummaryCompletion(a);
            return new Date(b.project.updatedAt).getTime() - new Date(a.project.updatedAt).getTime();
        });
    }, [allProjects, keyword, sort, status, needAll, summaries]);

    const { fetchNextPage, hasNextPage, isError, isFetchingNextPage } = query;
    const loadMoreRef = useLoadMoreSentinel({ enabled: Boolean(hasNextPage) && !isFetchingNextPage && !isError, onLoadMore: fetchNextPage });

    const filterActive = Boolean(keyword.trim()) || status !== "all" || sort !== "updated";
    const resetFilters = () => {
        setKeyword("");
        setStatus("all");
        setSort("updated");
    };
    const hasInitialError = query.isError && !query.data;
    const createButton = (
        <Button type="primary" icon={<Plus className="size-4" />} onClick={() => navigate("/drama-flow/new")}>
            新建短剧
        </Button>
    );

    return (
        <WorkspacePage>
            <PageHeader title="短剧新工作流" description="贴进剧本，后面自动做；只在要花钱、出了问题的时候叫你。和「短剧制作」是同一批项目。" actions={createButton} />
            <CollectionToolbar active={filterActive} onReset={resetFilters} label="筛选短剧项目">
                <Input allowClear className="app-list-search" prefix={<Search className="size-4 text-foreground/40" />} placeholder="搜索项目、简介或画风" value={keyword} onChange={(event) => setKeyword(event.target.value)} />
                <Select
                    className="w-36"
                    value={status}
                    onChange={(value) => setStatus(value as StatusFilter)}
                    options={[
                        { value: "all", label: "全部状态" },
                        { value: "active", label: "进行中" },
                        { value: "attention", label: "等我处理" },
                        { value: "done", label: "已完成" },
                        { value: "archived", label: "已归档" },
                    ]}
                />
                <Select
                    className="w-32"
                    value={sort}
                    onChange={(value) => setSort(value as SortKey)}
                    options={[
                        { value: "updated", label: "最近更新" },
                        { value: "progress", label: "章节进度" },
                        { value: "name", label: "项目名称" },
                    ]}
                />
            </CollectionToolbar>

            {hasInitialError ? (
                <WorkspaceErrorState description={query.error instanceof Error ? query.error.message : "项目列表读取失败"} onRetry={() => void query.refetch()} />
            ) : query.isLoading ? (
                <WorkspaceLoadingState label="正在整理项目" detail="读取每部短剧做到哪一步" />
            ) : !allProjects.length ? (
                <WorkspaceState title="还没有短剧" description="贴一段剧本或小说就能开始，画风、分镜、定妆都会自动做。" action={createButton} />
            ) : needAll && summaryQueries.some((item) => item.isLoading) ? (
                <WorkspaceLoadingState label="正在读每个项目的进度" detail="按进度筛选要先读完已加载的项目" />
            ) : (
                <>
                    {needAll && summaryFailed.length ? (
                        <div className="drama-flow-note" data-tone="error">
                            有 {summaryFailed.length} 个项目的进度没读到，没法判断它们算不算「{status === "done" ? "已完成" : "等我处理"}」，下面的结果不含它们。
                            <Button size="small" type="link" onClick={() => summaryFailed.forEach((item) => void item.refetch())}>
                                再读一次
                            </Button>
                        </div>
                    ) : null}
                    {projects.length ? (
                        <CollectionGrid className="library-grid project-library-grid">
                            {projects.map((item) => (
                                <FlowProjectCard key={item.project.id} item={item} onRemove={() => removeMutation.mutateAsync(item.project.id)} />
                            ))}
                        </CollectionGrid>
                    ) : query.hasNextPage ? (
                        <p className="drama-flow-muted">已加载的 {allProjects.length} 个项目里没有符合的，正在往后加载…</p>
                    ) : (
                        <WorkspaceNoResultsState onClear={resetFilters} />
                    )}
                    {/* 分页入口不跟筛选结果走：当前页没有匹配时也要能接着加载后面的项目。 */}
                    <div ref={loadMoreRef} className="library-load-more">
                        {query.isFetchingNextPage ? (
                            "正在加载更多项目…"
                        ) : query.isError ? (
                            <Button type="link" onClick={() => void query.fetchNextPage()}>
                                加载更多失败，点击重试
                            </Button>
                        ) : query.hasNextPage ? (
                            "继续下滑加载更多（每页 50 条）"
                        ) : (
                            `已加载全部 ${totalProjectCount} 个项目`
                        )}
                    </div>
                </>
            )}
        </WorkspacePage>
    );
}

function FlowProjectCard({ item, onRemove }: { item: ProjectSummary; onRemove: () => Promise<unknown> }) {
    const { project } = item;
    const cardRef = useRef<HTMLAnchorElement | null>(null);
    const [inView, setInView] = useState(false);
    // 进度按卡片各读一次：只有滚进视野的卡片才发请求，避免一次拉 50 个项目的进度。
    useEffect(() => {
        const node = cardRef.current;
        if (!node || inView) return;
        const observer = new IntersectionObserver(
            (entries) => {
                if (entries.some((entry) => entry.isIntersecting)) setInView(true);
            },
            { rootMargin: "200px" },
        );
        observer.observe(node);
        return () => observer.disconnect();
    }, [inView]);
    const summaryQuery = useQuery({ queryKey: summaryKey(project.id), queryFn: () => loadSummary(project.id), enabled: inView, staleTime: 60_000 });
    const summary = summaryQuery.data;
    const view = useMemo(() => (summary ? summaryFlow(summary) : undefined), [summary]);
    const flow = view?.flow;
    const stages = useMemo(() => flowStageStates(summary?.pipeline), [summary]);

    const projectStyle = resolveProjectCanvasStyle(project.stylePresetId, project.styleProfileJson);
    const styleTitle = projectVisualTypeLabel(project.visualType) || projectStyle?.title || parseStyleProfile(project.styleProfileJson)?.title || resolveCanvasStylePreset(project.stylePresetId)?.title || "未选画面类型";
    const coverUrl = project.coverResourceId ? resourceFileUrl(project.coverResourceId) : projectStyle?.imageUrl;
    const statusText = summaryQuery.isError ? "进度没读到，点进去再看" : flow ? flow.status : "正在读取进度…";

    return (
        <Link ref={cardRef} to={`/drama-flow/${project.id}`} className="product-collection-card library-card project-library-card group">
            <span className="project-library-cover">
                {coverUrl ? <CachedResourceImage className="project-library-cover-art" src={coverUrl} alt="" fallback={<MediaPlaceholder failed />} /> : <MediaPlaceholder label="故事由此开始" />}
                <span className="project-library-cover-scrim" />
                <span className="project-library-cover-ratio" style={{ aspectRatio: project.aspectRatio.replace(":", " / ") }} />
                <span className="project-library-cover-stage">{project.aspectRatio}</span>
                <span
                    className="project-collection-delete"
                    onClick={(event) => {
                        event.preventDefault();
                        event.stopPropagation();
                    }}
                >
                    <DeleteButton label={`删除项目 ${project.name}`} description="项目章节、画布关联和素材归属将一并移除；独立画布与素材库原始素材会保留。此操作不可撤销。" onConfirm={onRemove} />
                </span>
            </span>
            <span className="project-library-body">
                <span className="project-library-heading">
                    <strong title={project.name}>{project.name}</strong>
                    {project.status === "archived" ? <em>已归档</em> : null}
                    <ArrowRight className="project-library-arrow size-4" />
                </span>
                <span className="project-library-subtitle">
                    {styleTitle} · {sourceTypeLabel(project.sourceType)}
                </span>
                <span className="drama-flow-card-status">
                    <span className="drama-flow-dot" data-tone={flow?.tone ?? "idle"} aria-hidden="true" />
                    <span>{statusText}</span>
                </span>
                {summary ? (
                    <span className="drama-flow-card-facts">
                        <span>{summary.episodes ? `${summary.episodes} 集` : summary.chapters ? `${summary.chapters} 章原文，还没改编` : "还没有正文"}</span>
                        <span>
                            出片 {summary.counts.generated}/{summary.counts.segments}
                        </span>
                        {summary.runningVideos ? <span>在出 {summary.runningVideos}</span> : null}
                        {summary.failedImages + summary.failedVideos ? <span data-tone="bad">失败 {summary.failedImages + summary.failedVideos}</span> : null}
                        {view?.must ? <span data-tone="bad">待办 {view.must}</span> : null}
                        <span>{summary.pipeline ? dramaRunModeLabel(summary.pipeline.runMode) : "生成模式没读到"}</span>
                        <span>{new Date(project.updatedAt).toLocaleDateString("zh-CN")} 更新</span>
                    </span>
                ) : null}
                <span className="drama-flow-mini" aria-label="制作进度">
                    {stages.map((stage) => (
                        <span key={stage.key} data-state={stage.state}>
                            <i />
                            {stage.label}
                        </span>
                    ))}
                </span>
            </span>
        </Link>
    );
}
