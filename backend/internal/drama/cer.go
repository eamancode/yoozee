package drama

import (
	"regexp"
)

// 与 yoozee-skills 的 qa.py stt 一致：去掉空白和中英文标点后按字算编辑距离。
var cerStrip = regexp.MustCompile(`[\s，。、！？；：,.!?;:“”"'‘’…—\-（）()《》\[\]【】]`)

// DefaultCERThreshold 是台词过审的默认字错率门槛。
const DefaultCERThreshold = 0.05

// CER 计算听写结果相对期望台词的字错率；期望为空时返回 0。
func CER(expected, hypothesis string) float64 {
	ref := []rune(cerStrip.ReplaceAllString(expected, ""))
	hyp := []rune(cerStrip.ReplaceAllString(hypothesis, ""))
	if len(ref) == 0 {
		return 0
	}
	dp := make([]int, len(hyp)+1)
	for j := range dp {
		dp[j] = j
	}
	for i := 1; i <= len(ref); i++ {
		prev := dp[0]
		dp[0] = i
		for j := 1; j <= len(hyp); j++ {
			cur := dp[j]
			cost := 1
			if ref[i-1] == hyp[j-1] {
				cost = 0
			}
			dp[j] = min(dp[j]+1, dp[j-1]+1, prev+cost)
			prev = cur
		}
	}
	return float64(dp[len(hyp)]) / float64(len(ref))
}
