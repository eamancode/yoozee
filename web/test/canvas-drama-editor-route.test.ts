import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { dramaCanvasPath, parseDramaCanvasEditor, parseDramaCanvasFocus } from "../src/services/drama-canvas";

test("整集剪辑和单镜预演、时间轴入口各自保留焦点", () => {
    const cut = { kind: "cut", episodeId: "ep-1" } as const;
    const episode = new URL(dramaCanvasPath("board", "drama", cut), "https://canvas.test");
    expect(parseDramaCanvasFocus(episode.searchParams.get("dramaFocus"))).toEqual(cut);
    expect(parseDramaCanvasEditor(episode.searchParams.get("dramaEditor"))).toBe("canvas");
    const segment = { kind: "segment", episodeId: "ep-1", segmentIndex: 0 } as const;
    for (const editor of ["previs", "timeline"] as const) {
        const shot = new URL(dramaCanvasPath("board", "drama", segment, editor), "https://canvas.test");
        expect(parseDramaCanvasFocus(shot.searchParams.get("dramaFocus"))).toEqual(segment);
        expect(parseDramaCanvasEditor(shot.searchParams.get("dramaEditor"))).toBe(editor);
        expect(() => dramaCanvasPath("board", "drama", cut, editor)).toThrow();
    }
});

test("画布分镜只投送到已开放的短剧流水线，并保留原文与画布预填", () => {
    const canvas = readFileSync(new URL("../src/pages/canvas/project.tsx", import.meta.url), "utf8");
    const create = canvas.slice(canvas.indexOf("const createDramaFromStoryboard ="), canvas.indexOf("const renameAssetNodes ="));
    expect(create).toContain('navigate("/drama-flow-2/new",');
    expect(create).not.toContain('navigate("/projects');
    expect(create).toContain('sourceFormat: "storyboard", text: source.text');
    expect(create).toContain("canvasId: currentProject?.projectId ? undefined : projectId");
    expect(create.indexOf("if (!canUseDramaFlow2) return;")).toBeGreaterThanOrEqual(0);
    expect(create.indexOf("if (!canUseDramaFlow2) return;")).toBeLessThan(create.indexOf("canvasStoryboardToDramaSource(node)"));
    expect(canvas).toContain("state.features.shortDramaEnabled && state.features.dramaFlow2Enabled");
    expect(canvas).toContain("onCreateDramaFromStoryboard={canUseDramaFlow2 ? createDramaFromStoryboard : undefined}");
    expect(canvas).toContain('dramaId: canUseDramaFlow2 ? linkedProjectId : ""');
});
