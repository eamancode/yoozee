import { useQuery } from "@tanstack/react-query";
import { Button } from "antd";
import { ImagePlus, X } from "lucide-react";
import { useEffect, useMemo, useRef } from "react";

import { MasterPaletteStrip, styleAnchorBrief } from "@/components/drama/master-palette";
import { dramaStyleLibraryCoverUrl, dramaStyleLibraryKey, listDramaStyleLibrary, type DramaStyleLibraryItem } from "@/services/api/drama-style-library";
import { uploadResourceFile } from "@/services/api/resources";

/** 一次风格分析最多带几张参考图（与后端 StyleLockMaxImages 一致）。 */
export const STYLE_LOCK_MAX_IMAGES = 6;
/** 风格描述的字数上限（与后端 StyleLockTextChars 一致）。 */
export const STYLE_LOCK_TEXT_CHARS = 2000;

/** 风格分析的参考图：本地选好，建项目（或开跑）时再上传成资源。 */
export function StyleReferenceImagesPicker({ value, onChange, disabled }: { value: File[]; onChange: (files: File[]) => void; disabled?: boolean }) {
    const inputRef = useRef<HTMLInputElement>(null);
    const previews = useMemo(() => value.map((file) => ({ file, url: URL.createObjectURL(file) })), [value]);
    useEffect(() => () => previews.forEach((item) => URL.revokeObjectURL(item.url)), [previews]);
    const add = (files: FileList | null) => {
        const images = Array.from(files || []).filter((file) => file.type.startsWith("image/"));
        onChange([...value, ...images].slice(0, STYLE_LOCK_MAX_IMAGES));
    };
    return (
        <div>
            <div className="grid grid-cols-3 gap-2 sm:grid-cols-6">
                {previews.map((item, index) => (
                    <div key={`${item.file.name}-${index}`} className="relative aspect-square overflow-hidden rounded-md border border-border/70 bg-foreground/[.04]">
                        <img src={item.url} alt={item.file.name} className="h-full w-full object-cover" />
                        <button
                            type="button"
                            className="absolute right-1 top-1 grid size-5 place-content-center rounded-full bg-black/60 text-white"
                            onClick={() => onChange(value.filter((_, at) => at !== index))}
                            disabled={disabled}
                            aria-label={`移除参考图 ${item.file.name}`}
                        >
                            <X className="size-3" />
                        </button>
                    </div>
                ))}
                {value.length < STYLE_LOCK_MAX_IMAGES ? (
                    <Button className="!h-auto aspect-square" disabled={disabled} onClick={() => inputRef.current?.click()} aria-label="添加参考图">
                        <span className="grid place-items-center gap-1 text-[var(--fs-tiny)] text-foreground/55">
                            <ImagePlus className="size-4" />
                            添加
                        </span>
                    </Button>
                ) : null}
            </div>
            <input
                ref={inputRef}
                type="file"
                accept="image/*"
                multiple
                hidden
                onChange={(event) => {
                    add(event.target.files);
                    event.target.value = "";
                }}
            />
        </div>
    );
}

/** 把参考图上传成本人的图片资源，返回资源 ID（按顺序）。 */
export async function uploadStyleReferenceImages(files: File[]) {
    const ids: string[] = [];
    for (const file of files.slice(0, STYLE_LOCK_MAX_IMAGES)) {
        const resource = await uploadResourceFile(file, "image", { fileName: file.name });
        ids.push(resource.id);
    }
    return ids;
}

/** 平台风格库选择：按项目类型列出条目（主色卡缩略图、名称、风格锚摘要），点一下选中、再点取消。 */
export function StyleLibraryPicker({ projectType, value, onChange }: { projectType?: string; value?: string; onChange: (item?: DramaStyleLibraryItem) => void }) {
    const query = useQuery({
        queryKey: dramaStyleLibraryKey(projectType),
        queryFn: ({ signal }) => listDramaStyleLibrary(projectType, signal),
        enabled: Boolean(projectType),
    });
    if (!projectType) return <p className="text-xs text-foreground/45">先选类型，再从风格库里挑。</p>;
    if (query.isLoading) return <p className="text-xs text-foreground/45">正在读取风格库…</p>;
    if (query.isError) return <p className="text-xs text-red-600 dark:text-red-400">风格库读取失败：{query.error instanceof Error ? query.error.message : "请稍后重试"}</p>;
    const items = query.data?.items || [];
    if (!items.length) return <p className="text-xs text-foreground/45">这个类型的风格库还是空的：写风格描述或传参考图分析一个，锁定后可以存进来给团队用。</p>;
    return (
        <div className="thin-scrollbar grid max-h-64 grid-cols-1 gap-2 overflow-y-auto sm:grid-cols-2" role="radiogroup" aria-label="风格库">
            {items.map((item) => {
                const active = item.id === value;
                const cover = dramaStyleLibraryCoverUrl(item);
                return (
                    <button
                        key={item.id}
                        type="button"
                        role="radio"
                        aria-checked={active}
                        className={active ? "app-story-source is-active !block !h-auto !p-2 text-left" : "app-story-source !block !h-auto !p-2 text-left"}
                        onClick={() => onChange(active ? undefined : item)}
                    >
                        <span className="block aspect-video overflow-hidden rounded bg-foreground/[.04]">
                            {cover ? <img src={cover} alt={`${item.name} 主色卡`} className="h-full w-full object-contain" loading="lazy" /> : <MasterPaletteStrip palette={item.masterPalette} className="h-full" />}
                        </span>
                        <span className="mt-1.5 block truncate text-xs font-medium text-foreground/85">{item.name}</span>
                        <span className="block truncate text-[var(--fs-micro)] text-foreground/50">{styleAnchorBrief(item.styleAnchor) || `${item.colorCount} 色`}</span>
                    </button>
                );
            })}
        </div>
    );
}
