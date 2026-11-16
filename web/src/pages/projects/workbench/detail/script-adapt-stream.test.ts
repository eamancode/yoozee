import assert from "node:assert/strict";
import test from "node:test";

import { episodeSourcesComment, withEpisodeSourcesComment } from "./episode-sources";
import { scriptAdaptDefaultMode } from "./script-adapt-mode";
import { parseScriptAdaptStream, resumedScriptAdaptStream, scriptAdaptEpisodeNo, scriptAdaptGroupTitle, scriptAdaptRunningText, scriptAdaptStreamFromResult } from "./script-adapt-stream";

const marker = (index: number, attempt = 1) => JSON.stringify({ "@turn": { index, total: 2, scene: String(index), heading: `第 ${index} 集`, attempt } });
const header = (no: number, title: string) => JSON.stringify({ no, title, sources: [{ unitId: "c1", from: 1, to: 3 }] });

test("parseScriptAdaptStream 按集分组，JSON 头补标题，场次头单独记", () => {
    const text = [marker(1), header(1, "七床空了"), "## 内·病房走廊·夜", "（灯在闪。）", "林小满：七床？", marker(2), header(2, "现在信了"), "## 内·电梯口·夜", "周岸：退"].join("\n");
    const state = parseScriptAdaptStream(text);
    assert.equal(state.total, 2);
    assert.equal(state.current, 2);
    assert.equal(state.episodes.length, 2);
    assert.equal(state.episodes[0].title, "七床空了");
    assert.deepEqual(state.episodes[0].scenes, ["内·病房走廊·夜"]);
    assert.equal(state.episodes[0].lines.length, 3);
    // 最后一行没写完也显示：看得出模型在写。
    assert.equal(state.episodes[1].lines.at(-1), "周岸：退");
    assert.equal(state.sceneCount, 2);
});

test("同一集重试时清掉之前的半截输出", () => {
    const state = parseScriptAdaptStream([marker(1), header(1, "a"), "## 内·客厅·夜", "甲：你", marker(1, 2), header(1, "a"), "## 内·客厅·日"].join("\n"));
    assert.equal(state.episodes.length, 1);
    assert.equal(state.episodes[0].attempt, 2);
    assert.deepEqual(state.episodes[0].scenes, ["内·客厅·日"]);
});

test("scriptAdaptStreamFromResult 从失败任务的各轮原文或成功任务的 text 还原", () => {
    const failed = scriptAdaptStreamFromResult(JSON.stringify({ dramaRun: { total: 3, turns: [{ index: 1, heading: "第 1 集", text: `${header(1, "a")}\n## 内·客厅·夜\n甲：你好。` }] } }));
    assert.equal(failed.total, 3);
    assert.equal(failed.episodes[0].title, "a");
    const done = scriptAdaptStreamFromResult(JSON.stringify({ text: `${header(1, "a")}\n## 内·客厅·夜\n${header(2, "b")}\n## 外·街·日\n`, dramaRun: { total: 2, turns: [{ index: 1 }, { index: 2 }] } }));
    assert.deepEqual(
        done.episodes.map((item) => item.title),
        ["a", "b"],
    );
    assert.equal(scriptAdaptStreamFromResult("not json").episodes.length, 0);
});

test("保存剧集时接回来源注释，不重复", () => {
    const stored = "<!-- sources: c1:1-3;c2:1-1 --><p>## 内·客厅·夜</p>";
    assert.equal(episodeSourcesComment(stored), "<!-- sources: c1:1-3;c2:1-1 -->");
    assert.equal(withEpisodeSourcesComment(stored, "<p>## 内·客厅·日</p>"), "<!-- sources: c1:1-3;c2:1-1 --><p>## 内·客厅·日</p>");
    assert.equal(withEpisodeSourcesComment(stored, stored), stored);
    assert.equal(withEpisodeSourcesComment("<p>导入的剧本</p>", "<p>改过</p>"), "<p>改过</p>");
});

test("续写一开始就按续写起点显示，前面的集接在新输出前面", () => {
    const previous = parseScriptAdaptStream([marker(1), header(1, "七床空了"), "## 内·病房走廊·夜", "林小满：七床？"].join("\n"));
    const resume = { from: 2, total: 2, episodes: previous.episodes };
    const waiting = resumedScriptAdaptStream(parseScriptAdaptStream(""), resume);
    assert.equal(waiting.current, 2);
    assert.equal(waiting.total, 2);
    assert.deepEqual(
        waiting.episodes.map((episode) => episode.index),
        [1],
    );
    const live = resumedScriptAdaptStream(parseScriptAdaptStream([marker(2), header(2, "现在信了"), "## 内·电梯口·夜"].join("\n")), resume);
    assert.equal(live.current, 2);
    assert.deepEqual(
        live.episodes.map((episode) => episode.title),
        ["七床空了", "现在信了"],
    );
    assert.equal(live.sceneCount, 2);
    // 不是续写：原样返回。
    assert.equal(resumedScriptAdaptStream(waiting), waiting);
});

