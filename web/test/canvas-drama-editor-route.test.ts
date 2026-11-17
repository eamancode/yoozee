import { expect, test } from "bun:test";
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
