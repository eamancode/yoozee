import { useVirtualizer } from "@tanstack/react-virtual";
import { useRef } from "react";

/** 一次最多导入的章 / 集数（与后端 ImportProjectUnits 的上限一致）。 */
export const MAX_NOVEL_IMPORT_CHAPTERS = 2500;

/** 按章 / 集拆分的预览（章节页导入和建项导入共用同一份切分结果）。 */
export function ImportChapterPreview({ chapters, noun }: { chapters: Array<{ title: string; plainText: string }>; noun: string }) {
    const scrollRef = useRef<HTMLDivElement>(null);
    const virtualizer = useVirtualizer({
        count: chapters.length,
        getScrollElement: () => scrollRef.current,
        estimateSize: () => 49,
        overscan: 10,
    });
    return (
        <div ref={scrollRef} className="thin-scrollbar min-h-0 flex-1 overflow-y-auto p-2">
            {chapters.length ? <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
                {virtualizer.getVirtualItems().map((virtualItem) => {
                    const chapter = chapters[virtualItem.index];
                    return <div key={`${chapter.title}-${virtualItem.index}`} className="absolute left-0 top-0 flex w-full gap-2 border-b border-border/60 px-1.5 py-2" style={{ height: virtualItem.size, transform: `translateY(${virtualItem.start}px)` }}><span className="w-8 shrink-0 pt-0.5 text-[var(--fs-tiny)] tabular-nums text-foreground/35">{String(virtualItem.index + 1).padStart(Math.max(2, String(chapters.length).length), "0")}</span><div className="min-w-0"><div className="truncate text-xs font-medium">{chapter.title}</div><div className="mt-0.5 text-[var(--fs-tiny)] text-foreground/40">{chapter.plainText.length.toLocaleString("zh-CN")} 字</div></div></div>;
                })}
            </div> : <div className="grid h-full place-items-center px-4 text-center text-xs leading-5 text-foreground/40">选择 TXT 文件或粘贴正文后，这里会显示按{noun}拆分的结果</div>}
        </div>
    );
}
