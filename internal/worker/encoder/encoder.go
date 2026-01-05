package encoder

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nzws/flux-encoder/internal/shared/logger"
	"github.com/nzws/flux-encoder/internal/worker/preset"
	"github.com/nzws/flux-encoder/internal/worker/validator"
	"go.uber.org/zap"
)

// Encoder はエンコード処理を管理する
type Encoder struct {
	workDir   string
	validator validator.Validator
}

const (
	outputTypeHLS  = "hls"
	outputTypeDASH = "dash"
)

// ProgressCallback は進捗通知のコールバック関数
type ProgressCallback func(progress float32, message string)

// New は新しい Encoder を作成する
func New(workDir string) *Encoder {
	return &Encoder{
		workDir:   workDir,
		validator: validator.New(),
	}
}

// Encode はエンコード処理を実行する
func (e *Encoder) Encode(
	ctx context.Context,
	jobID string,
	inputURL string,
	presetName string,
	callback ProgressCallback,
) (string, error) {
	// プリセット取得
	preset, err := preset.Get(presetName)
	if err != nil {
		return "", fmt.Errorf("failed to get preset: %w", err)
	}

	// 作業ディレクトリ作成
	jobDir := filepath.Join(e.workDir, jobID)
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create job directory: %w", err)
	}

	// 出力パス（ファイルまたはディレクトリ）
	outputPath, outputFile, err := resolveOutputPaths(jobDir, preset)
	if err != nil {
		return "", err
	}

	// ffmpeg コマンド構築
	args := buildFFmpegArgs(inputURL, outputFile, preset)

	logger.Info("Starting ffmpeg",
		zap.String("job_id", jobID),
		zap.String("input", inputURL),
		zap.String("preset", presetName),
		zap.String("output", outputFile),
	)

	// ffmpeg コマンド実行
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	// HLS/DASHの場合は出力ディレクトリをカレントディレクトリに設定
	setFFmpegWorkingDir(cmd, preset, outputPath)

	// stderr をパイプ
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// コマンド開始
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// 動画の総時間（マイクロ秒）を取得するため、最初にffprobeで調べる
	duration, err := e.getDuration(ctx, inputURL)
	if err != nil {
		logger.Warn("Failed to get input duration", zap.String("job_id", jobID), zap.Error(err))
		duration = 0
	}

	stderrLines, err := readFFmpegProgress(jobID, stderr, duration, callback)
	if err != nil {
		logger.Error("Failed to read ffmpeg progress",
			zap.String("job_id", jobID),
			zap.Error(err),
		)
	}

	// コマンド完了を待つ
	if err := cmd.Wait(); err != nil {
		// エラー時はffmpegの出力をログに記録
		logger.Error("ffmpeg stderr output",
			zap.String("job_id", jobID),
			zap.Strings("stderr", stderrLines[max(0, len(stderrLines)-50):]), // 最後の50行
		)
		return "", fmt.Errorf("ffmpeg failed: %w", err)
	}

	logger.Info("Encoding completed",
		zap.String("job_id", jobID),
		zap.String("output", outputPath),
	)

	// エンコード完了後に検証を実行
	if err := e.validateOutput(ctx, jobID, outputPath, preset); err != nil {
		return "", fmt.Errorf("output validation failed: %w", err)
	}

	return outputPath, nil
}

func resolveOutputPaths(jobDir string, preset preset.Preset) (string, string, error) {
	if preset.OutputType == outputTypeHLS || preset.OutputType == outputTypeDASH {
		outputPath := filepath.Join(jobDir, "output")
		if err := os.MkdirAll(outputPath, 0755); err != nil {
			return "", "", fmt.Errorf("failed to create output directory: %w", err)
		}
		outputFileName := preset.OutputFileName
		if outputFileName == "" {
			outputFileName = defaultOutputFileName(preset.OutputType)
		}
		if outputFileName == "" {
			return "", "", fmt.Errorf("missing output file name for preset type: %s", preset.OutputType)
		}
		return outputPath, outputFileName, nil
	}

	outputPath := filepath.Join(jobDir, fmt.Sprintf("output.%s", preset.Extension))
	return outputPath, outputPath, nil
}

func defaultOutputFileName(outputType string) string {
	switch outputType {
	case outputTypeHLS:
		return "playlist.m3u8"
	case outputTypeDASH:
		return "manifest.mpd"
	default:
		return ""
	}
}

func buildFFmpegArgs(inputURL, outputFile string, preset preset.Preset) []string {
	args := []string{
		"-i", inputURL, // 入力URL
		"-progress", "pipe:2", // 進捗をstderrに出力
		"-y", // 上書き
	}
	args = append(args, preset.FFmpegArgs...)
	args = append(args, outputFile)
	return args
}

func setFFmpegWorkingDir(cmd *exec.Cmd, preset preset.Preset, outputPath string) {
	if preset.OutputType == outputTypeHLS || preset.OutputType == outputTypeDASH {
		cmd.Dir = outputPath
	}
}

