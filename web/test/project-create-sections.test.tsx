import { describe, expect, test } from "bun:test";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import type { ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";

import { importedUnits } from "@/pages/projects/create/source-import-modal";
import { ProjectComposer } from "@/pages/projects/create/project-composer";
import { ProductionSection, StyleSourceSection, VisualTypeCards } from "@/pages/projects/create/composer-sections";
import { composerSectionOfError, ideaWordCount } from "@/pages/projects/create/create-project-flow";
import { PipelineStrip } from "@/pages/projects/pipeline-strip";
import { splitTextIntoChapters } from "@/lib/canvas/canvas-document";
import type { DramaModel } from "@/services/api/drama";
import { ApiError } from "@/services/api/request";
import { sortDramaStyleLibrary, type DramaStyleLibraryItem } from "@/services/api/drama-style-library";

function render(node: ReactElement) {
    return renderToStaticMarkup(
        <MemoryRouter>
            <App>{node}</App>
        </MemoryRouter>,
    );
}

function model(id: string, max: number): DramaModel {
    return { id, label: id, vendor: "", kind: "video", match: [], duration: { min: 4, max, step: 1 }, refs: { images: 9, videos: 3, audios: 3, total: null }, frames_exclusive_with_refs: null, prompt_max_chars: null, resolutions: [], calibrated: false, checked: "" };
}

function libraryItem(patch: Partial<DramaStyleLibraryItem> & { id: string }): DramaStyleLibraryItem {
    return { projectType: "live_action", projectTypeLabel: "仿真人", name: patch.id, description: "", masterPalette: { groups: [] }, colorCount: 0, source: "manual", createdBy: "someone", createdByName: "", updatedBy: "", createdAt: "", updatedAt: "", canEdit: false, ...patch };
}

const noop = () => {};

describe("首页作曲区", () => {
    test("没输入时只有作曲区：输入框、导入文件和「先建一个空项目」，四段还没展开", () => {
        const html = render(
            <QueryClientProvider client={new QueryClient()}>
                <ProjectComposer />
            </QueryClientProvider>,
        );
        expect(html).toContain('aria-label="要拍什么"');
        expect(html).toContain("导入文件");
        expect(html).toContain("先建一个空项目");
        expect(html).not.toContain("画面类型");
        expect(html).not.toContain("开始制作");
    });
});

describe("段 2 · 画面类型", () => {
    test("四张样张卡是 radiogroup，不预选，第一张可以 Tab 进来", () => {
        const html = render(<VisualTypeCards covers={new Map()} onChange={noop} />);
        expect(html).toContain('role="radiogroup"');
        expect(html).toContain('aria-label="画面类型"');
        expect(html.match(/role="radio"/g)?.length).toBe(4);
        expect(html).not.toContain('aria-checked="true"');
        for (const label of ["仿真人", "写实 CG", "3D 动漫", "2D 动漫"]) expect(html).toContain(label);
        expect(html.match(/tabindex="0"/g)?.length).toBe(1);
    });
});

describe("段 3 · 画风从哪来", () => {
    test("默认交给导演分析，两样都可以不填", () => {
        const html = render(<StyleSourceSection mode="director" brief="" images={[]} items={[]} loading={false} onMode={noop} onBrief={noop} onImages={noop} onStyle={noop} />);
        expect(html).toContain("交给导演分析");
        expect(html).toContain("都不填就让导演按故事自己定");
        expect(html).not.toContain("选中即锁定");
        expect(html).not.toContain("项目画风");
    });

    test("风格中心空的时候给按钮切回导演分析", () => {
        const html = render(<StyleSourceSection mode="library" visualType="anime_2d" brief="" images={[]} items={[]} loading={false} onMode={noop} onBrief={noop} onImages={noop} onStyle={noop} />);
        expect(html).toContain("改为交给导演分析");
    });

    test("来自导演分析的、本人存的排在前面", () => {
        const items = [libraryItem({ id: "a" }), libraryItem({ id: "b", source: "director" }), libraryItem({ id: "c", createdBy: "me" }), libraryItem({ id: "d" })];
        expect(sortDramaStyleLibrary(items, "me").map((item) => item.id)).toEqual(["b", "c", "a", "d"]);
    });
});

describe("段 4 · 怎么做", () => {
    const models = [model("seedance-2.0", 15), model("seedance-2.5", 30)];

    test("没有视频渠道也能建项，顶部有提醒；生成模式三档，出视频必停", () => {
        const html = render(<ProductionSection aspectRatio="9:16" segmentSeconds={15} models={models} recommendedModel="seedance-2.0" model={models[0]} families={new Set()} runMode="until_video" onAspectRatio={noop} onSegmentSeconds={noop} onModel={noop} onRunMode={noop} />);
        expect(html).toContain("还没有视频渠道，可以先建项目，出视频前要配好");
        expect(html).toContain("建项后不可改");
        for (const label of ["跑到出视频前", "资产我要过目", "每步都问我"]) expect(html).toContain(label);
        expect(html).toContain("出视频要花钱，任何模式都会在这里停下，带上预估花费等你点。");
        expect(html).toContain("推荐");
        expect(html).not.toContain("未校准");
        expect(html).not.toContain("授权档位");
    });

    test("30 秒配一段最长 15 秒的模型：原地提示换成哪个，不悄悄换", () => {
        const html = render(<ProductionSection aspectRatio="9:16" segmentSeconds={30} models={models} recommendedModel="seedance-2.0" model={models[0]} families={new Set(["seedance-2.0", "seedance-2.5"])} runMode="until_video" onAspectRatio={noop} onSegmentSeconds={noop} onModel={noop} onRunMode={noop} />);
        expect(html).toContain("seedance-2.0 最长 15 秒");
        expect(html).toContain("换成 seedance-2.5？");
        expect(html).toMatch(/aria-checked="true"[^>]*>[\s\S]*?seedance-2\.0/);
    });
});

describe("流水线条", () => {
    test("按生成模式标出停靠点", () => {
        const html = render(<PipelineStrip stops={["assets", "shots", "video"]} />);
        expect(html.match(/is-stop/g)?.length).toBe(3);
        expect(html).toContain("拆剧本");
        expect(html).toContain("剪辑");
    });
});

describe("建项流程", () => {
    test("后端按段给的 reason 落到对应的段", () => {
        const error = (reason: string) => new ApiError("x", { status: 400, code: 400, reason });
        expect(composerSectionOfError(error("project_visual_type_required"))).toBe("visual");
        expect(composerSectionOfError(error("project_source_invalid"))).toBe("source");
        expect(composerSectionOfError(error("project_style_invalid"))).toBe("style");
        expect(composerSectionOfError(error("project_production_invalid"))).toBe("production");
        expect(composerSectionOfError(error("internal"))).toBeUndefined();
    });

    test("导入切分和章节页导入是同一套解析", () => {
        const text = "第一章 雨夜\n正文一\n第二章 灯塔\n正文二";
        expect(importedUnits(text, "novel", true)).toEqual(splitTextIntoChapters(text));
        expect(importedUnits("林小满：七床？", "script", true)[0].title).toBe("第 1 集");
        expect(importedUnits(text, "novel", false)).toHaveLength(1);
    });

    test("创意每集字数按时长折", () => {
        expect(ideaWordCount(60)).toBe("800");
        expect(ideaWordCount(30)).toBe("400");
    });
});
