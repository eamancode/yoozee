import assert from "node:assert/strict";
import test from "node:test";

import type { DramaShot, DramaShotIssue } from "@/services/api/drama";

import { issueFixLabel, issueFixShots, issueScenes, scriptLineOptions, shotIssues } from "./shot-issues";

const shots: DramaShot[] = [
    { id: "E01S01-04", scene: "E01S01", lines: [{ line_id: "E01S01L04" }] },
    { id: "E01S02-05", scene: "E01S02", lines: [{ line_id: "E01S02L02", delivery: "压低声音追问" }] },
    { id: "E01S02-06", scene: "E01S02", lines: [{ line_id: "E01S02L02" }] },
];
const missing: DramaShotIssue = { level: "error", rule: "R-SHOT-001", message: "台词 E01S01L05 没有落在任何镜头：「七床的人呢？」", lineId: "E01S01L05", scene: "E01S01", shots: ["E01S01-04"], fix: "add_line" };
const twice: DramaShotIssue = { level: "error", rule: "R-SHOT-001", message: "台词 E01S02L02 落在 2 个镜头", shotId: "E01S02-06", lineId: "E01S02L02", scene: "E01S02", shots: ["E01S02-05", "E01S02-06"], fix: "keep_line" };
const arc: DramaShotIssue = { level: "error", rule: "R-SHOT-025", message: "E01S02-05 情绪弧 arc-x 不在词库里", shotId: "E01S02-05", scene: "E01S02", shots: ["E01S02-05"] };

test("issueScenes 按出现顺序去重，整章级问题不算", () => {
    assert.deepEqual(issueScenes([twice, missing, arc, { level: "error", rule: "R-SHOT-015", message: "伏笔" }]), ["E01S02", "E01S01"]);
});

test("shotIssues 取挂在这一镜上的问题；落在两镜的两镜都有", () => {
    assert.deepEqual(shotIssues([missing, twice, arc], "E01S02-05"), [twice, arc]);
    assert.deepEqual(shotIssues([missing, twice, arc], "E01S02-06"), [twice]);
    assert.deepEqual(shotIssues([missing], undefined), []);
});

test("补进这个镜头：把这句加到这一镜，已经在就不改", () => {
    assert.deepEqual(issueFixShots(shots, missing, "E01S01-04"), [{ id: "E01S01-04", scene: "E01S01", lines: [{ line_id: "E01S01L04" }, { line_id: "E01S01L05" }] }]);
    assert.deepEqual(issueFixShots(shots, { ...missing, lineId: "E01S01L04" }, "E01S01-04"), []);
    assert.equal(issueFixLabel(missing), "补进这个镜头");
});

test("只保留在这个镜头：别的镜头去掉这句，空了就去掉 lines；没有快捷修复的问题不改", () => {
    assert.deepEqual(issueFixShots(shots, twice, "E01S02-05"), [{ id: "E01S02-06", scene: "E01S02" }]);
    assert.deepEqual(issueFixShots(shots, twice, "E01S02-06"), [{ id: "E01S02-05", scene: "E01S02" }]);
    assert.deepEqual(issueFixShots(shots, arc, "E01S02-05"), []);
    assert.equal(issueFixLabel(arc), undefined);
});

test("scriptLineOptions 只取台词节拍，画外音标出来", () => {
    const options = scriptLineOptions({
        scenes: [
            {
                id: "E01S01",
                heading: "内·病房走廊·夜",
                beats: [
                    { id: "E01S01A01", kind: "action", text: "推门" },
                    { id: "E01S01L01", kind: "line", speaker: "林小满", text: "七床？" },
                    { id: "E01S01L02", kind: "vo", speaker: "周岸", text: "别回头。" },
                ],
            },
        ],
    });
    assert.deepEqual(options, [
        { id: "E01S01L01", scene: "E01S01", speaker: "林小满", text: "七床？", vo: false },
        { id: "E01S01L02", scene: "E01S01", speaker: "周岸", text: "别回头。", vo: true },
    ]);
});
