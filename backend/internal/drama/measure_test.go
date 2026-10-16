package drama

import (
	"context"
	"os/exec"
	"path/filepath"
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
