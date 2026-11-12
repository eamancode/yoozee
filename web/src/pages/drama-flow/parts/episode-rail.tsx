import { useMemo, useState } from "react";
import { Input } from "antd";
import { LayoutGrid, List } from "lucide-react";

export type RailEpisode = { id: string; title: string; summary: string; warn: boolean; completion?: number };

const RAIL_VIEW_KEY = "drama-flow:rail-view";

/** 左侧分集列表：每集写段数、已出、在出、失败；可搜索，可切矩阵一屏看全剧。 */
export function EpisodeRail({ label, episodes, activeId, onSelect }: { label: string; episodes: RailEpisode[]; activeId: string; onSelect: (id: string) => void }) {
    const [keyword, setKeyword] = useState("");
    const [view, setView] = useState<"list" | "matrix">(() => (localStorage.getItem(RAIL_VIEW_KEY) === "matrix" ? "matrix" : "list"));
    const switchView = (next: "list" | "matrix") => {
        setView(next);
        localStorage.setItem(RAIL_VIEW_KEY, next);
    };
    const shown = useMemo(() => {
        const text = keyword.trim();
        return text ? episodes.filter((episode) => episode.title.includes(text)) : episodes;
    }, [episodes, keyword]);
    return (
        <aside className="drama-flow-rail" aria-label={label}>
            <div className="drama-flow-rail-head">
                <span>
                    {label} · {episodes.length}
                </span>
                <div className="drama-flow-rail-views">
                    <button type="button" aria-label="列表" aria-pressed={view === "list"} onClick={() => switchView("list")}>
                        <List className="size-3.5" />
                    </button>
                    <button type="button" aria-label="矩阵" aria-pressed={view === "matrix"} onClick={() => switchView("matrix")}>
                        <LayoutGrid className="size-3.5" />
                    </button>
                </div>
            </div>
            {episodes.length > 8 ? <Input size="small" allowClear placeholder={`搜索${label}`} value={keyword} onChange={(event) => setKeyword(event.target.value)} /> : null}
            {view === "matrix" ? (
                <div className="drama-flow-matrix">
                    {shown.map((episode, index) => (
                        <button
                            key={episode.id}
                            type="button"
                            title={`${episode.title}：${episode.summary}`}
                            aria-label={`${episode.title}：${episode.summary}`}
                            aria-pressed={episode.id === activeId}
                            data-warn={episode.warn || undefined}
                            style={{ ["--done" as string]: `${Math.round((episode.completion ?? 0) * 100)}%` }}
                            data-empty={episode.completion === undefined || undefined}
                            onClick={() => onSelect(episode.id)}
                        >
                            {index + 1}
                        </button>
                    ))}
                </div>
            ) : (
                shown.map((episode) => (
                    <button key={episode.id} type="button" className="drama-flow-ep" aria-pressed={episode.id === activeId} onClick={() => onSelect(episode.id)}>
                        <strong title={episode.title}>{episode.title}</strong>
                        <span data-warn={episode.warn || undefined}>{episode.summary}</span>
                    </button>
                ))
            )}
            {!shown.length ? <p className="drama-flow-rail-none">没有符合的{label}</p> : null}
        </aside>
    );
}
