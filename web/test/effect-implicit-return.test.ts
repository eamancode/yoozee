import { expect, test } from "bun:test";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";

// 这些浏览器 API 在新版浏览器里返回 Promise 或对象（Chrome 157 的 scrollIntoView 返回 Promise）。
// effect 写成不带大括号的箭头函数时会把返回值交给 React 当清理函数，卸载或依赖变化时崩溃（线上「l is not a function」）。
const RETURNS_VALUE = /\b(scrollIntoView|scrollTo|scrollBy|requestFullscreen|exitFullscreen|showPicker|animate|play|writeText|focus\(\)\.then)\s*\(/;
const EFFECT = /\buse(?:Layout)?Effect\(\s*\(\)\s*=>\s*(?!\{|\(\)\s*=>)([^\n]*)/g;

function sources(dir: string): string[] {
    return readdirSync(dir).flatMap((name) => {
        const path = join(dir, name);
        if (statSync(path).isDirectory()) return sources(path);
        return /\.(tsx?|jsx?)$/.test(name) ? [path] : [];
    });
}

test("effect 不隐式返回 scrollIntoView 等浏览器 API 的结果", () => {
    const root = join(import.meta.dir, "..", "src");
    const offenders: string[] = [];
    for (const file of sources(root)) {
        const text = readFileSync(file, "utf8");
        for (const match of text.matchAll(EFFECT)) if (RETURNS_VALUE.test(match[1])) offenders.push(`${relative(root, file)}: ${match[0].trim()}`);
    }
    expect(offenders).toEqual([]);
});
