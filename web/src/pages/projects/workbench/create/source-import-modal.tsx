import { Button } from "antd";
import { FileUp } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type DragEvent } from "react";

import { AppModal } from "@/components/ui/product/app-modal";
import { decodeNovelText, splitTextIntoChapters } from "@/lib/canvas/canvas-document";
import { detectProjectSource, formatSourceChars, sourceCharCount } from "@/lib/project-source";

import { MAX_NOVEL_IMPORT_CHAPTERS } from "../import-chapter-preview";

/** 导入确认后的正文：类型（小说导入成原文章节，剧本导入成剧集）和按章节页同一套解析切好的章 / 集。 */
export type ImportedProjectSource = { text: string; fileName: string; kind: "novel" | "script"; split: boolean };

type Step = "upload" | "preview" | "confirm";
const STEPS: Array<{ key: Step; label: string }> = [
    { key: "upload", label: "上传" },
    { key: "preview", label: "解析预览" },
    { key: "confirm", label: "确认" },
];

/** 按类型和切法给出章 / 集：按标题切时用章节页导入同一套解析；整篇一个时只有一章（一集）。剧本没有集标题时标题写「第 1 集」。 */
export function importedUnits(text: string, kind: "novel" | "script", split: boolean) {
    const noun = kind === "script" ? "集" : "章";
    if (!split) return [{ title: `第 1 ${noun}`, plainText: text.trim() }];
    return splitTextIntoChapters(text).map((unit) => (kind === "script" && unit.title === "第 1 章" ? { ...unit, title: "第 1 集" } : unit));
}

/**
 * 建项的「导入文件」：上传 → 解析预览（前三章 / 前三集的切分和总字数）→ 确认是小说还是剧本、章节怎么切。
 * 切分和章节页导入用同一套解析（splitTextIntoChapters），确认后在作曲区显示成文件卡。
 */
