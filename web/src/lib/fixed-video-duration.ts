import type { VideoCapabilityConfig } from "@/lib/model-capabilities";

/** Only single-slot 15/30 second models pad project content to their fixed output duration. */
export function fixedVideoDuration(duration?: VideoCapabilityConfig["duration"]) {
    const values = duration?.selection === "enum" ? Array.from(new Set(duration.values || [])) : duration?.selection === "range" && duration.min === duration.max ? [duration.min] : [];
    return values.length === 1 && (values[0] === 15 || values[0] === 30) ? values[0] : undefined;
}

export function fixedVideoSubmissionSeconds(fixedSeconds: number | undefined, contentSeconds: number) {
    if (!Number.isFinite(contentSeconds) || contentSeconds <= 0) return undefined;
    return fixedSeconds === undefined ? contentSeconds : contentSeconds <= fixedSeconds ? fixedSeconds : undefined;
}
