package validator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidationResult_AddError(t *testing.T) {
	result := &ValidationResult{
		Valid: true,
	}

	result.addError("TEST_ERROR", "test error message", "test_field")

	if result.Valid {
		t.Error("Expected Valid to be false after adding error")
	}

	if len(result.Errors) != 1 {
		t.Errorf("Expected 1 error, got %d", len(result.Errors))
	}

	if result.Errors[0].Code != "TEST_ERROR" {
		t.Errorf("Expected error code TEST_ERROR, got %s", result.Errors[0].Code)
	}
}

func TestValidationResult_AddWarning(t *testing.T) {
	result := &ValidationResult{
		Valid: true,
	}

	result.addWarning("TEST_WARNING", "test warning message", "test_field")

	if !result.Valid {
		t.Error("Expected Valid to remain true after adding warning")
	}

	if len(result.Warnings) != 1 {
		t.Errorf("Expected 1 warning, got %d", len(result.Warnings))
	}

	if result.Warnings[0].Code != "TEST_WARNING" {
		t.Errorf("Expected warning code TEST_WARNING, got %s", result.Warnings[0].Code)
	}
}

func TestValidationResult_GetErrorMessages(t *testing.T) {
	result := &ValidationResult{}
	result.addError("ERROR1", "first error", "field1")
	result.addError("ERROR2", "second error", "field2")

	messages := result.GetErrorMessages()

	if len(messages) != 2 {
		t.Errorf("Expected 2 error messages, got %d", len(messages))
	}

	expectedFirst := "[ERROR1] first error"
	if messages[0] != expectedFirst {
		t.Errorf("Expected first message %q, got %q", expectedFirst, messages[0])
	}
}

func TestValidationResult_GetWarningMessages(t *testing.T) {
	result := &ValidationResult{}
	result.addWarning("WARN1", "first warning", "field1")
	result.addWarning("WARN2", "second warning", "field2")

	messages := result.GetWarningMessages()

	if len(messages) != 2 {
		t.Errorf("Expected 2 warning messages, got %d", len(messages))
	}

	expectedFirst := "[WARN1] first warning"
	if messages[0] != expectedFirst {
		t.Errorf("Expected first message %q, got %q", expectedFirst, messages[0])
	}
}

func TestDefaultValidator_ValidateFileExists(t *testing.T) {
	validator := &DefaultValidator{}

	// 存在しないファイル
	err := validator.validateFileExists("/nonexistent/file.mp4")
	if err == nil {
		t.Error("Expected error for nonexistent file")
	}

	// 一時ファイルを作成
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.mp4")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// 存在するファイル
	err = validator.validateFileExists(tmpFile)
	if err != nil {
		t.Errorf("Expected no error for existing file, got: %v", err)
	}

	// 空ファイル
	emptyFile := filepath.Join(tmpDir, "empty.mp4")
	if err := os.WriteFile(emptyFile, []byte{}, 0644); err != nil {
		t.Fatalf("Failed to create empty file: %v", err)
	}

	err = validator.validateFileExists(emptyFile)
	if err == nil {
		t.Error("Expected error for empty file")
	}
}

