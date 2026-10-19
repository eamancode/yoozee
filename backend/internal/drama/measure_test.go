package drama

import (
	"context"
	"encoding/json"
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHolesAndPeaks(t *testing.T) {
	holes, peaks := HolesAndPeaks([]float64{-20, -50, -60, -50, -20, -3, -50, -10})
	if len(holes) != 1 || holes[0] != [2]int{1, 3} {
		t.Fatalf("holes = %v", holes)
	}
	if len(peaks) != 1 || peaks[0] != 5 {
		t.Fatalf("peaks = %v", peaks)
	}
}

func TestLoudnessOfSilentTrack(t *testing.T) {
	// ffmpeg ebur128 对一条完全无声的音轨的摘要。
	summary := `[Parsed_ebur128_0 @ 0x0] Summary:

  Integrated loudness:
    I:         -70.0 LUFS
    Threshold:   0.0 LUFS

  Loudness range:
    LRA:         0.0 LU
    Threshold:   0.0 LUFS
    LRA low:     0.0 LUFS
    LRA high:    0.0 LUFS

  True peak:
    Peak:       -inf dBFS`
	reading := parseLoudness(summary)
	if !reading.HasTruePeak || !math.IsInf(reading.TruePeak, -1) {
		t.Fatalf("真峰值应读成 -inf: %+v", reading)
	}
	if !reading.Silent() {
		t.Fatalf("应判为无声: %+v", reading)
	}
	ok, detail := truePeakCheck(reading)
	if !ok || !strings.Contains(detail, "无声") || strings.Contains(detail, "0.0 dBTP") {
		t.Fatalf("无声不能报 0.0 dBTP: ok=%v detail=%q", ok, detail)
	}
	if detail := loudnessDetail(reading, -16); !strings.Contains(detail, "无声") {
		t.Fatalf("响度明细应标无声: %q", detail)
	}
	if finiteOrNil(reading.TruePeak) != nil {
		t.Fatal("-inf 必须以 null 写进 JSON")
	}
	if _, err := json.Marshal(map[string]any{"truePeak": finiteOrNil(reading.TruePeak)}); err != nil {
		t.Fatal(err)
	}
}

func TestLoudnessMissingTruePeak(t *testing.T) {
	reading := parseLoudness("    I:         -16.2 LUFS\n")
	if ok, detail := truePeakCheck(reading); ok || detail != "没量到真峰值" {
		t.Fatalf("量不到真峰值时不能当成 0 dBTP: ok=%v detail=%q", ok, detail)
	}
	reading = parseLoudness("    I:   -16.2 LUFS\n    Peak:   -0.4 dBFS\n")
	if ok, detail := truePeakCheck(reading); ok || !strings.Contains(detail, "-0.4 dBTP") {
		t.Fatalf("超限真峰值应不通过: ok=%v detail=%q", ok, detail)
	}
}

func TestCER(t *testing.T) {
	if got := CER("七床？……七床的人呢？", "七床七床的人呢"); got != 0 {
		t.Fatalf("标点不应计入字错率: %v", got)
	}
	if got := CER("你好世界", "你好时间"); got != 0.5 {
		t.Fatalf("CER = %v", got)
	}
}

func TestGateAndContactSheetOnGeneratedClip(t *testing.T) {
	tools, err := DefaultTools()
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	// 6 秒测试片：彩条画面 + 440Hz 正弦（无无声洞、无爆音）。
	cmd := exec.Command(tools.FFmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=24:duration=6", "-f", "lavfi", "-i", "sine=frequency=440:duration=6:sample_rate=48000", "-af", "volume=-18dB", "-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", clip)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("无法生成测试片: %v %s", err, out)
	}
	report, err := Gate(context.Background(), tools, clip, -16)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, c := range report.Checks {
		names[c.Name] = c.OK
	}
	if !names["无无声洞"] || !names["无爆音"] || !names["画面与声音等长"] {
		t.Fatalf("测试片的基本检查应通过: %+v", report.Checks)
	}
	if _, ok := report.Metrics["lufs"].(float64); !ok {
		t.Fatalf("应量出整合响度: %+v", report.Metrics)
	}
	sheet := filepath.Join(dir, "sheet.jpg")
	if err := ContactSheet(context.Background(), tools, clip, sheet); err != nil {
		t.Fatal(err)
	}
}
