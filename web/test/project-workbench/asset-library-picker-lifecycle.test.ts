import { expect, test } from "bun:test";

test("real picker retains uncached selections and prevents duplicate writes", () => {
    const run = Bun.spawnSync([process.execPath, "run", "test/fixtures/asset-library-picker.harness.tsx"], { cwd: process.cwd(), stdout: "pipe", stderr: "pipe", timeout: 30000 });
    const output = new TextDecoder().decode(run.stdout) + new TextDecoder().decode(run.stderr);
    expect(output).toContain("PASS: remote selection");
    expect(run.exitCode).toBe(0);
}, 35000);