export function SourceImportModal({ open, initialFile, onClose, onConfirm }: { open: boolean; initialFile?: File; onClose: () => void; onConfirm: (source: ImportedProjectSource) => void }) {
    const inputRef = useRef<HTMLInputElement>(null);
    const [step, setStep] = useState<Step>("upload");
    const [text, setText] = useState("");
    const [fileName, setFileName] = useState("");
    const [kind, setKind] = useState<"novel" | "script">("novel");
    const [split, setSplit] = useState(true);
    const [error, setError] = useState("");
    const [dragging, setDragging] = useState(false);

    const read = async (file: File) => {
        setError("");
        if (!/\.(txt|md)$/i.test(file.name) && file.type && !file.type.startsWith("text/")) {
            setError("只支持 TXT 或 Markdown 文本文件");
            return;
        }
        const decoded = decodeNovelText(await file.arrayBuffer());
        if (!decoded.trim()) {
            setError("文件是空的");
            return;
        }
        const detected = detectProjectSource(decoded);
        setText(decoded);
        setFileName(file.name);
        setKind(detected.kind === "script" ? "script" : "novel");
        setSplit(true);
        setStep("preview");
    };

    useEffect(() => {
        if (!open) {
            setStep("upload");
            setText("");
            setFileName("");
            setError("");
            return;
        }
        if (initialFile) void read(initialFile);
    }, [initialFile, open]);

    const units = useMemo(() => (text ? importedUnits(text, kind, split) : []), [kind, split, text]);
    const splitUnits = useMemo(() => (text ? splitTextIntoChapters(text) : []), [text]);
    const noun = kind === "script" ? "集" : "章";
    const chars = sourceCharCount(text);
    const tooMany = units.length > MAX_NOVEL_IMPORT_CHAPTERS;
    const stepIndex = STEPS.findIndex((item) => item.key === step);

    const drop = (event: DragEvent<HTMLDivElement>) => {
        event.preventDefault();
        setDragging(false);
        const file = event.dataTransfer.files?.[0];
        if (file) void read(file);
    };

    return (
        <AppModal flush title={null} open={open} footer={null} onCancel={onClose} width={640}>
            <div className="project-import">
                <header className="project-import-head">
                    <h2>导入文件</h2>
                    <ol className="project-import-steps" aria-label="导入步骤">
                        {STEPS.map((item, index) => (
                            <li key={item.key} className={index < stepIndex ? "is-done" : index === stepIndex ? "is-active" : ""} aria-current={index === stepIndex ? "step" : undefined}>
                                <span>{index + 1}</span>
                                {item.label}
                            </li>
                        ))}
                    </ol>
                </header>
                <div className="project-import-body">
                    {step === "upload" ? (
                        <div
                            className={`project-import-drop${dragging ? " is-dragging" : ""}`}
                            onDragOver={(event) => {
                                event.preventDefault();
                                setDragging(true);
                            }}
                            onDragLeave={() => setDragging(false)}
                            onDrop={drop}
                        >
                            <FileUp className="size-6" aria-hidden />
                            <p>把小说或剧本的 TXT 文件拖到这里</p>
                            <Button onClick={() => inputRef.current?.click()}>选择文件</Button>
                            <span className="project-import-hint">支持 TXT、Markdown；自动识别 UTF-8 和 GBK 编码</span>
                            {error ? (
                                <p className="project-import-error" role="alert">
                                    {error}
                                </p>
                            ) : null}
                            <input
                                ref={inputRef}
                                type="file"
                                accept=".txt,.md,text/plain,text/markdown"
                                hidden
                                onChange={(event) => {
                                    const file = event.target.files?.[0];
                                    event.target.value = "";
                                    if (file) void read(file);
                                }}
                            />
                        </div>
                    ) : null}
                    {step === "preview" ? (
                        <div className="project-import-preview">
                            <p className="project-import-summary">
                                <strong>{fileName}</strong> · {formatSourceChars(chars)} · 按标题切出 {splitUnits.length} {noun}
                            </p>
                            <ol className="project-import-units">
                                {splitUnits.slice(0, 3).map((unit, index) => (
                                    <li key={`${unit.title}-${index}`}>
                                        <div className="project-import-unit-head">
                                            <span>{unit.title}</span>
                                            <em>{formatSourceChars(sourceCharCount(unit.plainText))}</em>
                                        </div>
                                        <p>{unit.plainText.slice(0, 120) || "（空）"}</p>
                                    </li>
                                ))}
                            </ol>
                            {splitUnits.length > 3 ? (
                                <p className="project-import-hint">
                                    只预览前三{noun}，后面还有 {splitUnits.length - 3} {noun}
                                </p>
                            ) : null}
                        </div>
                    ) : null}
                    {step === "confirm" ? (
                        <div className="project-import-confirm">
                            <fieldset>
                                <legend>这是</legend>
                                <div role="radiogroup" aria-label="正文类型" className="project-choice-row">
                                    {(["novel", "script"] as const).map((value) => (
                                        <button key={value} type="button" role="radio" aria-checked={kind === value} className={`project-choice${kind === value ? " is-selected" : ""}`} onClick={() => setKind(value)}>
                                            <strong>{value === "script" ? "剧本" : "小说"}</strong>
                                            <span>{value === "script" ? "导入成剧集，建项后只调节奏" : "导入成原文章节，建项后改编成剧集"}</span>
                                        </button>
                                    ))}
                                </div>
                            </fieldset>
                            <fieldset>
                                <legend>怎么切</legend>
                                <div role="radiogroup" aria-label="切分方式" className="project-choice-row">
                                    <button type="button" role="radio" aria-checked={split} className={`project-choice${split ? " is-selected" : ""}`} onClick={() => setSplit(true)}>
                                        <strong>按「第 N {noun}」标题切</strong>
                                        <span>
                                            切成 {importedUnits(text, kind, true).length} {noun}
                                        </span>
                                    </button>
                                    <button type="button" role="radio" aria-checked={!split} className={`project-choice${!split ? " is-selected" : ""}`} onClick={() => setSplit(false)}>
                                        <strong>整篇算一{noun}</strong>
                                        <span>不切分</span>
                                    </button>
                                </div>
                            </fieldset>
                            {tooMany ? (
                                <p className="project-import-error" role="alert">
                                    最多一次导入 {MAX_NOVEL_IMPORT_CHAPTERS.toLocaleString("zh-CN")} {noun}
                                </p>
                            ) : null}
                        </div>
                    ) : null}
                </div>
                <footer className="project-import-foot">
                    <Button onClick={step === "upload" ? onClose : () => setStep(step === "confirm" ? "preview" : "upload")}>{step === "upload" ? "取消" : "上一步"}</Button>
                    {step === "preview" ? (
                        <Button type="primary" onClick={() => setStep("confirm")}>
                            下一步
                        </Button>
                    ) : null}
                    {step === "confirm" ? (
                        <Button type="primary" disabled={!units.length || tooMany} onClick={() => onConfirm({ text, fileName, kind, split })}>
                            用这份正文（{units.length} {noun}）
                        </Button>
                    ) : null}
                </footer>
            </div>
        </AppModal>
    );
}
