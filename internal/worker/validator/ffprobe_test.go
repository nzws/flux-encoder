package validator

import (
	"testing"
)

func TestFFProbe_ParseFrameRate(t *testing.T) {
	ffprobe := NewFFProbe()

	tests := []struct {
		input    string
		expected float64
	}{
		{"30000/1001", 29.97002997002997},
		{"24000/1001", 23.976023976023978},
		{"30/1", 30.0},
		{"60/1", 60.0},
		{"25", 25.0},
		{"29.97", 29.97},
		{"invalid", 0.0},
		{"30/0", 0.0}, // division by zero
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ffprobe.parseFrameRate(tt.input)
			if result != tt.expected {
				t.Errorf("parseFrameRate(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFFProbe_ConvertToMediaInfo(t *testing.T) {
	ffprobe := NewFFProbe()

	tests := []struct {
		name     string
		input    *ffprobeOutput
		validate func(*testing.T, *MediaInfo)
	}{
		{
			name: "basic video with audio",
			input: &ffprobeOutput{
				Format: ffprobeFormat{
					FormatName: "mp4",
					Duration:   "10.5",
					Size:       "1048576",
					BitRate:    "800000",
				},
				Streams: []ffprobeStream{
					{
						CodecType:    "video",
						CodecName:    "h264",
						Profile:      "High",
						Width:        1280,
						Height:       720,
						PixFmt:       "yuv420p",
						RFrameRate:   "30/1",
						AvgFrameRate: "30/1",
						BitRate:      "700000",
					},
					{
						CodecType:     "audio",
						CodecName:     "aac",
						SampleRate:    "48000",
						Channels:      2,
						ChannelLayout: "stereo",
						BitRate:       "128000",
					},
				},
			},
			validate: func(t *testing.T, info *MediaInfo) {
				if info.Format != "mp4" {
					t.Errorf("Expected format mp4, got %s", info.Format)
				}
				if info.Duration != 10.5 {
					t.Errorf("Expected duration 10.5, got %f", info.Duration)
				}
				if info.Size != 1048576 {
					t.Errorf("Expected size 1048576, got %d", info.Size)
				}
				if info.Bitrate != 800000 {
					t.Errorf("Expected bitrate 800000, got %d", info.Bitrate)
				}
				if len(info.VideoStreams) != 1 {
					t.Fatalf("Expected 1 video stream, got %d", len(info.VideoStreams))
				}
				video := info.VideoStreams[0]
				if video.Codec != "h264" {
					t.Errorf("Expected codec h264, got %s", video.Codec)
				}
				if video.Width != 1280 {
					t.Errorf("Expected width 1280, got %d", video.Width)
				}
				if video.Height != 720 {
					t.Errorf("Expected height 720, got %d", video.Height)
				}
				if video.FrameRate != 30.0 {
					t.Errorf("Expected frame rate 30.0, got %f", video.FrameRate)
				}
				if len(info.AudioStreams) != 1 {
					t.Fatalf("Expected 1 audio stream, got %d", len(info.AudioStreams))
				}
				audio := info.AudioStreams[0]
				if audio.Codec != "aac" {
					t.Errorf("Expected codec aac, got %s", audio.Codec)
				}
				if audio.SampleRate != 48000 {
					t.Errorf("Expected sample rate 48000, got %d", audio.SampleRate)
				}
				if audio.Channels != 2 {
					t.Errorf("Expected 2 channels, got %d", audio.Channels)
				}
			},
		},
		{
			name: "video only",
			input: &ffprobeOutput{
				Format: ffprobeFormat{
					FormatName: "mp4",
					Duration:   "5.0",
				},
				Streams: []ffprobeStream{
					{
						CodecType: "video",
						CodecName: "h264",
						Width:     640,
						Height:    480,
					},
				},
			},
			validate: func(t *testing.T, info *MediaInfo) {
				if len(info.VideoStreams) != 1 {
					t.Fatalf("Expected 1 video stream, got %d", len(info.VideoStreams))
				}
				if len(info.AudioStreams) != 0 {
					t.Errorf("Expected 0 audio streams, got %d", len(info.AudioStreams))
				}
			},
		},
		{
			name: "multiple video streams",
			input: &ffprobeOutput{
				Format: ffprobeFormat{
					FormatName: "mp4",
				},
				Streams: []ffprobeStream{
					{
						CodecType: "video",
						CodecName: "h264",
						Width:     1920,
						Height:    1080,
					},
					{
						CodecType: "video",
						CodecName: "h264",
						Width:     1280,
						Height:    720,
					},
				},
			},
			validate: func(t *testing.T, info *MediaInfo) {
				if len(info.VideoStreams) != 2 {
					t.Fatalf("Expected 2 video streams, got %d", len(info.VideoStreams))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mediaInfo, err := ffprobe.convertToMediaInfo(tt.input)
			if err != nil {
				t.Fatalf("convertToMediaInfo failed: %v", err)
			}
			tt.validate(t, mediaInfo)
		})
	}
}
