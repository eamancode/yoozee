# 评测章节种子

M6 第三期流程技能包评测集（`backend/internal/drama/steps.go` 的 `EvalSet`）里四个测试章节的种子：每章一个目录，`seed.json`（项目、章节标题、确认成资产的角色 / 场景 / 道具、待确认角色）和 `chapter.txt`（章节正文）。七床第一章用 `testdata/qichuang-chapter-shots/`。

来源是本机 `.local/yoozee-test/<项目>`（yoozee 2.1 四类短剧测试项目，不进仓库）：

    python build_seed.py <测试项目目录> <画风快照 JSON> <输出目录>

`script_to_chapter.py` 把 `剧本/剧本.md` 第 1 集转成章节正文（与七床 chapter.txt 同一写法，七床逐字一致）；资产来自 `剧本/资产圣经.json`。画风快照是前端预设编译器的输出（`customCanvasStylePreset(id).profile` 经 `serializeStyleProfile`）：

| 章节 | 预设 |
| --- | --- |
| dadou-e01 | `v2-urban--epic--live-action--realistic` |
| fenshou-e01 | `v2-urban--romantic--live-action--realistic` |
| kehuan-e01 | `v2-science-fiction--epic--3d-anime--semi-real` |
| xuanyi-e01 | `v2-suspense--dark--live-action--realistic` |

改了种子后重新导出评测数据：

    go test ./internal/app -run TestDramaEvalSetFixtures -update-drama-evalset
