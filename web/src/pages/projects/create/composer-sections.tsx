import { Button, Input } from "antd";
import { AlertTriangle, Check } from "lucide-react";
import { useRef, type KeyboardEvent, type ReactNode } from "react";
import { Link } from "react-router";

import { MasterPaletteStrip } from "@/components/drama/master-palette";
import { MediaPlaceholder } from "@/components/ui/product/media-placeholder";
import { PROJECT_VISUAL_TYPES } from "@/lib/project-visual-type";
import { settingsPath } from "@/lib/settings-navigation";
import type { DramaModel, DramaRunMode } from "@/services/api/drama";
import { dramaStyleLibraryCoverUrl, type DramaStyleLibraryItem } from "@/services/api/drama-style-library";
import type { ProjectVisualType } from "@/services/api/projects";

import { DRAMA_RUN_MODES, DRAMA_VIDEO_STOP_NOTE } from "../drama-model";
import { PipelineStrip } from "../pipeline-strip";
import { STYLE_LOCK_TEXT_CHARS, StyleReferenceImagesPicker } from "../project-style-inputs";
import type { ComposerSection } from "./create-project-flow";

/** 建项页的一段：编号、标题、是否必填、落在这一段的错误。 */
export function ComposerSectionShell({ id, index, title, hint, required, error, children }: { id: ComposerSection; index: number; title: string; hint?: ReactNode; required?: boolean; error?: string; children: ReactNode }) {
    return (
        <section id={`project-create-${id}`} className={`project-create-section${error ? " has-error" : ""}`} aria-labelledby={`project-create-${id}-title`}>
            <header className="project-create-section-head">
                <span className="project-create-section-index" aria-hidden>
                    {index}
                </span>
                <h3 id={`project-create-${id}-title`}>{title}</h3>
                {required ? <span className="project-create-section-required">必填</span> : null}
                {hint ? <span className="project-create-section-hint">{hint}</span> : null}
            </header>
            {error ? (
                <p className="project-create-section-error" role="alert">
                    <AlertTriangle className="size-3.5" aria-hidden />
                    {error}
                </p>
            ) : null}
            {children}
        </section>
    );
}

/**
 * 单选卡片组（role=radiogroup）：方向键在选项间移动并选中，只有选中的那个（没选时第一个）能用 Tab 进来。
 */
export function ChoiceGroup<T extends string>({ label, value, options, onChange, className = "", optionClassName = "", render }: { label: string; value?: T; options: ReadonlyArray<{ value: T; disabled?: boolean }>; onChange: (value: T) => void; className?: string; optionClassName?: string; render: (value: T, selected: boolean) => ReactNode }) {
    const refs = useRef<Array<HTMLButtonElement | null>>([]);
    const enabled = options.filter((option) => !option.disabled);
    const focusIndex = Math.max(0, options.findIndex((option) => option.value === value));
    const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
        const step = event.key === "ArrowRight" || event.key === "ArrowDown" ? 1 : event.key === "ArrowLeft" || event.key === "ArrowUp" ? -1 : 0;
        if (!step || !enabled.length) return;
        event.preventDefault();
        let next = index;
        for (let i = 0; i < options.length; i += 1) {
            next = (next + step + options.length) % options.length;
            if (!options[next].disabled) break;
        }
        onChange(options[next].value);
        refs.current[next]?.focus();
    };
    return (
        <div role="radiogroup" aria-label={label} className={className}>
            {options.map((option, index) => {
                const selected = option.value === value;
                return (
                    <button
                        key={option.value}
                        ref={(node) => {
                            refs.current[index] = node;
                        }}
                        type="button"
                        role="radio"
                        aria-checked={selected}
                        disabled={option.disabled}
                        tabIndex={index === focusIndex ? 0 : -1}
                        className={`${optionClassName}${selected ? " is-selected" : ""}`}
                        onClick={() => onChange(option.value)}
                        onKeyDown={(event) => onKeyDown(event, index)}
                    >
                        {render(option.value, selected)}
                    </button>
                );
            })}
        </div>
    );
}

/** 段 2 · 画面类型：四张样张卡，不预选。样张取风格中心里该类型的封面，没有就用中性占位。 */
export function VisualTypeCards({ value, covers, notice, onChange }: { value?: ProjectVisualType; covers: Map<string, string>; notice?: ReactNode; onChange: (value: ProjectVisualType) => void }) {
    return (
        <>
            <ChoiceGroup
                label="画面类型"
                value={value}
                options={PROJECT_VISUAL_TYPES}
                onChange={onChange}
                className="project-create-visual-grid"
                optionClassName="project-create-visual-card"
                render={(type, selected) => {
                    const item = PROJECT_VISUAL_TYPES.find((entry) => entry.value === type)!;
                    const cover = covers.get(type);
                    return (
                        <>
                            <span className="project-create-visual-sample">{cover ? <img src={cover} alt="" loading="lazy" /> : <MediaPlaceholder label={item.label} />}</span>
                            <span className="project-create-visual-text">
                                <strong>{item.label}</strong>
                                <span>{item.hint}</span>
                            </span>
                            {selected ? (
                                <span className="project-create-check" aria-hidden>
                                    <Check className="size-3.5" strokeWidth={3} />
                                </span>
                            ) : null}
                        </>
                    );
                }}
            />
            {notice}
        </>
    );
}

