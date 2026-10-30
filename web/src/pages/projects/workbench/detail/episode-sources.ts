/**
 * 剧集（kind=episode）正文开头的来源注释 `<!-- sources: unitId:from-to;… -->`：剧本优化写入，是多章合一集、一章拆多集时来源
 * 唯一可回溯的位置（不加表不加列）。编辑器（TipTap）不显示注释，保存时要原样带回，否则来源就丢了。
 */

const SOURCES_COMMENT = /^\s*<!--\s*sources:[^>]*?-->/;

/** 正文开头的来源注释（没有返回空串）。 */
export function episodeSourcesComment(sourceText: string) {
    return SOURCES_COMMENT.exec(sourceText)?.[0].trim() || "";
}

/** 保存剧集时把原来的来源注释接回编辑器给出的 HTML 前面（HTML 里已带着就不重复）。 */
export function withEpisodeSourcesComment(previousSourceText: string, html: string) {
    const comment = episodeSourcesComment(previousSourceText);
    const body = html.replace(SOURCES_COMMENT, "");
    return comment + body;
}
