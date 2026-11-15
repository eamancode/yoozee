// 单元卡标题行：与后端 drama.ontologyUnitHeader 同口径（### 与「单元卡」之间、编号前后可有空白，编号可带【】），编号精确匹配。
const UNIT_HEADING = /^###\s*单元卡\s*【?(s\d{2,3}-u\d+[a-z]?)】?(?=\s*[｜|]|\s*$)/;

// SSS 单元卡标题行：与后端 drama.sssCardHead 同口径（### E集-U单元｜…）。
const SSS_UNIT_HEADING = /^###\s*(E\d{1,3}-U\d{1,2}[a-z]?)\s*[｜|]/;
// 单元卡里粘贴提示词那一栏：模板写「#### 粘贴提示词」，范例写「**粘贴提示词：**」。
const SSS_PASTE_LABEL = /^(?:#{3,4}\s*|\*\*)粘贴提示词/;

/** 从 SSS 导演的八部分里取一个单元的单元卡正文（标题行到粘贴提示词之前）；找不到返回空字符串。 */
export function sssDirectorCard(episode: string, unit: string) {
    if (!unit) return "";
    const lines = episode.split("\n");
    const start = lines.findIndex((line) => SSS_UNIT_HEADING.exec(line.trim())?.[1] === unit);
    if (start < 0) return "";
    const rest = lines.slice(start);
    const end = rest.findIndex((line, index) => index > 0 && (SSS_UNIT_HEADING.test(line.trim()) || /^##\s/.test(line) || SSS_PASTE_LABEL.test(line.trim())));
    return (end < 0 ? rest : rest.slice(0, end)).join("\n").trim();
}

/** 按项目分镜模式取单元卡正文。 */
export function directorCard(mode: string, episode: string, unit: string) {
    return mode === "sss" ? sssDirectorCard(episode, unit) : ontologyDirectorCard(episode, unit);
}

/** 取八部分里的一部分（「第八部分」之类，按前缀匹配，忽略空白）；没有返回空字符串。 */
export function episodePart(episode: string, part: string) {
    const lines = episode.split("\n");
    const want = part.replace(/\s/g, "");
    const start = lines.findIndex((line) => /^##\s/.test(line) && line.replace(/^##/, "").replace(/\s/g, "").startsWith(want));
    if (start < 0) return "";
    const end = lines.findIndex((line, index) => index > start && /^##\s/.test(line));
    return (end < 0 ? lines.slice(start + 1) : lines.slice(start + 1, end)).join("\n").trim();
}

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