/** 段 3 · 画风从哪来：交给导演分析（默认，参考图和一句描述都可不填）或从风格中心挑（只列当前类型）。 */
export function StyleSourceSection(props: {
    mode: "director" | "library";
    visualType?: ProjectVisualType;
    brief: string;
    images: File[];
    styleId?: string;
    items: DramaStyleLibraryItem[];
    loading: boolean;
    onMode: (mode: "director" | "library") => void;
    onBrief: (brief: string) => void;
    onImages: (images: File[]) => void;
    onStyle: (item?: DramaStyleLibraryItem) => void;
}) {
    const { mode, visualType, brief, images, styleId, items, loading, onMode, onBrief, onImages, onStyle } = props;
    return (
        <>
            <ChoiceGroup
                label="画风从哪来"
                value={mode}
                options={[{ value: "director" as const }, { value: "library" as const }]}
                onChange={onMode}
                className="project-choice-row"
                optionClassName="project-choice"
                render={(value) =>
                    value === "director" ? (
                        <>
                            <strong>交给导演分析</strong>
                            <span>拆完剧本先由导演定画风，结果存进风格中心</span>
                        </>
                    ) : (
                        <>
                            <strong>从风格中心挑</strong>
                            <span>直接用现成的风格，跳过全局风格分析</span>
                        </>
                    )
                }
            />
            {mode === "director" ? (
                <div className="project-create-style-director">
                    <Input.TextArea value={brief} onChange={(event) => onBrief(event.target.value)} autoSize={{ minRows: 2, maxRows: 5 }} maxLength={STYLE_LOCK_TEXT_CHARS} placeholder="一句风格描述（可不填），例如：都市冷调悬疑，低饱和、实景光源" aria-label="风格描述" />
                    <div>
                        <span className="project-create-field-label">参考图（可不填，最多 6 张）</span>
                        <StyleReferenceImagesPicker value={images} onChange={onImages} />
                    </div>
                    <p className="project-create-note">都不填就让导演按故事自己定。</p>
                </div>
            ) : (
                <div className="project-create-style-library">
                    {!visualType ? (
                        <p className="project-create-note">先选画面类型，风格中心只列这个类型的风格。</p>
                    ) : loading ? (
                        <p className="project-create-note">正在读取风格中心…</p>
                    ) : !items.length ? (
                        <div className="project-create-empty">
                            <p>这个类型的风格中心还是空的。</p>
                            <Button size="small" onClick={() => onMode("director")}>
                                改为交给导演分析
                            </Button>
                        </div>
                    ) : (
                        <ChoiceGroup
                            label="风格中心"
                            value={styleId}
                            options={items.map((item) => ({ value: item.id }))}
                            onChange={(id) => onStyle(items.find((item) => item.id === id))}
                            className="project-create-style-grid"
                            optionClassName="project-create-style-card"
                            render={(id, selected) => {
                                const item = items.find((entry) => entry.id === id)!;
                                const cover = dramaStyleLibraryCoverUrl(item);
                                return (
                                    <>
                                        <span className="project-create-style-thumb">{cover ? <img src={cover} alt={`${item.name} 主色卡`} loading="lazy" /> : <MasterPaletteStrip palette={item.masterPalette} className="h-full" />}</span>
                                        <span className="project-create-style-name">{item.name}</span>
                                        <span className="project-create-style-meta">{item.source === "director" ? "来自导演分析" : item.description || `${item.colorCount} 色`}</span>
                                        {selected ? (
                                            <span className="project-create-check" aria-hidden>
                                                <Check className="size-3.5" strokeWidth={3} />
                                            </span>
                                        ) : null}
                                    </>
                                );
                            }}
                        />
                    )}
                </div>
            )}
        </>
    );
}

const ASPECT_RATIOS = ["9:16", "16:9", "1:1", "4:3"] as const;

function ratioIconStyle(ratio: string) {
    const [w, h] = ratio.split(":").map(Number);
    const scale = 16 / Math.max(w, h);
    return { width: `${Math.round(w * scale)}px`, height: `${Math.round(h * scale)}px` };
}

