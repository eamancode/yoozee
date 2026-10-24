# 评测章节

每章一个目录，放 project.json、unit.json、assets.json（接口 JSON）。重新导出：

    go test ./internal/app -run TestDramaEvalSetFixtures -update-drama-evalset

种子在 internal/app/testdata/drama-eval-seeds/（七床用 testdata/qichuang-chapter-shots/）。
