import { expect, test } from "bun:test";
import { chapterDialogueCoverage, parseDialogue } from "../../src/pages/projects/workbench/detail/chapter-segments";
import type { ProjectShot, ShotRevision } from "../../src/services/api/projects";

function spoken(...texts: string[]) {
    const shots = [{ id: "shot", currentRevisionId: "rev" }] as ProjectShot[];
    const revisions = [{ id: "rev", specJson: JSON.stringify({ shots: texts.map((text) => ({ lines: [{ speaker: "阿婆", text }] })) }) }] as ShotRevision[];
    return { shots, revisions };
}

test("unquoted script speech is counted even with zero shots, including single-character replies", () => {
    const result = chapterDialogueCoverage("△灯泡暗了。\n阿婆：（画外，声音很轻）又跳了。\n阿婆：嗯。\n林深：谢谢您。", [], []);
    expect(result.expected).toBe(3);
    expect(result.covered).toBe(0);
    expect(result.missing).toEqual(["又跳了", "嗯", "谢谢您"]);
});

test("saved HTML paragraphs retain separate speech without quotation marks", () => {
    const result = chapterDialogueCoverage("<p>△灯暗了。</p><p>阿婆：（画外）又跳了。</p><p>林深：谢谢您。</p><p>阿婆：嗯。</p>", [], []);
    expect(result.expected).toBe(3);
    expect(result.missing).toEqual(["又跳了", "谢谢您", "嗯"]);
});

test("repeated source speech requires repeated coverage", () => {
    const { shots, revisions } = spoken("收着。");
    const result = chapterDialogueCoverage("阿婆：收着。\n阿婆：收着。", shots, revisions);
    expect(result.expected).toBe(2);
    expect(result.covered).toBe(1);
    expect(result.missing).toEqual(["收着"]);
});

test("independent ledger exposes speech omitted with a whole scene, while split speech remains covered", () => {
    const { shots, revisions } = spoken("我一直", "记着。");
    const result = chapterDialogueCoverage("没有引号的动作原文", shots, revisions, [{ text: "我一直记着。" }, { text: "五年前。" }]);
    expect(result.expected).toBe(2);
    expect(result.covered).toBe(1);
    expect(result.missing).toEqual(["五年前"]);
});

test("post-colon performance cues do not become dialogue and correctly mark offscreen voice", () => {
    expect(parseDialogue("阿婆：（画外，声音很轻）又跳了。")).toEqual([{ speaker: "阿婆", text: "又跳了。", vo: true }]);
    expect(parseDialogue("林深：(低声)谢谢您。")).toEqual([{ speaker: "林深", text: "谢谢您。" }]);
});
