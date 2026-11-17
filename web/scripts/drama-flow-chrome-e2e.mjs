/**
 * 短剧新工作流真实路由 Chrome E2E（无模型）。
 *
 * 自己启动 Vite DEV 与 headless Chrome，经 CDP 打开真实路由 /drama-flow/...；
 * 浏览器里所有 /api/* 请求由 CDP Fetch 拦下，交给 test/fixtures/drama-flow-api-fixture.ts 回答
 * （只读接口 + 分镜保存；任何生成、出片、改项目的写请求一律 403 并判失败），不连后端、不调模型。
 *
 * 覆盖第四轮 D-00（Chrome 原生 scrollIntoView 返回 Promise 时 effect 清理崩溃）：
 *   - 分镜：选中 → 编辑镜头 → 保存 → 段数据更新 → 刷新后保持 → 换选 → 关详情 → 离开阶段
 *   - 定妆资产、出片详情：选中 → 换选 → 关详情 → 离开
 * 以及 D-01 画风锁定、D-02 空花费、D-03 模型名在真实页面上的文案。
 *
 * 运行：cd web && bun scripts/drama-flow-chrome-e2e.mjs（需 Bun：夹具是 TS）。CHROME_BIN 可指定浏览器。
 * 所有等待都有硬超时；任何断言失败 → exit 1。只终止本脚本启动的进程，只删本脚本建的临时 profile。
 */
import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { createDramaFlowFixture, FIXTURE_ASSET_ID, FIXTURE_ASSET_ID_2, FIXTURE_PROJECT_ID as P, FIXTURE_SHOT_ID, FIXTURE_SHOT_ID_2, FIXTURE_UNIT_ID, FIXTURE_VIDEO_MODEL_NAME } from "../test/fixtures/drama-flow-api-fixture.ts";

const CHROME_CANDIDATES = [
    process.env.CHROME_BIN,
    "C:/Program Files/Google/Chrome/Application/chrome.exe",
    "C:/Program Files (x86)/Google/Chrome/Application/chrome.exe",
    "/usr/bin/google-chrome",
    "/usr/bin/google-chrome-stable",
    "/opt/google/chrome/chrome",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
].filter(Boolean);

/** 唯一允许的浏览器噪声：精确子串。其余 console error / 未捕获异常一律判失败。 */
const ALLOWED_NOISE = [
    "Download the React DevTools",
    // 复用的旧组件（需求文档弹窗等）还在用 antd 旧属性，只是弃用提示，不影响行为；按原文精确放行。
    "Warning: [antd: Modal] `destroyOnClose` is deprecated. Please use `destroyOnHidden` instead.",
];

const results = [];
let failures = 0;
const check = (condition, name, detail = "") => {
    results.push({ ok: Boolean(condition), name, detail });
    if (!condition) failures += 1;
    console.log(`${condition ? "PASS" : "FAIL"}  ${name}${detail ? ` — ${detail}` : ""}`);
    return Boolean(condition);
};
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function freePort() {
    return new Promise((resolve, reject) => {
        const server = createServer();
        server.on("error", reject);
        server.listen(0, "127.0.0.1", () => {
            const { port } = server.address();
            server.close(() => resolve(port));
        });
    });
}

async function launchVite(port) {
    const mode = process.env.DRAMA_FLOW_E2E_PREVIEW === "1" ? ["preview"] : [];
    const child = spawn("bunx", ["vite", ...mode, "--host", "127.0.0.1", "--port", String(port), "--strictPort"], { cwd: process.cwd(), stdio: ["ignore", "pipe", "pipe"], shell: process.platform === "win32" });
    let log = "";
    child.stdout.on("data", (chunk) => (log += chunk));
    child.stderr.on("data", (chunk) => (log += chunk));
    const deadline = Date.now() + 120000;
    while (Date.now() < deadline) {
        if (child.exitCode !== null) throw new Error(`Vite exited early (${child.exitCode}):\n${log}`);
        try {
            if ((await fetch(`http://127.0.0.1:${port}/`)).ok) return child;
        } catch {
            // not up yet
        }
        await sleep(500);
    }
    throw new Error(`Vite not ready in 120s:\n${log}`);
}

async function launchChrome(chromePath, cdpPort, profileDir) {
    const args = ["--headless=new", `--remote-debugging-port=${cdpPort}`, `--user-data-dir=${profileDir}`, "--no-first-run", "--no-default-browser-check", "--window-size=1440,1000"];
    if (process.platform === "linux" || process.env.CI) args.push("--no-sandbox", "--disable-dev-shm-usage");
    args.push("about:blank");
    const child = spawn(chromePath, args, { stdio: ["ignore", "pipe", "pipe"] });
    const deadline = Date.now() + 60000;
    while (Date.now() < deadline) {
        if (child.exitCode !== null) throw new Error(`Chrome exited early (${child.exitCode})`);
        try {
            if ((await fetch(`http://127.0.0.1:${cdpPort}/json/version`)).ok) return child;
        } catch {
            // not up yet
        }
        await sleep(400);
    }
    throw new Error("Chrome CDP not ready in 60s");
}

async function connect(cdpPort, appOrigin, getFixture) {
    const target = (await (await fetch(`http://127.0.0.1:${cdpPort}/json/list`)).json()).find((item) => item.type === "page");
    const ws = new WebSocket(target.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => {
        ws.addEventListener("open", resolve, { once: true });
        ws.addEventListener("error", () => reject(new Error("CDP websocket error")), { once: true });
    });
    const pending = new Map();
    let nextId = 0;
    const problems = [];
    const record = (kind, text) => {
        const clean = String(text ?? "").trim();
        if (clean && !ALLOWED_NOISE.some((noise) => clean.includes(noise))) problems.push({ kind, text: clean.slice(0, 600) });
    };
    const send = (method, params = {}) =>
        new Promise((resolve, reject) => {
            const id = ++nextId;
            pending.set(id, { resolve, reject });
            ws.send(JSON.stringify({ id, method, params }));
            setTimeout(() => pending.has(id) && (pending.delete(id), reject(new Error(`CDP timeout: ${method}`))), 30000);
        });
    ws.addEventListener("message", (event) => {
        const message = JSON.parse(event.data);
        if (message.id && pending.has(message.id)) {
            const { resolve, reject } = pending.get(message.id);
            pending.delete(message.id);
            if (message.error) reject(new Error(JSON.stringify(message.error)));
            else resolve(message.result);
            return;
        }
        const params = message.params;
        if (message.method === "Runtime.exceptionThrown") record("exception", params.exceptionDetails?.exception?.description || params.exceptionDetails?.text);
        if (message.method === "Runtime.consoleAPICalled" && params.type === "error") record("console.error", (params.args || []).map((arg) => arg.value ?? arg.description ?? "").join(" "));
        if (message.method === "Fetch.requestPaused") {
            // 所有 /api 请求交给夹具；夹具没回（在读）的情况本脚本不用。
            const url = new URL(params.request.url);
            const fixture = getFixture();
            const body = params.request.postData ? JSON.parse(params.request.postData) : undefined;
            const result = fixture.handle(params.request.method, `${url.pathname}${url.search}`, body) || { status: 504, body: { code: 504, msg: "pending" } };
            if (result.status >= 400) record("api", `${result.status} ${params.request.method} ${url.pathname}`);
            // 夹具可以回二进制（资源文件的 PNG）：binary 是 base64，按 contentType 回。
            void send("Fetch.fulfillRequest", {
                requestId: params.requestId,
                responseCode: result.status,
                responseHeaders: [{ name: "Content-Type", value: result.contentType || "application/json; charset=utf-8" }],
                body: result.binary || Buffer.from(JSON.stringify(result.body)).toString("base64"),
            }).catch(() => {});
        }
    });
    await send("Runtime.enable");
    await send("Page.enable");
    await send("Fetch.enable", { patterns: [{ urlPattern: `${appOrigin}/api/*`, requestStage: "Request" }] });

    const evaluate = async (expression) => {
        const result = await send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
        if (result.exceptionDetails) throw new Error(`evaluate threw: ${result.exceptionDetails.exception?.description || result.exceptionDetails.text}`);
        return result.result.value;
    };
    const poll = async (expression, label, timeout = 15000) => {
        const deadline = Date.now() + timeout;
        let last;
        while (Date.now() < deadline) {
            last = await evaluate(expression).catch(() => undefined);
            if (last) return last;
            await sleep(200);
        }
        console.log(`      (poll timeout: ${label}; last=${JSON.stringify(last)})`);
        return false;
    };
    /** 真实鼠标点击：目标稳定且在最上层再派发 Input 事件。 */
    const click = async (locator, label) => {
        // 和真人一样：目标被浮层（如右下角的柚智）挡住时，换个滚动位置再点，不去隐藏浮层。
        const box = await poll(
            `(() => { const el = ${locator}; if (!(el instanceof HTMLElement)) return null;
              for (const block of ["center", "start", "end"]) { el.scrollIntoView({ block }); const r = el.getBoundingClientRect(); if (!r.width || !r.height) continue;
                const x = r.left + r.width / 2, y = r.top + r.height / 2; const hit = document.elementFromPoint(x, y); if (hit && (hit === el || el.contains(hit))) return { x: Math.round(x), y: Math.round(y) }; }
              return null; })()`,
            `click ${label}`,
            8000,
        );
        if (!box) return check(false, `可以点到：${label}`);
        await sleep(250);
        for (const type of ["mouseMoved", "mousePressed", "mouseReleased"]) await send("Input.dispatchMouseEvent", { type, x: box.x, y: box.y, button: "left", buttons: type === "mousePressed" ? 1 : 0, clickCount: 1 });
        await sleep(400);
        return true;
    };
    const navigate = async (path) => {
        await send("Page.navigate", { url: `${appOrigin}${path}` });
        await poll(`document.readyState === "complete"`, `load ${path}`, 30000);
    };
    const shot = async (file) => {
        const { data } = await send("Page.captureScreenshot", { format: "png" });
        writeFileSync(file, Buffer.from(data, "base64"));
    };
    return { send, evaluate, poll, click, navigate, problems, shot, close: () => ws.close() };
}

const byText = (selector, text) => `[...document.querySelectorAll(${JSON.stringify(selector)})].find((el) => (el.textContent || "").includes(${JSON.stringify(text)}))`;
/** poll 要的是能序列化的真假值；DOM 元素本身回不到 Node 这边。 */
const exists = (locator) => `Boolean(${locator})`;
const crashed = `/页面运行异常|is not a function/.test(document.body.innerText)`;
const summary = (label) =>
    `(() => { const row = [...document.querySelectorAll("dl.drama-flow-summary > div")].find((r) => r.querySelector("dt")?.textContent === ${JSON.stringify(label)}); return (row?.querySelector(".drama-flow-model-name") ?? row?.querySelector("dd"))?.textContent })()`;
const info = (label) => `[...document.querySelectorAll("dl.drama-flow-info-facts dt")].find((dt) => dt.textContent === ${JSON.stringify(label)})?.nextElementSibling?.textContent`;

