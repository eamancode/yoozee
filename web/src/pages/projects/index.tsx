import { CollectionToolbar } from "@/components/layout/collection-toolbar";
import { DeleteButton } from "@/components/ui/base/buttons/delete-button";
import { CachedResourceImage } from "@/components/cached-resource-image";
import { MediaPlaceholder } from "@/components/ui/product/media-placeholder";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input } from "antd";
import { ArrowRight, BookOpenText, Images, LayoutGrid, Plus, Search } from "lucide-react";
import { Link } from "react-router";

import { CollectionGrid, WorkspacePage } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceLoadingState, WorkspaceState } from "@/components/layout/workspace-state";
import { resolveCanvasStylePreset, resolveProjectCanvasStyle } from "@/components/canvas/canvas-style-picker-modal";
import { resourceFileUrl } from "@/services/api/resources";
import { parseStyleProfile } from "@/lib/canvas/style-profile";
import { projectSummaryCompletion, projectSummaryStage } from "@/lib/project-workbench";
import { projectVisualTypeLabel } from "@/lib/project-visual-type";
import { deleteProject, listProjects, type ProjectSummary } from "@/services/api/projects";

import { sourceTypeLabel } from "./detail/shared";
import { Select } from "@/components/ui/base/select";
import { ProjectComposer } from "./create/project-composer";

export default function ProjectsPage() {
    const queryClient = useQueryClient();
    const { message } = App.useApp();
    const [keyword, setKeyword] = useState("");
    const [status, setStatus] = useState<"all" | "active" | "archived">("all");
    const [sort, setSort] = useState<"updated" | "progress" | "name">("updated");
    const loadMoreRef = useRef<HTMLDivElement>(null);
    const query = useInfiniteQuery({
        // 分页查询和画布页的全量项目查询不能共用缓存形状，否则两个页面会互相覆盖缓存数据。
        queryKey: ["projects", "paged"],
        queryFn: ({ pageParam }) => listProjects({ page: pageParam, pageSize: 50 }),
        initialPageParam: 1,
        getNextPageParam: (lastPage) => (lastPage.hasMore ? lastPage.page + 1 : undefined),
    });
    const deleteMutation = useMutation({
        mutationFn: deleteProject,
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ["projects"] });
            message.success("项目已删除");
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "项目删除失败"),
    });
    const allProjects = useMemo(() => query.data?.pages.flatMap((page) => page.projects) || [], [query.data]);
    const rows = useMemo(() => {
        const normalizedKeyword = keyword.trim().toLowerCase();
        return [...allProjects]
            .filter(({ project }) => status === "all" || project.status === status)
            .filter(
                ({ project }) =>
                    !normalizedKeyword ||
                    `${project.name} ${project.description} ${project.stylePresetId} ${parseStyleProfile(project.styleProfileJson)?.title || resolveCanvasStylePreset(project.stylePresetId)?.title || ""}`.toLowerCase().includes(normalizedKeyword),
            )
            .sort((left, right) => {
                if (sort === "name") return left.project.name.localeCompare(right.project.name, "zh-CN");
                if (sort === "progress") return projectSummaryCompletion(right) - projectSummaryCompletion(left);
                return right.project.updatedAt.localeCompare(left.project.updatedAt);
            });
    }, [allProjects, keyword, sort, status]);
    const totalProjectCount = query.data?.pages[0]?.total ?? allProjects.length;
    useEffect(() => {
        const node = loadMoreRef.current;
        if (!node || !query.hasNextPage || query.isError) return;
        const observer = new IntersectionObserver(
            ([entry]) => {
                if (entry?.isIntersecting && !query.isFetchingNextPage) void query.fetchNextPage();
            },
            { rootMargin: "600px" },
        );
        observer.observe(node);
        return () => observer.disconnect();
    }, [query.fetchNextPage, query.hasNextPage, query.isError, query.isFetchingNextPage]);
    const hasInitialError = query.isError && !query.data;
    return (
        <WorkspacePage className="library-page project-library-page" grid>
            <ProjectComposer />
            <CollectionToolbar
                active={Boolean(keyword || status !== "all" || sort !== "updated")}
                onReset={() => {
                    setKeyword("");
                    setStatus("all");
                    setSort("updated");
                }}
            >
                <Input allowClear className="app-list-search" prefix={<Search className="size-4 text-foreground/40" />} value={keyword} placeholder="搜索项目、简介或画风" onChange={(event) => setKeyword(event.target.value)} />
                <Select
                    className="w-32"
                    value={status}
                    onChange={setStatus}
                    options={[
                        { label: "全部状态", value: "all" },
                        { label: "进行中", value: "active" },
                        { label: "已归档", value: "archived" },
                    ]}
                />
                <Select
                    className="w-32"
                    value={sort}
                    onChange={setSort}
                    options={[
                        { label: "最近更新", value: "updated" },
                        { label: "章节进度", value: "progress" },
                        { label: "项目名称", value: "name" },
                    ]}
                />
            </CollectionToolbar>

            {hasInitialError ? <WorkspaceErrorState description={query.error instanceof Error ? query.error.message : "项目列表加载失败"} onRetry={() => void query.refetch()} /> : null}
            {query.isLoading ? <WorkspaceLoadingState label="正在整理项目" detail="读取章节、画布与资产进度" /> : null}
            {!query.isLoading && !hasInitialError && rows.length ? (
                <CollectionGrid className="library-grid project-library-grid">
                    {rows.map((row) => (
                        <ProjectRow key={row.project.id} row={row} onDelete={() => deleteMutation.mutateAsync(row.project.id)} />
                    ))}
                </CollectionGrid>
            ) : null}
            {!query.isLoading && !hasInitialError ? (
                <div ref={loadMoreRef} className="library-load-more" aria-live="polite">
                    {query.isFetchingNextPage ? (
                        "正在加载更多项目…"
                    ) : query.isError ? (
                        <button type="button" onClick={() => void query.fetchNextPage()}>
                            加载更多失败，点击重试
                        </button>
                    ) : query.hasNextPage ? (
                        "继续下滑加载更多（每页 50 条）"
                    ) : allProjects.length ? (
                        `已加载全部 ${totalProjectCount} 个项目`
                    ) : null}
                </div>
            ) : null}
            {!query.isLoading && !rows.length && !hasInitialError && (keyword || status !== "all") ? (
                <WorkspaceState
                    icon="projects"
                    title={keyword || status !== "all" ? "没有匹配的项目" : "创建第一个故事项目"}
                    description={keyword || status !== "all" ? "调整搜索词或状态筛选后再试。" : "项目会集中保存章节、项目画布、角色场景和制作进度。自由试图可从画布开始。"}
                    action={
                        !keyword && status === "all" ? (
                            <Button icon={<Plus className="size-3.5" />} onClick={() => document.querySelector<HTMLTextAreaElement>("textarea[aria-label='要拍什么']")?.focus()}>
                                创建项目
                            </Button>
                        ) : undefined
                    }
                />
            ) : null}

        </WorkspacePage>
    );
}

