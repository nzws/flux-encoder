package uploader

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalUploaderが単一ファイルをアップロードできる(t *testing.T) {
	// 一時ディレクトリを作成
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "storage")
	srcDir := filepath.Join(tempDir, "src")

	mustMkdirAll(t, srcDir)

	// テスト用ファイルを作成
	srcFile := filepath.Join(srcDir, "test.txt")
	content := []byte("test content")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatalf("テストファイルの作成に失敗: %v", err)
	}

	// LocalUploader を作成
	uploader := &LocalUploader{baseDir: baseDir}

	// ファイルをアップロード
	url, err := uploader.Upload(context.Background(), srcFile, "uploads/test.txt")
	if err != nil {
		t.Fatalf("アップロードに失敗: %v", err)
	}

	// URL が正しいフォーマットか確認
	if !strings.HasPrefix(url, "file://") {
		t.Errorf("URL のプレフィックスが 'file://' でない: %s", url)
	}

	// ファイルがコピーされたか確認
	destFile := filepath.Join(baseDir, "uploads/test.txt")
	copiedContent, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("コピーされたファイルの読み込みに失敗: %v", err)
	}

	if string(copiedContent) != string(content) {
		t.Errorf("ファイル内容が一致しない: 期待値 '%s', 取得値 '%s'", content, copiedContent)
	}
}

func TestLocalUploaderがディレクトリをアップロードできる(t *testing.T) {
	// 一時ディレクトリを作成
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "storage")
	srcDir := filepath.Join(tempDir, "src")

	// HLS ファイル構造を作成
	mustMkdirAll(t, srcDir)
	files := map[string]string{
		"master.m3u8":      "#EXTM3U\n#EXT-X-STREAM-INF",
		"stream_0.m3u8":    "#EXTM3U\n#EXTINF:6.0",
		"segment_0_000.ts": "fake ts data",
		"segment_0_001.ts": "fake ts data 2",
	}

	for name, content := range files {
		path := filepath.Join(srcDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("テストファイルの作成に失敗 (%s): %v", name, err)
		}
	}

	// LocalUploader を作成
	uploader := &LocalUploader{baseDir: baseDir}

	// ディレクトリをアップロード
	url, err := uploader.UploadDirectory(context.Background(), srcDir, "uploads/hls")
	if err != nil {
		t.Fatalf("ディレクトリのアップロードに失敗: %v", err)
	}

	// URL が master.m3u8 を指しているか確認
	if !strings.Contains(url, "master.m3u8") {
		t.Errorf("URL が master.m3u8 を含んでいない: %s", url)
	}

	// すべてのファイルがコピーされたか確認
	for name := range files {
		destFile := filepath.Join(baseDir, "uploads/hls", name)
		if _, err := os.Stat(destFile); os.IsNotExist(err) {
			t.Errorf("ファイル '%s' がコピーされていない", name)
		}
	}
}

func TestLocalUploaderがmaster_m3u8を優先して検出する(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "storage")
	srcDir := filepath.Join(tempDir, "src")

	mustMkdirAll(t, srcDir)

	// master.m3u8 と playlist.m3u8 の両方を作成
	files := map[string]string{
		"master.m3u8":   "#EXTM3U\nmaster",
		"playlist.m3u8": "#EXTM3U\nplaylist",
		"segment.ts":    "data",
	}

	for name, content := range files {
		path := filepath.Join(srcDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("テストファイルの作成に失敗: %v", err)
		}
	}

	uploader := &LocalUploader{baseDir: baseDir}
	url, err := uploader.UploadDirectory(context.Background(), srcDir, "uploads/test")
	if err != nil {
		t.Fatalf("アップロードに失敗: %v", err)
	}

	// master.m3u8 が優先されるはず
	if !strings.Contains(url, "master.m3u8") {
		t.Errorf("master.m3u8 が優先されていない: %s", url)
	}
}

func TestLocalUploaderがplaylist_m3u8をフォールバックとして検出する(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "storage")
	srcDir := filepath.Join(tempDir, "src")

	mustMkdirAll(t, srcDir)

	// playlist.m3u8 のみ作成（master.m3u8 なし）
	files := map[string]string{
		"playlist.m3u8": "#EXTM3U\nplaylist",
		"segment.ts":    "data",
	}

	for name, content := range files {
		path := filepath.Join(srcDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("テストファイルの作成に失敗: %v", err)
		}
	}

	uploader := &LocalUploader{baseDir: baseDir}
	url, err := uploader.UploadDirectory(context.Background(), srcDir, "uploads/test")
	if err != nil {
		t.Fatalf("アップロードに失敗: %v", err)
	}

	// playlist.m3u8 が使用されるはず
	if !strings.Contains(url, "playlist.m3u8") {
		t.Errorf("playlist.m3u8 が使用されていない: %s", url)
	}
}

func TestLocalUploaderがmanifest_mpdを検出する(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "storage")
	srcDir := filepath.Join(tempDir, "src")

	mustMkdirAll(t, srcDir)

	// DASH 形式のファイル構造
	files := map[string]string{
		"manifest.mpd":  "<?xml version=\"1.0\"?>",
		"segment_1.m4s": "data",
	}

	for name, content := range files {
		path := filepath.Join(srcDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("テストファイルの作成に失敗: %v", err)
		}
	}

	uploader := &LocalUploader{baseDir: baseDir}
	url, err := uploader.UploadDirectory(context.Background(), srcDir, "uploads/dash")
	if err != nil {
		t.Fatalf("アップロードに失敗: %v", err)
	}

	// manifest.mpd が使用されるはず
	if !strings.Contains(url, "manifest.mpd") {
		t.Errorf("manifest.mpd が使用されていない: %s", url)
	}
}

