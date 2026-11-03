function record(value: unknown): Record<string, unknown> {
    return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}
function text(value: unknown) {
    return typeof value === "string" ? value : "";
}
function rows(value: unknown) {
    return Array.isArray(value) ? value.map(record) : [];
}
function names(value: unknown) {
    return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string").join("、") : "";
}

/** The attachment's source/design records stay visible independently of render prompts. */
export function AssetDesignEvidence({ definition }: { definition?: Record<string, unknown> }) {
    const design = record(definition?.extractionDesign);
    if (!Object.keys(design).length) return null;
    return (
        <details className="mt-2 rounded border border-border/60 p-2 text-xs">
            <summary className="cursor-pointer font-medium focus-visible:outline">设计依据、关联与状态变化</summary>
            <div className="mt-2 max-h-80 space-y-3 overflow-y-auto break-words text-foreground/70">
                {text(design.prop_design) ? <p>{text(design.prop_design)}</p> : null}
                {text(design.spatial_layout) ? <p>{text(design.spatial_layout)}</p> : null}
                {[
                    ["characters", "关联人物"],
                    ["props", "关联道具"],
                    ["environments", "关联场景"],
                ].map(([key, label]) =>
                    names(design[key]) ? (
                        <p key={key}>
                            {label}：{names(design[key])}
                        </p>
                    ) : null,
                )}
                {rows(design.evidence).map((item, index) => (
                    <p key={index}>
                        <strong>
                            {text(item.kind)} · {text(item.location)}
                        </strong>
                        <br />
                        {text(item.text)}
                    </p>
                ))}
                {rows(design.states).map((item, index) => (
                    <p key={index}>
                        <strong>状态 · {text(item.location)}</strong>
                        <br />
                        {text(item.trigger)}：{text(item.state)}
                    </p>
                ))}
            </div>
        </details>
    );
}
