package uploader

import (
	"context"
)

// Uploader はファイルをアップロードするインターフェース
type Uploader interface {
	// Upload はファイルをアップロードし、アクセス可能なURLを返す
	Upload(ctx context.Context, localPath string, remotePath string) (string, error)

	// UploadDirectory はディレクトリを再帰的にアップロードし、マスターファイルのURLを返す
	UploadDirectory(ctx context.Context, localDir string, remoteDir string) (string, error)
}
