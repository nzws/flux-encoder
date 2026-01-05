package validator

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// FFProbe はffprobeコマンドのラッパー
type FFProbe struct {
	execPath string
}

// NewFFProbe は新しいFFProbeを作成する
func NewFFProbe() *FFProbe {
	return &FFProbe{
		execPath: "ffprobe",
	}
}

// ffprobeOutput はffprobeのJSON出力形式
type ffprobeOutput struct {
	Format  ffprobeFormat   `json:"format"`
	Streams []ffprobeStream `json:"streams"`
}

type ffprobeFormat struct {
	Filename   string `json:"filename"`
	FormatName string `json:"format_name"`
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	BitRate    string `json:"bit_rate"`
}

type ffprobeStream struct {
	Index         int    `json:"index"`
	CodecName     string `json:"codec_name"`
	CodecType     string `json:"codec_type"`
	CodecLongName string `json:"codec_long_name"`
	Profile       string `json:"profile"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	PixFmt        string `json:"pix_fmt"`
	SampleRate    string `json:"sample_rate"`
	Channels      int    `json:"channels"`
	ChannelLayout string `json:"channel_layout"`
	BitRate       string `json:"bit_rate"`
	RFrameRate    string `json:"r_frame_rate"`
	AvgFrameRate  string `json:"avg_frame_rate"`
}

// GetMediaInfo はメディアファイルの情報を取得する
func (f *FFProbe) GetMediaInfo(ctx context.Context, filePath string) (*MediaInfo, error) {
	cmd := exec.CommandContext(ctx, f.execPath,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		filePath,
	)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("ffprobe failed: %w, stderr: %s", err, string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var probeOutput ffprobeOutput
	if err := json.Unmarshal(output, &probeOutput); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	return f.convertToMediaInfo(&probeOutput)
}

// convertToMediaInfo はffprobeの出力をMediaInfoに変換する
func (f *FFProbe) convertToMediaInfo(output *ffprobeOutput) (*MediaInfo, error) {
	mediaInfo := &MediaInfo{
		Format: output.Format.FormatName,
	}

	f.applyFormatInfo(mediaInfo, output.Format)

	// Streams
	for _, stream := range output.Streams {
		f.appendStream(mediaInfo, stream)
	}

	return mediaInfo, nil
}

func (f *FFProbe) applyFormatInfo(mediaInfo *MediaInfo, format ffprobeFormat) {
	if duration, ok := parseFloat(format.Duration); ok {
		mediaInfo.Duration = duration
	}
	if size, ok := parseInt64(format.Size); ok {
		mediaInfo.Size = size
	}
	if bitrate, ok := parseInt64(format.BitRate); ok {
		mediaInfo.Bitrate = bitrate
	}
}

func (f *FFProbe) appendStream(mediaInfo *MediaInfo, stream ffprobeStream) {
	switch stream.CodecType {
	case "video":
		mediaInfo.VideoStreams = append(mediaInfo.VideoStreams, f.buildVideoStream(stream))
	case "audio":
		mediaInfo.AudioStreams = append(mediaInfo.AudioStreams, f.buildAudioStream(stream))
	}
}

func (f *FFProbe) buildVideoStream(stream ffprobeStream) VideoStreamInfo {
	videoInfo := VideoStreamInfo{
		Codec:       stream.CodecName,
		Profile:     stream.Profile,
		Width:       stream.Width,
		Height:      stream.Height,
		PixelFormat: stream.PixFmt,
	}

	frameRate := f.selectFrameRate(stream)
	if frameRate > 0 {
		videoInfo.FrameRate = frameRate
	}
	if bitrate, ok := parseInt64(stream.BitRate); ok {
		videoInfo.Bitrate = bitrate
	}
	return videoInfo
}

func (f *FFProbe) selectFrameRate(stream ffprobeStream) float64 {
	if stream.RFrameRate != "" {
		return f.parseFrameRate(stream.RFrameRate)
	}
	if stream.AvgFrameRate != "" {
		return f.parseFrameRate(stream.AvgFrameRate)
	}
	return 0
}

func (f *FFProbe) buildAudioStream(stream ffprobeStream) AudioStreamInfo {
	audioInfo := AudioStreamInfo{
		Codec:         stream.CodecName,
		Channels:      stream.Channels,
		ChannelLayout: stream.ChannelLayout,
	}
	if sampleRate, ok := parseInt(stream.SampleRate); ok {
		audioInfo.SampleRate = sampleRate
	}
	if bitrate, ok := parseInt64(stream.BitRate); ok {
		audioInfo.Bitrate = bitrate
	}
	return audioInfo
}

func parseFloat(value string) (float64, bool) {
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func parseInt64(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func parseInt(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// parseFrameRate はフレームレート文字列（例: "30000/1001"）をfloat64に変換する
func (f *FFProbe) parseFrameRate(frameRateStr string) float64 {
	parts := strings.Split(frameRateStr, "/")
	if len(parts) != 2 {
		// "/" がない場合は直接パース
		rate, err := strconv.ParseFloat(frameRateStr, 64)
		if err != nil {
			return 0
		}
		return rate
	}

	numerator, err1 := strconv.ParseFloat(parts[0], 64)
	denominator, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil || denominator == 0 {
		return 0
	}

	return numerator / denominator
}

// ValidatePlaylist はプレイリストファイルの構文をチェックする
func (f *FFProbe) ValidatePlaylist(ctx context.Context, playlistPath string) error {
	cmd := exec.CommandContext(ctx, f.execPath,
		"-v", "error",
		"-i", playlistPath,
		"-f", "null",
		"-",
	)

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("playlist validation failed: %s", string(exitErr.Stderr))
		}
		return fmt.Errorf("playlist validation failed: %w", err)
	}

	return nil
}

// GetSegmentInfo はセグメントファイルの情報を取得する
func (f *FFProbe) GetSegmentInfo(ctx context.Context, segmentPath string) (*SegmentInfo, error) {
	mediaInfo, err := f.GetMediaInfo(ctx, segmentPath)
	if err != nil {
		return nil, err
	}

	return &SegmentInfo{
		Path:     segmentPath,
		Duration: mediaInfo.Duration,
		Size:     mediaInfo.Size,
	}, nil
}
