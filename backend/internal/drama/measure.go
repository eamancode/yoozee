package drama

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 成片门禁（与 yoozee-skills 的 gate.py 同一套阈值）：
// 画面与声音等长；无连续 2 秒低于 -45 dBFS 的无声洞；无单秒高于 -6 dBFS；整合响度 = 目标 ±1 LU；真峰值 ≤ -1.5 dBTP。
const (
	gateSampleRate    = 16000
	gateHoleDB        = -45.0
	gatePeakDB        = -6.0
	gateLoudnessTol   = 1.0
	gateTruePeakMax   = -1.5
	DefaultTargetLUFS = -16.0
	measureTimeout    = 4 * time.Minute
)

// GateCheck 是门禁的一项。
type GateCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// GateReport 是一次量数的结果。
type GateReport struct {
	Passed  bool           `json:"passed"`
	Checks  []GateCheck    `json:"checks"`
	Metrics map[string]any `json:"metrics"`
}

// Tools 是外部命令的位置（测试和部署可替换）。
type Tools struct {
	FFmpeg  string
	FFprobe string
}

func DefaultTools() (Tools, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return Tools{}, fmt.Errorf("服务器没有安装 ffmpeg，无法量成片")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return Tools{}, fmt.Errorf("服务器没有安装 ffprobe，无法量成片")
	}
	return Tools{FFmpeg: ffmpeg, FFprobe: ffprobe}, nil
}

