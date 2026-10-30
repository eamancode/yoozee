import { Input } from "antd";
import { useEffect, useState } from "react";

import { MasterPaletteGroups } from "@/components/drama/master-palette";
import { dramaSceneCode, masterPaletteColors, type DramaBible, type DramaColorScriptEntry, type DramaStyleAnchor } from "@/services/api/project-drama";

import { GroupedPaletteSwatches } from "./project-palette-card";

const ANCHOR_INPUTS: Array<{ key: Exclude<keyof DramaStyleAnchor, "forbidden">; label: string; multiline?: boolean }> = [
    { key: "medium", label: "画风 / 媒介" },
    { key: "art_style", label: "美术风格" },
    { key: "lighting", label: "光比与光线政策", multiline: true },
    { key: "camera", label: "镜头语言", multiline: true },
    { key: "grading", label: "调色倾向", multiline: true },
    { key: "era", label: "时代" },
    { key: "genre", label: "类型" },
];

const LOCK_SOURCE_LABELS: Record<string, string> = { analysis: "风格分析后锁定", library: "取自平台风格库", preset: "按画风生成后锁定" };

/**
 * 连戏表里的风格：风格锚（逐项可改；建项目时锁定）、主基调色卡（只读，换色板请重新分析项目风格再锁定）和色彩脚本（每场色块只读，
 * 整段 JSON 可改）。改动只改连戏表草稿，由连戏表的「保存」一起提交；改色彩脚本不会重出色卡。
 */
export function StyleBibleEditor({ document, onChange }: { document: DramaBible; onChange: (next: DramaBible) => void }) {
    const anchor = document.style_anchor || {};
    const script = Array.isArray(document.color_script) ? document.color_script : [];
    const masterCount = masterPaletteColors(document.master_palette).length;
    const scriptKey = JSON.stringify(script, null, 2);
    const [scriptText, setScriptText] = useState(scriptKey);
    const [scriptError, setScriptError] = useState("");
    useEffect(() => {
        setScriptText(scriptKey);
        setScriptError("");
    }, [scriptKey]);
    const setAnchor = (patch: Partial<DramaStyleAnchor>) => onChange({ ...document, style_anchor: { ...anchor, ...patch } });
    const applyScript = () => {
        try {
            const parsed = JSON.parse(scriptText || "[]") as unknown;
            if (!Array.isArray(parsed)) throw new Error("色彩脚本必须是数组");
            setScriptError("");
            onChange({ ...document, color_script: parsed as DramaColorScriptEntry[] });
        } catch (error) {
            setScriptError(error instanceof Error ? error.message : "JSON 格式不对");
        }
    };
    return (
        <div className="grid gap-4">
            <section aria-label="风格锚">
                <h4 className="mb-2 text-xs font-semibold text-foreground/75">风格锚</h4>
                <div className="grid gap-2 sm:grid-cols-2">
                    {ANCHOR_INPUTS.map((field) => (
                        <label key={field.key} className={field.multiline ? "grid gap-1 sm:col-span-2" : "grid gap-1"}>
                            <span className="text-[var(--fs-tiny)] text-foreground/50">{field.label}</span>
                            {field.multiline ? (
                                <Input.TextArea size="small" autoSize={{ minRows: 1, maxRows: 4 }} value={anchor[field.key] || ""} onChange={(event) => setAnchor({ [field.key]: event.target.value })} />
                            ) : (
                                <Input size="small" value={anchor[field.key] || ""} onChange={(event) => setAnchor({ [field.key]: event.target.value })} />
                            )}
                        </label>
                    ))}
                    <label className="grid gap-1 sm:col-span-2">
                        <span className="text-[var(--fs-tiny)] text-foreground/50">禁用项（用顿号分开）</span>
                        <Input
                            size="small"
                            value={(anchor.forbidden || []).join("、")}
                            onChange={(event) =>
                                setAnchor({
                                    forbidden: event.target.value
                                        .split(/[、,，]/)
                                        .map((item) => item.trim())
                                        .filter(Boolean),
                                })
                            }
                        />
                    </label>
                </div>
            </section>
            <section aria-label="主基调色卡">
                <h4 className="mb-2 text-xs font-semibold text-foreground/75">
                    主基调色卡{masterCount ? ` · ${masterCount} 色` : ""}
                    {document.style_lock?.source ? <span className="ml-2 font-normal text-foreground/45">{LOCK_SOURCE_LABELS[document.style_lock.source] || document.style_lock.source}</span> : null}
                </h4>
                {masterCount ? <MasterPaletteGroups palette={document.master_palette} /> : <p className="m-0 text-[var(--fs-label)] text-foreground/45">还没有锁定项目风格：在章节页点「分析项目风格」，或建项目时从风格库选。</p>}
            </section>
            <section aria-label="色彩脚本">
                <h4 className="mb-2 text-xs font-semibold text-foreground/75">色彩脚本 · {script.length} 场</h4>
                {script.length ? (
                    <ul className="thin-scrollbar grid max-h-72 gap-2 overflow-y-auto pl-0">
                        {script.map((entry) => (
                            <li key={`${entry.episode}-${entry.scene_no}`} className="list-none rounded-md border border-border/60 px-2.5 py-2">
                                <div className="mb-1.5 flex items-center gap-2 text-xs">
                                    <span className="font-mono text-[var(--fs-micro)] text-foreground/45">{dramaSceneCode(entry.episode, entry.scene_no)}</span>
                                    <span className="min-w-0 flex-1 truncate font-medium text-foreground/80">{entry.scene}</span>
                                    {entry.inherits_from ? <span className="shrink-0 text-[var(--fs-micro)] text-foreground/45">承接 {entry.inherits_from}</span> : null}
                                </div>
                                <GroupedPaletteSwatches colors={[...(entry.shared_colors || []), ...(entry.new_colors || [])]} />
                            </li>
                        ))}
                    </ul>
                ) : (
                    <p className="m-0 text-[var(--fs-label)] text-foreground/45">还没有逐场色彩脚本（章节页「生成场次色卡」）。</p>
                )}
                <details className="mt-2">
                    <summary className="cursor-pointer text-[var(--fs-tiny)] text-foreground/55">编辑色彩脚本 JSON</summary>
                    <Input.TextArea className="mt-2 font-mono !text-xs" autoSize={{ minRows: 6, maxRows: 18 }} value={scriptText} onChange={(event) => setScriptText(event.target.value)} onBlur={applyScript} aria-label="色彩脚本 JSON" spellCheck={false} />
                    <p className={scriptError ? "mt-1 text-[var(--fs-tiny)] text-red-600 dark:text-red-400" : "mt-1 text-[var(--fs-tiny)] text-foreground/45"}>
                        {scriptError || "离开输入框时并入连戏表草稿；HEX 写 #RRGGBB。改这里不会重画色卡，要让色卡跟着变就在章节页重新生成场次色卡。"}
                    </p>
                </details>
            </section>
        </div>
    );
}