const primaryButton = `document.querySelector(".drama-flow-bar-actions .ant-btn-primary")`;
const launcherBox = `(() => { const el = document.querySelector(".canvas-agent-launcher"); if (!el || getComputedStyle(el).display === "none") return null; const r = el.getBoundingClientRect();
  return { x: Math.round(r.left), y: Math.round(r.top), w: Math.round(r.width), h: Math.round(r.height), media: /launcher-(media|live2d)/.test(el.className), collapsed: el.classList.contains("is-collapsed") }; })()`;

/** 按钮中心和四个内缩角都要点在按钮自己（或它的子元素）上。 */
const hitTest = (locator) => `(() => { const el = ${locator}; if (!el) return { ok: false, reason: "no-element" }; const r = el.getBoundingClientRect(); const pad = 3;
  const points = [[r.left + r.width / 2, r.top + r.height / 2], [r.left + pad, r.top + pad], [r.right - pad, r.top + pad], [r.left + pad, r.bottom - pad], [r.right - pad, r.bottom - pad]];
  const misses = points.filter(([x, y]) => { const hit = document.elementFromPoint(x, y); return !hit || (hit !== el && !el.contains(hit)); }).map(([x, y]) => { const hit = document.elementFromPoint(x, y); return { x: Math.round(x), y: Math.round(y), hit: String(hit?.className || hit?.tagName).slice(0, 60) }; });
  return { ok: !misses.length, misses, rect: { x: Math.round(r.left), y: Math.round(r.top), w: Math.round(r.width), h: Math.round(r.height) } }; })()`;

/** 详情栏里每个按钮滚进可见区后，中心要能点到它自己（真人会滚动详情栏）。 */
const paneButtonsHit = `(() => { const pane = document.querySelector("aside.drama-flow-pane"); if (!pane) return { ok: false, reason: "no-pane" }; const body = pane.querySelector(".drama-flow-pane-body");
  const buttons = [...pane.querySelectorAll("button")].filter((el) => el.getClientRects().length);
  const misses = [];
  for (const el of buttons) { el.scrollIntoView({ block: "nearest" }); const r = el.getBoundingClientRect(); const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2); if (!hit || (hit !== el && !el.contains(hit))) misses.push((el.getAttribute("aria-label") || el.textContent || "").trim().slice(0, 30) + " -> " + String(hit?.className || hit?.tagName).slice(0, 40)); }
  const box = pane.getBoundingClientRect();
  return { ok: buttons.length > 0 && !misses.length, buttons: buttons.length, misses, scrolls: body ? body.scrollHeight > body.clientHeight : false, pane: { top: Math.round(box.top), bottom: Math.round(box.bottom), right: Math.round(box.right) }, paneMax: getComputedStyle(pane).getPropertyValue("--drama-flow-pane-max"), clear: getComputedStyle(pane).getPropertyValue("--drama-flow-launcher-clear") }; })()`;

/** 等浮层尺寸和位置连续 2 秒不变（头像媒体加载完会变大），超时返回最后一次读数。 */
async function modelCardCase(cdp) {
    const card = `document.querySelector('button[aria-label^="更换视频模型"]')`;
    const expanded = (value) => `(${card})?.getAttribute("aria-expanded") === "${value}"`;
    const key = async (name, code, vk, text) => {
        await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", key: name, code, windowsVirtualKeyCode: vk, text });
        await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: name, code, windowsVirtualKeyCode: vk });
    };
    for (const part of [".drama-flow-model-label", ".drama-flow-model-name"]) {
        await cdp.click(`(${card})?.querySelector("${part}")`, part);
        check(await cdp.poll(expanded(true), "picker opened"), `模型卡点击 ${part} 打开`);
        await cdp.click(`(${card})?.querySelector("${part}")`, part);
        check(await cdp.poll(expanded(false), "picker closed"), `再次点击 ${part} 收起`);
    }
    const point = await cdp.evaluate(`(() => { const b = ${card}; const r = b.getBoundingClientRect(); const x = r.left + 3, y = r.top + r.height / 2; return document.elementFromPoint(x,y) === b ? { x,y } : null })()`);
    if (!check(point, "留白直接命中原生模型按钮")) return;
    for (const type of ["mouseMoved", "mousePressed", "mouseReleased"]) await cdp.send("Input.dispatchMouseEvent", { type, ...point, button: "left", buttons: type === "mousePressed" ? 1 : 0, clickCount: 1 });
    check(await cdp.poll(expanded(true), "padding opens"), "点击模型卡留白打开");
    await cdp.click(card, "收起模型卡");
    // 浮层关闭时浏览器可以把焦点还给页面，从当前焦点按 Tab 验证卡片可达。
    await key("Tab", "Tab", 9);
    for (let i = 0; i < 100 && !(await cdp.evaluate(`document.activeElement === ${card}`)); i++) await key("Tab", "Tab", 9);
    if (!check(await cdp.evaluate(`document.activeElement === ${card}`), "Tab 能聚焦整张模型卡")) return;
    for (const [name, code, vk, text] of [
        ["Enter", "Enter", 13, "\r"],
        [" ", "Space", 32, " "],
    ]) {
        await key(name, code, vk, text);
        if (!check(await cdp.poll(`${expanded(true)} && document.activeElement?.matches('[role="option"]')`, "keyboard opens"), `${code} 打开并聚焦候选模型`, await cdp.evaluate("document.activeElement?.outerHTML.slice(0,240)"))) return;
        await key("Enter", "Enter", 13, "\r");
        check(await cdp.poll(`${expanded(false)} && document.activeElement === ${card}`, "choice returns focus"), "Enter 选中模型并返回触发按钮");
    }
}

async function stableLauncher(cdp) {
    let last = "";
    let same = 0;
    let box = null;
    const deadline = Date.now() + 20000;
    while (Date.now() < deadline) {
        box = await cdp.evaluate(launcherBox);
        const key = JSON.stringify(box);
        same = key === last ? same + 1 : 0;
        last = key;
        if (box && same >= 5) return box;
        await sleep(400);
    }
    return box;
}

async function footerCase(cdp, { viewport, theme, collapsed, outDir, expectWrap = false }) {
    const label = `${viewport.width}×${viewport.height} ${theme === "dark" ? "暗" : "亮"} ${collapsed ? "收起" : "展开"}`;
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: viewport.width, height: viewport.height, deviceScaleFactor: 1, mobile: false });
    // 主题是用户偏好，按应用自己的存储写入后重开页面。
    await cdp.evaluate(`localStorage.setItem("yingce:theme_store", JSON.stringify({ state: { theme: ${JSON.stringify(theme)} }, version: 0 }))`);
    await cdp.navigate(`/drama-flow/${P}/shots?item=${FIXTURE_SHOT_ID}`);
    const actual = await cdp.evaluate(`({ width: innerWidth, height: innerHeight, dark: document.documentElement.classList.contains("dark") })`);
    check(await cdp.poll(`document.documentElement.classList.contains("dark") === ${theme === "dark"}`, "theme"), `${label}：主题生效`, JSON.stringify(actual));

    // 这个夹具状态下底栏一定有主操作「处理 1 件待办」：没有按钮直接判失败，不和「没被挡」混为一项。
    check(await cdp.poll(exists(primaryButton), "primary button", 20000), `${label}：底栏主操作存在`);
    check(await cdp.poll(`Boolean(${launcherBox})`, "launcher"), `${label}：柚智浮层已挂载`);
    const initial = await cdp.evaluate(hitTest(primaryButton));
    check(initial.ok, `${label}：初次进入时底栏主操作没被盖住`, JSON.stringify(initial));

    let launcher = await stableLauncher(cdp);
    if (launcher && launcher.collapsed !== collapsed) {
        await cdp.click(`document.querySelector(".canvas-agent-launcher-toggle")`, `${collapsed ? "收起" : "展开"}柚智`);
        launcher = await stableLauncher(cdp);
    }
    check(launcher && launcher.collapsed === collapsed, `${label}：柚智处于${collapsed ? "收起" : "展开"}状态且尺寸稳定`, JSON.stringify(launcher));
    await sleep(300);
    const stable = await cdp.evaluate(hitTest(primaryButton));
    check(stable.ok, `${label}：浮层稳定后底栏主操作没被盖住`, JSON.stringify(stable));
    const bar =
        await cdp.evaluate(`(() => { const bar = document.querySelector(".drama-flow-bar"); const status = bar.querySelector(".drama-flow-bar-status").getBoundingClientRect(); const actions = bar.querySelector(".drama-flow-bar-actions").getBoundingClientRect();
      return { inset: getComputedStyle(bar).getPropertyValue("--drama-flow-launcher-inset"), wrapped: actions.top >= status.bottom - 1, text: bar.innerText.split(String.fromCharCode(10)).join(" / "), bar: Math.round(bar.getBoundingClientRect().width), status: [Math.round(status.width), Math.round(status.height)], actions: [Math.round(actions.width), Math.round(actions.height)] }; })()`);
    // 给浮层让位后说明文字不能被挤成窄条：放不下时应整行换行，而不是压缩文字。
    check(bar.status[0] >= 150, `${label}：底栏说明文字没被挤窄`, JSON.stringify(bar));
    if (expectWrap) check(bar.wrapped, `${label}：底栏按钮已换到下一行（换行情形确实被覆盖）`, JSON.stringify(bar));

    const short = await cdp.evaluate(paneButtonsHit);
    check(short.ok, `${label}：短内容详情栏的按钮都点得到`, JSON.stringify(short));
    // 窄屏时详情栏是盖在列表上的浮层，先收起再选下一段（真人也这样操作）。
    await cdp.click(`document.querySelector('aside.drama-flow-pane button[aria-label="收起详情"]')`, "收起详情");
    await cdp.poll(`!document.querySelector("aside.drama-flow-pane")`, "pane closed", 5000);
    await cdp.click(byText("button.drama-flow-seg-head", "S02"), "选中长内容 S02");
    await cdp.poll(`location.search.includes("item=${FIXTURE_SHOT_ID_2}")`, "S02");
    await sleep(300);
    const long = await cdp.evaluate(paneButtonsHit);
    check(long.ok && long.scrolls, `${label}：长内容详情栏（需要滚动）的按钮都点得到`, JSON.stringify(long));
    if (outDir) await cdp.shot(join(outDir, `footer-${viewport.width}-${theme}-${collapsed ? "collapsed" : "expanded"}.png`));

    // 「处理待办」不花钱：真鼠标点下去，待办抽屉要真的打开。
    await cdp.click(primaryButton, "处理待办");
    check(await cdp.poll(`[...document.querySelectorAll(".ant-drawer-open .ant-drawer-title")].some((el) => el.textContent.includes("待办"))`, "inbox open"), `${label}：真鼠标点「处理待办」打开待办抽屉`);
    await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
    await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
    await cdp.poll(`!document.querySelector(".ant-drawer-open")`, "inbox closed", 5000);
    return { label, viewport: actual, launcher, initial, stable, bar, short, long };
}

