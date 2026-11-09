export async function linkSelectedProjectAssets<T>(ids: string[], linkOne: (id: string) => Promise<T>) {
    const uniqueIds = Array.from(new Set(ids.filter(Boolean)));
    if (!uniqueIds.length) throw new Error("请选择要引用的素材");

    const results = await Promise.allSettled(uniqueIds.map(linkOne));
    const linked = results.flatMap((result) => result.status === "fulfilled" ? [result.value] : []);
    const failures = results.flatMap((result, index) => result.status === "rejected" ? [{ id: uniqueIds[index], reason: result.reason instanceof Error ? result.reason.message : "素材引用失败" }] : []);
    if (!linked.length) {
        const firstFailure = failures[0];
        throw new Error(firstFailure?.reason || "素材引用失败，请重试");
    }
    return { linked, failedCount: failures.length, failures };
}