func TestDefaultValidator_IsHLSOutput(t *testing.T) {
	validator := &DefaultValidator{}

	tests := []struct {
		name      string
		path      string
		setupFunc func(string) error
		expected  bool
	}{
		{
			name: "m3u8 file",
			path: "test.m3u8",
			setupFunc: func(path string) error {
				return os.WriteFile(path, []byte("#EXTM3U\n"), 0644)
			},
			expected: true,
		},
		{
			name: "mp4 file",
			path: "test.mp4",
			setupFunc: func(path string) error {
				return os.WriteFile(path, []byte("test"), 0644)
			},
			expected: false,
		},
		{
			name: "directory with m3u8",
			path: "testdir",
			setupFunc: func(path string) error {
				if err := os.MkdirAll(path, 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(path, "playlist.m3u8"), []byte("#EXTM3U\n"), 0644)
			},
			expected: true,
		},
	}

	tmpDir := t.TempDir()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testPath := filepath.Join(tmpDir, tt.path)
			if err := tt.setupFunc(testPath); err != nil {
				t.Fatalf("Setup failed: %v", err)
			}

			result := validator.isHLSOutput(testPath, &MediaInfo{})
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestDefaultValidator_ValidateMediaStreams(t *testing.T) {
	validator := &DefaultValidator{}

	tests := []struct {
		name           string
		mediaInfo      *MediaInfo
		expected       *ExpectedMediaInfo
		expectErrors   int
		expectWarnings int
	}{
		{
			name: "valid video stream",
			mediaInfo: &MediaInfo{
				VideoStreams: []VideoStreamInfo{
					{
						Codec:  "h264",
						Width:  1280,
						Height: 720,
					},
				},
				Duration: 10.0,
				Bitrate:  2000000,
			},
			expected: &ExpectedMediaInfo{
				VideoCodec:  "h264",
				Width:       1280,
				Height:      720,
				MinDuration: 5.0,
				MaxDuration: 15.0,
				MinBitrate:  1000000,
				MaxBitrate:  5000000,
			},
			expectErrors:   0,
			expectWarnings: 0,
		},
		{
			name: "codec mismatch",
			mediaInfo: &MediaInfo{
				VideoStreams: []VideoStreamInfo{
					{
						Codec:  "hevc",
						Width:  1280,
						Height: 720,
					},
				},
			},
			expected: &ExpectedMediaInfo{
				VideoCodec: "h264",
			},
			expectErrors:   1,
			expectWarnings: 0,
		},
		{
			name: "resolution mismatch",
			mediaInfo: &MediaInfo{
				VideoStreams: []VideoStreamInfo{
					{
						Codec:  "h264",
						Width:  1920,
						Height: 1080,
					},
				},
			},
			expected: &ExpectedMediaInfo{
				VideoCodec: "h264",
				Width:      1280,
				Height:     720,
			},
			expectErrors:   2, // width and height mismatch
			expectWarnings: 0,
		},
		{
			name: "no video stream",
			mediaInfo: &MediaInfo{
				VideoStreams: []VideoStreamInfo{},
			},
			expected: &ExpectedMediaInfo{
				VideoCodec: "h264",
			},
			expectErrors:   1,
			expectWarnings: 0,
		},
		{
			name: "duration too short",
			mediaInfo: &MediaInfo{
				VideoStreams: []VideoStreamInfo{
					{Codec: "h264"},
				},
				Duration: 2.0,
			},
			expected: &ExpectedMediaInfo{
				MinDuration: 5.0,
			},
			expectErrors:   1,
			expectWarnings: 0,
		},
		{
			name: "duration too long",
			mediaInfo: &MediaInfo{
				VideoStreams: []VideoStreamInfo{
					{Codec: "h264"},
				},
				Duration: 20.0,
			},
			expected: &ExpectedMediaInfo{
				MaxDuration: 15.0,
			},
			expectErrors:   0,
			expectWarnings: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &ValidationResult{Valid: true}
			validator.validateMediaStreams(tt.mediaInfo, tt.expected, result)

			if len(result.Errors) != tt.expectErrors {
				t.Errorf("Expected %d errors, got %d: %v", tt.expectErrors, len(result.Errors), result.GetErrorMessages())
			}

			if len(result.Warnings) != tt.expectWarnings {
				t.Errorf("Expected %d warnings, got %d: %v", tt.expectWarnings, len(result.Warnings), result.GetWarningMessages())
			}
		})
	}
}

func TestDefaultValidator_Validate_MinimalLevel(t *testing.T) {
	// 最小限の検証レベルのテスト
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.mp4")
	if err := os.WriteFile(testFile, []byte("test video content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	validator := New()
	ctx := context.Background()

	options := &ValidationOptions{
		Level:          ValidationLevelMinimal,
		Timeout:        5 * time.Second,
		SkipDecodeTest: true,
	}

	// ffprobeが利用できない環境でもファイル存在性チェックは通る
	result, err := validator.Validate(ctx, testFile, options)
	if err != nil {
		t.Logf("Validation error (expected if ffprobe not available): %v", err)
	}

	// ファイルが存在するので、少なくともファイル存在性エラーは出ないはず
	if result != nil {
		for _, e := range result.Errors {
			if e.Code == "FILE_NOT_FOUND" {
				t.Errorf("Unexpected FILE_NOT_FOUND error for existing file")
			}
		}
	}
}

func TestDefaultValidator_LevelToString(t *testing.T) {
	validator := &DefaultValidator{}

	tests := []struct {
		level    ValidationLevel
		expected string
	}{
		{ValidationLevelMinimal, "minimal"},
		{ValidationLevelStandard, "standard"},
		{ValidationLevelStrict, "strict"},
	}

	for _, tt := range tests {
		result := validator.levelToString(tt.level)
		if result != tt.expected {
			t.Errorf("Expected %q for level %d, got %q", tt.expected, tt.level, result)
		}
	}
}
