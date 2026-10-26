# 评测章节

每章一个目录，放 project.json、unit.json、assets.json（接口 JSON）。重新导出：

    go test ./internal/app -run TestDramaEvalSetFixtures -update-drama-evalset

种子在 internal/app/testdata/drama-eval-seeds/（七床用 testdata/qichuang-chapter-shots/）。

qichuang-novel-c01 是剧本优化（script.adapt）的评测章节：七床第一集剧本改写成的小说体正文，手写维护，不由上面的命令导出。