function ProjectRow({ row, onDelete }: { row: ProjectSummary; onDelete: () => Promise<unknown> }) {
    const completion = projectSummaryCompletion(row);
    const stage = projectSummaryStage(row);
    const projectStyle = resolveProjectCanvasStyle(row.project.stylePresetId, row.project.styleProfileJson);
    const styleTitle = projectVisualTypeLabel(row.project.visualType) || projectStyle?.title || parseStyleProfile(row.project.styleProfileJson)?.title || resolveCanvasStylePreset(row.project.stylePresetId)?.title || "未选画面类型";
    const coverUrl = row.project.coverResourceId ? resourceFileUrl(row.project.coverResourceId) : projectStyle?.imageUrl;
    return (
        <Link to={`/projects/${row.project.id}/overview`} className="product-collection-card library-card project-library-card group">
            <span className="project-library-cover">
                {coverUrl ? <CachedResourceImage className="project-library-cover-art" src={coverUrl} alt="" fallback={<MediaPlaceholder failed />} /> : <MediaPlaceholder label="故事由此开始" />}
                <span className="project-library-cover-scrim" />
                <span className="project-library-cover-ratio">{row.project.aspectRatio}</span>
                <span className="project-library-cover-stage">{stage.label}</span>
                <span className="project-collection-delete">
                    <DeleteButton label={`删除项目 ${row.project.name}`} description="项目章节、画布关联和素材归属将一并移除；独立画布与素材库原始素材会保留。此操作不可撤销。" onConfirm={onDelete} />
                </span>
            </span>
            <span className="project-library-body">
                <span className="project-library-heading">
                    <strong title={row.project.name}>{row.project.name}</strong>
                    {row.project.status === "archived" ? <em>已归档</em> : null}
                    <ArrowRight className="project-library-arrow size-4" />
                </span>
                <span className="project-library-subtitle">
                    {styleTitle} · {sourceTypeLabel(row.project.sourceType)}
                </span>
                <span className="project-library-progress">
                    <span>
                        <span>
                            {row.completedUnitCount}/{row.unitCount} 章
                        </span>
                        <span>{completion}%</span>
                    </span>
                    <i>
                        <b style={{ width: `${completion}%` }} />
                    </i>
                </span>
                <span className="project-library-stats">
                    <ProjectCount icon={<BookOpenText className="size-3.5" />} label="章节" value={row.unitCount} />
                    <ProjectCount icon={<LayoutGrid className="size-3.5" />} label="画布" value={row.canvasCount} />
                    <ProjectCount icon={<Images className="size-3.5" />} label="资产" value={row.assetCount} />
                </span>
            </span>
        </Link>
    );
}

function ProjectCount({ icon, label, value }: { icon: ReactNode; label: string; value: number }) {
    return (
        <span className="inline-flex items-center gap-1.5" title={`${value} ${label}`}>
            <span className="text-foreground/32">{icon}</span>
            <strong className="font-medium tabular-nums text-foreground/65">{value}</strong>
            <span>{label}</span>
        </span>
    );
}
