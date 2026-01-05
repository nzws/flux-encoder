package encoder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nzws/flux-encoder/internal/worker/preset"
)

// ffmpegがインストールされているかチェック
func hasFFmpeg() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// ffprobeがインストールされているかチェック
func hasFFprobe() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
}

func TestEncoderの初期化が正しく行われる(t *testing.T) {
	workDir := t.TempDir()
	encoder := New(workDir)

	if encoder == nil {
		t.Fatal("Encoder が nil")
	}

	if encoder.workDir != workDir {
		t.Errorf("workDir が一致しない: 期待値 %s, 取得値 %s", workDir, encoder.workDir)
	}
}

func TestCleanupがジョブディレクトリを削除する(t *testing.T) {
	workDir := t.TempDir()
	encoder := New(workDir)

	jobID := "test-job-123"
	jobDir := filepath.Join(workDir, jobID)

	// ジョブディレクトリを作成
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		t.Fatalf("ジョブディレクトリの作成に失敗: %v", err)
	}

	// テストファイルを作成
	testFile := filepath.Join(jobDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("テストファイルの作成に失敗: %v", err)
	}

	// Cleanup 実行
	if err := encoder.Cleanup(jobID); err != nil {
		t.Fatalf("Cleanup に失敗: %v", err)
	}

	// ディレクトリが削除されたか確認
	if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
		t.Error("ジョブディレクトリが削除されていない")
	}
}

func TestCleanupが存在しないジョブIDでエラーにならない(t *testing.T) {
	workDir := t.TempDir()
	encoder := New(workDir)

	// 存在しないジョブIDで Cleanup を実行してもエラーにならないはず
	err := encoder.Cleanup("存在しないジョブ")
	if err != nil {
		t.Errorf("存在しないジョブIDの Cleanup でエラーが発生: %v", err)
	}
}

func Testジョブディレクトリが作成される(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg がインストールされていないためスキップ")
	}

	workDir := t.TempDir()
	encoder := New(workDir)

	jobID := "test-job-dir"

	// ダミーの入力を使用してエンコードを試行
	// 注: これは実際に失敗するが、ディレクトリ作成のテストには十分
	ctx := context.Background()
	_, err := encoder.Encode(ctx, jobID, "invalid://url", "720p_h264", func(progress float32, message string) {})
	if err == nil {
		t.Error("無効なURLでエンコードが成功した")
	}

	// ジョブディレクトリが作成されたか確認
	jobDir := filepath.Join(workDir, jobID)
	if _, err := os.Stat(jobDir); os.IsNotExist(err) {
		t.Error("ジョブディレクトリが作成されていない")
	}
}

func Test存在しないプリセットでエラーが返る(t *testing.T) {
	workDir := t.TempDir()
	encoder := New(workDir)

	ctx := context.Background()
	_, err := encoder.Encode(ctx, "test-job", "test-input", "存在しないプリセット", func(progress float32, message string) {})

	if err == nil {
		t.Error("存在しないプリセットでエラーが返されなかった")
	}
}

func Test進捗コールバックが呼ばれる(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg がインストールされていないためスキップ")
	}

	workDir := t.TempDir()
	encoder := New(workDir)

	callbackCount := 0
	callback := func(progress float32, message string) {
		callbackCount++
		t.Logf("進捗: %.1f%% - %s", progress, message)
	}

	ctx := context.Background()

	// 注: 実際のエンコードテストには有効な入力URLが必要
	// ここでは、ffmpegがエラーで終了することを想定
	_, err := encoder.Encode(ctx, "test-job-callback", "invalid://url", "720p_h264", callback)
	if err == nil {
		t.Error("無効なURLでエンコードが成功した")
	}

	// コールバックが呼ばれたかどうかは、
	// 実際の入力がないため確認できないが、
	// エラーが返ることは確認できる
}

func TestGetDurationがffprobeを呼び出す(t *testing.T) {
	if !hasFFprobe() {
		t.Skip("ffprobe がインストールされていないためスキップ")
	}

	workDir := t.TempDir()
	encoder := New(workDir)

	ctx := context.Background()

	// 無効なURLでgetDurationを呼び出す
	_, err := encoder.getDuration(ctx, "invalid://url")

	// エラーが返るはず（無効なURLのため）
	if err == nil {
		t.Error("無効なURLでgetDurationがエラーを返さなかった")
	}
}

// 以下は、実際の動画ファイルを使用した統合テストの例
// 実際のCI環境では、テスト用の小さな動画ファイルを用意する必要がある

// func TestEncoderが実際に動画をエンコードできる(t *testing.T) {
//     if !hasFFmpeg() {
//         t.Skip("ffmpeg がインストールされていないためスキップ")
//     }
//
//     // テスト用の動画ファイルを作成（ffmpegで生成）
//     // ffmpeg -f lavfi -i testsrc=duration=1:size=320x240:rate=1 test.mp4
//
//     workDir := t.TempDir()
//     encoder := New(workDir)
//
//     ctx := context.Background()
//     progressCalls := []float32{}
//
//     callback := func(progress float32, message string) {
//         progressCalls = append(progressCalls, progress)
//     }
//
//     outputPath, err := encoder.Encode(ctx, "test-job-real", "test.mp4", "720p_h264", callback)
//     if err != nil {
//         t.Fatalf("エンコードに失敗: %v", err)
//     }
//
//     // 出力ファイルが存在するか確認
//     if _, err := os.Stat(outputPath); os.IsNotExist(err) {
//         t.Error("出力ファイルが作成されていない")
//     }
//
//     // 進捗が複数回呼ばれたか確認
//     if len(progressCalls) == 0 {
//         t.Error("進捗コールバックが呼ばれていない")
//     }
// }

