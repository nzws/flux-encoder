package validator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nzws/flux-encoder/internal/shared/logger"
	"go.uber.org/zap"
)

// Validator はメディアファイルの検証を行うインターフェース
type Validator interface {
	Validate(ctx context.Context, outputPath string, options *ValidationOptions) (*ValidationResult, error)
}

// ValidationLevel は検証の厳密さのレベル
type ValidationLevel int

const (
	// ValidationLevelMinimal は最小限の検証（ファイル存在性とffprobe基本情報のみ）
	ValidationLevelMinimal ValidationLevel = iota
	// ValidationLevelStandard は標準検証（メディアストリーム情報の詳細チェック）
	ValidationLevelStandard
	// ValidationLevelStrict は厳密な検証（デコードテストを含む完全チェック）
	ValidationLevelStrict
)

// HLSValidationDepth はHLS検証の深さ
type HLSValidationDepth int

const (
	// HLSValidationDepthBasic はプレイリストの構文チェックのみ
	HLSValidationDepthBasic HLSValidationDepth = iota
	// HLSValidationDepthMedium は全セグメントの存在確認
	HLSValidationDepthMedium
	// HLSValidationDepthFull は各セグメントの内容検証
	HLSValidationDepthFull
)

// ValidationOptions は検証オプション
type ValidationOptions struct {
	Level              ValidationLevel
	Expected           *ExpectedMediaInfo
	Timeout            time.Duration
	SkipDecodeTest     bool
	HLSValidationDepth HLSValidationDepth
}

// ExpectedMediaInfo は期待されるメディア情報
type ExpectedMediaInfo struct {
	VideoCodec  string
	Width       int
	Height      int
	AudioCodec  string
	MinDuration float64
	MaxDuration float64
	MinBitrate  int64
	MaxBitrate  int64
}

// ValidationResult は検証結果
type ValidationResult struct {
	Valid              bool
	Errors             []ValidationError
	Warnings           []ValidationWarning
	MediaInfo          *MediaInfo
	ValidationDuration time.Duration
}

// ValidationError は検証エラー
type ValidationError struct {
	Code    string
	Message string
	Field   string
	Details map[string]interface{}
}

// ValidationWarning は検証警告
type ValidationWarning struct {
	Code    string
	Message string
	Field   string
}

// MediaInfo はメディアファイルの情報
type MediaInfo struct {
	Format       string
	Duration     float64
	Size         int64
	Bitrate      int64
	VideoStreams []VideoStreamInfo
	AudioStreams []AudioStreamInfo
	HLSInfo      *HLSInfo
}

// VideoStreamInfo は映像ストリーム情報
type VideoStreamInfo struct {
	Codec       string
	Profile     string
	Width       int
	Height      int
	FrameRate   float64
	PixelFormat string
	Bitrate     int64
}

// AudioStreamInfo は音声ストリーム情報
type AudioStreamInfo struct {
	Codec         string
	SampleRate    int
	Channels      int
	ChannelLayout string
	Bitrate       int64
}

// HLSInfo はHLS固有の情報
type HLSInfo struct {
	MasterPlaylist string
	Playlists      []PlaylistInfo
	TotalSegments  int
	TargetDuration float64
}

// PlaylistInfo はプレイリスト情報
type PlaylistInfo struct {
	Path         string
	Bandwidth    int64
	Resolution   string
	Codecs       string
	SegmentCount int
	Segments     []SegmentInfo
}

// SegmentInfo はセグメント情報
type SegmentInfo struct {
	Path     string
	Duration float64
	Size     int64
}

// DefaultValidator はデフォルトのValidator実装
type DefaultValidator struct {
	ffprobe         *FFProbe
	hlsParser       *HLSParser
	decodeValidator *DecodeValidator
	logger          *zap.Logger
}

// New は新しいValidatorを作成する
func New() Validator {
	return &DefaultValidator{
		ffprobe:         NewFFProbe(),
		hlsParser:       NewHLSParser(),
		decodeValidator: NewDecodeValidator(),
		logger:          zap.NewNop(), // デフォルトはNopLogger、後でlogger.Logを使用
	}
}

