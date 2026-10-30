import { describe, expect, test } from "bun:test";

import { DEFAULT_DRAMA_MODEL, DRAMA_PIPELINE_STAGES, DRAMA_RUN_MODES, dramaModelOptions, dramaRunModeLabel, findDramaModel } from "@/pages/projects/drama-model";
import type { DramaModel, DramaRegistry } from "@/services/api/drama";

function model(patch: Partial<DramaModel> & { id: string }): DramaModel {
    return {
        label: patch.id,
        vendor: "ByteDance",
        kind: "video",
        duration: { min: 4, max: 15, step: 1 },
        refs: { images: 9, videos: 3, audios: 3, total: null },
        frames_exclusive_with_refs: null,
        prompt_max_chars: null,
        resolutions: [],
        calibrated: true,
        checked: "2026-10-15",
        ...patch,
    } as DramaModel;
}

const registry: DramaRegistry = {
    version: "2026-10-15",
    default: "seedance-2.0",
    models: [
        model({ id: "seedance-2.0", label: "Seedance 2.0", duration: { min: 4, max: 15, step: 1 }, calibrated: false }),
        model({ id: "seedance-2.5", label: "Seedance 2.5", duration: { min: 4, max: 30, step: 1 }, calibrated: false }),
        model({ id: "minimax-h3", label: "MiniMax-H3", vendor: "MiniMax", duration: { min: 4, max: 15, step: 1 }, calibrated: true }),
    ],
};

describe("短剧生成模型选项", () => {
    test("每项都带「每段最长 N 秒」，不在主标签里写「未校准」；不传过滤条件时全部可选", () => {
        expect(dramaModelOptions(registry)).toEqual([
            { label: "Seedance 2.0 · 每段最长 15 秒", value: "seedance-2.0", disabled: false },
            { label: "Seedance 2.5 · 每段最长 30 秒", value: "seedance-2.5", disabled: false },
            { label: "MiniMax-H3 · 每段最长 15 秒", value: "minimax-h3", disabled: false },
        ]);
    });

    test("目录还没读到时退回默认模型，且仍然给出 15 秒上限", () => {
        for (const empty of [undefined, { version: "", default: "", models: [] } as DramaRegistry]) {
            expect(dramaModelOptions(empty)).toEqual([{ label: "Seedance 2.0 · 每段最长 15 秒", value: DEFAULT_DRAMA_MODEL, disabled: false }]);
        }
        expect(DEFAULT_DRAMA_MODEL).toBe("seedance-2.0");
    });

    test("单段最长时长只留一段上限够得着的模型", () => {
        expect(dramaModelOptions(registry, undefined, { minSeconds: 30 }).map((item) => item.value)).toEqual(["seedance-2.5"]);
        expect(dramaModelOptions(registry, undefined, { minSeconds: 15 }).map((item) => item.value)).toEqual(["seedance-2.0", "seedance-2.5", "minimax-h3"]);
    });

    test("渠道不支持所选画幅的模型置灰并注明；没有渠道的模型不判断画幅", () => {
        const ratios = new Map([
            ["seedance-2.0", new Set(["16:9", "9:16", "1:1"])],
            ["seedance-2.5", new Set(["16:9", "9:16", "1:1", "4:3"])],
        ]);
        const available = new Set(["seedance-2.0", "seedance-2.5"]);
        expect(dramaModelOptions(registry, available, { aspectRatio: "4:3", ratios })).toEqual([
            { label: "Seedance 2.0 · 每段最长 15 秒 · 渠道不支持 4:3", value: "seedance-2.0", disabled: true },
            { label: "Seedance 2.5 · 每段最长 30 秒", value: "seedance-2.5", disabled: false },
            { label: "MiniMax-H3 · 每段最长 15 秒 · 没有可用渠道", value: "minimax-h3", disabled: false },
        ]);
        // 不带画幅时不置灰。
        expect(dramaModelOptions(registry, available, { ratios }).every((item) => !item.disabled)).toBe(true);
    });
});

describe("按 ID 解析生成模型", () => {
    test("命中时返回该模型", () => {
        expect(findDramaModel(registry, "seedance-2.5")?.duration.max).toBe(30);
        expect(findDramaModel(registry, "minimax-h3")?.label).toBe("MiniMax-H3");
    });

    test("未指定或未知 ID 时回退到目录默认模型", () => {
        expect(findDramaModel(registry, undefined)?.id).toBe("seedance-2.0");
        expect(findDramaModel(registry, "")?.id).toBe("seedance-2.0");
        expect(findDramaModel(registry, "kling")?.id).toBe("seedance-2.0");
    });

    test("目录缺失时不猜模型", () => {
        expect(findDramaModel(undefined, "seedance-2.0")).toBeUndefined();
    });
});

describe("生成模式", () => {
    test("三档，默认跑到出视频前；每一档都停在出视频", () => {
        expect(DRAMA_RUN_MODES.map((item) => item.value)).toEqual(["until_video", "review_from_assets", "every_step"]);
        expect(DRAMA_RUN_MODES.every((item) => item.label && item.hint && item.stops.includes("video"))).toBe(true);
        expect(DRAMA_RUN_MODES.find((item) => item.value === "review_from_assets")?.stops).toEqual(["assets", "shots", "video"]);
    });

    test("标签映射：不认识的按默认档显示", () => {
        expect(dramaRunModeLabel("every_step")).toBe("每步都问我");
        expect(dramaRunModeLabel(undefined)).toBe("跑到出视频前");
        expect(dramaRunModeLabel("full")).toBe("跑到出视频前");
    });

    test("流水线八格与后端同序", () => {
        expect(DRAMA_PIPELINE_STAGES.map((item) => item.label).join(" → ")).toBe("拆剧本 → 导演定画风 → 场次色卡 → 角色场景资产 → 分镜 → 装段 → 出视频 → 剪辑");
    });
});
