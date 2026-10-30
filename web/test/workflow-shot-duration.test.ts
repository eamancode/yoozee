import { describe, expect, test } from "bun:test";

import type { VideoCapabilityConfig } from "../src/lib/model-capabilities";
import { generationErrorMessage } from "../src/lib/generation-error";
import { shotTaskRunningLabel, shotVideoSeconds } from "../src/pages/projects/workbench/detail/shot-generation";

function profile(duration: VideoCapabilityConfig["duration"]) {
    return { duration } as VideoCapabilityConfig;
}

const workbenchSource = () => Bun.file(new URL("../src/pages/projects/workbench/detail/workflow-production-workbench.tsx", import.meta.url)).text();

describe("镜头视频段时长", () => {
    test("默认取镜头时长，不退回渠道默认的 15 秒", () => {
        const range = profile({ selection: "range", min: 4, max: 15, step: 1, default: 15 });
        expect(shotVideoSeconds({ shotSeconds: 5, profile: range })).toBe(5);
        expect(shotVideoSeconds({ shotSeconds: 4.8, profile: range })).toBe(5);
        expect(shotVideoSeconds({ shotSeconds: 2, profile: range })).toBe(4);
    });

    test("档位制模型取不短于镜头的最近一档，而不是默认档", () => {
        const enumProfile = profile({ selection: "enum", values: [15, 5, 10], default: 15 });
        expect(shotVideoSeconds({ shotSeconds: 5, profile: enumProfile })).toBe(5);
        expect(shotVideoSeconds({ shotSeconds: 5.5, profile: enumProfile })).toBe(10);
        expect(shotVideoSeconds({ shotSeconds: 30, profile: enumProfile })).toBe(15);
    });

    test("受项目生成模型一段上限约束，分段表的 dur 不被档位改写", () => {
        const range = profile({ selection: "range", min: 4, max: 15, step: 1, default: 15 });
        const enumProfile = profile({ selection: "enum", values: [5, 10, 15], default: 15 });
        expect(shotVideoSeconds({ shotSeconds: 14, profile: range, segmentMaxSeconds: 12 })).toBe(12);
        expect(shotVideoSeconds({ shotSeconds: 12, profile: enumProfile, segmentMaxSeconds: 12 })).toBe(10);
        expect(shotVideoSeconds({ specSeconds: 7, shotSeconds: 5, profile: enumProfile })).toBe(7);
    });

    test("工作台不再用渠道默认时长覆盖段时长", async () => {
        const source = await workbenchSource();
        expect(source).not.toContain("seconds: effectiveConfig.videoSeconds");
        expect(source).toContain("shotVideoSeconds({ specSeconds, shotSeconds: shotDurationSeconds");
    });
});

describe("镜头生成状态", () => {
    test("排队中的任务不显示「已运行」计时", () => {
        const now = Date.parse("2026-10-19T10:01:05Z");
        expect(shotTaskRunningLabel({ status: "queued", createdAt: "2026-10-19T09:50:00Z" }, now)).toBe("排队中");
        expect(shotTaskRunningLabel({ status: "running", createdAt: "2026-10-19T10:00:30Z" }, now)).toBe("已运行0分钟35秒");
        expect(shotTaskRunningLabel({ status: "running", startedAt: "2026-10-19T10:00:00Z" }, now)).toBe("已运行1分钟05秒");
    });

    test("章节分镜按钮排队时显示排队中，打开对话框不再清空技能", async () => {
        const source = await Bun.file(new URL("../src/pages/projects/workbench/detail/chapters.tsx", import.meta.url)).text();
        expect(source).toContain('return operation.queued ? "排队中" : `已运行${formatOperationElapsed(operation.startedAt, now)}`;');
        expect(source).not.toContain("setSelectedSkillIds([])");
    });

    test("失败时在产物区显示上游原因", async () => {
        const source = await workbenchSource();
        expect(source).toContain("refreshGenerationTaskStatus(shotTask!.id, { signal })");
        expect(source).toContain("generationErrorMessage(failedTaskQuery.data.error)");
        expect(generationErrorMessage("模型服务暂时不可用（HTTP 503）；上游：No available channel for model seedance under group default")).toBe("模型服务暂时不可用（HTTP 503）；上游：No available channel for model seedance under group default");
    });

    test("窄窗口也能从「镜头资产」框打开资产选择器", async () => {
        const source = await workbenchSource();
        expect(source).not.toContain("从左侧资产栏点击绑定");
        expect(source).toContain("onPick={() => setAssetPickerOpen(true)}");
        expect(source).toContain('title="绑定镜头资产"');
    });
});
