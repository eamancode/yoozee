/** 从本体论导演的七部分里取一个单元的【导演分镜卡】正文（标题行到【粘贴提示词】之前）；找不到返回空字符串。 */
export function ontologyDirectorCard(episode: string, unit: string) {
    if (!unit) return "";
    const head = `### 单元卡 ${unit}`;
    const lines = episode.split("\n");
    const start = lines.findIndex((line) => line.replace(/[【】]/g, "").startsWith(head));
    if (start < 0) return "";
    const rest = lines.slice(start);
    const end = rest.findIndex((line, index) => index > 0 && (line.startsWith("### 单元卡") || line.startsWith("## ")));
    const block = (end < 0 ? rest : rest.slice(0, end)).join("\n");
    const paste = block.indexOf("【粘贴提示词】");
    return (paste < 0 ? block : block.slice(0, paste)).trim();
}
