/** 一个数据源现在能不能拿来算数：读到了、还在读、没读到（含带旧缓存的刷新失败）。 */
export type ReadState = "ready" | "loading" | "error";

type QueryLike = { isError: boolean; data?: unknown };

export function queryReadState(query: QueryLike): ReadState {
    if (query.isError) return "error";
    return query.data === undefined ? "loading" : "ready";
}

/** 多个数据源合在一起：有一个没读到就是没读到，有一个还在读就是还在读。 */
export function combineReadStates(states: ReadState[]): ReadState {
    if (states.includes("error")) return "error";
    return states.includes("loading") ? "loading" : "ready";
}

/**
 * 某一集的出片状态（生成中、排队、失败）能不能算：先要进度读到（段从进度来），
 * 再要这一集的出片链读到。进度里这一集没有段时不用读链，算读到。
 */
export function videoStateRead(progress: ReadState, chain: ReadState | undefined): ReadState {
    if (progress !== "ready") return progress;
    return chain ?? "ready";
}

/** 读到了显示数值；还在读显示「…」；没读到写「没读到」，不拿 0 顶替。 */
export function readValue<T>(state: ReadState, value: T): T | string {
    if (state === "ready") return value;
    return state === "loading" ? "…" : "没读到";
}
