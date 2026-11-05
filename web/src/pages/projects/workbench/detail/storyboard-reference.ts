export function storyboardReferenceSource(artifact: { metadataJson?: string }) {
    try {
        const metadata = JSON.parse(artifact.metadataJson || "{}");
        if (metadata.role !== "continuity_reference" || !metadata.continuityReference) return undefined;
        const source = metadata.continuityReference;
        return {
            title: typeof source.sourceShotTitle === "string" ? source.sourceShotTitle : "上一镜头组",
            videoResourceId: typeof source.sourceVideoResourceId === "string" ? source.sourceVideoResourceId : "",
            timeSeconds: typeof source.frameTimeSeconds === "number" && Number.isFinite(source.frameTimeSeconds) ? source.frameTimeSeconds : undefined,
        };
    } catch { return undefined; }
}
