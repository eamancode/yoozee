import { useMemo, useState } from "react";
import { Button, Checkbox, Input, InputNumber } from "antd";
import { Plus, X } from "lucide-react";

import { Select } from "@/components/ui/base/select";
import type { DramaShot, DramaShotBlocking, DramaShotLight, DramaShotLine, DramaShotVocab } from "@/services/api/drama";

import type { ScriptLineOption } from "./shot-issues";

type LineDraft = DramaShotLine & { key: number; rewrite: boolean };

const LIGHT_FIELDS: Array<{ key: keyof DramaShotLight; label: string; placeholder: string }> = [
    { key: "source_anchor", label: "光源", placeholder: "走廊顶灯" },
    { key: "direction", label: "方向", placeholder: "顶光偏左" },
    { key: "key", label: "主光", placeholder: "冷白顶光" },
    { key: "fill", label: "补光", placeholder: "无补光" },
];

/**
 * 分镜卡编辑：改段内一个镜头背后的镜头表镜头。景别、机位、运镜、情绪弧只能从镜头表的可选值里选（情绪弧取情绪弧索引），
 * 台词从本章台词表里选，也可以改写原文（后端按原文对回台词表，对不上的检查会报出来）。保存后由后端重编段内文字并重检。
 */
export function ShotCardEditor({ shot, vocab, lines, saving, onSave, onCancel }: { shot: DramaShot; vocab?: DramaShotVocab; lines: ScriptLineOption[]; saving: boolean; onSave: (shot: DramaShot) => void; onCancel: () => void }) {
    const [action, setAction] = useState(shot.action || "");
    const [composition, setComposition] = useState(shot.composition || "");
    const [size, setSize] = useState(shot.size || "");
    const [est, setEst] = useState<number | null>(Number(shot.est) || null);
    const [camX, setCamX] = useState(shot.cam?.x || "");
    const [camY, setCamY] = useState(shot.cam?.y || "");
    const [moves, setMoves] = useState<string[]>(() => (Array.isArray(shot.move) ? shot.move : shot.move ? [shot.move] : []));
    const [arcId, setArcId] = useState(shot.emotion_arc?.id || "");
    const [blocking, setBlocking] = useState<DramaShotBlocking[]>(() => shot.blocking?.map((item) => ({ ...item })) || []);
    const [light, setLight] = useState<DramaShotLight>(() => ({ ...(shot.light || {}) }));
    const [lineDrafts, setLineDrafts] = useState<LineDraft[]>(() => (shot.lines || []).map((line, key) => ({ ...line, key, rewrite: !line.line_id })));
    const lineOptions = useMemo(() => lines.map((line) => ({ value: line.id, label: `${line.id} ${line.speaker || "旁白"}${line.vo ? "（画外）" : ""}：${line.text}` })), [lines]);
    const arcOptions = useMemo(() => (vocab?.emotionArcs || []).map((arc) => ({ value: arc.id, label: `${arc.name}（${arc.id}）${arc.phases.length ? ` · ${arc.phases.join(" → ")}` : ""}` })), [vocab]);
    const knownArc = !arcId || arcOptions.some((option) => option.value === arcId);
    const codeOptions = (words?: Record<string, string>) => Object.entries(words || {}).map(([value, label]) => ({ value, label: `${label}（${value}）` }));
    const updateLine = (key: number, patch: Partial<LineDraft>) => setLineDrafts((current) => current.map((line) => (line.key === key ? { ...line, ...patch } : line)));
    const updateBlocking = (index: number, patch: Partial<DramaShotBlocking>) => setBlocking((current) => current.map((item, position) => (position === index ? { ...item, ...patch } : item)));

    const build = (): DramaShot => {
        const next: DramaShot = { ...shot, est: est || 0 };
        const assign = <K extends keyof DramaShot>(key: K, value: DramaShot[K] | undefined) => {
            if (value === undefined || value === "" || (Array.isArray(value) && !value.length)) delete next[key];
            else next[key] = value;
        };
        assign("action", action.trim());
        assign("composition", composition.trim());
        assign("size", size);
        const cam = { ...(shot.cam || {}), x: camX || undefined, y: camY || undefined };
        assign("cam", cam.x || cam.y || cam.z || cam.f ? cam : undefined);
        assign("move", moves);
        // 换了情绪弧时去掉段位（phase_from / phase_to 是按原来那条弧的段数写的），强度、触发、承接留着。
        const arc = shot.emotion_arc;
        assign(
            "emotion_arc",
            arcId ? (arc?.id === arcId ? arc : { id: arcId, ...(arc?.continues ? { continues: arc.continues } : {}), ...(arc?.intensity ? { intensity: arc.intensity } : {}), ...(arc?.trigger ? { trigger: arc.trigger } : {}) }) : undefined,
        );
        assign(
            "blocking",
            blocking.map((item) => ({ ...item, who: item.who.trim(), at: item.at.trim(), facing: item.facing?.trim() || undefined })).filter((item) => item.who && item.at),
        );
        const cleanLight = Object.fromEntries(Object.entries(light).filter(([, value]) => typeof value === "string" && value.trim())) as DramaShotLight;
        assign("light", Object.keys(cleanLight).length ? cleanLight : undefined);
        const cleanLines = lineDrafts.flatMap(({ key: _key, rewrite, ...line }): DramaShotLine[] => {
            const base: DramaShotLine = { ...line, delivery: line.delivery?.trim() || undefined };
            if (rewrite) return line.text?.trim() ? [{ ...base, line_id: undefined, text: line.text.trim(), speaker: line.speaker?.trim() || undefined }] : [];
            return line.line_id ? [{ ...base, text: undefined, speaker: undefined }] : [];
        });
        assign("lines", cleanLines);
        return next;
    };

    return (
        <div className="grid gap-2 rounded-md border border-border/70 bg-surface-active p-2" aria-label={`编辑分镜卡 ${shot.id}`}>
            <div className="flex items-center justify-between text-foreground/50">
                <span className="font-mono">{shot.id}</span>
                <span>改的是镜头表，保存后按镜头表重编这一镜并重检</span>
            </div>
            <label className="grid gap-1">
                <span className="text-foreground/50">动作</span>
                <Input.TextArea autoFocus value={action} onChange={(event) => setAction(event.target.value)} autoSize={{ minRows: 2, maxRows: 6 }} placeholder="人物做了什么、看得见的变化" />
            </label>
            <label className="grid gap-1">
                <span className="text-foreground/50">画面（构图）</span>
                <Input.TextArea value={composition} onChange={(event) => setComposition(event.target.value)} autoSize={{ minRows: 1, maxRows: 4 }} placeholder="主体在画面哪里、前后景" />
            </label>
            <div className="grid gap-1">
                <span className="text-foreground/50">台词（从本章台词表里选，一句只放一个镜头）</span>
                {lineDrafts.map((line) => (
                    <div key={line.key} className="grid gap-1 rounded border border-border/60 p-1.5">
                        <div className="flex items-center gap-1.5">
                            {line.rewrite ? (
                                <>
                                    <Input size="small" className="!w-24" value={line.speaker || ""} placeholder="说话人" aria-label="说话人" onChange={(event) => updateLine(line.key, { speaker: event.target.value })} />
                                    <Input size="small" className="min-w-0 flex-1" value={line.text || ""} placeholder="台词原文" aria-label="台词原文" onChange={(event) => updateLine(line.key, { text: event.target.value })} />
                                </>
                            ) : (
                                <Select
                                    size="sm"
                                    appearance="field"
                                    className="min-w-0 flex-1"
                                    showSearch
                                    optionFilterProp="label"
                                    value={line.line_id || undefined}
                                    placeholder="选一句台词"
                                    ariaLabel="台词"
                                    options={lineOptions}
                                    onChange={(value: string) => updateLine(line.key, { line_id: value })}
                                />
                            )}
                            <Button size="small" type="text" icon={<X className="size-3.5" />} aria-label="去掉这句台词" onClick={() => setLineDrafts((current) => current.filter((item) => item.key !== line.key))} />
                        </div>
                        <div className="flex flex-wrap items-center gap-2">
                            <Input size="small" className="min-w-0 flex-1" value={line.delivery || ""} placeholder="怎么说（语气、动作）" aria-label="台词说法" onChange={(event) => updateLine(line.key, { delivery: event.target.value })} />
                            <Checkbox checked={Boolean(line.offscreen)} onChange={(event) => updateLine(line.key, { offscreen: event.target.checked || undefined })}>
                                画外
                            </Checkbox>
                            <Button
                                size="small"
                                type="link"
                                className="!px-0"
                                onClick={() =>
                                    updateLine(
                                        line.key,
                                        line.rewrite ? { rewrite: false } : { rewrite: true, text: line.text || lines.find((item) => item.id === line.line_id)?.text, speaker: line.speaker || lines.find((item) => item.id === line.line_id)?.speaker },
                                    )
                                }
                            >
                                {line.rewrite ? "改回从台词表选" : "改写原文"}
                            </Button>
                        </div>
                    </div>
                ))}
                <Button size="small" type="dashed" icon={<Plus className="size-3.5" />} onClick={() => setLineDrafts((current) => [...current, { key: Math.max(0, ...current.map((item) => item.key + 1)), rewrite: false }])}>
                    加一句台词
                </Button>
            </div>
            <div className="grid gap-2 sm:grid-cols-2">
                <label className="grid gap-1">
                    <span className="text-foreground/50">景别</span>
                    <Select size="sm" appearance="field" allowClear value={size || undefined} ariaLabel="景别" options={(vocab?.sizes || []).map((value) => ({ value, label: value }))} onChange={(value?: string) => setSize(value || "")} />
                </label>
                <label className="grid gap-1">
                    <span className="text-foreground/50">时长（秒）</span>
                    <InputNumber size="small" min={0.5} max={60} step={0.5} value={est} onChange={setEst} className="!w-full" />
                </label>
                <label className="grid gap-1">
                    <span className="text-foreground/50">机位朝向</span>
                    <Select size="sm" appearance="field" allowClear value={camX || undefined} ariaLabel="机位朝向" options={codeOptions(vocab?.cameraX)} onChange={(value?: string) => setCamX(value || "")} />
                </label>
                <label className="grid gap-1">
                    <span className="text-foreground/50">机位高度</span>
                    <Select size="sm" appearance="field" allowClear value={camY || undefined} ariaLabel="机位高度" options={codeOptions(vocab?.cameraY)} onChange={(value?: string) => setCamY(value || "")} />
                </label>
                <label className="grid gap-1 sm:col-span-2">
                    <span className="text-foreground/50">运镜</span>
                    <Select size="sm" appearance="field" mode="multiple" value={moves} ariaLabel="运镜" options={codeOptions(vocab?.moves)} onChange={(value: string[]) => setMoves(value)} />
                </label>
                <label className="grid gap-1 sm:col-span-2">
                    <span className="text-foreground/50">情绪弧（只能从情绪弧索引里选）</span>
                    <Select
                        size="sm"
                        appearance="field"
                        allowClear
                        showSearch
                        optionFilterProp="label"
                        value={arcId || undefined}
                        status={knownArc ? undefined : "error"}
                        placeholder="不用情绪弧"
                        ariaLabel="情绪弧"
                        options={knownArc ? arcOptions : [{ value: arcId, label: `${arcId}（不在索引里，重新选一条）`, disabled: true }, ...arcOptions]}
                        onChange={(value?: string) => setArcId(value || "")}
                    />
                </label>
            </div>
            <div className="grid gap-1">
                <span className="text-foreground/50">站位（谁、在哪、朝哪）</span>
                {blocking.map((item, index) => (
                    <div key={index} className="flex items-center gap-1.5">
                        <Input size="small" className="!w-24" value={item.who} placeholder="谁" aria-label="站位人物" onChange={(event) => updateBlocking(index, { who: event.target.value })} />
                        <Input size="small" className="min-w-0 flex-1" value={item.at} placeholder="在哪（锚点）" aria-label="站位位置" onChange={(event) => updateBlocking(index, { at: event.target.value })} />
                        <Input size="small" className="!w-24" value={item.facing || ""} placeholder="朝向" aria-label="站位朝向" onChange={(event) => updateBlocking(index, { facing: event.target.value })} />
                        <Button size="small" type="text" icon={<X className="size-3.5" />} aria-label="去掉这个站位" onClick={() => setBlocking((current) => current.filter((_, position) => position !== index))} />
                    </div>
                ))}
                <Button size="small" type="dashed" icon={<Plus className="size-3.5" />} onClick={() => setBlocking((current) => [...current, { who: "", at: "" }])}>
                    加一个站位
                </Button>
            </div>
            <div className="grid gap-1">
                <span className="text-foreground/50">灯光</span>
                <div className="grid gap-1.5 sm:grid-cols-2">
                    {LIGHT_FIELDS.map((field) => (
                        <label key={field.key} className="flex items-center gap-1.5">
                            <span className="w-8 shrink-0 text-foreground/45">{field.label}</span>
                            <Input size="small" value={light[field.key] || ""} placeholder={field.placeholder} onChange={(event) => setLight((current) => ({ ...current, [field.key]: event.target.value }))} />
                        </label>
                    ))}
                </div>
            </div>
            <div className="flex justify-end gap-1.5">
                <Button size="small" onClick={onCancel} disabled={saving}>
                    取消
                </Button>
                <Button size="small" type="primary" loading={saving} disabled={!est || !knownArc} onClick={() => onSave(build())}>
                    保存并重检
                </Button>
            </div>
        </div>
    );
}
