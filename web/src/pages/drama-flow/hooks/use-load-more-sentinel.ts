import { useCallback, useEffect, useRef, useState } from "react";

/**
 * 「加载更多」哨兵：把返回的 ref 挂到列表底部的节点上，节点进入视野就加载下一页。
 * 用回调 ref 记住当前节点：筛选切换时结果区会先换成加载态再换回来，哨兵是新的 DOM 节点，
 * 节点一换就重新观察；只读 ref.current 的写法会一直盯着已经卸掉的旧节点。
 */
export function useLoadMoreSentinel({ enabled, onLoadMore, rootMargin = "600px" }: { enabled: boolean; onLoadMore: () => unknown; rootMargin?: string }) {
    const [node, setNode] = useState<HTMLElement | null>(null);
    const loadMore = useRef(onLoadMore);
    useEffect(() => {
        loadMore.current = onLoadMore;
    }, [onLoadMore]);

    useEffect(() => {
        if (!node || !enabled) return;
        const observer = new IntersectionObserver(
            (entries) => {
                if (entries.some((entry) => entry.isIntersecting)) void loadMore.current();
            },
            { rootMargin },
        );
        observer.observe(node);
        return () => observer.disconnect();
    }, [node, enabled, rootMargin]);

    return useCallback((next: HTMLElement | null) => setNode(next), []);
}
