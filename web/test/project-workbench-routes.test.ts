import { expect, test } from "bun:test";
import { matchRoutes } from "react-router";
import { projectWorkbenchRoutePaths } from "../src/lib/project-workbench-routes";

const routes = projectWorkbenchRoutePaths.map((path) => ({ path }));

test("项目工作台的导航地址命中详情页并保留所需参数", () => {
    for (const [url, params] of [
        ["/projects/project-1", { projectId: "project-1" }],
        ["/projects/project-1/overview", { projectId: "project-1", view: "overview" }],
        ["/projects/project-1/assets", { projectId: "project-1", view: "assets" }],
        ["/projects/project-1/chapters/unit-1", { projectId: "project-1", chapterId: "unit-1" }],
        ["/projects/project-1/workflow/unit-1/video", { projectId: "project-1", unitId: "unit-1", stage: "video" }],
    ] as const) {
        const matches = matchRoutes(routes, url);
        expect(matches?.at(-1)?.params).toEqual(params);
    }
    expect(matchRoutes(routes, "/projects/project-1/overview/unexpected")).toBeNull();
});
