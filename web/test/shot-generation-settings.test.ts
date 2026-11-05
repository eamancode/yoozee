import { describe, expect, test } from "bun:test";
import type { ShotArtifact } from "../src/services/api/projects";
import { parseShotGenerationSettings, restoreShotGenerationSettings, shotGenerationSettingsKey } from "../src/pages/projects/workbench/detail/shot-generation-settings";

const artifact = (model: string, patch: Partial<ShotArtifact> = {}) => ({ id: model, status: "ready", version: 1, selected: true, metadataJson: JSON.stringify({ model, aspectRatio: "9:16", resolution: "720" }), ...patch }) as ShotArtifact;
const restore = (patch: Partial<Parameters<typeof restoreShotGenerationSettings>[0]> = {}) =>
    restoreShotGenerationSettings({ artifacts: [artifact("channel::mj")], defaultModel: "channel::vp", available: (model) => ["channel::mj", "channel::vp"].includes(model), ...patch });

describe("镜头生成参数恢复", () => {
    test("最近使用优先于旧会话和历史产物，失效时才回退", () => {
        expect(restore({recentModel:"channel::vp",stored:{model:"channel::mj"}})).toMatchObject({settings:{model:"channel::vp"},source:"remembered"});
        expect(restore({recentModel:"disabled"})).toMatchObject({settings:{model:"channel::vp"},source:"default"});
    });
    test("重新打开有成品的镜头沿用实际渠道，不改用更贵的项目默认", () => {
        expect(restore()).toMatchObject({ source: "artifact", settings: { model: "channel::mj", aspectRatio: "9:16", resolution: "720" } });
    });
    test("显式选择优先，失效时说明回退，不使用被禁用的渠道", () => {
        expect(restore({ stored: { model: "channel::vp", resolution: "1080" } })).toMatchObject({ source: "remembered", settings: { model: "channel::vp", resolution: "1080" } });
        const unavailable = restore({ stored: { model: "disabled::old" } });
        expect(unavailable.settings.model).toBe("channel::mj");
        expect(unavailable.notice).toContain("不可用");
        expect(restore({ artifacts: [artifact("disabled::old")] }).notice).toContain("核对");
    });
    test("过期产物和无效元数据不能覆盖当前选择", () => {
        expect(restore({ artifacts: [artifact("channel::mj", { status: "stale" })] }).settings.model).toBe("channel::vp");
        expect(parseShotGenerationSettings("{")).toBeUndefined();
        expect(parseShotGenerationSettings('{"model":12}')).toBeUndefined();
        expect(parseShotGenerationSettings('{"model":"m","apiKey":"secret","resolution":"720"}')).toEqual({ model: "m", resolution: "720", aspectRatio: undefined, imageQuality: undefined });
    });
    test("账号、项目、镜头、阶段的选择互不串用", () => {
        const keys = [
            ["a", "p", "s", "video"],
            ["b", "p", "s", "video"],
            ["a", "q", "s", "video"],
            ["a", "p", "t", "video"],
            ["a", "p", "s", "storyboard"],
        ].map(([user, project, shot, stage]) => shotGenerationSettingsKey(user, project, shot, stage));
        expect(new Set(keys).size).toBe(5);
    });
});