/** 段 4 · 怎么做：画幅、每段时长、视频模型、生成模式（都有默认值）。 */
export function ProductionSection(props: {
    aspectRatio: string;
    segmentSeconds: number;
    segmentEstimate?: number;
    models: DramaModel[];
    recommendedModel: string;
    model?: DramaModel;
    families?: Set<string>;
    runMode: DramaRunMode;
    onAspectRatio: (ratio: string) => void;
    onSegmentSeconds: (seconds: number) => void;
    onModel: (id: string) => void;
    onRunMode: (mode: DramaRunMode) => void;
}) {
    const { aspectRatio, segmentSeconds, segmentEstimate, models, recommendedModel, model, families, runMode, onAspectRatio, onSegmentSeconds, onModel, onRunMode } = props;
    // 30 秒的段配一段最长 15 秒的模型：原地提示换成哪个，点了才换，不悄悄替用户换模型。
    const overLimit = model && model.duration.max < segmentSeconds;
    const replacement = overLimit ? models.filter((item) => item.duration.max >= segmentSeconds).sort((left, right) => Number(Boolean(families?.has(right.id))) - Number(Boolean(families?.has(left.id))))[0] : undefined;
    const selectedMode = DRAMA_RUN_MODES.find((item) => item.value === runMode) || DRAMA_RUN_MODES[0];
    return (
        <div className="project-create-production">
            {families && families.size === 0 ? (
                <p className="project-create-warning" role="status">
                    <AlertTriangle className="size-3.5" aria-hidden />
                    还没有视频渠道，可以先建项目，出视频前要配好。
                    <Link to={settingsPath("channels")}>去配置渠道</Link>
                </p>
            ) : null}
            <div className="project-create-field">
                <span className="project-create-field-label">
                    画幅<em>建项后不可改</em>
                </span>
                <ChoiceGroup
                    label="画幅"
                    value={aspectRatio}
                    options={ASPECT_RATIOS.map((value) => ({ value }))}
                    onChange={onAspectRatio}
                    className="project-segmented"
                    optionClassName="project-segmented-item"
                    render={(ratio) => (
                        <>
                            <span className="project-ratio-icon" style={ratioIconStyle(ratio)} aria-hidden />
                            {ratio}
                        </>
                    )}
                />
            </div>
            <div className="project-create-field">
                <span className="project-create-field-label">
                    每段时长
                    {segmentEstimate ? <em>约 {segmentEstimate} 段</em> : null}
                </span>
                <ChoiceGroup label="每段时长" value={String(segmentSeconds)} options={[{ value: "15" }, { value: "30" }]} onChange={(value) => onSegmentSeconds(Number(value))} className="project-segmented" optionClassName="project-segmented-item" render={(value) => `${value} 秒`} />
                {overLimit && model ? (
                    <p className="project-create-inline-prompt" role="status">
                        {model.label} 最长 {model.duration.max} 秒
                        {replacement ? (
                            <>
                                ，换成 {replacement.label}？
                                <Button size="small" onClick={() => onModel(replacement.id)}>
                                    换
                                </Button>
                            </>
                        ) : "，改回 15 秒才能建项。"}
                    </p>
                ) : null}
            </div>
            <div className="project-create-field">
                <span className="project-create-field-label">视频模型</span>
                <ChoiceGroup
                    label="视频模型"
                    value={model?.id}
                    options={models.map((item) => ({ value: item.id }))}
                    onChange={onModel}
                    className="project-create-model-list"
                    optionClassName="project-create-model"
                    render={(id) => {
                        const item = models.find((entry) => entry.id === id)!;
                        const noChannel = Boolean(families && !families.has(id));
                        return (
                            <>
                                <span className={noChannel ? "project-create-model-name is-muted" : "project-create-model-name"}>
                                    {item.label}
                                    {id === recommendedModel ? <em className="project-create-badge">推荐</em> : null}
                                </span>
                                <span className="project-create-model-meta">每段最长 {item.duration.max} 秒{noChannel ? " · 没有渠道" : ""}</span>
                            </>
                        );
                    }}
                />
                {models.some((item) => families && !families.has(item.id)) && families && families.size > 0 ? (
                    <Link className="project-create-link" to={settingsPath("channels")}>
                        去配置渠道
                    </Link>
                ) : null}
            </div>
            <div className="project-create-field">
                <span className="project-create-field-label">生成模式</span>
                <ChoiceGroup
                    label="生成模式"
                    value={runMode}
                    options={DRAMA_RUN_MODES}
                    onChange={onRunMode}
                    className="project-choice-row project-choice-row-3"
                    optionClassName="project-choice"
                    render={(value) => {
                        const item = DRAMA_RUN_MODES.find((entry) => entry.value === value)!;
                        return (
                            <>
                                <strong>
                                    {item.label}
                                    {value === "until_video" ? <em className="project-create-badge">默认</em> : null}
                                </strong>
                                <span>{item.hint}</span>
                            </>
                        );
                    }}
                />
                <PipelineStrip stops={selectedMode.stops} />
                <p className="project-create-note">{DRAMA_VIDEO_STOP_NOTE}</p>
            </div>
        </div>
    );
}
