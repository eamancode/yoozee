import { expect, test } from "bun:test";
import { App } from "antd";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { ProductionSection } from "@/pages/projects/workbench/create/composer-sections";
import { EMPTY_PROJECT_DRAFT } from "@/pages/projects/workbench/create/project-draft";

function production(workflowId: "pipeline-1" | "pipeline-2") {
    return renderToStaticMarkup(<MemoryRouter><App><ProductionSection workflowId={workflowId} onWorkflowId={() => {}} whiteModelStoryboardEnabled={false} onWhiteModelStoryboardEnabled={() => {}} aspectRatio="9:16" segmentSeconds={15} models={[]} recommendedModel="" runMode="until_video" onAspectRatio={() => {}} onSegmentSeconds={() => {}} onModel={() => {}} onRunMode={() => {}} /></App></MemoryRouter>);
}

test("全新建项默认流水线一，白模与生成模式保持独立", () => {
    expect(EMPTY_PROJECT_DRAFT.workflowId).toBe("pipeline-1");
    expect(EMPTY_PROJECT_DRAFT.whiteModelStoryboardEnabled).toBe(false);
    expect(EMPTY_PROJECT_DRAFT.runMode).toBe("until_video");
});

test("工作流选择位于每段时长与视频模型之间，准确说明两种图片规格", () => {
    const html = production("pipeline-1");
    expect(html.indexOf('aria-label="工作流"')).toBeGreaterThan(html.indexOf('aria-label="每段时长"'));
    expect(html.indexOf('aria-label="工作流"')).toBeLessThan(html.indexOf('aria-label="视频模型"'));
    expect(html).toContain("角色四宫格");
    expect(html).toContain("微缩场景图");
    expect(html).toContain("角色三视图");
    expect(html).toContain("（在这里停下等你点）");
});

test("流水线切换只改变选中状态，原生成模式与白模选项保留", () => {
    for (const id of ["pipeline-1", "pipeline-2"] as const) {
        const html = production(id);
        const workflowGroup = html.split('aria-label="工作流"')[1]?.split("</div>")[0] || "";
        const selected = workflowGroup.match(/aria-checked="true"[\s\S]*?<strong>([^<]+)/)?.[1];
        expect(selected).toStartWith(id === "pipeline-1" ? "流水线一" : "流水线二");
        expect(html).toContain("流水线一");
        expect(html).toContain("流水线二");
        expect(html).toContain("白模分镜");
        expect(html).toContain("跑到出视频前");
        expect(html).toContain("资产我要过目");
    }
});
