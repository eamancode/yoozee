import { expect, test } from "bun:test";
import { App } from "antd";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { ProductionSection } from "@/pages/projects/workbench/create/composer-sections";
import { EMPTY_PROJECT_DRAFT } from "@/pages/projects/workbench/create/project-draft";
import { DEFAULT_PROJECT_WORKFLOW, hasDirectorDocuments, projectWorkflow } from "@/lib/project-workflow";
import type { ProjectAssetWorkflow } from "@/services/api/projects";

function production(workflowId: ProjectAssetWorkflow) {
    return renderToStaticMarkup(
        <MemoryRouter>
            <App>
                <ProductionSection
                    workflowId={workflowId}
                    onWorkflowId={() => {}}
                    whiteModelStoryboardEnabled={false}
                    onWhiteModelStoryboardEnabled={() => {}}
                    aspectRatio="9:16"
                    segmentSeconds={15}
                    models={[]}
                    recommendedModel=""
                    runMode="until_video"
                    onAspectRatio={() => {}}
                    onSegmentSeconds={() => {}}
                    onModel={() => {}}
                    onRunMode={() => {}}
                />
            </App>
        </MemoryRouter>,
    );
}

test("全新建项默认流水线二 SSS 短剧，白模与生成模式保持独立", () => {
    expect(EMPTY_PROJECT_DRAFT.workflowId).toBe("pipeline-2");
    expect(DEFAULT_PROJECT_WORKFLOW).toBe("pipeline-2");
    expect(EMPTY_PROJECT_DRAFT.whiteModelStoryboardEnabled).toBe(false);
    expect(EMPTY_PROJECT_DRAFT.runMode).toBe("until_video");
});

test("工作流选择位于每段时长与视频模型之间，说明五条流水线与两种图片规格", () => {
    const html = production("pipeline-1");
    expect(html.indexOf('aria-label="工作流"')).toBeGreaterThan(html.indexOf('aria-label="每段时长"'));
    expect(html.indexOf('aria-label="工作流"')).toBeLessThan(html.indexOf('aria-label="视频模型"'));
    expect(html).toContain("角色四宫格");
    expect(html).toContain("微缩场景图");
    expect(html).toContain("角色三视图");
    expect(html).toContain("流水线一 SSS 电影导演");
    expect(html).toContain("流水线二 SSS 短剧导演 · 默认");
    expect(html).toContain("流水线三 本体论导演");
    expect(html).toContain("流水线四 资产一体化");
    expect(html).toContain("流水线五 原流程");
    expect(html).toContain("（在这里停下等你点）");
});

test("流水线切换只改变选中状态，原生成模式与白模选项保留", () => {
    for (const [id, label] of [["pipeline-1", "流水线一 SSS 电影导演"], ["pipeline-2", "流水线二 SSS 短剧导演"], ["pipeline-3", "流水线三 本体论导演"], ["pipeline-4", "流水线四 资产一体化"], ["pipeline-5", "流水线五 原流程"]] as const) {
        const html = production(id);
        const workflowGroup = html.split('aria-label="工作流"')[1]?.split("</div>")[0] || "";
        const selected = workflowGroup.match(/aria-checked="true"[\s\S]*?<strong>([^<]+)/)?.[1];
        expect(selected).toStartWith(label);
        expect(html).toContain("流水线一");
        expect(html).toContain("流水线二");
        expect(html).toContain("流水线三");
        expect(html).toContain("流水线五");
        expect(html).toContain("白模分镜");
        expect(html).toContain("跑到出视频前");
        expect(html).toContain("资产我要过目");
    }
});

test("顺延后的资产版式维持原行为，SSS 两条流水线复用一体化资产", () => {
    for (const id of ["pipeline-1", "pipeline-2", "pipeline-3", "pipeline-4"] as const) expect(projectWorkflow(id).integratedAssets).toBe(true);
    expect(projectWorkflow("pipeline-5").integratedAssets).toBe(false);
    expect(projectWorkflow(undefined).value).toBe("pipeline-5");
    expect(projectWorkflow("pipeline-1").aspectRatio).toBe("16:9");
    expect(projectWorkflow("pipeline-2").aspectRatio).toBe("9:16");
    expect(projectWorkflow("pipeline-1").storyboardMode).toBe("sss");
    expect(projectWorkflow("pipeline-3").storyboardMode).toBe("ontology");
    expect([1, 2, 3, 4, 5].map((n) => hasDirectorDocuments(`pipeline-${n}` as ProjectAssetWorkflow))).toEqual([true, true, true, false, false]);
});