type probeResult struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		Duration  string `json:"duration"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Rate      string `json:"r_frame_rate"`
	} `json:"streams"`
}

func run(ctx context.Context, bin string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func parseRate(rate string) float64 {
	num, den, ok := strings.Cut(rate, "/")
	n, _ := strconv.ParseFloat(num, 64)
	if !ok {
		return n
	}
	d, _ := strconv.ParseFloat(den, 64)
	if d == 0 {
		return 0
	}
	return n / d
}

var (
	reIntegrated = regexp.MustCompile(`I:\s+(-?[\d.]+) LUFS`)
	reTruePeak   = regexp.MustCompile(`Peak:\s+(-?[\d.]+) dBFS`)
	reLRA        = regexp.MustCompile(`LRA:\s+([\d.]+) LU`)
)

func lastMatch(re *regexp.Regexp, text string) (float64, bool) {
	all := re.FindAllStringSubmatch(text, -1)
	if len(all) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(all[len(all)-1][1], 64)
	return v, err == nil
}

// PerSecondDB 把单声道浮点采样按秒算 RMS 电平（与 measure.py per_second_db 一致）。
func PerSecondDB(samples []float32, sampleRate int) []float64 {
	var out []float64
	for i := 0; i+sampleRate < len(samples); i += sampleRate {
		var sum float64
		for _, s := range samples[i : i+sampleRate] {
			sum += float64(s) * float64(s)
		}
		out = append(out, 20*math.Log10(math.Sqrt(sum/float64(sampleRate))+1e-9))
	}
	return out
}

// HolesAndPeaks 找无声洞（连续 ≥2 秒低于阈值，返回 [起秒, 止秒]）和爆音秒。
func HolesAndPeaks(values []float64) ([][2]int, []int) {
	var holes [][2]int
	var peaks []int
	run := []int{}
	flush := func() {
		if len(run) >= 2 {
			holes = append(holes, [2]int{run[0], run[len(run)-1]})
		}
		run = run[:0]
	}
	for i, v := range values {
		if v < gateHoleDB {
			run = append(run, i)
		} else {
			flush()
		}
		if v > gatePeakDB {
			peaks = append(peaks, i)
		}
	}
	flush()
	return holes, peaks
}

// Gate 量一个成片或候选文件。target 为整合响度目标（LUFS）。
func Gate(ctx context.Context, tools Tools, path string, target float64) (GateReport, error) {
	ctx, cancel := context.WithTimeout(ctx, measureTimeout)
	defer cancel()
	report := GateReport{Passed: true, Metrics: map[string]any{"targetLufs": target}}
	check := func(name string, ok bool, detail string) {
		report.Checks = append(report.Checks, GateCheck{name, ok, detail})
		report.Passed = report.Passed && ok
	}
	raw, stderr, err := run(ctx, tools.FFprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	if err != nil {
		return GateReport{}, fmt.Errorf("读取媒体信息失败：%s", strings.TrimSpace(string(stderr)))
	}
	var probe probeResult
	if err := json.Unmarshal(raw, &probe); err != nil {
		return GateReport{}, fmt.Errorf("媒体信息格式错误：%w", err)
	}
	var videoDur, audioDur, fps float64
	hasVideo, hasAudio := false, false
	for _, s := range probe.Streams {
		d, _ := strconv.ParseFloat(s.Duration, 64)
		switch s.CodecType {
		case "video":
			hasVideo, videoDur, fps = true, d, parseRate(s.Rate)
			report.Metrics["width"], report.Metrics["height"] = s.Width, s.Height
		case "audio":
			hasAudio, audioDur = true, d
		}
	}
	formatDur, _ := strconv.ParseFloat(probe.Format.Duration, 64)
	report.Metrics["durationSec"] = math.Round(formatDur*1000) / 1000
	if !hasAudio {
		check("有声音", false, "文件里没有音轨")
		return report, nil
	}
	if hasVideo && fps > 0 && videoDur > 0 && audioDur > 0 {
		diff := math.Abs(videoDur - audioDur)
		check("画面与声音等长", diff <= 1.5/fps, fmt.Sprintf("画面 %.3f 秒、声音 %.3f 秒，差 %.3f 秒（≤ 1.5 帧）", videoDur, audioDur, diff))
	}
	pcm, stderr, err := run(ctx, tools.FFmpeg, "-v", "error", "-i", path, "-vn", "-ac", "1", "-ar", strconv.Itoa(gateSampleRate), "-f", "f32le", "-")
	if err != nil {
		return GateReport{}, fmt.Errorf("解不出声音：%s", strings.TrimSpace(string(stderr)))
	}
	samples := make([]float32, len(pcm)/4)
	if err := binary.Read(bytes.NewReader(pcm[:len(samples)*4]), binary.LittleEndian, samples); err != nil {
		return GateReport{}, err
	}
	values := PerSecondDB(samples, gateSampleRate)
	holes, peaks := HolesAndPeaks(values)
	check("无无声洞", len(holes) == 0, fmt.Sprintf("连续 2 秒低于 %.0f dBFS：%s", gateHoleDB, secondsList(holes)))
	check("无爆音", len(peaks) == 0, fmt.Sprintf("单秒高于 %.0f dBFS：%v", gatePeakDB, peaksText(peaks)))
	_, loud, err := run(ctx, tools.FFmpeg, "-hide_banner", "-nostats", "-i", path, "-af", "ebur128=peak=true", "-f", "null", "-")
	if err != nil {
		return GateReport{}, fmt.Errorf("量响度失败：%s", tailText(string(loud)))
	}
	lufs, okI := lastMatch(reIntegrated, string(loud))
	tp, okTP := lastMatch(reTruePeak, string(loud))
	lra, _ := lastMatch(reLRA, string(loud))
	report.Metrics["lufs"], report.Metrics["truePeak"], report.Metrics["lra"] = lufs, tp, lra
	report.Metrics["holes"], report.Metrics["peaks"] = holes, peaks
	check("整合响度", okI && math.Abs(lufs-target) <= gateLoudnessTol, fmt.Sprintf("%.1f LUFS（目标 %.0f ± %.0f）", lufs, target, gateLoudnessTol))
	check("真峰值", okTP && tp <= gateTruePeakMax, fmt.Sprintf("%.1f dBTP（≤ %.1f）", tp, gateTruePeakMax))
	return report, nil
}

// ContactSheet 抽帧拼成一张图（约 24 格，6 列），写到 outPath（PNG/JPG 按扩展名）。
func ContactSheet(ctx context.Context, tools Tools, path, outPath string) error {
	ctx, cancel := context.WithTimeout(ctx, measureTimeout)
	defer cancel()
	raw, stderr, err := run(ctx, tools.FFprobe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path)
	if err != nil {
		return fmt.Errorf("读取时长失败：%s", strings.TrimSpace(string(stderr)))
	}
	dur, _ := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	if dur <= 0 {
		return fmt.Errorf("媒体没有时长，无法抽帧")
	}
	rate := math.Min(8, math.Max(0.5, 24/dur))
	filter := fmt.Sprintf("fps=%.4f,scale=320:-2,tile=6x4:padding=4:color=0x111111", rate)
	_, stderr, err = run(ctx, tools.FFmpeg, "-v", "error", "-y", "-i", path, "-vf", filter, "-frames:v", "1", outPath)
	if err != nil {
		return fmt.Errorf("抽帧失败：%s", strings.TrimSpace(string(stderr)))
	}
	return nil
}

func secondsList(holes [][2]int) string {
	if len(holes) == 0 {
		return "无"
	}
	parts := make([]string, 0, len(holes))
	for _, h := range holes {
		parts = append(parts, fmt.Sprintf("%d–%d 秒", h[0], h[1]))
	}
	return strings.Join(parts, "、")
}

func peaksText(peaks []int) string {
	if len(peaks) == 0 {
		return "无"
	}
	parts := make([]string, 0, len(peaks))
	for _, p := range peaks {
		parts = append(parts, fmt.Sprintf("%d 秒", p))
	}
	return strings.Join(parts, "、")
}

func tailText(text string) string {
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > 200 {
		return string(r[len(r)-200:])
	}
	return text
}
