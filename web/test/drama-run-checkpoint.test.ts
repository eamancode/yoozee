import { expect, test } from "bun:test";
import { dramaRunCheckpoint } from "@/pages/projects/workbench/detail/drama-run-checkpoint";
import type { GenerationTask } from "@/services/api/task-center";

function task(id: string, parent?: string, completed = 0, projectId = "project-a", runId = "run-a") {
    return {
        id,
        projectId,
        status: "failed",
        inputJson: JSON.stringify({ metadata: { dramaRun: { runId, step: "assets.extract", unitId: "unit-a", resumeTaskId: parent } } }),
        resultJson: completed ? JSON.stringify({ dramaRun: { runId, total: 7, completed, turns: [] } }) : "",
    } as GenerationTask;
}

test("first resumed request failure keeps the original completed-turn checkpoint", async () => {
    const original = task("original", undefined, 3);
    const resume = task("resume", original.id);
    const loaded: string[] = [];
    expect(
        await dramaRunCheckpoint(resume, async (id) => {
            loaded.push(id);
            return original;
        }),
    ).toBe(original);
    expect(loaded).toEqual([original.id]);
});

test("a newer checkpoint takes precedence without another lookup", async () => {
    const latest = task("latest", "old", 4);
    expect(
        await dramaRunCheckpoint(latest, async () => {
            throw Error("must not fetch");
        }),
    ).toBe(latest);
});

test("checkpoint lookup refuses other projects, runs, and cyclic history", async () => {
    const current = task("current", "previous");
    for (const previous of [task("previous", undefined, 3, "project-b"), task("previous", undefined, 3, "project-a", "run-b")]) {
        expect(await dramaRunCheckpoint(current, async () => previous)).toBe(current);
    }
    let calls = 0;
    expect(
        await dramaRunCheckpoint(current, async (id) => {
            calls++;
            return id === "previous" ? task("previous", "current") : current;
        }),
    ).toBe(current);
    expect(calls).toBe(2);
});
