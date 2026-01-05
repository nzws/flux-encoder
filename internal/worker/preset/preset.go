package preset

import (
	"fmt"
)

// Preset はエンコード設定のプリセット
type Preset struct {
	Name           string   // プリセット名
	Description    string   // 説明
	FFmpegArgs     []string // ffmpeg引数
	Extension      string   // 出力ファイル拡張子
	OutputType     string   // 出力タイプ: "single" (default), "hls", "dash"
	OutputFileName string   // 出力ファイル名（HLS/DASH用、%vはバリアント番号のプレースホルダー）
	OutputFiles    []string // 生成されるファイルのパターン（マルチファイル出力用）
}

var (
	// presets は利用可能なプリセットのマップ
	presets = map[string]Preset{
		"720p_h264": {
			Name:        "720p_h264",
			Description: "HD 720p with H.264 encoding",
			FFmpegArgs: []string{
				"-vf", "scale=-2:720", // 720p にスケール
				"-c:v", "libx264", // H.264 コーデック
				"-preset", "medium", // エンコード速度
				"-crf", "23", // 品質（18-28, 低いほど高品質）
				"-c:a", "aac", // 音声コーデック
				"-b:a", "128k", // 音声ビットレート
				"-movflags", "+faststart", // ストリーミング最適化
			},
			Extension:  "mp4",
			OutputType: "single",
		},
		"1080p_h264": {
			Name:        "1080p_h264",
			Description: "Full HD 1080p with H.264 encoding",
			FFmpegArgs: []string{
				"-vf", "scale=-2:1080",
				"-c:v", "libx264",
				"-preset", "medium",
				"-crf", "23",
				"-c:a", "aac",
				"-b:a", "192k",
				"-movflags", "+faststart",
			},
			Extension:  "mp4",
			OutputType: "single",
		},
		"480p_h264": {
			Name:        "480p_h264",
			Description: "SD 480p with H.264 encoding",
			FFmpegArgs: []string{
				"-vf", "scale=-2:480",
				"-c:v", "libx264",
				"-preset", "fast",
				"-crf", "24",
				"-c:a", "aac",
				"-b:a", "96k",
				"-movflags", "+faststart",
			},
			Extension:  "mp4",
			OutputType: "single",
		},
		"hls_720p_video_only": {
			Name:        "hls_720p_video_only",
			Description: "HLS 720p single variant - Video only",
			FFmpegArgs: []string{
				"-vf", "scale=-2:720",
				"-c:v", "libx264",
				"-b:v", "2500k",
				"-f", "hls",
				"-hls_time", "6",
				"-hls_playlist_type", "vod",
				"-hls_segment_filename", "segment_%03d.ts",
			},
			Extension:      "m3u8",
			OutputType:     "hls",
			OutputFileName: "playlist.m3u8",
			OutputFiles: []string{
				"playlist.m3u8",
				"segment_*.ts",
			},
		},
		"hls_720p": {
			Name:        "hls_720p",
			Description: "HLS 720p single variant - With audio",
			FFmpegArgs: []string{
				"-vf", "scale=-2:720",
				"-c:v", "libx264",
				"-b:v", "2500k",
				"-c:a", "aac",
				"-b:a", "128k",
				"-f", "hls",
				"-hls_time", "6",
				"-hls_playlist_type", "vod",
				"-hls_segment_filename", "segment_%03d.ts",
			},
			Extension:      "m3u8",
			OutputType:     "hls",
			OutputFileName: "playlist.m3u8",
			OutputFiles: []string{
				"playlist.m3u8",
				"segment_*.ts",
			},
		},
		"hls_720p_abr_video_only": {
			Name:        "hls_720p_abr_video_only",
			Description: "HLS with 3 quality variants (720p, 480p, 360p) - Video only",
			FFmpegArgs: []string{
				// 3つの品質バリアント
				"-filter_complex",
				"[0:v]split=3[v1][v2][v3];" +
					"[v1]scale=w=1280:h=720[v1out];" +
					"[v2]scale=w=854:h=480[v2out];" +
					"[v3]scale=w=640:h=360[v3out]",
				// 720p variant
				"-map", "[v1out]",
				"-c:v:0", "libx264",
				"-b:v:0", "2800k",
				"-maxrate:v:0", "3000k",
				"-bufsize:v:0", "6000k",
				// 480p variant
				"-map", "[v2out]",
				"-c:v:1", "libx264",
				"-b:v:1", "1400k",
				"-maxrate:v:1", "1500k",
				"-bufsize:v:1", "3000k",
				// 360p variant
				"-map", "[v3out]",
				"-c:v:2", "libx264",
				"-b:v:2", "800k",
				"-maxrate:v:2", "900k",
				"-bufsize:v:2", "1800k",
				// HLS設定（映像のみ）
				"-f", "hls",
				"-hls_time", "6",
				"-hls_playlist_type", "vod",
				"-hls_segment_filename", "segment_%v_%03d.ts",
				"-master_pl_name", "master.m3u8",
				"-var_stream_map", "v:0 v:1 v:2",
				"-hls_segment_type", "mpegts",
			},
			Extension:      "m3u8",
			OutputType:     "hls",
			OutputFileName: "stream_%v.m3u8",
			OutputFiles: []string{
				"master.m3u8",
				"stream_*.m3u8",
				"segment_*_*.ts",
			},
		},
		"hls_720p_abr": {
			Name:        "hls_720p_abr",
			Description: "HLS with 3 quality variants (720p, 480p, 360p) - With audio",
			FFmpegArgs: []string{
				// 3つの品質バリアント
				"-filter_complex",
				"[0:v]split=3[v1][v2][v3];" +
					"[v1]scale=w=1280:h=720[v1out];" +
					"[v2]scale=w=854:h=480[v2out];" +
					"[v3]scale=w=640:h=360[v3out]",
				// 720p variant
				"-map", "[v1out]",
				"-c:v:0", "libx264",
				"-b:v:0", "2800k",
				"-maxrate:v:0", "3000k",
				"-bufsize:v:0", "6000k",
				// 480p variant
				"-map", "[v2out]",
				"-c:v:1", "libx264",
				"-b:v:1", "1400k",
				"-maxrate:v:1", "1500k",
				"-bufsize:v:1", "3000k",
				// 360p variant
				"-map", "[v3out]",
				"-c:v:2", "libx264",
				"-b:v:2", "800k",
				"-maxrate:v:2", "900k",
				"-bufsize:v:2", "1800k",
				// オーディオ（各バリアント用に3回マップ）
				"-map", "a:0",
				"-map", "a:0",
				"-map", "a:0",
				"-c:a", "aac",
				"-b:a", "128k",
				"-ac", "2",
				// HLS設定
				"-f", "hls",
				"-hls_time", "6",
				"-hls_playlist_type", "vod",
				"-hls_segment_filename", "segment_%v_%03d.ts",
				"-master_pl_name", "master.m3u8",
				"-var_stream_map", "v:0,a:0 v:1,a:1 v:2,a:2",
				"-hls_segment_type", "mpegts",
			},
			Extension:      "m3u8",
			OutputType:     "hls",
			OutputFileName: "stream_%v.m3u8",
			OutputFiles: []string{
				"master.m3u8",
				"stream_*.m3u8",
				"segment_*_*.ts",
			},
		},
	}
)

// Get は指定されたプリセット名のプリセットを返す
func Get(name string) (Preset, error) {
	preset, ok := presets[name]
	if !ok {
		return Preset{}, fmt.Errorf("preset not found: %s", name)
	}
	return preset, nil
}

// List は利用可能なすべてのプリセットを返す
func List() []Preset {
	result := make([]Preset, 0, len(presets))
	for _, p := range presets {
		result = append(result, p)
	}
	return result
}

// Exists は指定されたプリセット名が存在するかチェックする
func Exists(name string) bool {
	_, ok := presets[name]
	return ok
}