/** 详情栏实际几何：高度、关闭按钮能不能点到、内容在不在、能不能滚。 */
const paneState = `(() => { const pane = document.querySelector("aside.drama-flow-pane"); if (!pane) return { open: false };
  const r = pane.getBoundingClientRect(); const close = pane.querySelector('button[aria-label="收起详情"]'); const c = close?.getBoundingClientRect();
  const hit = c ? document.elementFromPoint(c.left + c.width / 2, c.top + c.height / 2) : null; const body = pane.querySelector(".drama-flow-pane-body");
  return { open: true, top: Math.round(r.top), height: Math.round(r.height), bottom: Math.round(r.bottom), closeHit: Boolean(close && hit && (hit === close || close.contains(hit))),
    text: (body?.innerText || "").length, scrolls: body ? body.scrollHeight > body.clientHeight : false, clear: getComputedStyle(pane).getPropertyValue("--drama-flow-launcher-clear"), paneMax: getComputedStyle(pane).getPropertyValue("--drama-flow-pane-max") }; })()`;
const agentPanelOpen = `Boolean(document.querySelector(".canvas-agent-launcher")) && getComputedStyle(document.querySelector(".canvas-agent-launcher")).display === "none"`;

/** 真鼠标拖动柚智：从浮层中心按下，分几步移到终点再松开（和人拖动一样，不改应用状态）。 */
async function dragLauncher(cdp, to) {
    const box = await stableLauncher(cdp);
    if (!box) return false;
    const from = { x: box.x + Math.round(box.w / 2), y: box.y + Math.round(box.h / 2) };
    await cdp.send("Input.dispatchMouseEvent", { type: "mouseMoved", x: from.x, y: from.y, button: "none", buttons: 0 });
    await cdp.send("Input.dispatchMouseEvent", { type: "mousePressed", x: from.x, y: from.y, button: "left", buttons: 1, clickCount: 1 });
    for (let step = 1; step <= 12; step += 1) {
        const x = Math.round(from.x + ((to.x - from.x) * step) / 12);
        const y = Math.round(from.y + ((to.y - from.y) * step) / 12);
        await cdp.send("Input.dispatchMouseEvent", { type: "mouseMoved", x, y, button: "left", buttons: 1 });
        await sleep(30);
    }
    await cdp.send("Input.dispatchMouseEvent", { type: "mouseReleased", x: to.x, y: to.y, button: "left", buttons: 0, clickCount: 1 });
    // 松手后挪开鼠标，避免悬停放大影响读数。
    await cdp.send("Input.dispatchMouseEvent", { type: "mouseMoved", x: 10, y: 10, button: "none", buttons: 0 });
    return stableLauncher(cdp);
}

async function ensureLauncherCollapsed(cdp, collapsed) {
    const box = await stableLauncher(cdp);
    if (box && box.collapsed !== collapsed) {
        await cdp.click(`document.querySelector(".canvas-agent-launcher-toggle")`, `${collapsed ? "收起" : "展开"}柚智`);
        await cdp.send("Input.dispatchMouseEvent", { type: "mouseMoved", x: 10, y: 10, button: "none", buttons: 0 });
    }
    return stableLauncher(cdp);
}

/** 第六轮 R5-01：把柚智拖高以后，详情栏不能被压没；拖回右下角后恢复。 */
async function dragCases(cdp, outDir) {
    const results = [];
    const record = (label, state, extra = {}) => results.push({ label, ...extra, pane: state });

    // 窄屏 1024×768：详情栏是固定在右侧的浮层。
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: 1024, height: 768, deviceScaleFactor: 1, mobile: false });
    await cdp.evaluate(`localStorage.setItem("yingce:theme_store", JSON.stringify({ state: { theme: "light" }, version: 0 }))`);
    await cdp.navigate(`/drama-flow/${P}/shots?item=${FIXTURE_SHOT_ID}`);
    await cdp.poll(`Boolean(document.querySelector("aside.drama-flow-pane")) && Boolean(${launcherBox})`, "pane + launcher", 20000);
    await ensureLauncherCollapsed(cdp, false);
    const before = await cdp.evaluate(paneState);
    record("1024 拖动前", before);
    const vp = await cdp.evaluate(`({ width: innerWidth, height: innerHeight })`);
    const start = await stableLauncher(cdp);
    const raised = await dragLauncher(cdp, { x: start.x + Math.round(start.w / 2), y: 125 });
    check(raised && raised.y < start.y - 200, "R5-01 1024×768：真鼠标把柚智拖到上方", JSON.stringify({ start, raised }));
    check(!(await cdp.evaluate(agentPanelOpen)), "R5-01 拖动结束没有误打开柚智对话面板");
    await sleep(300);
    const shortState = await cdp.evaluate(paneState);
    record("1024 拖高后 短内容", shortState, { launcher: raised });
    check(shortState.open && shortState.height >= 240, "R5-01 1024×768 拖高后：短内容详情栏至少 240px 高", JSON.stringify(shortState));
    check(shortState.closeHit && shortState.text > 0, "R5-01 1024×768 拖高后：详情内容在、关闭按钮点得到", JSON.stringify(shortState));
    if (outDir) await cdp.shot(join(outDir, "drag-1024-raised-short.png"));

    // 刷新后用持久化的位置直接打开长内容段。
    await cdp.navigate(`/drama-flow/${P}/shots?item=${FIXTURE_SHOT_ID_2}`);
    await cdp.poll(`Boolean(document.querySelector("aside.drama-flow-pane"))`, "pane after reload", 20000);
    const persisted = await stableLauncher(cdp);
    await sleep(300);
    const longState = await cdp.evaluate(paneState);
    record("1024 刷新后 长内容", longState, { launcher: persisted });
    // 比较中心点：刚松手时头像可能还带着按下的缩放，顶边差几像素，中心不变。
    const center = (box) => ({ x: box.x + box.w / 2, y: box.y + box.h / 2 });
    check(persisted && Math.abs(center(persisted).x - center(raised).x) <= 3 && Math.abs(center(persisted).y - center(raised).y) <= 3, "R5-01 刷新后柚智保持拖动后的位置", JSON.stringify({ raised, persisted }));
    check(longState.open && longState.height >= 240 && longState.scrolls && longState.closeHit, "R5-01 刷新后长内容详情栏至少 240px、可滚动、关闭按钮点得到", JSON.stringify(longState));

    // 收起后再看一次。
    const collapsedBox = await ensureLauncherCollapsed(cdp, true);
    await sleep(300);
    const collapsedState = await cdp.evaluate(paneState);
    record("1024 拖高后 收起", collapsedState, { launcher: collapsedBox });
    check(collapsedBox?.collapsed && collapsedState.height >= 240 && collapsedState.closeHit, "R5-01 拖高后收起柚智：详情栏至少 240px、关闭按钮点得到", JSON.stringify({ collapsedBox, collapsedState }));

    // 展开并拖回右下角：恢复默认让位高度，详情栏和底栏按钮都点得到。
    const expandedAgain = await ensureLauncherCollapsed(cdp, false);
    const back = await dragLauncher(cdp, { x: vp.width - 20 - Math.round(expandedAgain.w / 2), y: vp.height - 20 - Math.round(expandedAgain.h / 2) });
    await sleep(300);
    const restored = await cdp.evaluate(paneState);
    record("1024 拖回右下角", restored, { launcher: back });
    check(back && back.y >= start.y - 2, "R5-01 真鼠标把柚智拖回右下角", JSON.stringify({ start, back }));
    check(restored.open && Math.abs(restored.height - before.height) <= 4, "R5-01 拖回后详情栏恢复原来的让位高度", JSON.stringify({ before, restored }));
    const restoredButtons = await cdp.evaluate(paneButtonsHit);
    check(restoredButtons.ok, "R5-01 拖回后详情栏按钮都点得到", JSON.stringify(restoredButtons));
    const restoredBar = await cdp.evaluate(hitTest(primaryButton));
    check(restoredBar.ok, "R5-01 拖回后底栏主操作点得到", JSON.stringify(restoredBar));
    if (outDir) await cdp.shot(join(outDir, "drag-1024-restored.png"));

    // 宽屏 1280×900：并排的详情栏拖高后同样不低于 240px，拖回后恢复。
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false });
    await cdp.navigate(`/drama-flow/${P}/shots?item=${FIXTURE_SHOT_ID}`);
    await cdp.poll(`Boolean(document.querySelector("aside.drama-flow-pane")) && Boolean(${launcherBox})`, "wide pane", 20000);
    const wideStart = await ensureLauncherCollapsed(cdp, false);
    const wideBefore = await cdp.evaluate(paneState);
    const wideRaised = await dragLauncher(cdp, { x: wideStart.x + Math.round(wideStart.w / 2), y: 140 });
    await sleep(300);
    const wideState = await cdp.evaluate(paneState);
    record("1280 拖高后", wideState, { launcher: wideRaised });
    check(wideState.open && wideState.height >= Math.min(240, wideBefore.height) && wideState.closeHit && wideState.text > 0, "R5-01 1280×900 拖高后：并排详情栏不塌、关闭按钮点得到", JSON.stringify({ wideBefore, wideState }));
    if (outDir) await cdp.shot(join(outDir, "drag-1280-raised.png"));
    await dragLauncher(cdp, { x: 1280 - 20 - Math.round(wideStart.w / 2), y: 900 - 20 - Math.round(wideStart.h / 2) });
    await sleep(300);
    const wideRestored = await cdp.evaluate(paneState);
    record("1280 拖回右下角", wideRestored);
    check(Math.abs(wideRestored.height - wideBefore.height) <= 4, "R5-01 1280×900 拖回后详情栏恢复原来的高度", JSON.stringify({ wideBefore, wideRestored }));

    // 很矮的视口（1024×360）：放不下 240px 时按实际可用高度降级，不为 0、不为负。
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: 1024, height: 360, deviceScaleFactor: 1, mobile: false });
    await cdp.navigate(`/drama-flow/${P}/shots?item=${FIXTURE_SHOT_ID}`);
    await cdp.poll(`Boolean(document.querySelector("aside.drama-flow-pane"))`, "short pane", 20000);
    await stableLauncher(cdp);
    await sleep(300);
    const tiny = await cdp.evaluate(paneState);
    const expectedMin = Math.min(240, 360 - tiny.top - 72);
    record("1024×360 默认位置", tiny, { expectedMin });
    check(tiny.open && tiny.height >= expectedMin && tiny.height > 0 && tiny.closeHit, `R5-01 1024×360：详情栏按可用高度降级（至少 ${expectedMin}px），不消失`, JSON.stringify(tiny));
    if (outDir) await cdp.shot(join(outDir, "drag-1024x360.png"));

    await cdp.send("Emulation.clearDeviceMetricsOverride");
    return results;
}

/** 按去掉空白后的文字找按钮（antd 会在两个汉字的按钮文字中间加空格）。 */
const buttonIn = (scope, text) => `[...(${scope})?.querySelectorAll("button") || []].find((el) => (el.textContent || "").split(" ").join("").trim() === ${JSON.stringify(text)})`;
const vcard = (segmentId) => `[...document.querySelectorAll("article.drama-flow-vcard")].find((el) => el.querySelector(".drama-flow-seg-no")?.textContent === ${JSON.stringify(segmentId)})`;
const chainModal = `document.querySelector(".ant-modal")`;