// Validate はメディアファイルを検証する
func (v *DefaultValidator) Validate(ctx context.Context, outputPath string, options *ValidationOptions) (*ValidationResult, error) {
	startTime := time.Now()
	result := &ValidationResult{
		Valid: true,
	}

	// デフォルトオプション設定
	if options == nil {
		options = &ValidationOptions{
			Level:              ValidationLevelStandard,
			Timeout:            30 * time.Second,
			SkipDecodeTest:     false,
			HLSValidationDepth: HLSValidationDepthMedium,
		}
	}

	// タイムアウト設定
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}

	logger.Info("Starting validation",
		zap.String("output_path", outputPath),
		zap.String("level", v.levelToString(options.Level)),
	)

	// 1. ファイル存在性チェック
	if err := v.validateFileExists(outputPath); err != nil {
		result.addError("FILE_NOT_FOUND", err.Error(), "")
		result.ValidationDuration = time.Since(startTime)
		return result, nil
	}

	// 2. ffprobeでメディア情報取得
	mediaInfo, err := v.ffprobe.GetMediaInfo(ctx, outputPath)
	if err != nil {
		result.addError("FFPROBE_FAILED", err.Error(), "")
		result.ValidationDuration = time.Since(startTime)
		return result, nil
	}
	result.MediaInfo = mediaInfo

	// 3. フォーマット判定と検証
	if v.isHLSOutput(outputPath, mediaInfo) {
		v.validateHLS(ctx, outputPath, options, result)
	} else {
		v.validateSingleFile(ctx, outputPath, options, result)
	}

	// 4. メディアストリーム検証
	if options.Expected != nil {
		v.validateMediaStreams(mediaInfo, options.Expected, result)
	}

	// 5. デコードテスト（オプション）
	if !options.SkipDecodeTest && options.Level >= ValidationLevelStrict {
		if err := v.decodeValidator.TestDecode(ctx, outputPath); err != nil {
			result.addError("DECODE_FAILED", err.Error(), "")
		}
	}

	result.ValidationDuration = time.Since(startTime)

	logger.Info("Validation completed",
		zap.Bool("valid", result.Valid),
		zap.Int("error_count", len(result.Errors)),
		zap.Int("warning_count", len(result.Warnings)),
		zap.Duration("duration", result.ValidationDuration),
	)

	return result, nil
}

// validateFileExists はファイルの存在性をチェックする
func (v *DefaultValidator) validateFileExists(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("output file does not exist: %s", path)
		}
		return fmt.Errorf("failed to stat output file: %w", err)
	}

	// ディレクトリの場合は中身を確認
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("failed to read output directory: %w", err)
		}
		if len(entries) == 0 {
			return fmt.Errorf("output directory is empty: %s", path)
		}
	} else {
		// ファイルの場合はサイズチェック
		if info.Size() == 0 {
			return fmt.Errorf("output file is empty: %s", path)
		}
	}

	return nil
}

// isHLSOutput はHLS出力かどうかを判定する
func (v *DefaultValidator) isHLSOutput(path string, mediaInfo *MediaInfo) bool {
	// ディレクトリならHLSの可能性
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		// m3u8ファイルを探す
		entries, err := os.ReadDir(path)
		if err != nil {
			logger.Warn("Failed to read directory for HLS detection", zap.String("path", path), zap.Error(err))
			return false
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".m3u8") {
				return true
			}
		}
	}

	// ファイルの拡張子が.m3u8ならHLS
	if strings.HasSuffix(path, ".m3u8") {
		return true
	}

	return false
}

// validateSingleFile は単一ファイル出力を検証する
func (v *DefaultValidator) validateSingleFile(ctx context.Context, path string, options *ValidationOptions, result *ValidationResult) {
	// 基本的なファイルサイズチェック
	info, err := os.Stat(path)
	if err != nil {
		result.addError("FILE_ACCESS_ERROR", fmt.Sprintf("failed to access file: %v", err), "")
		return
	}

	result.MediaInfo.Size = info.Size()

	// サイズの妥当性チェック（ビットレートとデュレーションから期待サイズを計算）
	if result.MediaInfo.Duration > 0 && result.MediaInfo.Bitrate > 0 {
		expectedSize := int64(result.MediaInfo.Duration * float64(result.MediaInfo.Bitrate) / 8)
		// ±50%の範囲外なら警告
		if info.Size() < expectedSize/2 || info.Size() > expectedSize*2 {
			result.addWarning("FILE_SIZE_ABNORMAL",
				fmt.Sprintf("file size (%d bytes) differs significantly from expected (%d bytes)", info.Size(), expectedSize),
				"size")
		}
	}
}

// validateHLS はHLS出力を検証する
func (v *DefaultValidator) validateHLS(ctx context.Context, path string, options *ValidationOptions, result *ValidationResult) {
	// HLS固有の検証
	hlsInfo, err := v.validateHLSStructure(ctx, path, options.HLSValidationDepth)
	if err != nil {
		result.addError("HLS_VALIDATION_FAILED", err.Error(), "")
		return
	}

	result.MediaInfo.HLSInfo = hlsInfo

	// プレイリストの構文検証
	if hlsInfo.MasterPlaylist != "" {
		if err := v.ffprobe.ValidatePlaylist(ctx, hlsInfo.MasterPlaylist); err != nil {
			result.addError("HLS_PLAYLIST_SYNTAX_ERROR", err.Error(), "playlist")
		}
	}
}

// validateHLSStructure はHLS構造を検証する
func (v *DefaultValidator) validateHLSStructure(ctx context.Context, path string, depth HLSValidationDepth) (*HLSInfo, error) {
	// ディレクトリの場合
	var baseDir string
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		baseDir = path
	} else {
		// ファイルの場合はディレクトリを取得
		baseDir = filepath.Dir(path)
	}

	// HLSParserを使用してパース・検証
	return v.hlsParser.ParseAndValidate(ctx, baseDir, depth)
}

