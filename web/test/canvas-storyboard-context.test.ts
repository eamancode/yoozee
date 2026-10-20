import { describe, expect, it } from "bun:test";

import { expandStoryboardTextMentions, stripProjectStyleExecutionBlocks, withStoryboardOutputContract } from "@/lib/canvas/canvas-project-domain";
import { canvasStoryboardStyle, canvasStoryboardTaskPrompt, resolveStoryboardGenerationContext, storyboardCharacterAppearancePrompt, storyboardStylePrompt } from "@/lib/canvas/canvas-storyboard-context";
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

describe("canvas storyboard task prompt", () => {
    const inkStyle = ["【风格组合】视觉媒介：水墨；角色造型：写意。", "【视觉媒介】宣纸水墨，留白。", "【全局禁用】霓虹灯、赛博朋克。"].join("\n");
    const projectStyleNode = { ...node("style", CanvasNodeType.Text, { workflowKind: "styleboard", stylePresetId: "ink-v1", content: inkStyle, prompt: inkStyle, locked: true }), title: "项目画风 · 仙侠水墨" };
    const lin = node("lin", CanvasNodeType.Image, {
        workflowKind: "character",
        characterAssetId: "asset-lin",
        characterVersionId: "version-lin",
        characterName: "林小满",
        characterDefinition: { appearance: "圆脸短发", clothing: "白色护士服", personality: "倔强" },
    });
    const card = node("card", CanvasNodeType.Image, { workflowKind: "character", characterAssetId: "asset-card", characterVersionId: "version-card", characterName: "周医生", characterPrompt: "银框眼镜，灰色西装" });
    const nodes = [projectStyleNode, lin, card];
    const characters = resolveStoryboardGenerationContext(nodes).characters;
    const occurrences = (text: string, part: string) => text.split(part).length - 1;

    it("writes the style node and confirmed character looks before the output contract", () => {
        const story = "周医生在走廊叫住林小满。";
        const prompt = canvasStoryboardTaskPrompt(`<skill-context>拆镜方法</skill-context>\n\n${story}`, { sourceText: story, nodes, characters, shotCount: 6, shotDurationSeconds: 5 });

        expect(prompt).toContain("【项目画风：仙侠水墨】");
        expect(prompt).toContain("【视觉媒介】宣纸水墨，留白。");
        expect(prompt).toContain("【已确认角色外观】");
        expect(prompt).toContain("- 林小满｜外貌：圆脸短发；服装：白色护士服");
        expect(prompt).toContain("- 周医生｜银框眼镜，灰色西装");
        expect(prompt).not.toContain("倔强");
        expect(prompt.indexOf("- 周医生")).toBeLessThan(prompt.indexOf("- 林小满"));
        expect(prompt.indexOf(story)).toBeLessThan(prompt.indexOf("【项目画风：仙侠水墨】"));
        expect(prompt.indexOf("【项目画风：仙侠水墨】")).toBeLessThan(prompt.indexOf("【已确认角色外观】"));
        expect(prompt.indexOf("【已确认角色外观】")).toBeLessThan(prompt.indexOf("【受保护输出契约】"));
        expect(prompt).toContain("全片共 6 个镜头；每镜约 5 秒");
    });

    it("does not bring back the legacy execution block or repeat a style the user already mentioned", () => {
        const composer = `@[node:style] 周医生在走廊叫住林小满。\n\n【项目画风执行规范】\n${inkStyle}\n【全局负面 Prompt】\n低清晰度`;
        const story = stripProjectStyleExecutionBlocks(composer);
        const expanded = expandStoryboardTextMentions(story, [{ id: "style", nodeId: "style", kind: "text", label: "文本1", title: projectStyleNode.title, text: inkStyle, active: true }]);
        const prompt = canvasStoryboardTaskPrompt(expanded, { sourceText: story, nodes, characters });

        expect(prompt).not.toContain("【项目画风执行规范】");
        expect(prompt).not.toContain("低清晰度");
        expect(prompt).not.toContain("【项目画风：");
        expect(occurrences(prompt, "【视觉媒介】宣纸水墨，留白。")).toBe(1);
        expect(prompt).toContain("【已确认角色外观】");

        const plain = canvasStoryboardTaskPrompt(stripProjectStyleExecutionBlocks("周医生在走廊叫住林小满。\n\n【项目画风执行规范】\n" + inkStyle), { sourceText: "周医生在走廊叫住林小满。", nodes, characters });
        expect(occurrences(plain, "【项目画风：")).toBe(1);
        expect(occurrences(plain, "【视觉媒介】宣纸水墨，留白。")).toBe(1);
    });

    it("skips a style placeholder and keeps the added sections within budget", () => {
        expect(canvasStoryboardStyle([node("placeholder", CanvasNodeType.Text, { workflowKind: "styleboard", content: "" })])).toBeUndefined();
        expect(canvasStoryboardStyle([node("no-preset", CanvasNodeType.Text, { workflowKind: "styleboard", content: inkStyle })])).toBeUndefined();
        expect(canvasStoryboardTaskPrompt("剧情", { sourceText: "剧情", nodes: [], characters: [] })).toBe(withStoryboardOutputContract("剧情"));

        const longStyle = { ...projectStyleNode, metadata: { ...projectStyleNode.metadata, content: Array.from({ length: 12 }, (_, index) => `【小节${index}】${"长".repeat(300)}`).join("\n") } };
        const crowd = Array.from({ length: 30 }, (_, index) => ({ assetId: `asset-${index}`, versionId: `version-${index}`, name: `角色${index + 1}`, definition: { appearance: "外".repeat(120), clothing: "衣".repeat(120), props: "物".repeat(120) } }));
        const prompt = canvasStoryboardTaskPrompt("剧情", { sourceText: "剧情", nodes: [longStyle], characters: crowd });
        const added = prompt.length - withStoryboardOutputContract("剧情").length;

        expect(prompt).toContain("【项目画风：仙侠水墨】");
        expect(prompt).toContain("- 角色1｜");
        expect(prompt).not.toContain("- 角色30｜");
        expect(added).toBeLessThanOrEqual(1200 + 1600 + 400);
    });
});
