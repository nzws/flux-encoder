package validator

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// DecodeValidator はデコード検証を行う
type DecodeValidator struct {
	ffmpegPath string
}

// NewDecodeValidator は新しいDecodeValidatorを作成する
func NewDecodeValidator() *DecodeValidator {
	return &DecodeValidator{
		ffmpegPath: "ffmpeg",
	}
}

// TestDecode はメディアファイルのデコードテストを実行する
func (d *DecodeValidator) TestDecode(ctx context.Context, filePath string) error {
	// ffmpeg -v error -i input -f null - を実行
	// エラーがあればstderrに出力される
	cmd := exec.CommandContext(ctx, d.ffmpegPath,
		"-v", "error",
		"-i", filePath,
		"-f", "null",
		"-",
	)

	// stderrをキャプチャ
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// エラー出力を収集
	var errorLines []string
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		// 空行以外を収集
		if strings.TrimSpace(line) != "" {
			errorLines = append(errorLines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading ffmpeg output: %w", err)
	}

	// コマンド完了を待つ
	if err := cmd.Wait(); err != nil {
		// エラーメッセージを整形
		errorMsg := strings.Join(errorLines, "\n")
		if errorMsg != "" {
			return fmt.Errorf("decode test failed: %s", errorMsg)
		}
		return fmt.Errorf("decode test failed: %w", err)
	}

	// エラー行が含まれていたら失敗
	if len(errorLines) > 0 {
		// 一部の警告は許容する
		criticalErrors := d.filterCriticalErrors(errorLines)
		if len(criticalErrors) > 0 {
			return fmt.Errorf("decode errors detected: %s", strings.Join(criticalErrors, "; "))
		}
	}

	return nil
}

// filterCriticalErrors は致命的なエラーのみをフィルタする
func (d *DecodeValidator) filterCriticalErrors(errorLines []string) []string {
	var criticalErrors []string

	// 許容する警告メッセージのパターン
	allowedPatterns := []string{
		"deprecated",
		"metadata",
		"Estimating duration",
	}

	for _, line := range errorLines {
		isCritical := true
		lineLower := strings.ToLower(line)

		// 許容パターンに一致するか確認
		for _, pattern := range allowedPatterns {
			if strings.Contains(lineLower, strings.ToLower(pattern)) {
				isCritical = false
				break
			}
		}

		if isCritical {
			criticalErrors = append(criticalErrors, line)
		}
	}

	return criticalErrors
}
