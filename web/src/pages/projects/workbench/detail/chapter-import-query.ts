/** 章节页地址带 `?import=1` 时一进来就打开「导入小说」（建项目选了导入小说后跳过来）。 */
export const CHAPTER_IMPORT_QUERY = "import";

/**
 * 读到参数后把它从地址栏去掉，刷新或返回时不会再弹出导入框。
 * 去掉放在微任务里：同一次渲染里重复调用（StrictMode 会调两次 useState 初始化）读到的结果一致；
 * 用 history.replaceState 保留路由自己的 state，不触发一次额外的导航。
 */
export function consumeChapterImportQuery() {
    if (typeof window === "undefined") return false;
    if (new URL(window.location.href).searchParams.get(CHAPTER_IMPORT_QUERY) !== "1") return false;
    queueMicrotask(() => {
        const url = new URL(window.location.href);
        if (!url.searchParams.has(CHAPTER_IMPORT_QUERY)) return;
        url.searchParams.delete(CHAPTER_IMPORT_QUERY);
        window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
    });
    return true;
}