/**
 * R6-D04：出片页真实点「出这一段 / 重新出」→ 连续生成面板 → 预览花费 → 关闭。
 * 只走只读预览；创建、续跑、取消请求由夹具拦下并判失败。
 */
/** 点不到时把现场记下来：真实视口、按钮和浮层位置、按钮中心实际点中了谁、工作区滚动位置，再截一张图。 */
const clickDiagnosis = (locator) => `(() => { const el = ${locator}; const r = el?.getBoundingClientRect(); const hit = r ? document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2) : null;
  const l = document.querySelector(".canvas-agent-launcher")?.getBoundingClientRect(); const work = document.querySelector(".drama-flow-work");
  const box = (b) => b && [b.left, b.top, b.width, b.height].map((v) => Math.round(v * 10) / 10);
  return { viewport: [innerWidth, innerHeight], button: box(r), launcher: box(l), hit: hit ? String(hit.className || hit.tagName).slice(0, 80) : null, scroll: work ? { top: work.scrollTop, max: work.scrollHeight - work.clientHeight } : null }; })()`;

async function clickOrDiagnose(cdp, locator, label, outDir, shotName) {
    if (await cdp.click(locator, label)) return true;
    const diagnosis = await cdp.evaluate(clickDiagnosis(locator));
    if (outDir && shotName) await cdp.shot(join(outDir, shotName));
    check(false, `${label}：点击诊断`, JSON.stringify(diagnosis));
    return false;
}

async function quoteCase(cdp, fixture, { label, segmentId, shotId, resumeChainId, unresolved = false, viewport, outDir }) {
    const buttonText = resumeChainId ? "重新出" : "出这一段";
    const previewText = resumeChainId ? "从失败步骤重新报价" : "预览当前场景与费用";
    const confirm = buttonIn(chainModal, resumeChainId ? "确认报价并从失败段继续" : "开始连续生成");
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: viewport.width, height: viewport.height, deviceScaleFactor: 1, mobile: false });
    await cdp.evaluate(`localStorage.setItem("yingce:theme_store", JSON.stringify({ state: { theme: "light" }, version: 0 }))`);
    await cdp.navigate(`/drama-flow/${P}/video`);
    check(await cdp.poll(exists(buttonIn(vcard(segmentId), buttonText)), "video card", 20000), `${label}：${segmentId} 卡上有「${buttonText}」`);
    check(await cdp.poll(`!(${buttonIn(vcard(segmentId), buttonText)})?.disabled`, "button enabled", 10000), `${label}：「${buttonText}」可以点`);
    // 先等柚智头像媒体加载完、尺寸位置稳定（默认展开、默认右下位置），不靠媒体还没加载的短暂窗口。
    const launcher = await ensureLauncherCollapsed(cdp, false);
    check(launcher && launcher.media && !launcher.collapsed, `${label}：柚智默认展开、媒体已加载且稳定`, JSON.stringify(launcher));
    // 和真人一样先把按钮滚进工作区的可见范围（窄屏时 S02 在首屏下面），再查中心和四角。
    await cdp.evaluate(`void (${buttonIn(vcard(segmentId), buttonText)})?.scrollIntoView({ block: "nearest" })`);
    await sleep(300);
    const hit = await cdp.evaluate(hitTest(buttonIn(vcard(segmentId), buttonText)));
    check(hit.ok, `${label}：「${buttonText}」中心和四角都点在按钮上`, JSON.stringify(hit));
    // 点不到就记录现场并停在这里，避免后面一串无意义的连锁失败。
    if (!(await clickOrDiagnose(cdp, buttonIn(vcard(segmentId), buttonText), `${label}：${buttonText}`, outDir, `quote-click-failed-${viewport.width}x${viewport.height}-${segmentId}.png`))) return;
    check(await cdp.poll(`(${chainModal})?.textContent.includes("同场景连续生成")`, "chain modal", 10000), `${label}：打开连续生成面板`);
    await sleep(800);
    check(!(await cdp.evaluate(crashed)), `${label}：打开面板后页面没有崩（无 RouteErrorPage）`);
    check(!(await cdp.evaluate(exists(confirm))), `${label}：预览前没有付费确认按钮`);
    await cdp.click(buttonIn(chainModal, previewText), `${label}：${previewText}`);
    const quoteText = resumeChainId ? "剩余步骤预估 1.5 平台积分" : "1.5 平台积分（整链预估";
    check(await cdp.poll(`(${chainModal})?.textContent.includes(${JSON.stringify(quoteText)})`, "quote", 10000), `${label}：看到${resumeChainId ? "剩余步骤" : "整链"}预估花费`);
    const requests = fixture.state.previewRequests;
    check(fixture.state.previews === 1 && requests.length === 1 && requests[0].shotId === shotId && requests[0].resumeChainId === resumeChainId, `${label}：只发了 1 次只读预览，分段和原链正确`, JSON.stringify(requests));
    if (unresolved) {
        check(
            await cdp.evaluate(`(${chainModal})?.innerText.includes("原任务 original-video-s01") && (${chainModal})?.innerText.includes("受理状态 未知") && (${chainModal})?.querySelector('[role="status"]')?.textContent.includes("本次仅供估价")`),
            `${label}：显示原任务、未知受理和只读估价提醒`,
        );
        check(await cdp.evaluate(`(${confirm})?.disabled === true`), `${label}：原单未核实，付费确认禁用`);
        await cdp.click(confirm, `${label}：尝试点击禁用的付费确认`);
        if (outDir) await cdp.shot(join(outDir, `r6-unresolved-${viewport.width}x${viewport.height}.png`));
    } else {
        check(await cdp.evaluate(`(${confirm})?.disabled === false`), `${label}：获得报价后可以由用户确认`);
    }
    await cdp.click(buttonIn(chainModal, "关闭"), `${label}：关闭面板`);
    check(await cdp.poll(`!document.body.innerText.includes("同场景连续生成")`, "modal closed", 5000), `${label}：取消后面板关闭`);
    const submitted = fixture.calls.filter((call) => call.method !== "GET" && /\/video-chains(\/[^/]+\/(retry|cancel))?$/.test(call.path));
    check(!submitted.length && !fixture.forbidden.length, `${label}：没有创建、续跑或取消出片链`, JSON.stringify({ submitted, forbidden: fixture.forbidden }));
}

/** 出片页所有段卡操作按钮：逐个滚进可见区，中心和四个内缩角都要点在按钮自己身上。 */
const cardActionsHit = `(() => { const buttons = [...document.querySelectorAll(".drama-flow-vcard-actions button")].filter((el) => el.getClientRects().length); const misses = [];
  for (const el of buttons) { el.scrollIntoView({ block: "nearest" }); const r = el.getBoundingClientRect(); const pad = 3;
    const points = [[r.left + r.width / 2, r.top + r.height / 2], [r.left + pad, r.top + pad], [r.right - pad, r.top + pad], [r.left + pad, r.bottom - pad], [r.right - pad, r.bottom - pad]];
    for (const [x, y] of points) { const hit = document.elementFromPoint(x, y); if (!hit || (hit !== el && !el.contains(hit))) { misses.push({ button: (el.textContent || "").trim(), card: el.closest("article")?.querySelector(".drama-flow-seg-no")?.textContent, x: Math.round(x), y: Math.round(y), hit: String(hit?.className || hit?.tagName).slice(0, 60) }); break; } } }
  const work = document.querySelector(".drama-flow-work");
  return { ok: buttons.length > 0 && !misses.length, buttons: buttons.length, cards: document.querySelectorAll("article.drama-flow-vcard").length, misses, scrolls: work ? work.scrollHeight > work.clientHeight : false }; })()`;

async function cardActionsCase(cdp, { viewport, long, collapsed }) {
    const label = `R7-01 ${viewport.width}×${viewport.height} ${long ? "长列表" : "短列表"} 柚智${collapsed ? "收起" : "展开"}`;
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: viewport.width, height: viewport.height, deviceScaleFactor: 1, mobile: false });
    await cdp.navigate(`/drama-flow/${P}/video`);
    const rendered = await pollOrDiagnose(
        cdp,
        `document.querySelectorAll(".drama-flow-vcard-actions button").length >= ${long ? 20 : 4}`,
        "card actions",
        20000,
        `${label} card actions`,
        `({ buttons: document.querySelectorAll(".drama-flow-vcard-actions button").length, cards: document.querySelectorAll("article.drama-flow-vcard").length, visibleButtons: [...document.querySelectorAll(".drama-flow-vcard-actions button")].filter((el) => el.getClientRects().length).length, shell: Boolean(document.querySelector(".drama-flow-shell")), stage: document.querySelector(".drama-flow-stage, .drama-flow-main")?.className || "" })`,
    );
    check(rendered.ok, `${label}：段卡操作都已渲染`, rendered.evidence);
    const launcher = await ensureLauncherCollapsed(cdp, collapsed);
    check(launcher && launcher.collapsed === collapsed && (collapsed || launcher.media), `${label}：柚智状态稳定`, JSON.stringify(launcher));
    const result = await cdp.evaluate(cardActionsHit);
    check(result.ok && (!long || result.scrolls), `${label}：每张段卡的操作中心和四角都点得到`, JSON.stringify(result));
    return { label, launcher, ...result };
}

// 页面外壳在可视区内：顶栏、阶段条、主区、底部操作、详情栏都不越出视口（不靠全局 overflow:hidden 掩盖）。
const layoutProbe = `(() => {
  const vw = document.documentElement.clientWidth;
  const box = (el) => { if (!el) return null; const r = el.getBoundingClientRect(); return { left: Math.round(r.left), right: Math.round(r.right), width: Math.round(r.width) }; };
  const parts = {
    shell: document.querySelector(".drama-flow-shell"),
    top: document.querySelector(".drama-flow-top"),
    strip: document.querySelector(".drama-flow-strip, .drama-flow-stages"),
    main: document.querySelector(".drama-flow-main"),
    bar: document.querySelector(".drama-flow-bar"),
    pane: document.querySelector("aside.drama-flow-pane"),
  };
  const out = { vw, scrollWidth: document.documentElement.scrollWidth, parts: {}, clipped: [] };
  for (const [name, el] of Object.entries(parts)) { const b = box(el); out.parts[name] = b; if (b && b.right > vw + 1) out.clipped.push(name + ":" + b.right); }
  for (const el of document.querySelectorAll(".drama-flow-top button, .drama-flow-top a, .drama-flow-bar button, .drama-flow-top h1")) {
    const r = el.getBoundingClientRect();
    if (r.width && (r.right > vw + 1 || r.left < -1)) out.clipped.push((el.getAttribute("aria-label") || el.textContent || el.tagName).trim().slice(0, 20) + ":" + Math.round(r.right));
  }
  const title = document.querySelector(".drama-flow-top h1");
  out.titleTruncated = title ? title.scrollWidth > title.clientWidth : null;
  out.sidebar = box(document.querySelector(".app-workspace-sidebar"));
  return out;
})()`;

