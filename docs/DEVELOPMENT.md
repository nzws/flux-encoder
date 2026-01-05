## セットアップ

### 前提条件

- Go 1.22以上
- ffmpeg / ffprobe
- protoc
- Task (タスクランナー)

### インストール

```bash
# リポジトリをクローン
git clone https://github.com/nzws/flux-encoder.git
cd flux-encoder

# 依存関係をインストール
go mod download

# Protobufコード生成
task proto

# ビルド
task build
```

## 使い方

### Worker Node の起動

```bash
# 環境変数設定
export GRPC_PORT=50051
export MAX_CONCURRENT_JOBS=2
export WORK_DIR=/tmp/ffmpeg-jobs
export STORAGE_TYPE=s3
export S3_BUCKET=my-bucket
export S3_REGION=ap-northeast-1
export WORKER_ID=worker-1

# Worker起動
./bin/worker
```

または Task を使用:

```bash
task dev:worker
```

### Control Plane の起動

```bash
# 環境変数設定
export PORT=8080
export WORKER_NODES=localhost:50051
export WORKER_STARTUP_TIMEOUT=60

# Control Plane起動
./bin/controlplane
```

または Task を使用:

```bash
task dev:controlplane
```

### API使用例

```bash
# 単一ファイル出力（MP4）
curl -X POST http://localhost:8080/api/v1/jobs \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "input_url": "https://example.com/input.mp4",
    "preset": "720p_h264",
    "output": {
      "storage": "s3",
      "path": "outputs/video.mp4",
      "metadata": {}
    }
  }'

# HLS ストリーミング（ABR: 3品質）
curl -X POST http://localhost:8080/api/v1/jobs \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "input_url": "https://example.com/input.mp4",
    "preset": "hls_720p_abr",
    "output": {
      "storage": "s3",
      "path": "outputs/video_123/",
      "type": "hls",
      "metadata": {}
    }
  }'

# 進捗をSSEでストリーミング
curl -N http://localhost:8080/api/v1/jobs/{job_id}/stream \
  -H "Authorization: Bearer YOUR_API_KEY"
```

## 開発

### タスク一覧

```bash
# すべてのタスクを表示
task --list

# よく使うコマンド
task fmt      # コードフォーマット
task lint     # リント
task test     # テスト
task build    # ビルド
task ci       # CI用チェック
```

### プリセット

利用可能なプリセット:

**単一ファイル出力 (MP4)**
- `720p_h264`: HD 720p with H.264
- `1080p_h264`: Full HD 1080p with H.264
- `480p_h264`: SD 480p with H.264

**HLS ストリーミング**
- `hls_720p`: HLS 720p single variant (音声付き)
- `hls_720p_video_only`: HLS 720p single variant (映像のみ)
- `hls_720p_abr`: HLS with 3 quality variants - 720p/480p/360p (音声付き)
- `hls_720p_abr_video_only`: HLS with 3 quality variants - 720p/480p/360p (映像のみ)

プリセットは `internal/worker/preset/preset.go` で定義されています。

### 環境変数

#### Control Plane

| 変数名 | 説明 | デフォルト |
|--------|------|-----------|
| `PORT` | HTTPポート | `8080` |
| `WORKER_NODES` | Workerアドレス（カンマ区切り） | - |
| `WORKER_STARTUP_TIMEOUT` | Worker起動待ち時間（秒） | `60` |
| `ENV` | 環境（development/production） | `production` |
| `LOG_LEVEL` | ログレベル | `info` |

#### Worker Node

| 変数名 | 説明 | デフォルト |
|--------|------|-----------|
| `GRPC_PORT` | gRPCポート | `50051` |
| `MAX_CONCURRENT_JOBS` | 最大同時実行数 | `2` |
| `WORK_DIR` | 作業ディレクトリ | `/tmp/ffmpeg-jobs` |
| `STORAGE_TYPE` | ストレージタイプ（s3/local） | `s3` |
| `S3_BUCKET` | S3バケット名 | - |
| `S3_REGION` | S3リージョン | `us-east-1` |
| `WORKER_ID` | Worker識別子 | `worker-1` |
| `DISABLE_AUTO_SHUTDOWN` | 自動シャットダウン無効化（開発用：`true`または`1`） | - |
| `ENV` | 環境（development/production） | `production` |
| `LOG_LEVEL` | ログレベル | `info` |
