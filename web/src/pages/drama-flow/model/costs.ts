import { formatDramaCredits } from "@/lib/drama/drama-format";
import { taskBillingCharge, type GenerationTask } from "@/services/api/task-center";

/** 一个任务的花费（微积分）：结算后取实扣，没结算取预授权；没有计费单返回 undefined。 */
export function taskCost(task: Pick<GenerationTask, "billing" | "status">): number | undefined {
    if (!task.billing) return undefined;
    if (task.billing.status === "refunded") return 0;
    return taskBillingCharge(task.billing).amountMicrocredits;
}

/** 每一段视频的花费：按任务的 clientContext.shotId 累加，退款的不算。 */
export function shotCosts(tasks: GenerationTask[]): Map<string, number> {
    const costs = new Map<string, number>();
    for (const task of tasks) {
        const shotId = task.clientContext?.shotId;
        const cost = taskCost(task);
        if (!shotId || cost === undefined) continue;
        costs.set(shotId, (costs.get(shotId) ?? 0) + cost);
    }
    return costs;
}

/** 一组任务的花费合计；一个计费单都没有时返回 undefined（页面写「没读到」而不是 0）。 */
export function totalCost(tasks: Array<Pick<GenerationTask, "billing" | "status">>): number | undefined {
    let total: number | undefined;
    for (const task of tasks) {
        const cost = taskCost(task);
        if (cost === undefined) continue;
        total = (total ?? 0) + cost;
    }
    return total;
}

export function creditsText(microcredits: number | undefined) {
    return microcredits === undefined ? "没读到" : `${formatDramaCredits(microcredits)} 积分`;
}
