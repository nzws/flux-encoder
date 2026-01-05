package uploader

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/nzws/flux-encoder/internal/shared/logger"
	"github.com/nzws/flux-encoder/internal/shared/retry"
	"go.uber.org/zap"
)

// S3Uploader はS3にファイルをアップロードする
type S3Uploader struct {
	client *s3.Client
	bucket string
	region string
}

// NewS3Uploader は新しい S3Uploader を作成する
func NewS3Uploader(ctx context.Context, bucket, region string) (*S3Uploader, error) {
	// AWS設定をロード
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &S3Uploader{
		client: s3.NewFromConfig(cfg),
		bucket: bucket,
		region: region,
	}, nil
}

// Upload はファイルをS3にアップロードする
func (u *S3Uploader) Upload(ctx context.Context, localPath string, remotePath string) (string, error) {
	// ファイルを開く
	file, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			logger.Warn("Failed to close source file", zap.Error(err))
		}
	}()

	// ファイルサイズ取得
	fileInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to get file info: %w", err)
	}

	logger.Info("Uploading to S3",
		zap.String("bucket", u.bucket),
		zap.String("key", remotePath),
		zap.Int64("size", fileInfo.Size()),
	)

	// S3にアップロード（リトライあり）
	err = retry.Do(ctx, retry.DefaultConfig, func() error {
		// ファイルポインタを先頭に戻す
		if _, seekErr := file.Seek(0, 0); seekErr != nil {
			return fmt.Errorf("failed to seek file: %w", seekErr)
		}

		_, putErr := u.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(u.bucket),
			Key:    aws.String(remotePath),
			Body:   file,
		})
		return putErr
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload to S3 after retries: %w", err)
	}

	// URLを生成
	url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", u.bucket, u.region, remotePath)

	logger.Info("Upload completed",
		zap.String("url", url),
	)

	return url, nil
}

// UploadDirectory はディレクトリ全体を再帰的にS3にアップロードする
func (u *S3Uploader) UploadDirectory(ctx context.Context, localDir string, remoteDir string) (string, error) {
	uploadedFiles, err := u.uploadDirectoryFiles(ctx, localDir, remoteDir)
	if err != nil {
		return "", err
	}

	// マスタープレイリスト/マニフェストのURLを返す
	// HLS: master.m3u8 or playlist.m3u8
	// DASH: manifest.mpd
	masterFile, err := findMasterFile(uploadedFiles)
	if err != nil {
		return "", err
	}

	// S3のキーをスラッシュ区切りに変換
	masterKey := filepath.ToSlash(filepath.Join(remoteDir, masterFile))
	masterURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s",
		u.bucket, u.region, masterKey)

	logger.Info("Directory upload completed",
		zap.String("url", masterURL),
		zap.Int("files", len(uploadedFiles)),
	)

	return masterURL, nil
}

// NewUploader は環境変数から適切な Uploader を作成する
func NewUploader(ctx context.Context, storageType string) (Uploader, error) {
	switch storageType {
	case "s3":
		bucket := os.Getenv("S3_BUCKET")
		region := os.Getenv("S3_REGION")
		if bucket == "" {
			return nil, fmt.Errorf("S3_BUCKET environment variable is required")
		}
		if region == "" {
			region = "us-east-1" // デフォルト
		}
		return NewS3Uploader(ctx, bucket, region)

	case "local":
		// テスト用: ローカルファイルシステムに保存
		return &LocalUploader{baseDir: os.Getenv("LOCAL_STORAGE_DIR")}, nil

	default:
		return nil, fmt.Errorf("unsupported storage type: %s", storageType)
	}
}

// LocalUploader はローカルファイルシステムにファイルを保存する（テスト用）
type LocalUploader struct {
	baseDir string
}

// Upload はファイルをローカルにコピーする
func (u *LocalUploader) Upload(ctx context.Context, localPath string, remotePath string) (string, error) {
	destPath := filepath.Join(u.baseDir, remotePath)

	// ディレクトリ作成
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// ファイルをストリーミングコピー（メモリ効率的）
	srcFile, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to open source file: %w", err)
	}
	defer func() {
		if err := srcFile.Close(); err != nil {
			logger.Warn("Failed to close source file", zap.Error(err))
		}
	}()

	dstFile, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() {
		if err := dstFile.Close(); err != nil {
			logger.Warn("Failed to close destination file", zap.Error(err))
		}
	}()

	// ストリーミングコピー
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return "", fmt.Errorf("failed to copy file: %w", err)
	}

	return "file://" + destPath, nil
}

// UploadDirectory はディレクトリをローカルにコピーする
func (u *LocalUploader) UploadDirectory(ctx context.Context, localDir string, remoteDir string) (string, error) {
	destDir := filepath.Join(u.baseDir, remoteDir)
	uploadedFiles, err := copyDirectory(localDir, destDir)
	if err != nil {
		return "", err
	}

	masterFile, err := findMasterFile(uploadedFiles)
	if err != nil {
		return "", err
	}

	masterPath := filepath.Join(destDir, masterFile)
	return "file://" + masterPath, nil
}

func (u *S3Uploader) uploadDirectoryFiles(ctx context.Context, localDir, remoteDir string) ([]string, error) {
	var uploadedFiles []string
	err := filepath.WalkDir(localDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}

		s3Key := filepath.ToSlash(filepath.Join(remoteDir, relPath))
		logger.Info("Uploading file to S3",
			zap.String("local", path),
			zap.String("s3_key", s3Key),
		)

		if _, err := u.Upload(ctx, path, s3Key); err != nil {
			return fmt.Errorf("failed to upload %s: %w", relPath, err)
		}

		uploadedFiles = append(uploadedFiles, relPath)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upload directory: %w", err)
	}
	return uploadedFiles, nil
}

func copyDirectory(srcDir, destDir string) ([]string, error) {
	var uploadedFiles []string
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open source file: %w", err)
		}
		defer func() {
			if err := srcFile.Close(); err != nil {
				logger.Warn("Failed to close source file", zap.Error(err))
			}
		}()

		dstFile, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("failed to create destination file: %w", err)
		}
		defer func() {
			if err := dstFile.Close(); err != nil {
				logger.Warn("Failed to close destination file", zap.Error(err))
			}
		}()

		if _, err := io.Copy(dstFile, srcFile); err != nil {
			return fmt.Errorf("failed to copy file: %w", err)
		}

		uploadedFiles = append(uploadedFiles, relPath)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return uploadedFiles, nil
}

func findMasterFile(files []string) (string, error) {
	masterFile := ""
	for _, file := range files {
		if strings.HasSuffix(file, "master.m3u8") {
			return file, nil
		}
		if strings.HasSuffix(file, "playlist.m3u8") && masterFile == "" {
			masterFile = file
		}
		if strings.HasSuffix(file, "manifest.mpd") && masterFile == "" {
			masterFile = file
		}
	}

	if masterFile == "" {
		return "", fmt.Errorf("master playlist/manifest not found in uploaded files")
	}
	return masterFile, nil
}