func TestLocalUploaderでマスターファイルが見つからない場合はエラーを返す(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "storage")
	srcDir := filepath.Join(tempDir, "src")

	mustMkdirAll(t, srcDir)

	// マスターファイルなしでファイルを作成
	files := map[string]string{
		"segment.ts": "data",
		"video.mp4":  "video data",
	}

	for name, content := range files {
		path := filepath.Join(srcDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("テストファイルの作成に失敗: %v", err)
		}
	}

	uploader := &LocalUploader{baseDir: baseDir}
	_, err := uploader.UploadDirectory(context.Background(), srcDir, "uploads/test")
	if err == nil {
		t.Error("マスターファイルがないのにエラーが返されなかった")
	}

	if !strings.Contains(err.Error(), "master playlist/manifest not found") {
		t.Errorf("エラーメッセージが期待と異なる: %v", err)
	}
}

func TestLocalUploaderが存在しないファイルでエラーを返す(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "storage")

	uploader := &LocalUploader{baseDir: baseDir}
	_, err := uploader.Upload(context.Background(), "/存在しないファイル.txt", "test.txt")
	if err == nil {
		t.Error("存在しないファイルでエラーが返されなかった")
	}
}

func TestLocalUploaderがサブディレクトリを作成する(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "storage")
	srcDir := filepath.Join(tempDir, "src")

	mustMkdirAll(t, srcDir)

	// テスト用ファイルを作成
	srcFile := filepath.Join(srcDir, "test.txt")
	if err := os.WriteFile(srcFile, []byte("test"), 0644); err != nil {
		t.Fatalf("テストファイルの作成に失敗: %v", err)
	}

	uploader := &LocalUploader{baseDir: baseDir}

	// 深い階層にアップロード
	_, err := uploader.Upload(context.Background(), srcFile, "a/b/c/test.txt")
	if err != nil {
		t.Fatalf("アップロードに失敗: %v", err)
	}

	// ディレクトリが作成されたか確認
	destFile := filepath.Join(baseDir, "a/b/c/test.txt")
	if _, err := os.Stat(destFile); os.IsNotExist(err) {
		t.Error("サブディレクトリが作成されていない")
	}
}

func TestNewUploaderがs3タイプでエラーを返す(t *testing.T) {
	// S3_BUCKET が設定されていない場合
	mustUnsetenv(t, "S3_BUCKET")
	mustUnsetenv(t, "S3_REGION")

	_, err := NewUploader(context.Background(), "s3")
	if err == nil {
		t.Error("S3_BUCKET が未設定なのにエラーが返されなかった")
	}

	if !strings.Contains(err.Error(), "S3_BUCKET") {
		t.Errorf("エラーメッセージが S3_BUCKET を含んでいない: %v", err)
	}
}

func TestNewUploaderがlocalタイプでLocalUploaderを返す(t *testing.T) {
	tempDir := t.TempDir()
	mustSetenv(t, "LOCAL_STORAGE_DIR", tempDir)
	defer func() {
		mustUnsetenv(t, "LOCAL_STORAGE_DIR")
	}()

	uploader, err := NewUploader(context.Background(), "local")
	if err != nil {
		t.Fatalf("local タイプで Uploader の作成に失敗: %v", err)
	}

	if uploader == nil {
		t.Fatal("Uploader が nil")
	}

	// LocalUploader 型であることを確認
	_, ok := uploader.(*LocalUploader)
	if !ok {
		t.Error("返された Uploader が LocalUploader 型でない")
	}
}

func TestNewUploaderが不正なタイプでエラーを返す(t *testing.T) {
	_, err := NewUploader(context.Background(), "不正なタイプ")
	if err == nil {
		t.Error("不正なタイプでエラーが返されなかった")
	}

	if !strings.Contains(err.Error(), "unsupported storage type") {
		t.Errorf("エラーメッセージが期待と異なる: %v", err)
	}
}

func TestLocalUploaderが大きなファイルをストリーミングコピーできる(t *testing.T) {
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "storage")
	srcDir := filepath.Join(tempDir, "src")

	mustMkdirAll(t, srcDir)

	// 大きなファイルを作成（1MB）
	srcFile := filepath.Join(srcDir, "large.bin")
	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if err := os.WriteFile(srcFile, data, 0644); err != nil {
		t.Fatalf("大きなファイルの作成に失敗: %v", err)
	}

	uploader := &LocalUploader{baseDir: baseDir}
	_, err := uploader.Upload(context.Background(), srcFile, "large.bin")
	if err != nil {
		t.Fatalf("大きなファイルのアップロードに失敗: %v", err)
	}

	// ファイルサイズが一致するか確認
	destFile := filepath.Join(baseDir, "large.bin")
	destData, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("コピーされたファイルの読み込みに失敗: %v", err)
	}

	if len(destData) != len(data) {
		t.Errorf("ファイルサイズが一致しない: 期待値 %d, 取得値 %d", len(data), len(destData))
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("ディレクトリの作成に失敗: %v", err)
	}
}

func mustSetenv(t *testing.T, key, value string) {
	t.Helper()
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("環境変数の設定に失敗: %v", err)
	}
}

func mustUnsetenv(t *testing.T, key string) {
	t.Helper()
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("環境変数の削除に失敗: %v", err)
	}
}

// Note: S3Uploader のテストは AWS SDK のモックが必要なため、
// ここでは基本的な初期化のテストのみを含めています。
// より詳細なテストを書くには、以下のようなモックライブラリを使用できます:
// - github.com/aws/aws-sdk-go-v2/service/s3/mocks (AWS 公式)
// - github.com/golang/mock (汎用モック)
//
// 参考実装:
// func TestS3Uploaderが初期化できる(t *testing.T) {
//     // AWS 認証情報のモックが必要
//     // または実際の AWS 環境が必要
// }
