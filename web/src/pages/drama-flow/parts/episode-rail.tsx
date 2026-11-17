import { useMemo, useState } from "react";
import { Button, Input } from "antd";
import { LayoutGrid, List, Plus, Trash2 } from "lucide-react";

import { archivedEpisodeNotice, EpisodeDialog, type EpisodeDialogTarget } from "./episode-dialog";

export type RailEpisode = { id: string; title: string; summary: string; warn: boolean; completion?: number };

const RAIL_VIEW_KEY = "drama-flow:rail-view";

/** 左侧分集列表：每集写段数、已出、在出、失败；可搜索，可切矩阵一屏看全剧。 */
export function EpisodeRail({
    projectId,
    episodes,
    activeId,
    archived,
    onSelect,
    readState,
    onRetry,
}: {
    projectId: string;
    episodes: RailEpisode[];
    activeId: string;
    archived: boolean;
    onSelect: (id: string, replace?: boolean) => void;
    readState: "ready" | "loading" | "error";
    onRetry: () => void;
}) {
    const [keyword, setKeyword] = useState("");
    const [dialog, setDialog] = useState<EpisodeDialogTarget>();
    const [view, setView] = useState<"list" | "matrix">(() => (localStorage.getItem(RAIL_VIEW_KEY) === "matrix" ? "matrix" : "list"));
    const switchView = (next: "list" | "matrix") => {
        setView(next);
        localStorage.setItem(RAIL_VIEW_KEY, next);
    };
    const shown = useMemo(() => {
        const text = episodes.length > 8 ? keyword.trim() : "";
        return episodes.map((episode, index) => ({ episode, number: index + 1 })).filter(({ episode }) => episode.title.includes(text));
    }, [episodes, keyword]);
    const saved = (createdId?: string) => {
        if (createdId) {
            setKeyword("");
            onSelect(createdId);
        } else if (dialog?.kind === "delete" && dialog.id === activeId) {
            const index = episodes.findIndex((episode) => episode.id === dialog.id);
            onSelect(episodes[index + 1]?.id || episodes[index - 1]?.id || "", true);
        }
        setDialog(undefined);
    };
    const deleteButton = (episode: RailEpisode) => (
        <Button
            type="text"
            size="small"
            icon={<Trash2 className="size-3.5" />}
            disabled={archived}
            title={archived ? archivedEpisodeNotice : `删除此剧集：${episode.title}`}
            aria-label={`删除此剧集：${episode.title}`}
            onClick={() => setDialog({ kind: "delete", id: episode.id, title: episode.title })}
        />
    );
    return (
        <aside className="drama-flow-rail" aria-label="剧集">
            <div className="drama-flow-rail-head">
                <span>剧集{readState === "ready" || episodes.length ? ` · ${episodes.length}` : ""}</span>
                <div className="drama-flow-rail-views">
                    <button type="button" aria-label="列表" aria-pressed={view === "list"} onClick={() => switchView("list")}>
                        <List className="size-3.5" />
                    </button>
                    <button type="button" aria-label="矩阵" aria-pressed={view === "matrix"} onClick={() => switchView("matrix")}>
                        <LayoutGrid className="size-3.5" />
                    </button>
                </div>
            </div>
            <Button size="small" icon={<Plus className="size-3.5" />} disabled={archived || readState !== "ready"} onClick={() => setDialog({ kind: "create" })}>
                {readState === "ready" && !episodes.length ? "添加首集" : "添加剧集"}
            </Button>
            {archived ? <p className="drama-flow-rail-none">{archivedEpisodeNotice}</p> : null}
            {episodes.length > 8 ? <Input size="small" allowClear placeholder="搜索剧集" value={keyword} onChange={(event) => setKeyword(event.target.value)} /> : null}
            {view === "matrix" ? (
                <div className="drama-flow-matrix">
                    {shown.map(({ episode, number }) => (
                        <div className="drama-flow-ep-row" key={episode.id}>
                            <button
                                className="drama-flow-ep-cell"
                                type="button"
                                title={`${episode.title}：${episode.summary}`}
                                aria-label={`${episode.title}：${episode.summary}`}
                                aria-pressed={episode.id === activeId}
                                data-warn={episode.warn || undefined}
                                style={{ ["--done" as string]: `${Math.round((episode.completion ?? 0) * 100)}%` }}
                                data-empty={episode.completion === undefined || undefined}
                                onClick={() => onSelect(episode.id)}
                            >
                                {number}
                            </button>
                            {deleteButton(episode)}
                        </div>
                    ))}
                </div>
            ) : (
                shown.map(({ episode }) => (
                    <div key={episode.id} className="drama-flow-ep-row">
                        <button type="button" className="drama-flow-ep" aria-pressed={episode.id === activeId} onClick={() => onSelect(episode.id)}>
                            <strong title={episode.title}>{episode.title}</strong>
                            <span data-warn={episode.warn || undefined}>{episode.summary}</span>
                        </button>
                        {deleteButton(episode)}
                    </div>
                ))
            )}
            {readState === "error" ? (
                <Button size="small" onClick={onRetry}>
                    重新读取剧集
                </Button>
            ) : readState === "loading" ? (
                <p className="drama-flow-rail-none">正在读取剧集…</p>
            ) : !shown.length ? (
                <p className="drama-flow-rail-none">{episodes.length ? "没有符合的剧集" : "还没有剧集"}</p>
            ) : null}
            {dialog ? <EpisodeDialog projectId={projectId} target={dialog} archived={archived} onClose={() => setDialog(undefined)} onSaved={saved} /> : null}
        </aside>
    );
}
