import type { VideoCapabilityConfig } from "@/lib/model-capabilities";

/** Project padding applies to single 15/30-second slots and the 15/30 dual-slot model. */
export function projectVideoDurationSlots(duration?: VideoCapabilityConfig["duration"]): number[] | undefined {
    const values = duration?.selection === "enum" ? Array.from(new Set(duration.values || [])) : duration?.selection === "range" && duration.min !== undefined && duration.min === duration.max ? [duration.min] : [];
    values.sort((left, right) => left - right);
    return (values.length === 1 && (values[0] === 15 || values[0] === 30)) || (values.length === 2 && values[0] === 15 && values[1] === 30) ? values : undefined;
}

export function projectVideoSubmissionSeconds(slots: number[] | undefined, contentSeconds: number) {
    if (!Number.isFinite(contentSeconds) || contentSeconds <= 0) return undefined;
    if (!slots) return contentSeconds;
    if (contentSeconds > slots[slots.length - 1]) return undefined;
    // The 15-second boundary deliberately enters the 30-second tier for dual-slot models.
    return slots.length === 2 ? (contentSeconds < 15 ? 15 : 30) : slots[0];
}
