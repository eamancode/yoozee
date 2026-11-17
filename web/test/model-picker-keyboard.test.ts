import { expect, test } from "bun:test";
import { resolve } from "node:path";

test("模型选择器在目录异步更新时保留键盘操作", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/model-picker-keyboard.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), timeout: 30000 });
    expect(result.exitCode, result.stderr.toString()).toBe(0);
}, 35000);
