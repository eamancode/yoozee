import { useQuery } from "@tanstack/react-query";
import { Button } from "antd";
import { Check, ImagePlus, X } from "lucide-react";
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
