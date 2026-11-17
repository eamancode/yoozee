import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { EpisodePredecessor } from "../src/pages/projects/workbench/detail/episode-predecessor";

const unit = (id: string, kind: string, position: number, title: string) => ({ id, kind, position, title });
const one = unit("ep-z", "episode", 10, "来信");
const two = unit("ep-a", "episode", 80, "会面");
const three = unit("ep-q", "episode", 90, "回程");
const chapter = unit("src", "", 85, "原文章节");
const cards = [
    { unitId: one.id, content: "车票在包里", version: 2 },
    { unitId: two.id, content: "雨已停", version: 1 },
    { unitId: chapter.id, content: "原文卡", version: 3 },
];

test("删除中间集后，当前前序和卡片正文按剩余剧集稳定 ID 更新", () => {
    const before = renderToStaticMarkup(<EpisodePredecessor units={[three, one, chapter, two]} unitId={three.id} handoffs={cards} />);
    expect(before).toContain("第 2 集《会面》");
    expect(before).toContain("雨已停");
    // 模拟单元列表已刷新，导演文档缓存里暂时仍有被删集的卡。
    const after = renderToStaticMarkup(<EpisodePredecessor units={[three, chapter, one]} unitId={three.id} handoffs={cards} />);
    expect(after).toContain("第 1 集《来信》");
    expect(after).toContain("第 2 版");
    expect(after).toContain("车票在包里");
    expect(after).not.toContain("雨已停");
    expect(after).not.toContain("原文卡");
});

test("新集尚无导演文档时区分前序缺卡、首集和旧版原文章节", () => {
    const pending = renderToStaticMarkup(<EpisodePredecessor units={[three, chapter, one]} unitId={three.id} handoffs={cards.slice(1)} />);
    expect(pending).toContain("第 1 集《来信》");
    expect(pending).toContain("前序交接卡待更新");
    const first = renderToStaticMarkup(<EpisodePredecessor units={[three, chapter]} unitId={three.id} handoffs={cards} />);
    expect(first).toContain("当前为首集");
    expect(first).not.toContain("前序交接卡待更新");
    expect(renderToStaticMarkup(<EpisodePredecessor units={[chapter]} unitId={chapter.id} handoffs={cards} />)).toBe("");
});
