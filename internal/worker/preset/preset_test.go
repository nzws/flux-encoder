package preset

import (
	"testing"
)

const (
	expectedMP4Extension     = "mp4"
	expectedOutputTypeSingle = "single"
)

func Test存在するプリセットをGetで取得できる(t *testing.T) {
	testCases := []struct {
		name string
	}{
		{"720p_h264"},
		{"1080p_h264"},
		{"480p_h264"},
		{"hls_720p"},
		{"hls_720p_abr"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			preset, err := Get(tc.name)
			if err != nil {
				t.Errorf("プリセット '%s' の取得に失敗: %v", tc.name, err)
			}
			if preset.Name != tc.name {
				t.Errorf("プリセット名が一致しない: 期待値 %s, 取得値 %s", tc.name, preset.Name)
			}
		})
	}
}

func Test存在しないプリセットをGetするとエラーが返る(t *testing.T) {
	testCases := []string{
		"存在しないプリセット",
		"4k_h265",
		"",
		"720p",
	}

	for _, name := range testCases {
		t.Run("存在しないプリセット_"+name, func(t *testing.T) {
			_, err := Get(name)
			if err == nil {
				t.Errorf("存在しないプリセット '%s' でエラーが返されるべき", name)
			}
		})
	}
}

func TestListですべてのプリセットが返される(t *testing.T) {
	list := List()

	// 期待されるプリセット数
	expectedCount := 7
	if len(list) != expectedCount {
		t.Errorf("プリセット数が一致しない: 期待値 %d, 取得値 %d", expectedCount, len(list))
	}

	// すべてのプリセットが含まれているか確認
	expectedNames := []string{
		"720p_h264", "1080p_h264", "480p_h264",
		"hls_720p", "hls_720p_video_only",
		"hls_720p_abr", "hls_720p_abr_video_only",
	}
	foundNames := make(map[string]bool)
	for _, p := range list {
		foundNames[p.Name] = true
	}

	for _, name := range expectedNames {
		if !foundNames[name] {
			t.Errorf("プリセット '%s' がリストに含まれていない", name)
		}
	}
}

func TestExistsが正しく動作する(t *testing.T) {
	testCases := []struct {
		name   string
		exists bool
	}{
		{"720p_h264", true},
		{"1080p_h264", true},
		{"480p_h264", true},
		{"hls_720p", true},
		{"hls_720p_abr", true},
		{"存在しないプリセット", false},
		{"4k_h265", false},
		{"", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			exists := Exists(tc.name)
			if exists != tc.exists {
				t.Errorf("Exists('%s') = %v, 期待値: %v", tc.name, exists, tc.exists)
			}
		})
	}
}

func Test720p_h264プリセットのフィールドが正しい(t *testing.T) {
	preset, err := Get("720p_h264")
	if err != nil {
		t.Fatalf("プリセットの取得に失敗: %v", err)
	}

	if preset.Name != "720p_h264" {
		t.Errorf("Name が一致しない: %s", preset.Name)
	}
	if preset.Extension != expectedMP4Extension {
		t.Errorf("Extension が一致しない: %s", preset.Extension)
	}
	if preset.OutputType != expectedOutputTypeSingle {
		t.Errorf("OutputType が一致しない: %s", preset.OutputType)
	}
	if len(preset.FFmpegArgs) == 0 {
		t.Error("FFmpegArgs が空")
	}
	if preset.Description == "" {
		t.Error("Description が空")
	}
}

func Test1080p_h264プリセットのフィールドが正しい(t *testing.T) {
	preset, err := Get("1080p_h264")
	if err != nil {
		t.Fatalf("プリセットの取得に失敗: %v", err)
	}

	if preset.Name != "1080p_h264" {
		t.Errorf("Name が一致しない: %s", preset.Name)
	}
	if preset.Extension != expectedMP4Extension {
		t.Errorf("Extension が一致しない: %s", preset.Extension)
	}
	if preset.OutputType != expectedOutputTypeSingle {
		t.Errorf("OutputType が一致しない: %s", preset.OutputType)
	}
}

func Test480p_h264プリセットのフィールドが正しい(t *testing.T) {
	preset, err := Get("480p_h264")
	if err != nil {
		t.Fatalf("プリセットの取得に失敗: %v", err)
	}

	if preset.Name != "480p_h264" {
		t.Errorf("Name が一致しない: %s", preset.Name)
	}
	if preset.Extension != expectedMP4Extension {
		t.Errorf("Extension が一致しない: %s", preset.Extension)
	}
	if preset.OutputType != expectedOutputTypeSingle {
		t.Errorf("OutputType が一致しない: %s", preset.OutputType)
	}
}

func TestHLS720pプリセットのフィールドが正しい(t *testing.T) {
	preset, err := Get("hls_720p")
	if err != nil {
		t.Fatalf("プリセットの取得に失敗: %v", err)
	}

	if preset.Name != "hls_720p" {
		t.Errorf("Name が一致しない: %s", preset.Name)
	}
	if preset.Extension != "m3u8" {
		t.Errorf("Extension が一致しない: %s", preset.Extension)
	}
	if preset.OutputType != "hls" {
		t.Errorf("OutputType が一致しない: %s", preset.OutputType)
	}
	if len(preset.OutputFiles) == 0 {
		t.Error("OutputFiles が空")
	}
}

