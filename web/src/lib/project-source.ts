import { splitTextIntoChapters } from "@/lib/canvas/canvas-document";

/** 建项「要拍什么」的类型：一句创意、剧本、小说。 */
export type ProjectSourceKind = "idea" | "script" | "novel";

/** 识别结果：类型、按章节页导入同一套解析切出的章 / 集数、总字数。 */
export type ProjectSourceDetection = {
    kind: ProjectSourceKind;
    /** 去掉空白后的字数。 */
    chars: number;
    /** 剧本按「第 N 集」切出的集数，小说按章标题切出的章数；创意为 0。 */
    units: number;
};

/** 创意的字数上限（约 300 字）：更短、又没有场次结构的按创意。 */
export const IDEA_MAX_CHARS = 300;

// 剧本的格式记号：场次号（第1场、1-2、场1）、场景头（## 内·病房·夜、内景 / 外景、日 / 夜）、△ 动作行、「人物：台词」。
const SCENE_HEADING = /^\s*(#{1,3}\s*\S|第\s*[0-9一二三四五六七八九十百零〇两]+\s*场|场\s*[0-9]+|[0-9]+\s*[-－.．]\s*[0-9]+\s+\S|[内外](景)?\s*[·・.]|(日|夜|晨|昏)\s*[内外])/;
const ACTION_LINE = /^\s*[△▲]/;
const DIALOGUE_LINE = /^\s*[一-龥A-Za-z][一-龥A-Za-z0-9·]{0,7}(\s*[（(][^）)]{0,12}[）)])?\s*[：:]\s*\S/;
// 小说正文里也会有「他说：」这样的行，按台词行占比和行长再判一次。
const NARRATIVE_LINE_CHARS = 60;

/** 字数：去掉空白后的字符数（与导入预览一致）。 */
export function sourceCharCount(text: string) {
    return text.replace(/\s+/g, "").length;
}

/** 文本里像剧本格式的行：场次头、△ 动作行、台词行各几行。 */
export function scriptMarkers(text: string) {
    let scenes = 0;
    let actions = 0;
    let dialogue = 0;
    let lines = 0;
    let longLines = 0;
    for (const line of text.replace(/\r\n?/g, "\n").split("\n")) {
        if (!line.trim()) continue;
        lines += 1;
        if (line.trim().length > NARRATIVE_LINE_CHARS) longLines += 1;
        if (SCENE_HEADING.test(line)) scenes += 1;
        else if (ACTION_LINE.test(line)) actions += 1;
        else if (DIALOGUE_LINE.test(line)) dialogue += 1;
    }
    return { scenes, actions, dialogue, lines, longLines };
}

/**
 * 自动识别粘贴或导入的正文是什么：有场次号、△、「人物：台词」等剧本格式的是剧本；短于约 300 字、没有场次结构的是创意；
 * 其余长篇连续叙事是小说。识别结果只是默认值，用户可以改。
 */
export function detectProjectSource(text: string): ProjectSourceDetection {
    const trimmed = text.trim();
    const chars = sourceCharCount(trimmed);
    const markers = scriptMarkers(trimmed);
    const structured = markers.scenes > 0 || markers.actions > 0;
    const dialogueHeavy = markers.dialogue >= 3 && markers.dialogue * 3 >= markers.lines && markers.longLines * 2 < markers.lines;
    if (structured || dialogueHeavy) return { kind: "script", chars, units: sourceUnitCount(trimmed) };
    if (chars <= IDEA_MAX_CHARS) return { kind: "idea", chars, units: 0 };
    return { kind: "novel", chars, units: sourceUnitCount(trimmed) };
}

/** 按章节页导入的同一套解析（splitTextIntoChapters）切出几章 / 几集；没有章 / 集标题时整篇算一个。 */
export function sourceUnitCount(text: string) {
    return text.trim() ? splitTextIntoChapters(text).length : 0;
}

/** 字数怎么写：一万以上写「9.6 万字」，否则写「12,480 字」。 */
export function formatSourceChars(chars: number) {
    if (chars >= 10_000) return `${Math.round(chars / 1_000) / 10} 万字`;
    return `${chars.toLocaleString("en-US")} 字`;
}

/** 识别标签的文字：创意写要生成几集，剧本写集数和字数，小说写章数和字数。 */
export function sourceDetectionLabel(detection: Pick<ProjectSourceDetection, "kind" | "chars" | "units">, ideaEpisodes: number) {
    if (detection.kind === "idea") return `识别为 创意 · 将生成 ${ideaEpisodes} 集`;
    if (detection.kind === "script") return `识别为 剧本 · ${detection.units} 集 · ${formatSourceChars(detection.chars)}`;
    return `识别为 小说 · ${detection.units} 章 · ${formatSourceChars(detection.chars)}`;
}

/** 正文的纯文本转成章节编辑器的 HTML（空行分段、单换行是 <br>）；章节页导入和建项导入共用。 */
export function plainTextToHtml(value: string) {
    const escaped = value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    return escaped.split(/\n{2,}/).map((paragraph) => `<p>${paragraph.replace(/\n/g, "<br>")}</p>`).join("");
}

/** 自动生成的项目名：文件名去掉扩展名、剧本标题（第一行短标题）或首句，最多 24 字。 */
export function suggestProjectName(text: string, fileName?: string) {
    const fromFile = fileName?.replace(/\.[^.]+$/, "").trim();
    if (fromFile) return fromFile.slice(0, 24);
    const lines = text.replace(/\r\n?/g, "\n").split("\n").map((line) => line.trim()).filter(Boolean);
    const first = lines[0] || "";
    const title = first.match(/^[《【「]?([^》】」]{1,24})[》】」]?$/);
    if (title && first.length <= 26 && !DIALOGUE_LINE.test(first) && !SCENE_HEADING.test(first)) return title[1].trim();
    const sentence = first.split(/[。！？!?；;，,]/)[0] || first;
    return sentence.replace(/^[#△▲\s]+/, "").slice(0, 24) || "未命名项目";
}
