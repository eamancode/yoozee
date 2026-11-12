/**
 * 把一小段时间内各张卡片要的项目进度攒成一批读：卡片各自调用 load(id)，
 * 批量接口一次最多 50 个，超出的拆成几批；接口说「没有」（missing）的 id 按失败处理，不拿空数据顶替。
 */
export type SummaryFetcher<T> = (ids: string[]) => Promise<{ summaries: Record<string, T>; missing: string[] }>;

export const SUMMARY_BATCH_LIMIT = 50;

export function createSummaryBatcher<T>(fetcher: SummaryFetcher<T>, delayMs = 150) {
    let queue = new Map<string, Array<{ resolve: (value: T) => void; reject: (error: unknown) => void }>>();
    let timer: ReturnType<typeof setTimeout> | undefined;

    const flush = () => {
        timer = undefined;
        const batch = queue;
        queue = new Map();
        const ids = [...batch.keys()];
        for (let start = 0; start < ids.length; start += SUMMARY_BATCH_LIMIT) {
            const chunk = ids.slice(start, start + SUMMARY_BATCH_LIMIT);
            fetcher(chunk).then(
                (result) => {
                    for (const id of chunk) {
                        const found = Object.prototype.hasOwnProperty.call(result.summaries, id);
                        const summary = result.summaries[id];
                        for (const waiter of batch.get(id) || []) {
                            if (found) waiter.resolve(summary);
                            else waiter.reject(new Error("这个项目的进度没读到"));
                        }
                    }
                },
                (error) => {
                    for (const id of chunk) for (const waiter of batch.get(id) || []) waiter.reject(error);
                },
            );
        }
    };

    return (id: string) =>
        new Promise<T>((resolve, reject) => {
            queue.set(id, [...(queue.get(id) || []), { resolve, reject }]);
            timer ??= setTimeout(flush, delayMs);
        });
}
