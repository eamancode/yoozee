// 单元卡标题行：与后端 drama.ontologyUnitHeader 同口径（### 与「单元卡」之间、编号前后可有空白，编号可带【】），编号精确匹配。
const UNIT_HEADING = /^###\s*单元卡\s*【?(s\d{2,3}-u\d+[a-z]?)】?(?=\s*[｜|]|\s*$)/;

/** 从本体论导演的七部分里取一个单元的【导演分镜卡】正文（标题行到【粘贴提示词】之前）；找不到返回空字符串。 */
export function ontologyDirectorCard(episode: string, unit: string) {
    if (!unit) return "";
    const lines = episode.split("\n");
    const start = lines.findIndex((line) => UNIT_HEADING.exec(line.trim())?.[1] === unit);
    if (start < 0) return "";
    const rest = lines.slice(start);
    const end = rest.findIndex((line, index) => index > 0 && (UNIT_HEADING.test(line.trim()) || /^##\s/.test(line)));
    const block = (end < 0 ? rest : rest.slice(0, end)).join("\n");
    const paste = block.indexOf("【粘贴提示词】");
    return (paste < 0 ? block : block.slice(0, paste)).trim();
}