// validateMediaStreams はメディアストリームを検証する
func (v *DefaultValidator) validateMediaStreams(mediaInfo *MediaInfo, expected *ExpectedMediaInfo, result *ValidationResult) {
	if !v.validateVideoStream(mediaInfo, expected, result) {
		return
	}
	v.validateDuration(mediaInfo, expected, result)
	v.validateBitrate(mediaInfo, expected, result)
	v.validateAudioStream(mediaInfo, expected, result)
}

func (v *DefaultValidator) validateVideoStream(mediaInfo *MediaInfo, expected *ExpectedMediaInfo, result *ValidationResult) bool {
	if len(mediaInfo.VideoStreams) == 0 {
		result.addError("NO_VIDEO_STREAM", "no video stream found", "video")
		return false
	}

	video := mediaInfo.VideoStreams[0]
	if expected.VideoCodec != "" && video.Codec != expected.VideoCodec {
		result.addError("CODEC_MISMATCH",
			fmt.Sprintf("expected codec %s, got %s", expected.VideoCodec, video.Codec),
			"video.codec")
	}
	if expected.Width > 0 && video.Width != expected.Width {
		result.addError("RESOLUTION_MISMATCH",
			fmt.Sprintf("expected width %d, got %d", expected.Width, video.Width),
			"video.width")
	}
	if expected.Height > 0 && video.Height != expected.Height {
		result.addError("RESOLUTION_MISMATCH",
			fmt.Sprintf("expected height %d, got %d", expected.Height, video.Height),
			"video.height")
	}
	return true
}

func (v *DefaultValidator) validateDuration(mediaInfo *MediaInfo, expected *ExpectedMediaInfo, result *ValidationResult) {
	if expected.MinDuration > 0 && mediaInfo.Duration < expected.MinDuration {
		result.addError("DURATION_TOO_SHORT",
			fmt.Sprintf("duration %.2fs is less than minimum %.2fs", mediaInfo.Duration, expected.MinDuration),
			"duration")
	}
	if expected.MaxDuration > 0 && mediaInfo.Duration > expected.MaxDuration {
		result.addWarning("DURATION_TOO_LONG",
			fmt.Sprintf("duration %.2fs exceeds maximum %.2fs", mediaInfo.Duration, expected.MaxDuration),
			"duration")
	}
}

func (v *DefaultValidator) validateBitrate(mediaInfo *MediaInfo, expected *ExpectedMediaInfo, result *ValidationResult) {
	if expected.MinBitrate > 0 && mediaInfo.Bitrate < expected.MinBitrate {
		result.addWarning("BITRATE_TOO_LOW",
			fmt.Sprintf("bitrate %d is less than minimum %d", mediaInfo.Bitrate, expected.MinBitrate),
			"bitrate")
	}
	if expected.MaxBitrate > 0 && mediaInfo.Bitrate > expected.MaxBitrate {
		result.addWarning("BITRATE_TOO_HIGH",
			fmt.Sprintf("bitrate %d exceeds maximum %d", mediaInfo.Bitrate, expected.MaxBitrate),
			"bitrate")
	}
}

func (v *DefaultValidator) validateAudioStream(mediaInfo *MediaInfo, expected *ExpectedMediaInfo, result *ValidationResult) {
	if expected.AudioCodec == "" {
		return
	}
	if len(mediaInfo.AudioStreams) == 0 {
		result.addWarning("NO_AUDIO_STREAM", "no audio stream found (expected audio)", "audio")
		return
	}
	audio := mediaInfo.AudioStreams[0]
	if audio.Codec != expected.AudioCodec {
		result.addError("CODEC_MISMATCH",
			fmt.Sprintf("expected audio codec %s, got %s", expected.AudioCodec, audio.Codec),
			"audio.codec")
	}
}

// addError はエラーを追加し、Validフラグをfalseにする
func (r *ValidationResult) addError(code, message, field string) {
	r.Valid = false
	r.Errors = append(r.Errors, ValidationError{
		Code:    code,
		Message: message,
		Field:   field,
	})
}

// addWarning は警告を追加する
func (r *ValidationResult) addWarning(code, message, field string) {
	r.Warnings = append(r.Warnings, ValidationWarning{
		Code:    code,
		Message: message,
		Field:   field,
	})
}

// GetErrorMessages はエラーメッセージのスライスを返す
func (r *ValidationResult) GetErrorMessages() []string {
	messages := make([]string, len(r.Errors))
	for i, err := range r.Errors {
		messages[i] = fmt.Sprintf("[%s] %s", err.Code, err.Message)
	}
	return messages
}

// GetWarningMessages は警告メッセージのスライスを返す
func (r *ValidationResult) GetWarningMessages() []string {
	messages := make([]string, len(r.Warnings))
	for i, warn := range r.Warnings {
		messages[i] = fmt.Sprintf("[%s] %s", warn.Code, warn.Message)
	}
	return messages
}

// levelToString はValidationLevelを文字列に変換する
func (v *DefaultValidator) levelToString(level ValidationLevel) string {
	switch level {
	case ValidationLevelMinimal:
		return "minimal"
	case ValidationLevelStandard:
		return "standard"
	case ValidationLevelStrict:
		return "strict"
	default:
		return "unknown"
	}
}
