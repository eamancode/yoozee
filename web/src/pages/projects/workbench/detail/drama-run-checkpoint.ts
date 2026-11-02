import { dramaRunProgressOf } from "@/services/api/project-drama";
import type { GenerationTask } from "@/services/api/task-center";

function runMeta(task: GenerationTask) {
    try {
        return JSON.parse(task.inputJson || "{}").metadata?.dramaRun as { runId?: string; step?: string; unitId?: string; resumeTaskId?: string } | undefined;
    } catch {
        return undefined;
    }
}

export function checkpointHistoryCandidates(tasks: GenerationTask[], projectId: string, unitId: string, step: string) {
    return tasks.filter((task) => {
        const meta = runMeta(task);
        const operation = step === "assets.extract" ? "assets" : step === "chapter.shots" ? "storyboard" : undefined;
        const sameStep = meta ? meta.unitId === unitId && meta.step === step : Boolean(operation && task.clientContext?.chapterId === unitId && task.clientContext.chapterOperation === operation);
        return task.projectId === projectId && sameStep && (task.status === "failed" || task.status === "cancelled");
    });
}

/** A failed resume may inherit completed turns without having written a result of its own. */
export async function dramaRunCheckpoint(task: GenerationTask, load: (id: string) => Promise<GenerationTask>): Promise<GenerationTask> {
    const origin = runMeta(task);
    const seen = new Set<string>();
    let current = task;
    while (seen.size < 16 && !seen.has(current.id)) {
        seen.add(current.id);
        if (dramaRunProgressOf(current)?.completed) return current;
        const meta = runMeta(current);
        if (!meta?.resumeTaskId || !origin?.runId) break;
        const previous = await load(meta.resumeTaskId);
        const previousMeta = runMeta(previous);
        if (previous.projectId !== task.projectId || previousMeta?.runId !== origin.runId || previousMeta?.step !== origin.step || previousMeta?.unitId !== origin.unitId) break;
        current = previous;
    }
    return task;
}