async function layoutCase(cdp, { viewport, path, label, withPane }) {
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: viewport.width, height: viewport.height, deviceScaleFactor: 1, mobile: false });
    await cdp.evaluate(`localStorage.setItem("yingce:workspace-sidebar-collapsed", "0")`);
    await cdp.navigate(path);
    await cdp.poll(`Boolean(document.querySelector(".drama-flow-top h1")) && Boolean(document.querySelector(".drama-flow-bar"))`, "layout ready", 30000);
    if (withPane) await cdp.poll(`Boolean(document.querySelector("aside.drama-flow-pane"))`, "pane", 15000);
    await sleep(400);
    const got = await cdp.evaluate(layoutProbe);
    check(got.sidebar && got.sidebar.width > 100, `UI-1024 ${label}：侧栏是展开的`, JSON.stringify(got.sidebar));
    check(got.scrollWidth <= got.vw + 1 && got.clipped.length === 0, `UI-1024 ${label}：顶栏、阶段条、主区、底部操作、详情栏都在可视区内`, JSON.stringify(got));
    return got;
}

/**
 * 轮询失败时留证据（不改轮询表达式、超时和断言）：当时的地址、readyState、这条检查关心的 DOM 值、
 * 夹具最近收到的 /api 请求、浏览器资源计时里最近的 /api 请求、到这时为止记下的 console / 异常问题，有输出目录时另存截图和 JSON。
 * 证据直接写在 FAIL 行后面，没有输出目录（Codex 直接跑）时日志里也看得到。
 */
let currentFixture;

async function pollOrDiagnose(cdp, expression, label, timeout, name, domProbe) {
    const ok = await cdp.poll(expression, label, timeout);
    if (ok) return { ok, evidence: "" };
    const page = await cdp
        .evaluate(
            `(() => ({ url: location.pathname + location.search, ready: document.readyState, title: document.title, probe: (${domProbe}), body: (document.body?.innerText || "").replace(/\\s+/g, " ").slice(0, 400),
            api: performance.getEntriesByType("resource").filter((e) => e.name.includes("/api/")).slice(-12).map((e) => ({ path: new URL(e.name).pathname + new URL(e.name).search, start: Math.round(e.startTime), ms: Math.round(e.duration) })) }))()`,
        )
        .catch((error) => ({ error: String(error) }));
    const evidence = { label, timeout, page, fixtureCalls: (currentFixture?.calls || []).slice(-15), problems: cdp.problems.slice(-6) };
    const outDir = process.env.DRAMA_FLOW_E2E_OUT || "";
    if (outDir) {
        const safe = name.replace(/[^\w\-]+/g, "_");
        writeFileSync(join(outDir, `failure-${safe}.json`), JSON.stringify(evidence, null, 2));
        await cdp.shot(join(outDir, `failure-${safe}.png`)).catch(() => {});
    }
    return { ok, evidence: JSON.stringify(evidence).slice(0, 2400) };
}