func TestHLS720pABRプリセットのフィールドが正しい(t *testing.T) {
	preset, err := Get("hls_720p_abr")
	if err != nil {
		t.Fatalf("プリセットの取得に失敗: %v", err)
	}

	if preset.Name != "hls_720p_abr" {
		t.Errorf("Name が一致しない: %s", preset.Name)
	}
	if preset.Extension != "m3u8" {
		t.Errorf("Extension が一致しない: %s", preset.Extension)
	}
	if preset.OutputType != "hls" {
		t.Errorf("OutputType が一致しない: %s", preset.OutputType)
	}
	if len(preset.OutputFiles) == 0 {
		t.Error("OutputFiles が空")
	}

	// ABR は master.m3u8 を含むはず
	hasMaster := false
	for _, file := range preset.OutputFiles {
		if file == "master.m3u8" {
			hasMaster = true
			break
		}
	}
	if !hasMaster {
		t.Error("hls_720p_abr の OutputFiles に master.m3u8 が含まれていない")
	}
}

func TestすべてのプリセットがFFmpegArgsを持っている(t *testing.T) {
	list := List()
	for _, preset := range list {
		if len(preset.FFmpegArgs) == 0 {
			t.Errorf("プリセット '%s' が FFmpegArgs を持っていない", preset.Name)
		}
	}
}

func TestすべてのプリセットがDescriptionを持っている(t *testing.T) {
	list := List()
	for _, preset := range list {
		if preset.Description == "" {
			t.Errorf("プリセット '%s' が Description を持っていない", preset.Name)
		}
	}
}

func TestすべてのプリセットがExtensionを持っている(t *testing.T) {
	list := List()
	for _, preset := range list {
		if preset.Extension == "" {
			t.Errorf("プリセット '%s' が Extension を持っていない", preset.Name)
		}
	}
}

func TestOutputTypeが適切に設定されている(t *testing.T) {
	testCases := []struct {
		name       string
		outputType string
	}{
		{"720p_h264", "single"},
		{"1080p_h264", "single"},
		{"480p_h264", "single"},
		{"hls_720p", "hls"},
		{"hls_720p_abr", "hls"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			preset, err := Get(tc.name)
			if err != nil {
				t.Fatalf("プリセットの取得に失敗: %v", err)
			}
			if preset.OutputType != tc.outputType {
				t.Errorf("OutputType が一致しない: 期待値 %s, 取得値 %s", tc.outputType, preset.OutputType)
			}
		})
	}
}

func TestHLSプリセットがOutputFileNameを持っている(t *testing.T) {
	testCases := []struct {
		name             string
		expectedFileName string
	}{
		{"hls_720p", "playlist.m3u8"},
		{"hls_720p_abr", "stream_%v.m3u8"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			preset, err := Get(tc.name)
			if err != nil {
				t.Fatalf("プリセットの取得に失敗: %v", err)
			}

			if preset.OutputFileName == "" {
				t.Error("HLSプリセットが OutputFileName を持っていない")
			}

			if preset.OutputFileName != tc.expectedFileName {
				t.Errorf("OutputFileName が一致しない: 期待値 %s, 取得値 %s",
					tc.expectedFileName, preset.OutputFileName)
			}
		})
	}
}

func Test単一ファイルプリセットはOutputFileNameが空(t *testing.T) {
	singleFilePresets := []string{"720p_h264", "1080p_h264", "480p_h264"}

	for _, name := range singleFilePresets {
		t.Run(name, func(t *testing.T) {
			preset, err := Get(name)
			if err != nil {
				t.Fatalf("プリセットの取得に失敗: %v", err)
			}

			if preset.OutputFileName != "" {
				t.Errorf("単一ファイルプリセットの OutputFileName は空であるべき: 取得値 %s",
					preset.OutputFileName)
			}
		})
	}
}

func TestHLS単一バリアントとマルチバリアントのOutputFileNameが異なる(t *testing.T) {
	hls720p, err := Get("hls_720p")
	if err != nil {
		t.Fatalf("hls_720p プリセットの取得に失敗: %v", err)
	}

	hls720pAbr, err := Get("hls_720p_abr")
	if err != nil {
		t.Fatalf("hls_720p_abr プリセットの取得に失敗: %v", err)
	}

	if hls720p.OutputFileName == hls720pAbr.OutputFileName {
		t.Error("単一バリアントとマルチバリアントの OutputFileName が同じ")
	}

	// 単一バリアントは playlist.m3u8
	if hls720p.OutputFileName != "playlist.m3u8" {
		t.Errorf("hls_720p の OutputFileName が正しくない: %s", hls720p.OutputFileName)
	}

	// マルチバリアントは stream_%%v.m3u8
	if hls720pAbr.OutputFileName != "stream_%v.m3u8" {
		t.Errorf("hls_720p_abr の OutputFileName が正しくない: %s", hls720pAbr.OutputFileName)
	}
}