func readFFmpegProgress(jobID string, stderr io.Reader, duration float64, callback ProgressCallback) ([]string, error) {
	frameRe := regexp.MustCompile(`frame=\s*(\d+)`)
	timeRe := regexp.MustCompile(`out_time_ms=(\d+)`)

	var stderrLines []string
	lastLoggedProgress := float32(-10)
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		stderrLines = append(stderrLines, line)

		logger.Debug("ffmpeg output",
			zap.String("job_id", jobID),
			zap.String("line", line),
		)

		if matches := frameRe.FindStringSubmatch(line); len(matches) > 1 {
			callback(0, fmt.Sprintf("Encoding frame %s", matches[1]))
		}

		progress, ok := parseProgress(timeRe, line, duration)
		if !ok {
			continue
		}

		if progress-lastLoggedProgress >= 10 || progress >= 100 {
			logger.Info("Encoding progress",
				zap.String("job_id", jobID),
				zap.Float32("progress", progress),
				zap.String("status", fmt.Sprintf("%.1f%%", progress)),
			)
			lastLoggedProgress = progress
		}

		callback(progress, fmt.Sprintf("Encoding: %.1f%%", progress))
	}

	if err := scanner.Err(); err != nil {
		return stderrLines, err
	}
	return stderrLines, nil
}

func parseProgress(timeRe *regexp.Regexp, line string, duration float64) (float32, bool) {
	if duration <= 0 {
		return 0, false
	}

	matches := timeRe.FindStringSubmatch(line)
	if len(matches) <= 1 {
		return 0, false
	}

	timeMicros, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, false
	}
	progress := float32((timeMicros / 1000000.0) / duration * 100.0)
	if progress > 100 {
		progress = 100
	}
	return progress, true
}

// validateOutput はエンコード出力を検証する
func (e *Encoder) validateOutput(ctx context.Context, jobID, outputPath string, preset preset.Preset) error {
	logger.Info("Starting output validation",
		zap.String("job_id", jobID),
		zap.String("output", outputPath),
	)

	// 検証オプションを設定
	validationOpts := &validator.ValidationOptions{
		Level:              validator.ValidationLevelStandard,
		Timeout:            30 * time.Second,
		SkipDecodeTest:     false,
		HLSValidationDepth: validator.HLSValidationDepthMedium,
		Expected:           e.getExpectedInfoFromPreset(preset),
	}

	// 検証実行
	result, err := e.validator.Validate(ctx, outputPath, validationOpts)
	if err != nil {
		return fmt.Errorf("validation error: %w", err)
	}

	// 検証失敗
	if !result.Valid {
		logger.Error("Output validation failed",
			zap.String("job_id", jobID),
			zap.Strings("errors", result.GetErrorMessages()),
		)
		return fmt.Errorf("validation failed with %d errors: %s", len(result.Errors), result.GetErrorMessages()[0])
	}

	// 警告があればログ出力
	if len(result.Warnings) > 0 {
		logger.Warn("Output validation warnings",
			zap.String("job_id", jobID),
			zap.Strings("warnings", result.GetWarningMessages()),
		)
	}

	logger.Info("Output validation succeeded",
		zap.String("job_id", jobID),
		zap.Duration("duration", result.ValidationDuration),
	)

	return nil
}

// getExpectedInfoFromPreset はプリセットから期待されるメディア情報を取得する
func (e *Encoder) getExpectedInfoFromPreset(preset preset.Preset) *validator.ExpectedMediaInfo {
	expected := &validator.ExpectedMediaInfo{}

	// ffmpeg引数から期待値を抽出
	for i, arg := range preset.FFmpegArgs {
		switch arg {
		case "-c:v":
			if i+1 < len(preset.FFmpegArgs) {
				codec := preset.FFmpegArgs[i+1]
				// libx264 -> h264
				if codec == "libx264" {
					expected.VideoCodec = "h264"
				} else if codec == "libx265" {
					expected.VideoCodec = "hevc"
				}
			}
		case "-c:a":
			if i+1 < len(preset.FFmpegArgs) {
				expected.AudioCodec = preset.FFmpegArgs[i+1]
			}
		case "scale":
			// -vf scale=-2:720 のような形式から解像度を抽出
			if i+1 < len(preset.FFmpegArgs) {
				scaleArg := preset.FFmpegArgs[i+1]
				if strings.Contains(scaleArg, ":") {
					parts := strings.Split(scaleArg, ":")
					if len(parts) >= 2 {
						// 高さを取得
						if height, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
							expected.Height = height
						}
					}
				}
			}
		}
	}

	// ビットレートの許容範囲を設定（指定がない場合）
	if expected.MinBitrate == 0 {
		expected.MinBitrate = 100000 // 100 kbps
	}
	if expected.MaxBitrate == 0 {
		expected.MaxBitrate = 50000000 // 50 Mbps
	}

	return expected
}

// getDuration は動画の総時間（秒）を取得する
func (e *Encoder) getDuration(ctx context.Context, inputURL string) (float64, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputURL,
	)

	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	duration, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		return 0, err
	}

	return duration, nil
}

// Cleanup はジョブのディレクトリを削除する
func (e *Encoder) Cleanup(jobID string) error {
	jobDir := filepath.Join(e.workDir, jobID)
	return os.RemoveAll(jobDir)
}