async function main() {
    const chrome = CHROME_CANDIDATES.find((candidate) => existsSync(candidate));
    if (!chrome) throw new Error(`No Chrome found. Set CHROME_BIN. Tried:\n  ${CHROME_CANDIDATES.join("\n  ")}`);
    const outDir = process.env.DRAMA_FLOW_E2E_OUT || "";
    const vitePort = await freePort();
    const cdpPort = await freePort();
    const profile = mkdtempSync(join(tmpdir(), "drama-flow-e2e-"));
    const origin = `http://127.0.0.1:${vitePort}`;
    // 每次换夹具都记下来：最后汇总所有夹具实例里被拦下的写请求，不只看最后一个。
    const fixtures = [];
    const useFixture = (options) => {
        const next = createDramaFlowFixture(options);
        fixtures.push(next);
        currentFixture = next;
        return next;
    };
    let fixture = useFixture();
    let vite;
    let browser;
    let cdp;
    try {
        vite = await launchVite(vitePort);
        browser = await launchChrome(chrome, cdpPort, profile);
        cdp = await connect(cdpPort, origin, () => fixture);
        const version = await (await fetch(`http://127.0.0.1:${cdpPort}/json/version`)).json();
        console.log(`Chrome: ${version.Browser}`);

        await cdp.navigate("/");
        const native = await cdp.evaluate(`(() => { const r = document.body.scrollIntoView({ block: "nearest" }); return { type: typeof r, promise: r instanceof Promise }; })()`);
        console.log(`native scrollIntoView returns: ${JSON.stringify(native)}`);

        // D-00 分镜：编辑镜头并保存（真实分镜卡、真实保存接口），刷新后保持；换选、关详情、离开。
        await cdp.navigate(`/drama-flow/${P}/shots`);
        // 第一次打开要等 Vite 按需编译，给足时间。
        check(await cdp.poll(exists(byText("button.drama-flow-seg-head", "S01")), "shots ready", 90000), "分镜页打开");
        await cdp.click(byText("button.drama-flow-seg-head", "S01"), "选中 S01");
        check(await cdp.poll(`location.search.includes("item=${FIXTURE_SHOT_ID}") && Boolean(document.querySelector("aside.drama-flow-pane"))`, "pane"), "选中后详情栏打开、地址栏带 item");
        await cdp.click(`document.querySelector('aside.drama-flow-pane button[aria-label="编辑第 1 个镜头的画面、台词和时长"]')`, "编辑第 1 个镜头");
        const edited = "阿婆把热橙汁推到林深手边，雨声更大。";
        check(await cdp.poll(`document.activeElement?.tagName === "TEXTAREA"`, "editor focus"), "镜头编辑框拿到焦点");
        await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", key: "a", code: "KeyA", modifiers: process.platform === "darwin" ? 4 : 2, windowsVirtualKeyCode: 65 });
        await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: "a", code: "KeyA", modifiers: process.platform === "darwin" ? 4 : 2, windowsVirtualKeyCode: 65 });
        await cdp.send("Input.insertText", { text: edited });
        // antd 会在两个汉字的按钮文字中间自动加空格（「保 存」），按去掉空白后的文字找。
        await cdp.click(`[...document.querySelectorAll("aside.drama-flow-pane button")].find((el) => (el.textContent || "").split(" ").join("").trim() === "保存")`, "保存镜头");
        check(await cdp.poll(`document.body.innerText.includes("已保存 S01 第 1 个镜头")`, "saved toast"), "保存成功提示");
        await sleep(1200);
        check(!(await cdp.evaluate(crashed)), "保存后段数据更新，页面没有崩（无 RouteErrorPage / is not a function）");
        check(fixture.state.saves === 1, "真实保存接口收到 1 次保存", `saves=${fixture.state.saves}`);
        check(await cdp.evaluate(`document.body.innerText.includes(${JSON.stringify(edited)})`), "改后的画面显示在页面上");
        if (outDir) await cdp.shot(join(outDir, "shots-saved.png"));
        await cdp.navigate(`/drama-flow/${P}/shots?item=${FIXTURE_SHOT_ID}`);
        check(await cdp.poll(`document.body.innerText.includes(${JSON.stringify(edited)})`, "reload persisted"), "刷新后改动仍在");
        await cdp.click(byText("button.drama-flow-seg-head", "S02"), "换选 S02");
        check(await cdp.poll(`location.search.includes("item=${FIXTURE_SHOT_ID_2}")`, "switch"), "换选到 S02");
        await cdp.click(`document.querySelector('aside.drama-flow-pane button[aria-label="收起详情"]')`, "关分镜详情");
        check(await cdp.poll(`!document.querySelector("aside.drama-flow-pane")`, "pane closed"), "分镜详情关闭");
        check(!(await cdp.evaluate(crashed)), "分镜换选、关闭不崩");

        // D-00 定妆资产与 D-01 画风（夹具默认没锁画风）。
        await cdp.navigate(`/drama-flow/${P}/look?tab=style`);
        check(await cdp.poll(`${summary("状态")} === "还没锁定"`, "style state"), "D-01 未锁画风时定妆页写「还没锁定」");
        check(await cdp.evaluate(`document.body.innerText.includes("画风未定") && !document.body.innerText.includes("画风已定")`), "D-01 阶段条写「画风未定」，不写「画风已定」");
        await cdp.navigate(`/drama-flow/${P}/look?tab=character`);
        await cdp.click(byText("button.drama-flow-asset", "林深"), "选中林深");
        check(await cdp.poll(`location.search.includes("item=${FIXTURE_ASSET_ID}")`, "asset"), "选中资产");
        await cdp.click(byText("button.drama-flow-asset", "阿婆"), "换选阿婆");
        check(await cdp.poll(`location.search.includes("item=${FIXTURE_ASSET_ID_2}")`, "asset switch"), "换选资产");
        await cdp.click(`document.querySelector('aside.drama-flow-pane button[aria-label="收起详情"]')`, "关资产详情");
        check(await cdp.poll(`!location.search.includes("item=")`, "asset closed"), "资产详情关闭");
        check(!(await cdp.evaluate(crashed)), "定妆资产选择、切换、关闭不崩");

        // D-00 出片详情：只看，不提交。
        await cdp.navigate(`/drama-flow/${P}/video`);
        await cdp.click(byText("button.drama-flow-vcard-title", "S01"), "选中出片 S01");
        check(await cdp.poll(`document.body.innerText.includes("当前编译的提示词")`, "video pane"), "出片详情打开，显示当前编译的提示词");
        await cdp.click(byText("button.drama-flow-vcard-title", "S02"), "换选出片 S02");
        check(await cdp.poll(`location.search.includes("item=${FIXTURE_SHOT_ID_2}")`, "video switch"), "出片换选");
        await cdp.click(`document.querySelector('aside.drama-flow-pane button[aria-label="收起详情"]')`, "关出片详情");
        check(await cdp.poll(`!document.querySelector("aside.drama-flow-pane")`, "video closed"), "出片详情关闭");
        check(await cdp.evaluate(`${summary("视频模型")} === ${JSON.stringify(FIXTURE_VIDEO_MODEL_NAME)}`), "D-03 出片页视频模型显示名字");
        await modelCardCase(cdp);
        check(!(await cdp.evaluate(crashed)), "出片详情选择、切换、关闭不崩");

        // R5-ZOOM：资产详情的图点开放大（角色主卡、已出图子卡、场景）：按原图比例显示，能放大，Esc 退出；不写任何东西。
        fixture = useFixture({ workflowId: "pipeline-2", assetImages: true });
        const zoomProbe = `(() => { const wrap = [...document.querySelectorAll(".ant-image-preview")].find((el) => getComputedStyle(el).display !== "none" && el.getBoundingClientRect().width > 0); const img = wrap?.querySelector("img.ant-image-preview-img, .ant-image-preview-img img, img"); if (!img) return null; const r = img.getBoundingClientRect(); return { natural: [img.naturalWidth, img.naturalHeight], shown: [Math.round(r.width), Math.round(r.height)], transform: img.style.transform || getComputedStyle(img).transform }; })()`;
        const zoomCase = async (label, expectedRatio) => {
            check(await cdp.poll(`Boolean(${zoomProbe}) && (${zoomProbe}).natural[0] > 0`, `${label} preview`, 15000), `R5-ZOOM ${label}：点图打开放大预览`);
            const before = await cdp.evaluate(zoomProbe);
            const ratio = before ? before.shown[0] / before.shown[1] : 0;
            check(before && before.natural[0] / before.natural[1] === expectedRatio && Math.abs(ratio - expectedRatio) < 0.03, `R5-ZOOM ${label}：按原图比例显示`, JSON.stringify(before));
            await cdp.evaluate(
                `(() => { const btn = [...document.querySelectorAll(".ant-image-preview-actions-action, .ant-image-preview-operations-operation")].find((el) => /zoom-in|放大/.test(el.className + (el.getAttribute("aria-label") || "") + el.innerHTML)); btn?.click(); return Boolean(btn); })()`,
            );
            await sleep(400);
            const after = await cdp.evaluate(zoomProbe);
            check(after && after.shown[0] > before.shown[0], `R5-ZOOM ${label}：能放大`, JSON.stringify({ before, after }));
            await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
            await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
            check(await cdp.poll(`!(${zoomProbe})`, `${label} closed`, 5000), `R5-ZOOM ${label}：Esc 退出预览`);
            await sleep(300);
            check(await cdp.evaluate(`Boolean(document.querySelector("aside.drama-flow-pane"))`), `R5-ZOOM ${label}：一次 Esc 只关预览，详情栏还在`);
        };
        await cdp.navigate(`/drama-flow/${P}/look?tab=character&item=${FIXTURE_ASSET_ID}`);
        check(await cdp.poll(`Boolean(document.querySelector('aside.drama-flow-pane button.drama-flow-pane-media-zoom'))`, "zoom button", 30000), "R5-ZOOM 角色主卡图是可点的放大按钮");
        await cdp.click(`document.querySelector('aside.drama-flow-pane button.drama-flow-pane-media-zoom')`, "点主卡图");
        await zoomCase("角色主卡", 90 / 160);
        // 子卡列表默认收起，和用户一样先点「子卡 1 张」展开。
        await cdp.click(`[...document.querySelectorAll("aside.drama-flow-pane button[aria-expanded]")].find((el) => (el.textContent || "").includes("子卡 1 张"))`, "展开子卡");
        check(await cdp.poll(`Boolean(document.querySelector('aside.drama-flow-pane button[aria-label="预览子卡「林深·雨衣」"]'))`, "variant button", 15000), "R5-ZOOM 子卡缩略图可点");
        await cdp.click(`document.querySelector('aside.drama-flow-pane button[aria-label="预览子卡「林深·雨衣」"]')`, "点子卡图");
        await zoomCase("子卡", 90 / 160);
        await cdp.navigate(`/drama-flow/${P}/look?tab=environment&item=asset-market`);
        check(await cdp.poll(`Boolean(document.querySelector('aside.drama-flow-pane button.drama-flow-pane-media-zoom'))`, "scene zoom button", 30000), "R5-ZOOM 场景图是可点的放大按钮");
        await cdp.click(`document.querySelector('aside.drama-flow-pane button.drama-flow-pane-media-zoom')`, "点场景图");
        await zoomCase("场景", 1);
        check(!fixture.forbidden.length, "R5-ZOOM 全程没有写请求", JSON.stringify(fixture.forbidden));

        // R5-DIRECTOR：导演分镜段（带 verbatim）只读——不出镜头编辑、拆合按钮，写明提交用单元卡原文，给「症状返修」入口；
        // 打开返修框不提交；原流程的段（S02）照常能编辑，也不能并入导演段。
        fixture = useFixture({ workflowId: "pipeline-2", directorSegments: true });
        await cdp.navigate(`/drama-flow/${P}/shots?item=${FIXTURE_SHOT_ID}`);
        check(
            await cdp.poll(`Boolean(document.querySelector("aside.drama-flow-pane")) && (document.querySelector("aside.drama-flow-pane")?.innerText || "").includes("导演分镜单元")`, "director pane", 30000),
            "R5-DIRECTOR 导演段详情写明「导演分镜单元」、提交用单元卡原文",
        );
        const directorPane = await cdp.evaluate(
            `(() => { const pane = document.querySelector("aside.drama-flow-pane"); const labels = [...pane.querySelectorAll("button")].map((el) => (el.getAttribute("aria-label") || el.textContent || "").trim()); return { labels, text: pane.innerText.slice(0, 600) }; })()`,
        );
        check(!directorPane.labels.some((label) => /编辑第 \d+ 个镜头|拆段或移到相邻段|^合并 |^并入 /.test(label)), "R5-DIRECTOR 导演段没有镜头编辑、拆段、合并入口", JSON.stringify(directorPane.labels));
        check(directorPane.labels.includes("症状返修"), "R5-DIRECTOR 导演段有「症状返修」入口", JSON.stringify(directorPane.labels));
        await cdp.click(buttonIn(`document.querySelector("aside.drama-flow-pane")`, "症状返修"), "打开症状返修");
        check(await cdp.poll(`Boolean([...document.querySelectorAll(".ant-modal-container")].find((el) => (el.textContent || "").includes("写清翻车症状")))`, "repair modal", 10000), "R5-DIRECTOR 返修框打开（只替换这一段）");
        await cdp.click(`[...document.querySelectorAll(".ant-modal-container")].find((el) => (el.textContent || "").includes("写清翻车症状"))?.querySelector(".ant-modal-footer button")`, "关掉返修框");
        await cdp.navigate(`/drama-flow/${P}/shots?item=${FIXTURE_SHOT_ID_2}`);
        check(await cdp.poll(`Boolean(document.querySelector('aside.drama-flow-pane button[aria-label="编辑第 1 个镜头的画面、台词和时长"]'))`, "plain pane", 30000), "R5-DIRECTOR 原流程的段照常能编辑镜头");
        const plainLabels = await cdp.evaluate(`[...document.querySelector("aside.drama-flow-pane").querySelectorAll("button")].map((el) => (el.textContent || "").trim())`);
        check(!plainLabels.some((label) => label.startsWith("并入 ")), "R5-DIRECTOR 原流程的段不能并入导演段", JSON.stringify(plainLabels));
        check(!fixture.forbidden.length && fixture.state.saves === 0, "R5-DIRECTOR 全程没有写段、没有提交返修", JSON.stringify({ forbidden: fixture.forbidden, saves: fixture.state.saves }));

        // R4-CONFIRM：停在「角色场景资产」时点「继续」先开确认面板（选模型、排除资产），不直接建任务；去掉阿婆后只给林深出图；同一拍连点两下只发一次。
        fixture = useFixture({ workflowId: "pipeline-2", stopAtAssets: true, imageModel: "CHANNEL_000001::fixture-image" });
        await cdp.navigate(`/drama-flow/${P}/look?tab=character`);
        check(
            await cdp.poll(`Boolean((${primaryButton})?.textContent?.includes("继续：角色场景资产"))`, "continue ready", 30000),
            "R4-CONFIRM 停在角色场景资产时主按钮是「继续：角色场景资产」",
            await cdp.evaluate(`document.querySelector(".drama-flow-bar")?.innerText || ""`),
        );
        check(await cdp.poll(exists(byText("button.drama-flow-asset", "阿婆")), "assets listed", 30000), "R4-CONFIRM 资产卡片已读到");
        // 和底栏用例同一前提：柚智浮层挂好、底栏主操作没被它盖住再点（final 运行的失败证据：点击落在刚挂上的柚智上，打开了画布助手）。
        check(await cdp.poll(`Boolean(${launcherBox})`, "launcher", 15000), "R4-CONFIRM 柚智浮层已挂载");
        const continueClear = await pollOrDiagnose(cdp, `(${hitTest(primaryButton)}).ok`, "continue not covered", 15000, "R4-CONFIRM continue covered", hitTest(primaryButton));
        check(continueClear.ok, "R4-CONFIRM 点之前「继续」没被柚智盖住", continueClear.evidence);
        await cdp.click(primaryButton, "点继续：角色场景资产");
        const confirmPanel = `[...document.querySelectorAll(".ant-modal-container")].find((el) => (el.textContent || "").includes("继续资产出图"))`;
        const panelOpened = await pollOrDiagnose(
            cdp,
            `Boolean(${confirmPanel})`,
            "confirm panel",
            15000,
            "R4-CONFIRM confirm panel",
            `({ bar: document.querySelector(".drama-flow-bar")?.innerText || "", notices: [...document.querySelectorAll(".ant-message-notice")].map((el) => el.textContent), modals: [...document.querySelectorAll(".ant-modal-container")].map((el) => (el.textContent || "").slice(0, 60)), assetCards: document.querySelectorAll("button.drama-flow-asset").length, active: document.activeElement?.textContent?.slice(0, 40) || "" })`,
        );
        check(panelOpened.ok, "R4-CONFIRM 先打开确认面板", panelOpened.evidence);
        await sleep(800);
        check(!fixture.state.imageQueues.length && !fixture.forbidden.length, "R4-CONFIRM 没确认前没有建出图任务、没有续跑流水线", JSON.stringify({ queues: fixture.state.imageQueues, forbidden: fixture.forbidden }));
        const panelText = await cdp.evaluate(`(${confirmPanel})?.innerText || ""`);
        check(panelText.includes("林深") && panelText.includes("阿婆") && panelText.includes("为 2 个资产出图"), "R4-CONFIRM 面板列出两个待出图资产", panelText.slice(0, 400));
        check(await cdp.poll(`Boolean((${confirmPanel})?.innerText.includes("验收图片模型"))`, "image model", 10000), "R4-CONFIRM 面板显示所选图片模型", panelText.slice(0, 400));
        await cdp.click(`[...((${confirmPanel})?.querySelectorAll("label.ant-checkbox-wrapper") || [])].find((el) => (el.textContent || "").includes("阿婆"))`, "去掉阿婆");
        check(await cdp.poll(`Boolean([...((${confirmPanel})?.querySelectorAll("button") || [])].find((el) => (el.textContent || "").includes("为 1 个资产出图")))`, "one selected", 5000), "R4-CONFIRM 去掉阿婆后只剩 1 个");
        await cdp.evaluate(`(() => { const ok = [...((${confirmPanel})?.querySelectorAll("button") || [])].find((el) => (el.textContent || "").includes("为 1 个资产出图")); ok?.click(); ok?.click(); return Boolean(ok); })()`);
        for (let waited = 0; waited < 15000 && !fixture.state.imageQueues.length; waited += 200) await sleep(200);
        await sleep(1000);
        const queues = fixture.state.imageQueues;
        check(queues.length === 1 && JSON.stringify(queues[0].assetIds) === JSON.stringify([FIXTURE_ASSET_ID]) && Boolean(queues[0].imageTask?.model), "R4-CONFIRM 确认后只发一次，只含选中的林深，带所选图片模型", JSON.stringify(queues));
        check(await cdp.poll(`document.body.innerText.includes("已排 1 个资产出图")`, "queued notice", 10000), "R4-CONFIRM 提示已排 1 个资产出图");
        check(!fixture.forbidden.length, "R4-CONFIRM 全程没有续跑流水线或其他写请求", JSON.stringify(fixture.forbidden));

        // R4-SSS-RUN：SSS 分镜运行中，分镜页按已保存的轮次显示当前轮、实际 / 预估总轮数、各阶段和累计等待，不说「写好一节出现一节」。
        const runningSSS = {
            id: "task-sss-running",
            projectId: P,
            type: "canvas_text",
            status: "running",
            stage: "已写完第 5/8 轮：编译 E1-U1",
            prompt: "",
            attempts: 1,
            model: "CHANNEL_000001::fixture-text",
            createdAt: new Date(Date.now() - 125000).toISOString(),
            updatedAt: new Date().toISOString(),
            startedAt: new Date(Date.now() - 120000).toISOString(),
            dramaRun: true,
            clientContext: { chapterId: FIXTURE_UNIT_ID, chapterOperation: "storyboard" },
            dramaRounds: {
                mode: "sss",
                completed: 5,
                total: 8,
                turns: [
                    { kind: "plan", heading: "本集设计与台词定稿" },
                    { kind: "direct", heading: "讲戏本" },
                    { kind: "cut", heading: "整场镜头表与单元总表" },
                    { kind: "card", heading: "单元卡 E1-U1" },
                    { kind: "compile", heading: "编译 E1-U1" },
                ],
            },
        };
        fixture = useFixture({ workflowId: "pipeline-2", tasks: [runningSSS] });
        await cdp.navigate(`/drama-flow/${P}/shots`);
        check(await cdp.poll(`document.body.innerText.includes("导演正在写第 6 轮")`, "sss run", 30000), "R4-SSS-RUN 显示正在写第 6 轮");
        const sssText = await cdp.evaluate(`document.querySelector(".drama-flow-note")?.innerText || ""`);
        check(sssText.includes("单元卡（第 2 个单元）") && sssText.includes("已保存 5/8 轮（实际总轮数）") && /已等 2 分/.test(sssText) && sssText.includes("全部校验后统一装段"), "R4-SSS-RUN 当前轮、实际总轮数、累计等待、统一装段", sssText);
        const phases = await cdp.evaluate(`[...document.querySelectorAll(".drama-flow-director-phases li")].map((el) => el.textContent + "|" + el.dataset.state)`);
        check(
            JSON.stringify(phases) === JSON.stringify(["集设计 ✓|done", "讲戏 ✓|done", "切分 ✓|done", "单元卡 1/2（正在写）|current", "编译 1/2|pending", "复看|pending"]),
            "R4-SSS-RUN 各阶段（集设计、讲戏、切分、单元卡、编译、复看）状态对",
            JSON.stringify(phases),
        );
        check(!(await cdp.evaluate(`document.body.innerText.includes("写好一节出现一节")`)), "R4-SSS-RUN 不说「写好一节出现一节」");
        // 切分前总轮数是估计：同一任务只保存到讲戏时写「预估总轮数」。
        fixture = useFixture({ workflowId: "pipeline-2", tasks: [{ ...runningSSS, id: "task-sss-early", dramaRounds: { mode: "sss", completed: 2, total: 10, turns: runningSSS.dramaRounds.turns.slice(0, 2) } }] });
        await cdp.navigate(`/drama-flow/${P}/look`);
        await cdp.navigate(`/drama-flow/${P}/shots`);
        check(await cdp.poll(`document.body.innerText.includes("导演正在写第 3 轮：整场镜头表与单元总表")`, "sss early", 30000), "R4-SSS-RUN 切分前显示第 3 轮切分");
        check(await cdp.evaluate(`(document.querySelector(".drama-flow-note")?.innerText || "").includes("已保存 2/10 轮（预估总轮数")`), "R4-SSS-RUN 切分前写明是预估总轮数");

        fixture = useFixture({ workflowId: "pipeline-2", directorSetup: "current" });
        for (const width of [1024, 1280]) {
            for (const theme of ["light", "dark"]) {
                await cdp.evaluate(`localStorage.setItem("yingce:theme_store", JSON.stringify({ state: { theme: ${JSON.stringify(theme)} }, version: 0 }))`);
                await layoutCase(cdp, { viewport: { width, height: 900 }, path: `/drama-flow/${P}/script`, label: `R7 本集设计 ${width} ${theme}` });
                check(await cdp.poll(`document.querySelectorAll('[aria-label="本集导演文档"] summary').length === 2`, "设计及定稿表"), "R7-E01 两部分独立展示");
                await cdp.evaluate(`document.querySelectorAll('[aria-label="本集导演文档"] summary').forEach(el => el.click())`);
                check(await cdp.evaluate(`document.querySelector('[aria-label="本集导演文档"]').innerText.includes("收着。")`), "R7-E01 可展开定稿台词");
                if (outDir) await cdp.shot(join(outDir, `r7-script-${width}-${theme}.png`));
            }
        }
        fixture = useFixture({ workflowId: "pipeline-2", directorSetup: "missing" });
        await cdp.navigate(`/drama-flow/${P}/shots`);
        check(await cdp.poll(`document.querySelector('[aria-label="全剧立项"]')?.innerText.includes("未立项")`, "未立项状态"), "R7-E02 分镜页可发起立项");
        await cdp.navigate(`/drama-flow/${P}/script`);
        check(await cdp.poll(`document.body.innerText.includes("缺本集设计") && document.body.innerText.includes("补写本集设计")`, "补写入口"), "R7-E01 旧项目可补写设计");
        fixture = useFixture({
            workflowId: "pipeline-2",
            directorSetup: "legacy",
            tasks: [
                {
                    ...runningSSS,
                    dramaRounds: {
                        mode: "sss",
                        flow: 2,
                        completed: 2,
                        total: 7,
                        turns: [
                            { kind: "direct", heading: "讲戏" },
                            { kind: "cut", heading: "切分" },
                        ],
                    },
                },
            ],
        });
        await cdp.navigate(`/drama-flow/${P}/shots`);
        check(await cdp.poll(`document.body.innerText.includes("建议重新立项")`, "旧立项提示"), "R7-E02 旧圣经可继续使用");
        check(await cdp.poll(`document.querySelectorAll(".drama-flow-director-phases li").length === 5`, "Flow 2 阶段数"), "R7-E04 展示五个阶段");
        await cdp.click(buttonIn("document", "导演文档"), "导演文档");
        check(await cdp.poll(`document.body.innerText.includes("立项依据 1/1 集")`, "圣经依据"), "R7-E06 圣经展示依据集数");
        await cdp.click(byText('[role="tab"]', "E01 随行鲜"), "本集导演文档");
        await cdp.click(byText(".ant-segmented-item", "八部分"), "查看八部分");
        check(await cdp.poll(`["一","二","三","四","五","六","七","八"].every(n => document.body.innerText.includes("第"+n+"部分"))`, "本集八部分"), "R7-E06 本集导演文档八部分齐全");
        fixture = useFixture({
            workflowId: "pipeline-2",
            directorSetup: "current",
            tasks: [{ ...runningSSS, id: "task-project-setup", clientContext: {}, stage: "全剧立项 第 2/3 批", dramaRounds: { mode: "setup", completed: 1, total: 3, turns: [{ kind: "setup", heading: "全剧立项 第 1/3 批" }] } }],
        });
        await cdp.navigate(`/drama-flow/${P}/shots?fixture=project-setup`);
        check(await cdp.poll(`document.querySelector('[aria-label="全剧立项"]')?.innerText.includes("第 2/3 批")`, "增补批进度"), "R7-E02 全剧立项运行时显示当前批次");

        fixture = useFixture({ workflowId: "pipeline-2", directorSetup: "current", style: "locked", handoffGate: true });
        await cdp.navigate(`/drama-flow/${P}/shots?ep=${FIXTURE_UNIT_ID}`);
        const wholeEpisode = buttonIn("document", "整集重新生成");
        check(await cdp.poll(exists(wholeEpisode), "分镜生成入口"), "R7-E03 第二集可请求生成");
        await cdp.click(wholeEpisode, "第二集分镜");
        const independent = buttonIn("document", "独立开局（不接上一集）");
        check(await cdp.poll(exists(independent), "缺少交接卡确认"), "R7-E03 缺交接卡显示独立开局与上一集选项");
        check(await cdp.evaluate(exists(buttonIn("document", "先去做第一集"))), "R7-E03 可以返回前一集");
        if (outDir) await cdp.shot(join(outDir, "r7-handoff.png"));
        await cdp.click(buttonIn("document", "先去做第一集"), "先去上一集");
        check((await cdp.poll(`new URLSearchParams(location.search).get("ep") === "previous-episode"`, "定位上一集")) && JSON.stringify(fixture.handoffRequests) === "[false]", "R7-E03 回到上一集不提交生成");
        await cdp.navigate(`/drama-flow/${P}/shots?ep=${FIXTURE_UNIT_ID}`);
        await cdp.click(wholeEpisode, "重新查看第二集前置条件");
        await cdp.click(independent, "独立开局");
        await cdp.poll(`document.body.innerText.includes("fixture_independent_request_recorded")`, "收到独立开局请求");
        check(JSON.stringify(fixture.handoffRequests) === "[false,false,true]", "R7-E03 仅明确选择后提交 allowMissingHandoff");
        for (const freezeRelay of ["ready", "skipped"]) {
            fixture = useFixture({ workflowId: "pipeline-2", freezeRelay });
            await cdp.navigate(`/drama-flow/${P}/video`);
            const expected = freezeRelay === "ready" ? 'document.querySelector("img[alt=上一段站位参考]") && document.body.innerText.includes("来自上一段定格")' : 'document.body.innerText.includes("未使用上一段站位参考：截帧失败")';
            check(await cdp.poll(expected, "站位参考状态"), `R7-E05 ${freezeRelay} 展示来源或跳过原因`);
            if (outDir) await cdp.shot(join(outDir, `r7-reference-${freezeRelay}.png`));
        }

        // UI-1024：侧栏展开、长中文项目名，1024×900 和 1280×900 下页面外壳都在可视区内；详情栏开关、五个阶段都看一遍。
        fixture = useFixture({ name: "雨夜便民市场里阿婆递给林深一杯温热橙汁之后发生的那些事情（第一季）" });
        for (const viewport of [
            { width: 1024, height: 900 },
            { width: 1280, height: 900 },
        ]) {
            const size = `${viewport.width}×${viewport.height}`;
            for (const stage of ["script", "look", "shots", "video", "cut"]) {
                await layoutCase(cdp, { viewport, path: `/drama-flow/${P}/${stage}`, label: `${size} ${stage}` });
            }
            await layoutCase(cdp, { viewport, path: `/drama-flow/${P}/shots?item=${FIXTURE_SHOT_ID}`, label: `${size} 分镜详情栏打开`, withPane: true });
            await layoutCase(cdp, { viewport, path: `/drama-flow/${P}/look?tab=character&item=${FIXTURE_ASSET_ID}`, label: `${size} 资产详情栏打开`, withPane: true });
        }
        await cdp.evaluate(`localStorage.removeItem("yingce:workspace-sidebar-collapsed")`);

        // R6-D04 / R7-01：新工作流先读过出片链，再打开公共的连续生成面板，预览花费后取消。
        // 明确视口，柚智默认展开且媒体稳定后真鼠标点；S02 在 1280×900 下原来正好落在柚智的透明热区里。
        for (const viewport of [
            { width: 1280, height: 900 },
            { width: 1024, height: 768 },
            { width: 1280, height: 720 },
        ]) {
            const size = `${viewport.width}×${viewport.height}`;
            fixture = useFixture();
            await quoteCase(cdp, fixture, { label: `R6-D04 ${size} 没有出片链`, segmentId: "S02", shotId: FIXTURE_SHOT_ID_2, viewport, outDir });
            fixture = useFixture({ chains: "failed" });
            await quoteCase(cdp, fixture, { label: `R6-D04 ${size} 出片失败`, segmentId: "S01", shotId: FIXTURE_SHOT_ID, resumeChainId: "chain-failed", viewport, outDir });
            fixture = useFixture({ chains: "unresolved" });
            await quoteCase(cdp, fixture, { label: `R6-D04 ${size} 原单受理未知`, segmentId: "S01", shotId: FIXTURE_SHOT_ID, resumeChainId: "chain-unresolved", unresolved: true, viewport, outDir });
        }

        // R8-01：预览请求按所选模型能力对齐。夹具按权威能力校验真实发出的配置（同步音频 false、时长 15/30 秒档）。
        check(
            fixtures.filter((item) => item.state.previewConfigs.length).every((item) => item.state.previewConfigs.every((config) => String(config.videoGenerateAudio) === "false" && ["15", "30"].includes(String(config.videoSeconds)))),
            "R8-01 所有费用预览请求都按所选模型能力对齐（同步音频 false、时长取 15/30 秒档）",
            JSON.stringify(fixtures.flatMap((item) => item.state.previewConfigs).map((config) => ({ audio: config.videoGenerateAudio, seconds: config.videoSeconds }))),
        );

        // R8-02：预览被能力校验拒绝时，面板写出是哪个参数不支持；不提交。
        fixture = useFixture();
        fixture.overrides.set(`/projects/${P}/video-chains/preview`, "capability");
        await cdp.send("Emulation.setDeviceMetricsOverride", { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false });
        await cdp.navigate(`/drama-flow/${P}/video`);
        await cdp.poll(exists(buttonIn(vcard("S02"), "出这一段")), "card", 20000);
        await ensureLauncherCollapsed(cdp, false);
        if (await clickOrDiagnose(cdp, buttonIn(vcard("S02"), "出这一段"), "R8-02 出这一段", outDir, "capability-click-failed.png")) {
            await cdp.poll(`(${chainModal})?.textContent.includes("同场景连续生成")`, "chain modal", 10000);
            await clickOrDiagnose(cdp, buttonIn(chainModal, "预览当前场景与费用"), "R8-02 预览当前场景与费用", outDir, "capability-preview-failed.png");
            check(await cdp.poll(`[...(${chainModal})?.querySelectorAll("[role='alert']") || []].some((el) => el.textContent.includes("同步音频超出支持范围"))`, "capability alert", 10000), "R8-02 面板写出「同步音频超出支持范围」");
            if (outDir) await cdp.shot(join(outDir, "capability-error.png"));
            await cdp.click(buttonIn(chainModal, "关闭"), "R8-02 关闭面板");
            const sent = fixture.calls.filter((call) => call.method !== "GET" && /\/video-chains(\/[^/]+\/(retry|cancel))?$/.test(call.path));
            check(!sent.length && !fixture.forbidden.length && !(await cdp.evaluate(crashed)), "R8-02 能力错误后没有提交、页面不崩");
        }

        // R7-01：每张出片段卡的操作（出这一段 / 重出 / 详情），短列表和长列表 × 宽窄 × 柚智展开收起，中心和四角都要点得到。
        const cardHits = [];
        for (const viewport of [
            { width: 1280, height: 900 },
            { width: 1024, height: 768 },
            { width: 1280, height: 720 },
        ]) {
            for (const long of [false, true]) {
                for (const collapsed of [false, true]) {
                    fixture = useFixture({ extraSegments: long ? 8 : 0 });
                    cardHits.push(await cardActionsCase(cdp, { viewport, long, collapsed }));
                }
            }
        }
        if (outDir) writeFileSync(join(outDir, "card-actions-geometry.json"), JSON.stringify(cardHits, null, 2));
        await cdp.send("Emulation.clearDeviceMetricsOverride");
        fixture = useFixture();

        // D-02 / D-03：项目信息抽屉。
        await cdp.navigate(`/drama-flow/${P}?panel=info`);
        const infoCost = await pollOrDiagnose(
            cdp,
            `${info("最近 100 次运行")} === "0 积分 · 暂无消费"`,
            "info cost",
            15000,
            "D-02 info cost",
            `({ cost: ${info("最近 100 次运行")} ?? null, model: ${info("视频模型")} ?? null, drawer: Boolean(document.querySelector(".drama-flow-info")), facts: [...document.querySelectorAll("dl.drama-flow-info-facts dt")].map((dt) => dt.textContent + "=" + (dt.nextElementSibling?.textContent || "")) })`,
        );
        check(infoCost.ok, "D-02 读到空运行记录写「0 积分 · 暂无消费」", infoCost.evidence);
        check(await cdp.poll(`${info("视频模型")} === ${JSON.stringify(FIXTURE_VIDEO_MODEL_NAME)}`, "info model"), "D-03 项目信息里视频模型显示名字");
        check(await cdp.evaluate(`!document.querySelector(".drama-flow-info")?.textContent.includes("CHANNEL_000001")`), "D-03 项目信息不显示内部渠道 ID");
        if (outDir) await cdp.shot(join(outDir, "info-panel.png"));
        await cdp.navigate("/projects");
        await sleep(800);

        // R4-02 / R4-03：底栏主操作和详情栏按钮不被右下角柚智浮层盖住（真实视口 × 亮暗 × 展开收起 × 短长内容）。
        const geometry = [];
        for (const viewport of [
            { width: 1280, height: 900 },
            { width: 1024, height: 768 },
        ]) {
            for (const theme of ["light", "dark"]) {
                for (const collapsed of [false, true]) geometry.push(await footerCase(cdp, { viewport, theme, collapsed, outDir }));
            }
        }
        // 更窄的视口下底栏会换行（按钮行落到说明文字下面），换行后主操作也不能被盖住。
        // 有失败的段时底栏是两个按钮（「重新出失败的 N 段（先看花费）」+「处理 N 件待办」），会换行。次按钮只看不点。
        fixture = useFixture({ chains: "failed" });
        geometry.push(await footerCase(cdp, { viewport: { width: 1024, height: 768 }, theme: "light", collapsed: false, outDir, expectWrap: true }));
        fixture = useFixture();
        await cdp.send("Emulation.clearDeviceMetricsOverride");
        if (outDir) writeFileSync(join(outDir, "footer-geometry.json"), JSON.stringify(geometry, null, 2));

        // R5-01：真鼠标拖动柚智后，详情栏不被压没。
        const drag = await dragCases(cdp, outDir);
        if (outDir) writeFileSync(join(outDir, "drag-geometry.json"), JSON.stringify(drag, null, 2));

        // 入口开关关闭：直达路由被拦截。
        fixture = useFixture({ flowEnabled: false });
        await cdp.navigate(`/drama-flow/${P}/shots`);
        const featureOff = await pollOrDiagnose(
            cdp,
            `document.body.innerText.includes("短剧新工作流") && !document.querySelector(".drama-flow-shell")`,
            "feature off",
            15000,
            "feature off",
            `({ shell: Boolean(document.querySelector(".drama-flow-shell")), mentions: document.body.innerText.includes("短剧新工作流"), headings: [...document.querySelectorAll("h1, h2, .ant-result-title")].map((el) => el.textContent.trim()).slice(0, 5) })`,
        );
        check(featureOff.ok, "开关关闭时直达路由被拦截、不进新工作流页面", featureOff.evidence);

        const writes = fixtures.flatMap((item) => item.forbidden);
        check(writes.length === 0, `没有任何生成或改项目的写请求（${fixtures.length} 个夹具实例合计）`, writes.map((call) => `${call.method} ${call.path}`).join(" | "));
        // R8-02 那一项故意让预览回 400（能力不支持），只放过这一次；多出来的同类错误或别的错误照样判失败。
        const handoffResponse = `409 POST /api/projects/${P}/units/${FIXTURE_UNIT_ID}/drama/steps/chapter.shots/run`;
        const expectedApiErrors = [`400 POST /api/projects/${P}/video-chains/preview`, handoffResponse, handoffResponse, handoffResponse];
        const problems = cdp.problems.filter((item) => {
            if (item.kind === "api" && item.text.startsWith("404")) return false;
            const expected = expectedApiErrors.indexOf(item.text);
            if (item.kind === "api" && expected >= 0) {
                expectedApiErrors.splice(expected, 1);
                return false;
            }
            return true;
        });
        check(!expectedApiErrors.length, "费用预览和交接确认的夹具响应逐次匹配");
        const unknown = cdp.problems.filter((item) => item.kind === "api" && item.text.startsWith("404"));
        if (unknown.length) console.log(`      夹具没覆盖的读接口：\n        ${[...new Set(unknown.map((item) => item.text))].join("\n        ")}`);
        check(!problems.length, "浏览器没有未捕获异常或 console error", problems.map((item) => `${item.kind}: ${item.text}`).join(" | "));
        // 只记录不判失败：老一些的 Chrome 返回 undefined；返回 Promise 的条件由组件回归（drama-flow-lifecycle）固定覆盖。
        console.log(`INFO  原生 scrollIntoView 返回 ${native.promise ? "Promise（D-00 线上触发条件在本浏览器成立）" : native.type}`);
    } finally {
        cdp?.close();
        for (const child of [browser, vite]) {
            if (!child || child.exitCode !== null) continue;
            if (process.platform === "win32") spawn("taskkill", ["/pid", String(child.pid), "/t", "/f"], { stdio: "ignore" });
            else child.kill("SIGTERM");
        }
        await sleep(800);
        try {
            rmSync(profile, { recursive: true, force: true });
        } catch {
            // Chrome 可能还占着文件；临时目录留给系统清理。
        }
    }
    if (outDir) writeFileSync(join(outDir, "drama-flow-e2e-results.json"), JSON.stringify({ results, failures }, null, 2));
    console.log(`\n${results.length - failures}/${results.length} passed`);
    process.exit(failures ? 1 : 0);
}

main().catch((error) => {
    console.error(error);
    process.exit(1);
});
