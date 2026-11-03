import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AssetDesignEvidence } from "../../src/pages/projects/workbench/detail/asset-design-evidence";

test("资产卡展示附件依据、关联和变化，不将它们拼成初始出图提示词", () => {
    const html = renderToStaticMarkup(
        <AssetDesignEvidence definition={{ extractionDesign: { props: ["旧保温杯"], evidence: [{ kind: "原文", location: "第六场", text: "一只兔子" }], states: [{ location: "五年后", trigger: "天天摸", state: "金属磨亮" }] } }} />,
    );
    for (const value of ["旧保温杯", "原文", "第六场", "一只兔子", "五年后", "金属磨亮"]) expect(html).toContain(value);
    expect(renderToStaticMarkup(<AssetDesignEvidence definition={{ appearance: "普通角色" }} />)).toBe("");
});
