import { describe, expect, test } from "bun:test";

import { timelineClipLabel } from "../src/lib/timeline/clip-label";

describe("时间线片段名", () => {
    test("优先显示文字和标题，不把 asset:ID 给人看", () => {
        expect(timelineClipLabel({ id: "c1", kind: "image", nodeId: "asset:abc", title: "S01 分镜图" })).toBe("S01 分镜图");
        expect(timelineClipLabel({ id: "c1", kind: "image", nodeId: "asset:abc", directMedia: { id: "abc", kind: "image", title: "医院走廊" } })).toBe("医院走廊");
        expect(timelineClipLabel({ id: "c1", kind: "image", nodeId: "asset:abc" })).toBe("图片片段");
        expect(timelineClipLabel({ id: "c2", kind: "subtitle", nodeId: "srt", text: "七床的人呢？" })).toBe("七床的人呢？");
        expect(timelineClipLabel({ id: "c3", kind: "video", nodeId: "node-9" })).toBe("node-9");
    });
});
