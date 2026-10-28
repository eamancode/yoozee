import assert from "node:assert/strict";
import test from "node:test";
import { taskBillingCharge } from "./task-center";

test("taskBillingCharge 结算后显示实扣，与预授权不同时附上预授权", () => {
    assert.deepEqual(taskBillingCharge({ amountMicrocredits: 74_850, actualAmountMicrocredits: 1_054_252, status: "settled" }), { amountMicrocredits: 1_054_252, preAuthMicrocredits: 74_850 });
    assert.deepEqual(taskBillingCharge({ amountMicrocredits: 2_000_000, actualAmountMicrocredits: 2_000_000, status: "settled" }), { amountMicrocredits: 2_000_000, preAuthMicrocredits: undefined });
    assert.deepEqual(taskBillingCharge({ amountMicrocredits: 74_850, status: "running" }), { amountMicrocredits: 74_850 });
    assert.deepEqual(taskBillingCharge({ amountMicrocredits: 74_850, status: "settled" }), { amountMicrocredits: 74_850 });
});
