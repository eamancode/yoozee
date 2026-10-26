import assert from "node:assert/strict";
import test from "node:test";

import { episodeSourcesComment, withEpisodeSourcesComment } from "./episode-sources";
import { parseScriptAdaptStream, scriptAdaptStreamFromResult } from "./script-adapt-stream";

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
