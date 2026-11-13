import { expect, test } from "bun:test";
import { resolve } from "node:path";

// 真实路由页的挂载、更新、卸载要先装好 DOM，放到独立进程跑，避免影响其他 SSR 用例。
test("短剧新工作流：分镜、定妆、出片详情的 effect 清理，画风锁定、花费、模型名各状态（真实组件）", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/drama-flow-lifecycle.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), timeout: 120000 });
    const output = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
    if (result.exitCode !== 0) throw new Error(output);
    expect(output).toContain("27 pass");
    expect(output).toContain("0 fail");
}, 130000);
