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

import { createDramaFlowFixture, FIXTURE_ASSET_ID, FIXTURE_ASSET_ID_2, FIXTURE_PROJECT_ID as P, FIXTURE_SHOT_ID, FIXTURE_SHOT_ID_2, FIXTURE_VIDEO_MODEL_NAME } from "../test/fixtures/drama-flow-api-fixture.ts";

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
    const child = spawn("bunx", ["vite", "--host", "127.0.0.1", "--port", String(port), "--strictPort"], { cwd: process.cwd(), stdio: ["ignore", "pipe", "pipe"], shell: process.platform === "win32" });
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
            void send("Fetch.fulfillRequest", {
                requestId: params.requestId,
                responseCode: result.status,
                responseHeaders: [{ name: "Content-Type", value: "application/json; charset=utf-8" }],
                body: Buffer.from(JSON.stringify(result.body)).toString("base64"),
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
const summary = (label) => `[...document.querySelectorAll("dl.drama-flow-summary > div")].find((row) => row.querySelector("dt")?.textContent === ${JSON.stringify(label)})?.querySelector("dd")?.textContent`;
const info = (label) => `[...document.querySelectorAll("dl.drama-flow-info-facts dt")].find((dt) => dt.textContent === ${JSON.stringify(label)})?.nextElementSibling?.textContent`;

async function main() {
    const chrome = CHROME_CANDIDATES.find((candidate) => existsSync(candidate));
    if (!chrome) throw new Error(`No Chrome found. Set CHROME_BIN. Tried:\n  ${CHROME_CANDIDATES.join("\n  ")}`);
    const outDir = process.env.DRAMA_FLOW_E2E_OUT || "";
    const vitePort = await freePort();
    const cdpPort = await freePort();
    const profile = mkdtempSync(join(tmpdir(), "drama-flow-e2e-"));
    const origin = `http://127.0.0.1:${vitePort}`;
    let fixture = createDramaFlowFixture();
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
        // 底栏主操作在页面最右下角，默认位置的柚智浮层不能盖住它（真人点下去会打开柚智）。
        const barButton = `[...document.querySelectorAll(".drama-flow-bar-actions button")].at(-1)`;
        const barHit = await cdp.evaluate(
            `(() => { const el = ${barButton}; if (!el) return "no-button"; const r = el.getBoundingClientRect(); const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2); return hit === el || el.contains(hit) ? "ok" : String(hit?.className || hit?.tagName).slice(0, 80); })()`,
        );
        check(barHit === "ok" || barHit === "no-button", "底栏主操作没被右下角浮层盖住", barHit);
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
        check(!(await cdp.evaluate(crashed)), "出片详情选择、切换、关闭不崩");

        // D-02 / D-03：项目信息抽屉。
        await cdp.navigate(`/drama-flow/${P}?panel=info`);
        check(await cdp.poll(`${info("最近 100 次运行")} === "0 积分 · 暂无消费"`, "info cost"), "D-02 读到空运行记录写「0 积分 · 暂无消费」");
        check(await cdp.poll(`${info("视频模型")} === ${JSON.stringify(FIXTURE_VIDEO_MODEL_NAME)}`, "info model"), "D-03 项目信息里视频模型显示名字");
        check(await cdp.evaluate(`!document.querySelector(".drama-flow-info")?.textContent.includes("CHANNEL_000001")`), "D-03 项目信息不显示内部渠道 ID");
        if (outDir) await cdp.shot(join(outDir, "info-panel.png"));
        await cdp.navigate("/projects");
        await sleep(800);

        // 入口开关关闭：直达路由被拦截。
        fixture = createDramaFlowFixture({ flowEnabled: false });
        await cdp.navigate(`/drama-flow/${P}/shots`);
        check(await cdp.poll(`document.body.innerText.includes("短剧新工作流") && !document.querySelector(".drama-flow-shell")`, "feature off"), "开关关闭时直达路由被拦截、不进新工作流页面");

        const writes = fixture.forbidden.length;
        check(writes === 0, "没有任何生成或改项目的写请求");
        const problems = cdp.problems.filter((item) => item.kind !== "api" || !item.text.startsWith("404"));
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