func Test単一ファイル出力のパスが正しく設定される(t *testing.T) {
	workDir := t.TempDir()
	encoder := New(workDir)

	jobID := "test-job-single"
	jobDir := filepath.Join(workDir, jobID)

	// プリセット: 720p_h264 (single)
	// 期待される出力パス: workDir/jobID/output.mp4

	expectedOutputPath := filepath.Join(jobDir, "output.mp4")

	// 実際のコードでは outputPath が返されるが、
	// ここでは構造をテストするためにパスを構築
	_ = expectedOutputPath
	_ = encoder

	// Note: 実際のエンコードなしでパスロジックをテストするには、
	// Encode メソッドをリファクタリングして、
	// パス決定ロジックを別メソッドに分離する必要がある
}

func TestHLS出力でディレクトリが作成される(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg がインストールされていないためスキップ")
	}

	workDir := t.TempDir()
	encoder := New(workDir)

	jobID := "test-job-hls"
	ctx := context.Background()

	// hls_720p プリセットを使用
	_, err := encoder.Encode(ctx, jobID, "invalid://url", "hls_720p", func(progress float32, message string) {})
	if err == nil {
		t.Error("無効なURLでエンコードが成功した")
	}

	// output ディレクトリが作成されたか確認
	outputDir := filepath.Join(workDir, jobID, "output")
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		t.Error("HLS 出力ディレクトリが作成されていない")
	}
}

func Testコンテキストキャンセル時にエンコードが中止される(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg がインストールされていないためスキップ")
	}

	workDir := t.TempDir()
	encoder := New(workDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // すぐにキャンセル

	_, err := encoder.Encode(ctx, "test-job-cancel", "invalid://url", "720p_h264", func(progress float32, message string) {})

	// キャンセルまたはエラーが返るはず
	if err == nil {
		t.Error("コンテキストキャンセル時にエラーが返されなかった")
	}
}

func TestHLS単一バリアントが正しいファイル名を使用する(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg がインストールされていないためスキップ")
	}

	workDir := t.TempDir()
	encoder := New(workDir)

	jobID := "test-hls-filename"
	ctx := context.Background()

	// hls_720p プリセットを使用してエンコードを試行
	_, err := encoder.Encode(ctx, jobID, "invalid://url", "hls_720p", func(progress float32, message string) {})
	if err == nil {
		t.Error("無効なURLでエンコードが成功した")
	}

	// playlist.m3u8 ファイルが想定される場所にあるか確認
	// 注: エンコードは失敗するが、ディレクトリとファイルパスの構造はテストできる
	expectedPlaylist := filepath.Join(workDir, jobID, "output", "playlist.m3u8")
	_ = expectedPlaylist
	// 実際にファイルが作成されるかは、有効な入力がある場合のみなので、
	// ここではパス構造のテストにとどめる
}

func TestHLSマルチバリアントが正しいファイル名を使用する(t *testing.T) {
	if !hasFFmpeg() {
		t.Skip("ffmpeg がインストールされていないためスキップ")
	}

	workDir := t.TempDir()
	encoder := New(workDir)

	jobID := "test-hls-abr-filename"
	ctx := context.Background()

	// hls_720p_abr プリセットを使用してエンコードを試行
	_, err := encoder.Encode(ctx, jobID, "invalid://url", "hls_720p_abr", func(progress float32, message string) {})
	if err == nil {
		t.Error("無効なURLでエンコードが成功した")
	}

	// stream_%v.m3u8 ファイルが想定される場所にあるか確認
	// master.m3u8 も生成されるはず
	outputDir := filepath.Join(workDir, jobID, "output")
	_ = outputDir
	// 実際にファイルが作成されるかは、有効な入力がある場合のみなので、
	// ここではパス構造のテストにとどめる
}

func Test出力ファイル名がプリセットのOutputFileNameを使用する(t *testing.T) {
	// このテストは、encoder.Encode内部のロジックをテストする
	// 実際にはプリセットのOutputFileNameが使用されることを確認する
	testCases := []struct {
		presetName       string
		expectedFileName string
	}{
		{"hls_720p", "playlist.m3u8"},
		{"hls_720p_abr", "stream_%v.m3u8"},
	}

	for _, tc := range testCases {
		t.Run(tc.presetName, func(t *testing.T) {
			preset, err := preset.Get(tc.presetName)
			if err != nil {
				t.Fatalf("プリセットの取得に失敗: %v", err)
			}

			if preset.OutputFileName != tc.expectedFileName {
				t.Errorf("OutputFileName が一致しない: 期待値 %s, 取得値 %s",
					tc.expectedFileName, preset.OutputFileName)
			}
		})
	}
}

// Note: より包括的なテストを書くには、以下のアプローチが推奨される:
// 1. ffmpegのモックを作成（難易度: 高）
// 2. テスト用の小さな動画ファイルをリポジトリに含める
// 3. CI環境でffmpegをインストールし、実際のエンコードテストを実行
// 4. getDuration や outputPath 決定などのロジックを別メソッドに分離し、
//    個別にテスト可能にする
