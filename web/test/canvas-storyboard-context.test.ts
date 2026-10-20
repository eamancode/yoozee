import { describe, expect, it } from "bun:test";

import { resolveStoryboardGenerationContext, storyboardCharacterAppearancePrompt, storyboardStylePrompt } from "@/lib/canvas/canvas-storyboard-context";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

const node = (id: string, type: CanvasNodeType, metadata: CanvasNodeData["metadata"] = {}): CanvasNodeData => ({
    id,
    type,
    title: id,
    position: { x: 0, y: 0 },
    width: 320,
    height: 180,
    metadata,
});

const styleNode = node("style", CanvasNodeType.Text, {
    workflowKind: "styleboard",
    stylePresetId: "style-v1",
    content: "统一的项目画风",
});

describe("storyboard generation context", () => {
    it("does not require a project style node", () => {
        const context = resolveStoryboardGenerationContext([]);

        expect(context.characters).toEqual([]);
    });

    it("ignores standalone character-design workflows without a linked asset", () => {
        const context = resolveStoryboardGenerationContext([
            styleNode,
            node("character-turnaround", CanvasNodeType.Image, {
                workflowKind: "character",
                composerContent: "生成角色三视图",
            }),
        ]);

        expect(context.characters).toEqual([]);
    });

    it("keeps the version check for linked character cards", () => {
        expect(() => resolveStoryboardGenerationContext([
            styleNode,
            node("character-card", CanvasNodeType.Text, {
                workflowKind: "character",
                characterAssetId: "asset-1",
                characterName: "李当歌",
            }),
        ])).toThrow("角色卡“李当歌”版本未同步");
    });

    it("passes linked character cards with a current version", () => {
        const context = resolveStoryboardGenerationContext([
            styleNode,
            node("character-card", CanvasNodeType.Text, {
                workflowKind: "character",
                characterAssetId: "asset-1",
                characterVersionId: "version-1",
                characterName: "李当歌",
                characterDefinition: { role: "镇国公世子" },
            }),
        ]);

        expect(context.characters).toEqual([{
            assetId: "asset-1",
            versionId: "version-1",
            name: "李当歌",
            definition: { role: "镇国公世子" },
        }]);
    });
});

describe("storyboard style and character prompts", () => {
    it("keeps higher-priority style sections whole, cuts the lowest one to fit and keeps the original order", () => {
        const prompt = storyboardStylePrompt({
            title: "自定义水墨",
            prompt: [
                `【项目定位】${"定位说明".repeat(150)}`,
                "【风格组合】视觉媒介：水墨；角色造型：写意。",
                `【视觉媒介】宣纸水墨，留白，墨色浓淡${"层次".repeat(400)}`,
                "【全局禁用】霓虹灯、赛博朋克、塑料质感。",
            ].join("\n"),
        });

        expect(prompt.startsWith("【项目画风：自定义水墨】")).toBe(true);
        const positioning = prompt.split("\n").find((line) => line.startsWith("【项目定位】")) || "";
        expect(positioning.endsWith("…")).toBe(true);
        expect(positioning.length).toBeLessThan(600);
        expect(prompt).toContain("【风格组合】视觉媒介：水墨；角色造型：写意。");
        expect(prompt).toContain("【全局禁用】霓虹灯、赛博朋克、塑料质感。");
        expect(prompt).toContain("【视觉媒介】宣纸水墨，留白，墨色浓淡");
        expect(prompt).toContain(`墨色浓淡${"层次".repeat(400)}`);
        expect(prompt.indexOf("【项目定位】")).toBeLessThan(prompt.indexOf("【风格组合】"));
        expect(prompt.indexOf("【风格组合】")).toBeLessThan(prompt.indexOf("【视觉媒介】"));
        expect(prompt.indexOf("【视觉媒介】")).toBeLessThan(prompt.indexOf("【全局禁用】"));
        expect(prompt.split("\n").slice(1).join("").length).toBeLessThanOrEqual(1200);
    });

    it("truncates a free-form style and skips an empty one", () => {
        const prompt = storyboardStylePrompt({ title: "长描述", prompt: "胶片颗粒，暖色调".repeat(300) });

        expect(prompt.split("\n")[1].length).toBe(1200);
        expect(prompt.endsWith("…")).toBe(true);
        expect(storyboardStylePrompt({ title: "空", prompt: "  \n " })).toBe("");
        expect(storyboardStylePrompt(undefined)).toBe("");
    });

    it("writes visual character fields, orders by first mention and keeps the budget", () => {
        const prompt = storyboardCharacterAppearancePrompt([
            { name: "配角", definition: { appearance: "灰色短发", personality: "沉稳", voiceTimbre: "低沉" } },
            { name: "林小满", definition: { aliases: ["小满"], appearance: `圆脸短发${"细节".repeat(60)}`, clothing: "白色护士服", consistencyPrompt: "左手腕红绳" } },
            { name: "画布角色", definition: { prompt: "银色长发，黑色斗篷" } },
            { name: "无设定", definition: { role: "路人" } },
        ], "小满推开七床病房的门。画布角色站在走廊。");

        const lines = prompt.split("\n");
        expect(lines[0]).toContain("【已确认角色外观】");
        expect(lines[1]).toStartWith("- 林小满｜外貌：圆脸短发");
        expect(lines[1]).toContain("服装：白色护士服；一致性：左手腕红绳");
        expect(lines[1]).toContain("…");
        expect(lines[2]).toBe("- 画布角色｜银色长发，黑色斗篷");
        expect(lines[3]).toBe("- 配角｜外貌：灰色短发");
        expect(prompt).not.toContain("沉稳");
        expect(prompt).not.toContain("无设定");
        expect(storyboardCharacterAppearancePrompt([])).toBe("");
    });

    it("stops adding characters once the budget is used", () => {
        const characters = Array.from({ length: 20 }, (_, index) => ({ name: `角色${index + 1}`, definition: { appearance: "外".repeat(80), clothing: "衣".repeat(80), physique: "高".repeat(80) } }));
        const lines = storyboardCharacterAppearancePrompt(characters).split("\n").slice(1);

        expect(lines.length).toBeGreaterThan(0);
        expect(lines.length).toBeLessThan(20);
        expect(lines.every((line) => line.length <= 220)).toBe(true);
        expect(lines.join("").length).toBeLessThanOrEqual(1600);
        expect(lines[0]).toStartWith("- 角色1｜");
    });
});
