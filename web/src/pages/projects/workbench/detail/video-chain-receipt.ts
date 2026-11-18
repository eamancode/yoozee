export type VideoRecoveryMode = "fresh" | "resume" | "restart";

export type PendingVideoReceipt = {
    requestId: string;
    shotId: string;
    mode: VideoRecoveryMode;
    sourceId?: string;
};

export function videoReceiptKey(account: string, project: string, unit: string) {
    return `yingce:video-confirm:v2:${JSON.stringify([account, project, unit])}`;
}

export function readPendingVideoReceipt(storage: Storage, key: string): PendingVideoReceipt | undefined {
    const raw = storage.getItem(key);
    if (raw === null) return undefined;
    const value = JSON.parse(raw);
    if (
        typeof value?.requestId !== "string" || !value.requestId ||
        typeof value.shotId !== "string" || !value.shotId ||
        !["fresh", "resume", "restart"].includes(value.mode) ||
        (value.mode !== "fresh" && (typeof value.sourceId !== "string" || !value.sourceId))
    ) throw new Error("生成确认回执标识无法读取");
    return value;
}

export function storePendingVideoReceipt(storage: Storage, key: string, value: PendingVideoReceipt) {
    const previous = readPendingVideoReceipt(storage, key);
    if (previous && (previous.requestId !== value.requestId || previous.shotId !== value.shotId ||
        previous.mode !== value.mode || previous.sourceId !== value.sourceId)) {
        throw new Error("该集已有待查的提交回执，请先核对");
    }
    const {requestId, shotId, mode, sourceId} = value;
    storage.setItem(key, JSON.stringify({requestId, shotId, mode, sourceId}));
}

export function clearPendingVideoReceipt(storage: Storage, key: string, requestId: string) {
    if (readPendingVideoReceipt(storage, key)?.requestId === requestId) storage.removeItem(key);
}
