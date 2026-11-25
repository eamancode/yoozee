import { expect, test } from "bun:test";
import { clearPendingVideoReceipt, readPendingVideoReceipt, storePendingVideoReceipt, videoReceiptKey } from "../src/pages/projects/workbench/detail/video-chain-receipt";

function mockStorage() {
    const data = new Map<string, string>();
    return {
        data,
        storage: {
            getItem: (key: string) => data.get(key) ?? null,
            setItem: (key: string, value: string) => data.set(key, value),
            removeItem: (key: string) => data.delete(key),
        } as Storage,
    };
}

test("确认前保存的回执只含不透明标识，并按账号、项目、集隔离", () => {
    const { storage, data } = mockStorage();
    const key = videoReceiptKey("owner", "project", "episode");
    const intent = { requestId: "request-1", shotId: "shot-1", mode: "restart", sourceId: "previous" } as const;
    const form = { ...intent, modelInput: { token: "secret" } };
    storePendingVideoReceipt(storage, key, form);
    expect(readPendingVideoReceipt(storage, key)).toEqual(intent);
    expect(data.get(key)).not.toContain("secret");
    expect(readPendingVideoReceipt(storage, videoReceiptKey("other-owner", "project", "episode"))).toBeUndefined();
    expect(readPendingVideoReceipt(storage, videoReceiptKey("owner", "other-project", "episode"))).toBeUndefined();
    expect(readPendingVideoReceipt(storage, videoReceiptKey("owner", "project", "other-episode"))).toBeUndefined();
});

test("未核对回执前不可用另一个请求替换，缺失后相同标识可安全重试", () => {
    const { storage } = mockStorage();
    const key = videoReceiptKey("owner", "project", "episode");
    const intent = { requestId: "request-1", shotId: "shot-1", mode: "resume", sourceId: "previous" } as const;
    storePendingVideoReceipt(storage, key, intent);
    storePendingVideoReceipt(storage, key, intent);
    expect(() => storePendingVideoReceipt(storage, key, { ...intent, requestId: "new" })).toThrow("待查");
    expect(() => storePendingVideoReceipt(storage, key, { ...intent, mode: "restart" })).toThrow("待查");
    clearPendingVideoReceipt(storage, key, "wrong-id");
    expect(readPendingVideoReceipt(storage, key)).toEqual(intent);
    clearPendingVideoReceipt(storage, key, intent.requestId);
    expect(readPendingVideoReceipt(storage, key)).toBeUndefined();
});

test("旧回执无法解析时不会覆盖为新的付费请求", () => {
    const { storage } = mockStorage();
    const key = videoReceiptKey("owner", "project", "episode");
    storage.setItem(key, "{}");
    expect(() => storePendingVideoReceipt(storage, key, { requestId: "new", shotId: "shot", mode: "fresh" })).toThrow();
    expect(storage.getItem(key)).toBe("{}");
});
