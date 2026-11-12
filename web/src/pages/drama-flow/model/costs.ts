import { formatDramaCredits } from "@/lib/drama/drama-format";
import { taskBillingCharge, type GenerationTask } from "@/services/api/task-center";

import type { ReadState } from "./read-state";

/** 一个任务的花费（微积分）：结算后取实扣，没结算取预授权；没有计费单返回 undefined。 */
export function taskCost(task: Pick<GenerationTask, "billing" | "status">): number | undefined {
    if (!task.billing) return undefined;
    if (task.billing.status === "refunded") return 0;
    return taskBillingCharge(task.billing).amountMicrocredits;
}

/** 每一段视频的花费汇总：按任务的 clientContext.shotId 分组；没出过的段不在表里（即暂无消费）。 */
export function shotCosts(tasks: GenerationTask[]): Map<string, CostSummary> {
    const byShot = new Map<string, GenerationTask[]>();
    for (const task of tasks) {
        const shotId = task.clientContext?.shotId;
        if (shotId) byShot.set(shotId, [...(byShot.get(shotId) || []), task]);
    }
    return new Map([...byShot].map(([shotId, list]) => [shotId, costSummary(list)]));
}

const EMPTY_COST: CostSummary = { runs: 0, billed: 0, unbilled: 0, total: 0 };
export const noCost = () => EMPTY_COST;

export type CostSummary = { runs: number; billed: number; unbilled: number; total: number };

/** 一组任务的花费汇总：有计费单的累加（退款算 0），没有计费单的单独计数，不当成 0。 */
export function costSummary(tasks: Array<Pick<GenerationTask, "billing" | "status">>): CostSummary {
    const summary: CostSummary = { runs: tasks.length, billed: 0, unbilled: 0, total: 0 };
    for (const task of tasks) {
        const cost = taskCost(task);
        if (cost === undefined) summary.unbilled += 1;
        else {
            summary.billed += 1;
            summary.total += cost;
        }
    }
    return summary;
}

/**
 * 花费的一句话：读取状态优先（在读「…」、没读到「没读到」，带旧缓存的刷新失败也算没读到）；
 * 读到了空列表是「0 积分 · 暂无消费」；有运行缺计费单时写明合计不完整，不把已知小计当总额。
 */
export function costSummaryText(read: ReadState, summary: CostSummary) {
    if (read !== "ready") return read === "loading" ? "…" : "没读到";
    if (!summary.runs) return "0 积分 · 暂无消费";
    if (!summary.unbilled) return creditsText(summary.total);
    if (!summary.billed) return `${summary.runs} 次运行都没有计费记录`;
    return `已知 ${creditsText(summary.total)}，另有 ${summary.unbilled} 次没有计费记录（合计不完整）`;
}

export function creditsText(microcredits: number | undefined) {
    return microcredits === undefined ? "没读到" : `${formatDramaCredits(microcredits)} 积分`;
}