test("剧本优化默认模式：原文都改编过、导入剧本时只调节奏", () => {
    const chapterIds = ["c1", "c2"];
    assert.equal(scriptAdaptDefaultMode({ sourceType: "novel", chapterIds, episodeCount: 0 }), "adapt");
    assert.equal(scriptAdaptDefaultMode({ sourceType: "novel", chapterIds, episodeCount: 1, adaptedChapterIds: ["c1"] }), "adapt");
    assert.equal(scriptAdaptDefaultMode({ sourceType: "novel", chapterIds, episodeCount: 2, adaptedChapterIds: ["c1", "c2"] }), "rhythm");
    // 剧集清单还没读到：先按小说转剧本。
    assert.equal(scriptAdaptDefaultMode({ sourceType: "novel", chapterIds, episodeCount: 2 }), "adapt");
    assert.equal(scriptAdaptDefaultMode({ sourceType: "script", chapterIds, episodeCount: 0 }), "rhythm");
    assert.equal(scriptAdaptDefaultMode({ sourceType: "novel", chapterIds: [], episodeCount: 2 }), "rhythm");
});

// SSS 流水线拆剧本第 1 轮是「立意与全集结构」（轮次 ID concept）：不标成第 1 集，剧集轮按真实集号标。
const sssMarker = (index: number, scene: string, heading: string, total = 3) => JSON.stringify({ "@turn": { index, total, scene, heading, attempt: 1 } });

test("立意轮标「立意与全集结构」，剧集轮按集号标，立意页的二级标题不算场次", () => {
    const text = [sssMarker(1, "concept", "立意与全集结构"), "## 立意页", "一句话：雨夜送橙汁。", "## 全集结构表", "| 集 | 任务 |", sssMarker(2, "1", "第 1 集"), header(1, "雨夜"), "## 内·雨棚·夜", "林深：谢谢。"].join("\n");
    const state = parseScriptAdaptStream(text);
    assert.equal(state.concept, true);
    assert.equal(scriptAdaptGroupTitle(state.episodes[0], state), "立意与全集结构");
    assert.equal(scriptAdaptGroupTitle(state.episodes[1], state), "第 1 集");
    assert.deepEqual(state.episodes[0].scenes, []);
    assert.equal(state.sceneCount, 1);
    assert.equal(scriptAdaptRunningText(state), "正在写第 1/2 集");
    // 集号取回合标记里的轮次 ID，不按轮次序号推：第 2 轮写的是第 5 集时就标第 5 集。
    const shifted = parseScriptAdaptStream([sssMarker(1, "concept", "立意与全集结构"), "## 立意页", sssMarker(2, "5", "第 5 集")].join("\n"));
    assert.equal(scriptAdaptGroupTitle(shifted.episodes[1], shifted), "第 5 集");
});

test("立意轮进行中写「立意与全集结构（第 1/N 轮）」；普通流水线照旧按集", () => {
    assert.equal(scriptAdaptRunningText(parseScriptAdaptStream(sssMarker(1, "concept", "立意与全集结构", 4))), "正在写立意与全集结构（第 1/4 轮）");
    assert.equal(scriptAdaptRunningText(parseScriptAdaptStream([marker(1), header(1, "七床空了"), marker(2)].join("\n"))), "正在写第 2/2 集");
    assert.equal(scriptAdaptRunningText(parseScriptAdaptStream("")), "等待模型开始输出");
});

test("失败任务从结果还原时也认得立意轮（轮次带 scene）", () => {
    const result = JSON.stringify({
        dramaRun: {
            total: 3,
            turns: [
                { index: 1, scene: "concept", heading: "立意与全集结构", text: "## 立意页\n一句话。\n## 全集结构表\n表" },
                { index: 2, scene: "1", heading: "第 1 集", text: `${header(1, "雨夜")}\n## 内·雨棚·夜` },
            ],
        },
    });
    const state = scriptAdaptStreamFromResult(result);
    assert.equal(state.concept, true);
    assert.deepEqual(
        state.episodes.map((episode) => scriptAdaptGroupTitle(episode, state)),
        ["立意与全集结构", "第 1 集"],
    );
    // 轮次 ID 读不出时按序号推：有立意轮时减一。
    assert.equal(scriptAdaptEpisodeNo({ index: 3, title: "", attempt: 1, scenes: [], lines: [] }, { concept: true }), 2);
    assert.equal(scriptAdaptEpisodeNo({ index: 3, title: "", attempt: 1, scenes: [], lines: [] }, {}), 3);
});
