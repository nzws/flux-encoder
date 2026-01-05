# flux-encoder システム設計書

## 概要

ffmpegをバックエンドとしたビデオエンコーディングサービス。Control PlaneとWorker Nodeの2層アーキテクチャで構成され、REST APIでジョブを受け付け、分散ワーカーでエンコード処理を実行する。

## アーキテクチャ

```
┌─────────────┐
│   Client    │
└──────┬──────┘
       │ REST API (JSON)
       │ SSE (進捗通知)
       ▼
┌─────────────────────────────┐
│     Control Plane           │
│  - REST API Server          │
│  - Authentication           │
│  - Worker Load Balancer     │
│  - gRPC Client              │
└──────────┬──────────────────┘
           │ gRPC (型安全)
           │ - ジョブ配信
           │ - 進捗ストリーム
           ▼
    ┌──────────────┐
    │ Worker Node  │──┐
    │ - gRPC       │  │
    │ - ffmpeg     │  │ 複数台
    │ - Uploader   │  │
    └──────────────┘──┘
           │
           ▼
    ┌──────────────┐
    │   S3/FTP/    │
    │   Storage    │
    └──────────────┘
```

## コンポーネント設計

### 1. Control Plane

**責務**
- クライアントからのジョブリクエスト受付
- API認証・認可
- Workerの負荷状況を監視してジョブを振り分け
- クライアントへの進捗通知（SSE）

**特徴**
- **ステートレス設計**: ジョブ状態はWorkerが保持、Control Planeは状態を持たない
- **サーバーレス対応**: 永続化層を持たないため、AWS Lambda等でも動作可能
- **水平スケール可能**: 複数インスタンス起動可能

**API エンドポイント**
- `POST /api/v1/jobs` - ジョブ作成
- `GET /api/v1/jobs/:id/stream` - 進捗ストリーム（SSE）
- `GET /api/v1/workers/status` - 全Worker状態確認（管理用）

**環境変数設定例**
```env
# Worker Nodes (カンマ区切り)
WORKER_NODES=worker1.internal:50051,worker2.internal:50051,worker3.internal:50051

# 認証
API_KEY=your-secret-api-key

# タイムアウト
JOB_TIMEOUT=3600s
WORKER_STARTUP_TIMEOUT=60s  # Worker起動待ち時間（停止中のWorkerが起動するまで待つ）
```

### 2. Worker Node

**責務**
- gRPCサーバーとしてジョブを受信
- ffmpegを実行してエンコード処理
- 進捗情報をストリームで返す
- 成果物をストレージにアップロード
- **ジョブ完了後の自動停止**: 処理中ジョブが0になったら即座にプロセスを終了（コスト最適化）

**特徴**
- **外部公開なし**: 内部gRPC APIのみ、HTTPは不要
- **並行実行制御**: 同時実行数の上限を設定可能
- **リソース管理**: CPU/メモリを考慮した実行数制限

**gRPC API**
- `SubmitJob(JobRequest) returns (stream JobProgress)` - ジョブ実行（双方向ストリーム）
- `GetStatus() returns (WorkerStatus)` - Worker状態取得（実行中ジョブ数、最大同時実行数など）
- `CancelJob(JobID) returns (CancelResponse)` - ジョブキャンセル

**環境変数設定例**
```env
# gRPCポート
GRPC_PORT=50051

# 同時実行数
MAX_CONCURRENT_JOBS=2

# 作業ディレクトリ
WORK_DIR=/tmp/ffmpeg-jobs

# ストレージ設定
STORAGE_TYPE=s3
S3_BUCKET=my-encoded-videos
S3_REGION=ap-northeast-1
```

## データ構造

### ジョブリクエスト（REST API）

```json
{
  "input_url": "https://example.com/source.mp4",
  "preset": "720p_h264",
  "output": {
    "storage": "s3",
    "path": "outputs/video_123.mp4",
    "metadata": {
      "title": "Sample Video"
    }
  },
  "callback_url": "https://example.com/webhook"
}
```

### プリセット定義

プリセットはWorker側で定義し、以下のような構造を想定：

```go
type Preset struct {
    Name        string
    Description string
    FFmpegArgs  []string
    Extension   string
}

// 例: 720p H.264
Preset{
    Name:        "720p_h264",
    Description: "HD 720p with H.264 encoding",
    FFmpegArgs: []string{
        "-vf", "scale=-2:720",
        "-c:v", "libx264",
        "-preset", "medium",
        "-crf", "23",
        "-c:a", "aac",
        "-b:a", "128k",
    },
    Extension: "mp4",
}
```

### 進捗情報（SSE / gRPC Stream）

```json
{
  "job_id": "job_123",
  "status": "processing",
  "progress": 45.5,
  "message": "Encoding frame 1234/5678",
  "timestamp": "2026-01-01T12:34:56Z"
}
```

ステータス値:
- `queued` - ジョブが受け付けられた
- `processing` - エンコード中
- `uploading` - アップロード中
- `completed` - 完了
- `failed` - 失敗

## 通信フロー

### ジョブ実行フロー

```
1. Client → Control Plane
   POST /api/v1/jobs
   { "input_url": "...", "preset": "720p_h264" }

2. Control Plane
   - API認証
   - 利用可能なWorkerを選択（gRPC GetStatus()で各Workerの負荷確認）
   - 最も空いているWorkerにジョブを送信

3. Control Plane → Worker
   gRPC SubmitJob(JobRequest) → stream JobProgress
   - Workerがジョブを受け付け、streamを開く

4. Worker
   - ffmpegプロセス起動
   - 進捗をパース（ffmpegの出力からframe/time情報を抽出）
   - gRPC streamで進捗を送信

5. Worker → Control Plane
   stream JobProgress { status: "processing", progress: 45.5% }

6. Control Plane → Client
   SSE: data: {"status":"processing","progress":45.5}

7. Worker
   - エンコード完了
   - Uploaderで成果物をS3/FTPにアップロード
   - 完了通知を送信

8. Worker → Control Plane
   stream JobProgress { status: "completed", output_url: "s3://..." }

9. Control Plane → Client
   SSE: data: {"status":"completed","output_url":"..."}
   HTTP Response: 200 OK { "job_id": "...", "output_url": "..." }
```

### Worker選択アルゴリズム

コスト効率を重視し、必要最小限の通信で空きWorkerを見つける方式：

1. **ラウンドロビンで開始位置を決定**: 前回選択したWorkerの次から確認開始（負荷分散）
2. **順次確認**: Workerリストを順番に確認し、各Workerに`GetStatus()`をcall
   - `GetStatus()`のタイムアウトは`WORKER_STARTUP_TIMEOUT`（例: 60秒）
   - Workerが停止している場合、クラウド側が自動起動し、起動完了を待つ
3. **最初の空きWorkerを選択**: `current_jobs < max_concurrent_jobs`の最初のWorkerにジョブを割り当て
4. **全Worker満杯の場合**: すべて確認して空きがなければ`503 Service Unavailable`を返す

**メリット**:
- 無駄な通信コストを削減（平均して全Worker数の半分程度の確認で済む）
- Workerの起動・通信コストを最小化
- ラウンドロビンにより負荷が特定Workerに偏らない
- 停止中のWorkerも自動起動されるため、コスト最適化と可用性を両立

**実装例**:
```go
var lastWorkerIndex int // グローバルで保持（またはControl Plane構造体のフィールド）

func SelectWorker(workers []string, timeout time.Duration) (string, error) {
    startIdx := (lastWorkerIndex + 1) % len(workers)

    for i := 0; i < len(workers); i++ {
        idx := (startIdx + i) % len(workers)
        worker := workers[idx]

        // Worker起動待ちを考慮したタイムアウト設定
        ctx, cancel := context.WithTimeout(context.Background(), timeout)
        defer cancel()

        status, err := getWorkerStatus(ctx, worker)
        if err != nil {
            // タイムアウトまたはエラー: このWorkerはスキップ
            continue
        }

        if status.CurrentJobs < status.MaxConcurrentJobs {
            lastWorkerIndex = idx
            return worker, nil
        }
    }

    return "", errors.New("no available workers")
}
```

## コスト最適化

### Worker自動停止の仕組み

Workerは処理中のジョブがなくなると即座にプロセスを終了し、クラウドサービス側で自動的にインスタンスを停止します。これにより、使用していない時間のコストを削減できます。

**前提となるクラウドサービスの機能**:
- **オンデマンド起動**: HTTPリクエストやgRPCリクエストが来たときに自動的にインスタンスを起動
- **自動停止**: プロセスが終了するとインスタンスも自動的に停止
- **例**: Fly.io Machines、Google Cloud Run、AWS App Runner、Azure Container Apps等

### Worker停止ロジック

```go
type Worker struct {
    activeJobs int32 // atomic操作でカウント
    server     *grpc.Server
    // ...
}

func (w *Worker) SubmitJob(req *JobRequest, stream JobService_SubmitJobServer) error {
    // ジョブ開始時にカウントアップ
    atomic.AddInt32(&w.activeJobs, 1)
    defer func() {
        // ジョブ完了時にカウントダウン
        newCount := atomic.AddInt32(&w.activeJobs, -1)

        // ジョブがなくなったら停止
        if newCount == 0 {
            go w.gracefulShutdown()
        }
    }()

    // ffmpegでエンコード処理
    // ...
}

func (w *Worker) gracefulShutdown() {
    // 念のため少し待機（新しいジョブが来る可能性を考慮）
    time.Sleep(1 * time.Second)

    // まだジョブがないことを確認
    if atomic.LoadInt32(&w.activeJobs) == 0 {
        log.Info("No active jobs, shutting down...")
        w.server.GracefulStop()
        os.Exit(0)
    }
}
```

### Control Plane側の考慮

Control PlaneからWorkerへのリクエスト時、Workerが停止している場合：

1. **クラウド側が自動起動**: gRPCリクエストをトリガーにWorkerインスタンスが起動
2. **起動完了を待つ**: `WORKER_STARTUP_TIMEOUT`（例: 60秒）まで接続を待つ
3. **接続成功**: Worker起動完了、ジョブを送信
4. **タイムアウト**: 次のWorkerを試す

**起動時間の目安**:
- コンテナイメージが小さい場合: 5-15秒
- ffmpeg含む場合: 15-30秒
- 初回起動（イメージpull必要）: 30-60秒

### コスト削減効果

**例**: Worker 3台、1台あたり$0.01/分の場合

| シナリオ | 従来（常時起動） | 自動停止 | 削減率 |
|---------|----------------|---------|-------|
| 営業時間のみ利用（8時間/日） | $43.2/月 | $14.4/月 | **67%削減** |
| バースト的利用（1時間/日） | $43.2/月 | $1.8/月 | **96%削減** |

※実際のコストはクラウドサービスの料金体系により異なります

### 注意事項

- **コールドスタート**: 停止中のWorkerへの最初のリクエストは起動時間分遅延
- **同時大量リクエスト**: 複数Workerが同時に起動する場合、クラウドのリソース制限に注意
- **最小課金時間**: 一部クラウドサービスには最小課金時間（例: 1分単位）があり、短時間ジョブでは効果が限定的

## 拡張性の考慮

### Uploader インターフェース

異なるストレージバックエンドに対応するため、Uploaderを抽象化：

```go
type Uploader interface {
    Upload(ctx context.Context, localPath string, remotePath string) (string, error)
    // 戻り値: アクセス可能なURL
}

// 実装例
type S3Uploader struct { /* ... */ }
type FTPUploader struct { /* ... */ }
type LocalUploader struct { /* ... */ } // テスト用
```

環境変数`STORAGE_TYPE`でアップローダーを切り替え：

```go
func NewUploader(storageType string) (Uploader, error) {
    switch storageType {
    case "s3":
        return NewS3Uploader(), nil
    case "ftp":
        return NewFTPUploader(), nil
    case "local":
        return NewLocalUploader(), nil
    default:
        return nil, fmt.Errorf("unknown storage type: %s", storageType)
    }
}
```

### プリセット追加

Workerのコード内でプリセットを定義し、設定ファイル化することも可能：

```yaml
# presets.yaml
presets:
  - name: 720p_h264
    description: "HD 720p with H.264"
    ffmpeg_args:
      - "-vf"
      - "scale=-2:720"
      - "-c:v"
      - "libx264"
      # ...
    extension: "mp4"

  - name: 1080p_h265
    description: "Full HD with H.265"
    ffmpeg_args:
      - "-vf"
      - "scale=-2:1080"
      - "-c:v"
      - "libx265"
      # ...
    extension: "mp4"
```

## 技術スタック

### Control Plane
- **Webフレームワーク**: `gin-gonic/gin`（軽量でサーバーレス対応）
- **gRPCクライアント**: `google.golang.org/grpc`
- **SSE**: gin標準機能 or カスタムハンドラー
- **認証**: API Key（カスタムミドルウェア）

### Worker Node
- **gRPCサーバー**: `google.golang.org/grpc`
- **ffmpegラッパー**: `os/exec`でプロセス起動、stdoutパース
- **S3クライアント**: `aws/aws-sdk-go-v2`
- **並行制御**: `sync`パッケージ（セマフォパターン）

### 共通
- **Protobuf定義**: gRPC通信用
- **ロギング**: `uber-go/zap`（構造化ログ）
- **設定管理**: 環境変数（`12 Factor App`）

## ディレクトリ構成案

```
flux-encoder/
├── cmd/
│   ├── controlplane/     # Control Plane エントリーポイント
│   │   └── main.go
│   └── worker/           # Worker エントリーポイント
│       └── main.go
├── internal/
│   ├── controlplane/     # Control Plane ロジック
│   │   ├── api/          # REST API ハンドラー
│   │   ├── auth/         # 認証ミドルウェア
│   │   ├── balancer/     # Worker負荷分散
│   │   └── sse/          # SSE ストリーム管理
│   ├── worker/           # Worker ロジック
│   │   ├── grpc/         # gRPC サーバー実装
│   │   ├── encoder/      # ffmpeg ラッパー
│   │   ├── uploader/     # アップローダー (S3/FTP/etc)
│   │   └── preset/       # プリセット定義
│   └── shared/           # 共通ユーティリティ
│       └── logger/
├── proto/                # Protobuf 定義
│   └── worker/
│       └── v1/
│           └── worker.proto
├── pkg/                  # 外部公開可能なライブラリ（必要に応じて）
├── configs/              # 設定ファイル（プリセット定義など）
│   └── presets.yaml
├── scripts/              # ビルド・デプロイスクリプト
├── deployments/          # Docker/K8s設定
│   ├── Dockerfile.controlplane
│   └── Dockerfile.worker
├── docs/                 # ドキュメント
├── .golangci.yml         # golangci-lint設定
├── Taskfile.yml          # タスクランナー設定
├── go.mod
├── go.sum
└── README.md
```

## 開発環境・ツール

### 必須ツール

#### 1. Go本体
- **推奨バージョン**: Go 1.22以上
- インストール: https://go.dev/dl/

#### 2. フォーマッター
Goは厳格なフォーマット規則があり、以下のツールで自動整形します。

- **gofmt**: Go標準のコードフォーマッター
  ```bash
  # プロジェクト全体をフォーマット
  gofmt -s -w .
  ```
  - `-s`: 簡略化（例: `x[0:len(x)]` → `x[:]`）
  - `-w`: ファイルを直接書き換え

- **goimports** (推奨): gofmtに加えてimport文も自動整理
  ```bash
  # インストール
  go install golang.org/x/tools/cmd/goimports@latest

  # 実行
  goimports -w .
  ```
  - 未使用のimportを削除
  - 不足しているimportを自動追加
  - 標準ライブラリとサードパーティを分けて整列

**IDE設定**: VSCode、GoLandなどのIDEは保存時に自動でgoimportsを実行可能

#### 3. リンター
コードの問題を静的解析で検出します。

- **golangci-lint** (最推奨): 複数のリンターを統合したメタリンター
  ```bash
  # インストール
  go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

  # 実行
  golangci-lint run

  # 修正可能な問題を自動修正
  golangci-lint run --fix
  ```

  設定ファイル `.golangci.yml` を作成：
  ```yaml
  linters:
    enable:
      - gofmt          # フォーマットチェック
      - goimports      # importチェック
      - govet          # 疑わしい構造を検出
      - errcheck       # エラーハンドリング漏れ検出
      - staticcheck    # 高度な静的解析
      - gosimple       # 簡略化可能なコードを指摘
      - unused         # 未使用のコード検出
      - ineffassign    # 無駄な代入検出
      - typecheck      # 型チェック
      - misspell       # スペルミス検出
      - goconst        # 定数化可能な文字列検出
      - gocyclo        # 循環的複雑度チェック
      - gosec          # セキュリティ問題検出

  linters-settings:
    gocyclo:
      min-complexity: 15  # 関数の複雑度上限
    goconst:
      min-len: 3
      min-occurrences: 3

  issues:
    exclude-use-default: false
    max-same-issues: 0

  run:
    timeout: 5m
  ```

- **go vet**: Go標準の静的解析ツール（golangci-lintに含まれる）
  ```bash
  go vet ./...
  ```

#### 4. テストツール

- **go test**: 標準のテストツール
  ```bash
  # すべてのテストを実行
  go test ./...

  # 詳細出力
  go test -v ./...

  # カバレッジ測定
  go test -cover ./...

  # カバレッジレポート生成
  go test -coverprofile=coverage.out ./...
  go tool cover -html=coverage.out

  # 競合検出（race detector）
  go test -race ./...
  ```

#### 5. 依存関係管理

- **go mod**: Go標準の依存関係管理
  ```bash
  # go.modの初期化（プロジェクト開始時）
  go mod init github.com/nzws/flux-encoder

  # 依存をダウンロード
  go mod download

  # 不要な依存を削除、不足している依存を追加
  go mod tidy

  # 依存の整合性を確認
  go mod verify

  # vendorディレクトリに依存をコピー（オプション）
  go mod vendor
  ```

#### 6. Protobuf / gRPC

- **protoc**: Protocol Buffers コンパイラ
- **protoc-gen-go**: Go用プラグイン
- **protoc-gen-go-grpc**: gRPC用プラグイン

  ```bash
  # インストール
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

  # .protoファイルからGoコード生成
  protoc --go_out=. --go_opt=paths=source_relative \
         --go-grpc_out=. --go-grpc_opt=paths=source_relative \
         proto/worker/v1/worker.proto
  ```

### Taskfile の活用

開発タスクを簡単に実行できるよう、Taskfileを作成することを推奨。TaskfileはYAML形式で書けるタスクランナーで、Node.jsの`package.json`の`scripts`に近い感覚で使えます。

#### インストール

```bash
# macOS
brew install go-task/tap/go-task

# Linux/WSL
sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b ~/.local/bin

# または Go経由
go install github.com/go-task/task/v3/cmd/task@latest
```

#### Taskfile.yml

プロジェクトルートに`Taskfile.yml`を作成：

```yaml
version: '3'

vars:
  PROTO_PATH: proto/worker/v1
  BIN_DIR: bin

tasks:
  # フォーマット
  fmt:
    desc: Format Go code
    cmds:
      - goimports -w .
      - gofmt -s -w .

  # リント
  lint:
    desc: Run linters
    cmds:
      - golangci-lint run

  # テスト
  test:
    desc: Run tests
    cmds:
      - go test -v -race -cover ./...

  # テストカバレッジ
  coverage:
    desc: Generate test coverage report
    cmds:
      - go test -coverprofile=coverage.out ./...
      - go tool cover -html=coverage.out

  # ビルド
  build:
    desc: Build binaries
    deps: [build:controlplane, build:worker]

  build:controlplane:
    desc: Build Control Plane
    sources:
      - cmd/controlplane/**/*.go
      - internal/controlplane/**/*.go
    generates:
      - "{{.BIN_DIR}}/controlplane"
    cmds:
      - mkdir -p {{.BIN_DIR}}
      - go build -o {{.BIN_DIR}}/controlplane ./cmd/controlplane

  build:worker:
    desc: Build Worker
    sources:
      - cmd/worker/**/*.go
      - internal/worker/**/*.go
    generates:
      - "{{.BIN_DIR}}/worker"
    cmds:
      - mkdir -p {{.BIN_DIR}}
      - go build -o {{.BIN_DIR}}/worker ./cmd/worker

  # Protobuf生成
  proto:
    desc: Generate protobuf code
    sources:
      - "{{.PROTO_PATH}}/*.proto"
    generates:
      - "{{.PROTO_PATH}}/*.pb.go"
    cmds:
      - protoc --go_out=. --go_opt=paths=source_relative
               --go-grpc_out=. --go-grpc_opt=paths=source_relative
               {{.PROTO_PATH}}/worker.proto

  # 依存関係整理
  tidy:
    desc: Tidy Go modules
    cmds:
      - go mod tidy
      - go mod verify

  # クリーンアップ
  clean:
    desc: Clean build artifacts
    cmds:
      - rm -rf {{.BIN_DIR}}
      - rm -f coverage.out

  # すべてのチェック（CI用）
  ci:
    desc: Run all checks (format, lint, test)
    deps: [fmt, lint, test]
    cmds:
      - echo "All checks passed!"

  # 開発用: Control Planeを起動
  dev:controlplane:
    desc: Run Control Plane in dev mode
    deps: [build:controlplane]
    cmds:
      - "{{.BIN_DIR}}/controlplane"

  # 開発用: Workerを起動
  dev:worker:
    desc: Run Worker in dev mode
    deps: [build:worker]
    cmds:
      - "{{.BIN_DIR}}/worker"
```

#### 使い方

```bash
# タスク一覧を表示
task --list

# 実行例
task fmt          # フォーマット
task lint         # リント実行
task test         # テスト実行
task build        # ビルド（controlplane + worker）
task ci           # CI用チェック（fmt + lint + test）

# 開発用
task dev:controlplane   # Control Planeを起動
task dev:worker         # Workerを起動
```

#### Taskfile のメリット

- **YAML形式**: `package.json`に近い感覚で書ける
- **依存関係管理**: `deps`でタスク間の依存を定義
- **インクリメンタルビルド**: `sources`と`generates`でファイル変更を検知
- **クロスプラットフォーム**: Windows/macOS/Linuxで動作
- **変数・テンプレート**: `{{.VAR}}`で変数を使える

### CI/CD設定

GitHub Actionsの設定例 `.github/workflows/ci.yml`：

```yaml
name: CI

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4

    - name: Set up Go
      uses: actions/setup-go@v5
      with:
        go-version: '1.22'

    - name: Cache Go modules
      uses: actions/cache@v4
      with:
        path: ~/go/pkg/mod
        key: ${{ runner.os }}-go-${{ hashFiles('**/go.sum') }}
        restore-keys: |
          ${{ runner.os }}-go-

    - name: Install Task
      uses: arduino/setup-task@v2
      with:
        version: 3.x

    - name: Download dependencies
      run: go mod download

    - name: Run CI checks
      run: task ci

    - name: Upload coverage
      uses: codecov/codecov-action@v4
      with:
        file: ./coverage.out
```

**ポイント**: Taskfileを使うことで、ローカル開発とCI/CDで同じコマンド（`task ci`）を実行できます。

### エディタ設定

#### VSCode (推奨)
拡張機能をインストール：
- **Go** (by Go Team at Google)

`settings.json` に追加：
```json
{
  "[go]": {
    "editor.formatOnSave": true,
    "editor.codeActionsOnSave": {
      "source.organizeImports": true
    }
  },
  "go.useLanguageServer": true,
  "go.lintTool": "golangci-lint",
  "go.lintOnSave": "workspace"
}
```

#### GoLand / IntelliJ IDEA
- Go Pluginをインストール
- Settings → Tools → File Watchers でgoimportsを設定
- Settings → Editor → Inspections で各種チェックを有効化

### 開発ワークフロー

1. **コード変更**
2. **フォーマット**: `task fmt`（IDEで自動化推奨）
3. **リント**: `task lint`
4. **テスト**: `task test`
5. **コミット前**: `task ci` ですべてのチェックを実行
6. **Push**: CI/CDが自動でチェック（同じ`task ci`が実行される）

**ポイント**: ローカルとCI/CDで同じコマンドを実行できるので、CIで失敗する前にローカルで問題を発見できます。

### よくあるリントエラーと対処法

| エラー | 説明 | 対処法 |
|--------|------|--------|
| `Error return value is not checked` | エラーハンドリング漏れ | `if err != nil { ... }` を追加 |
| `unused variable/import` | 未使用の変数/import | 削除するか `_` で明示的に無視 |
| `ineffectual assignment` | 無駄な代入 | 不要なコードを削除 |
| `should have comment` | 公開関数にコメントがない | `// FuncName は...` とコメント追加 |
| `cognitive complexity is high` | 関数が複雑すぎる | 関数を分割してシンプルに |

## セキュリティ考慮事項

### Control Plane
- **API認証**: Bearer Token（API Key）によるアクセス制御
- **Rate Limiting**: クライアントごとのリクエスト数制限
- **入力検証**: input_urlのバリデーション（許可されたスキーマのみ）

### Worker
- **ネットワーク分離**: 内部ネットワークのみアクセス可能、外部公開しない
- **ffmpeg実行**: プリセット方式により任意のコマンド実行を防止
- **リソース制限**: cgroup/Docker等でCPU/メモリ制限

### データ
- **一時ファイル**: 処理後は確実に削除
- **認証情報**: 環境変数で管理、ハードコーディング禁止

## スケーラビリティ

### Control Plane
- **水平スケール**: ステートレスなため、複数インスタンス起動可能
- **ロードバランサー**: ALB/NLB等で複数インスタンスに負荷分散

### Worker
- **動的スケール**: 負荷に応じてWorkerインスタンスを増減
- **オートスケーリング**: Kubernetes HPA、AWS Auto Scaling等

### 制限事項
- Control PlaneがWorkerリストを静的に保持するため、動的なWorker追加は再起動が必要
- 将来的にサービスディスカバリ（Consul、etcd等）を導入することで動的登録に対応可能

## モニタリング・可観測性

### メトリクス
- **Control Plane**:
  - リクエスト数、レスポンスタイム
  - アクティブSSE接続数
  - Worker別ジョブ配信数

- **Worker**:
  - 実行中ジョブ数、完了数、失敗数
  - エンコード時間、アップロード時間
  - ffmpegプロセスのリソース使用率

### ログ
- 構造化ログ（JSON形式）
- ジョブIDをすべてのログに含める（トレーサビリティ）

### トレーシング（将来的）
- OpenTelemetryによる分散トレーシング
- ジョブの各段階（受付→エンコード→アップロード）を追跡

## エラーハンドリング

### Control Plane
- Worker全台が満杯の場合: `503 Service Unavailable` + Retry-After
- Workerとの通信エラー: 別のWorkerにリトライ、全台失敗で`500 Internal Server Error`
- タイムアウト: `JOB_TIMEOUT`を超えたらジョブをキャンセル、`504 Gateway Timeout`

### Worker
- ffmpeg失敗: エラーログを保存し、Control Planeに`failed`ステータスを返す
- アップロード失敗: リトライロジック（exponential backoff）、最終的に失敗通知

## HLS/DASH マルチファイル出力のサポート設計

### 概要

現在のシステムは単一ファイル出力（MP4等）のみをサポートしていますが、HLS（HTTP Live Streaming）やDASH（Dynamic Adaptive Streaming over HTTP）のようなアダプティブビットレートストリーミング形式では、1つの入力から大量のファイル（プレイリスト + セグメント）を生成する必要があります。

### 現状の制約

- **Encoder**: 単一の出力ファイルパスを想定 (`outputFile := filepath.Join(jobDir, fmt.Sprintf("output.%s", preset.Extension))`)
- **Uploader**: 単一ファイルのアップロードのみ対応 (`Upload(ctx, localPath, remotePath)`)
- **OutputConfig**: 単一の`path`フィールドのみ

### 必要な変更点

#### 1. API設計の拡張

**OutputConfig の拡張**

```go
type OutputConfig struct {
    Storage  string            `json:"storage" binding:"required"`
    Path     string            `json:"path" binding:"required"`
    Metadata map[string]string `json:"metadata"`

    // 新規追加
    Type     string            `json:"type"`  // "single" (default), "hls", "dash"
}
```

**リクエスト例（HLS）**

```json
{
  "input_url": "https://example.com/source.mp4",
  "preset": "hls_720p",
  "output": {
    "storage": "s3",
    "path": "outputs/video_123/",  // ディレクトリパス（末尾スラッシュ）
    "type": "hls",
    "metadata": {
      "title": "Sample Video"
    }
  }
}
```

#### 2. プリセットの拡張

**新しいプリセット構造**

```go
type Preset struct {
    Name        string
    Description string
    FFmpegArgs  []string
    Extension   string

    // 新規追加
    OutputType  string   // "single", "hls", "dash"
    OutputFiles []string // 生成されるファイルのパターン
}
```

**HLS プリセット例**

```go
// 720p HLS (ABR: 3 variants)
Preset{
    Name:        "hls_720p_abr",
    Description: "HLS with 3 quality variants (720p, 480p, 360p)",
    OutputType:  "hls",
    Extension:   "m3u8",
    FFmpegArgs: []string{
        // 3つの品質バリアント
        "-filter_complex",
        "[0:v]split=3[v1][v2][v3]; " +
        "[v1]scale=w=1280:h=720[v1out]; " +
        "[v2]scale=w=854:h=480[v2out]; " +
        "[v3]scale=w=640:h=360[v3out]",

        // 720p variant
        "-map", "[v1out]",
        "-c:v:0", "libx264",
        "-b:v:0", "2800k",
        "-maxrate:v:0", "3000k",
        "-bufsize:v:0", "6000k",

        // 480p variant
        "-map", "[v2out]",
        "-c:v:1", "libx264",
        "-b:v:1", "1400k",
        "-maxrate:v:1", "1500k",
        "-bufsize:v:1", "3000k",

        // 360p variant
        "-map", "[v3out]",
        "-c:v:2", "libx264",
        "-b:v:2", "800k",
        "-maxrate:v:2", "900k",
        "-bufsize:v:2", "1800k",

        // オーディオ
        "-map", "a:0",
        "-c:a", "aac",
        "-b:a", "128k",
        "-ac", "2",

        // HLS設定
        "-f", "hls",
        "-hls_time", "6",                    // セグメント長（秒）
        "-hls_playlist_type", "vod",         // VOD用
        "-hls_segment_filename", "segment_%v_%03d.ts",  // セグメントファイル名
        "-master_pl_name", "master.m3u8",    // マスタープレイリスト名
        "-var_stream_map", "v:0,a:0 v:1,a:0 v:2,a:0",  // ストリームマッピング
        "-hls_segment_type", "mpegts",       // セグメント形式
    },
    OutputFiles: []string{
        "master.m3u8",      // マスタープレイリスト
        "stream_*.m3u8",    // 各品質のプレイリスト
        "segment_*_*.ts",   // TSセグメント
    },
}
```

**シンプルなHLS（単一品質）**

```go
Preset{
    Name:        "hls_720p",
    Description: "HLS 720p single variant",
    OutputType:  "hls",
    Extension:   "m3u8",
    FFmpegArgs: []string{
        "-vf", "scale=-2:720",
        "-c:v", "libx264",
        "-b:v", "2500k",
        "-c:a", "aac",
        "-b:a", "128k",
        "-f", "hls",
        "-hls_time", "6",
        "-hls_playlist_type", "vod",
        "-hls_segment_filename", "segment_%03d.ts",
    },
    OutputFiles: []string{
        "playlist.m3u8",
        "segment_*.ts",
    },
}
```

**DASH プリセット例**

```go
Preset{
    Name:        "dash_1080p",
    Description: "DASH with multiple quality variants",
    OutputType:  "dash",
    Extension:   "mpd",
    FFmpegArgs: []string{
        "-filter_complex",
        "[0:v]split=2[v1][v2]; " +
        "[v1]scale=w=1920:h=1080[v1out]; " +
        "[v2]scale=w=1280:h=720[v2out]",

        // 1080p
        "-map", "[v1out]",
        "-c:v:0", "libx264",
        "-b:v:0", "5000k",

        // 720p
        "-map", "[v2out]",
        "-c:v:1", "libx264",
        "-b:v:1", "2500k",

        // オーディオ
        "-map", "a:0",
        "-c:a", "aac",
        "-b:a", "128k",

        // DASH設定
        "-f", "dash",
        "-seg_duration", "4",
        "-use_timeline", "1",
        "-use_template", "1",
        "-init_seg_name", "init_$RepresentationID$.m4s",
        "-media_seg_name", "chunk_$RepresentationID$_$Number$.m4s",
    },
    OutputFiles: []string{
        "manifest.mpd",
        "init_*.m4s",
        "chunk_*.m4s",
    },
}
```

#### 3. Encoder の変更

**変更前（単一ファイル）**

```go
func (e *Encoder) Encode(...) (string, error) {
    outputFile := filepath.Join(jobDir, fmt.Sprintf("output.%s", preset.Extension))
    args = append(args, outputFile)
    // ...
    return outputFile, nil
}
```

**変更後（ディレクトリ対応）**

```go
func (e *Encoder) Encode(...) (string, error) {
    var outputPath string

    if preset.OutputType == "hls" || preset.OutputType == "dash" {
        // ディレクトリを作成
        outputPath = filepath.Join(jobDir, "output")
        if err := os.MkdirAll(outputPath, 0755); err != nil {
            return "", fmt.Errorf("failed to create output directory: %w", err)
        }

        // ffmpegの出力先はディレクトリ内
        outputFile := filepath.Join(outputPath, fmt.Sprintf("output.%s", preset.Extension))
        args = append(args, outputFile)
    } else {
        // 単一ファイル
        outputPath = filepath.Join(jobDir, fmt.Sprintf("output.%s", preset.Extension))
        args = append(args, outputPath)
    }

    // ffmpeg実行
    // ...

    return outputPath, nil  // ファイルまたはディレクトリパスを返す
}
```

#### 4. Uploader の拡張

**インターフェース拡張**

```go
type Uploader interface {
    Upload(ctx context.Context, localPath string, remotePath string) (string, error)

    // 新規追加: ディレクトリアップロード
    UploadDirectory(ctx context.Context, localDir string, remoteDir string) (string, error)
}
```

**S3Uploader の実装**

```go
// UploadDirectory はディレクトリ全体を再帰的にS3にアップロードする
func (u *S3Uploader) UploadDirectory(ctx context.Context, localDir string, remoteDir string) (string, error) {
    var uploadedFiles []string

    // ディレクトリを再帰的に走査
    err := filepath.WalkDir(localDir, func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return err
        }

        // ディレクトリはスキップ
        if d.IsDir() {
            return nil
        }

        // ローカルパスから相対パスを取得
        relPath, err := filepath.Rel(localDir, path)
        if err != nil {
            return err
        }

        // S3のキーを構築
        s3Key := filepath.Join(remoteDir, relPath)

        // ファイルをアップロード
        logger.Info("Uploading file to S3",
            zap.String("local", path),
            zap.String("s3_key", s3Key),
        )

        url, err := u.Upload(ctx, path, s3Key)
        if err != nil {
            return fmt.Errorf("failed to upload %s: %w", relPath, err)
        }

        uploadedFiles = append(uploadedFiles, relPath)
        return nil
    })

    if err != nil {
        return "", fmt.Errorf("failed to upload directory: %w", err)
    }

    // マスタープレイリスト/マニフェストのURLを返す
    // HLS: master.m3u8 or playlist.m3u8
    // DASH: manifest.mpd
    masterFile := ""
    for _, file := range uploadedFiles {
        if strings.HasSuffix(file, "master.m3u8") ||
           strings.HasSuffix(file, "playlist.m3u8") ||
           strings.HasSuffix(file, "manifest.mpd") {
            masterFile = file
            break
        }
    }

    if masterFile == "" {
        return "", fmt.Errorf("master playlist/manifest not found")
    }

    masterURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s/%s",
        u.bucket, u.region, remoteDir, masterFile)

    logger.Info("Directory upload completed",
        zap.String("url", masterURL),
        zap.Int("files", len(uploadedFiles)),
    )

    return masterURL, nil
}
```

**LocalUploader の実装**

```go
func (u *LocalUploader) UploadDirectory(ctx context.Context, localDir string, remoteDir string) (string, error) {
    destDir := filepath.Join(u.baseDir, remoteDir)

    // ディレクトリをコピー
    err := filepath.WalkDir(localDir, func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return err
        }

        relPath, _ := filepath.Rel(localDir, path)
        destPath := filepath.Join(destDir, relPath)

        if d.IsDir() {
            return os.MkdirAll(destPath, 0755)
        }

        // ファイルをコピー
        return copyFile(path, destPath)
    })

    if err != nil {
        return "", err
    }

    // マスターファイルのURLを返す
    // 実装は S3Uploader と同様
    return "file://" + filepath.Join(destDir, "master.m3u8"), nil
}
```

#### 5. Worker gRPC ハンドラーの変更

```go
func (s *Server) SubmitJob(req *workerv1.JobRequest, stream workerv1.WorkerService_SubmitJobServer) error {
    // ...

    // エンコード実行
    outputPath, err := s.encoder.Encode(ctx, jobID, req.InputUrl, req.Preset, progressCallback)
    if err != nil {
        // エラー処理
    }

    // アップロード
    var outputURL string
    fileInfo, err := os.Stat(outputPath)
    if err != nil {
        return err
    }

    if fileInfo.IsDir() {
        // ディレクトリアップロード
        outputURL, err = uploader.UploadDirectory(ctx, outputPath, req.Output.Path)
    } else {
        // 単一ファイルアップロード
        outputURL, err = uploader.Upload(ctx, outputPath, req.Output.Path)
    }

    // ...
}
```

### S3 構造例

**HLS出力の場合**

```
s3://my-bucket/outputs/video_123/
  ├── master.m3u8          # マスタープレイリスト
  ├── stream_0.m3u8        # 720p variant
  ├── stream_1.m3u8        # 480p variant
  ├── stream_2.m3u8        # 360p variant
  ├── segment_0_000.ts     # 720p segments
  ├── segment_0_001.ts
  ├── segment_1_000.ts     # 480p segments
  ├── segment_1_001.ts
  ├── segment_2_000.ts     # 360p segments
  └── segment_2_001.ts
```

**DASH出力の場合**

```
s3://my-bucket/outputs/video_456/
  ├── manifest.mpd          # マニフェスト
  ├── init_0.m4s            # 1080p initialization
  ├── init_1.m4s            # 720p initialization
  ├── chunk_0_1.m4s         # 1080p chunks
  ├── chunk_0_2.m4s
  ├── chunk_1_1.m4s         # 720p chunks
  └── chunk_1_2.m4s
```

### レスポンス例

**成功時（HLS）**

```json
{
  "job_id": "job_123",
  "status": "completed",
  "output_url": "https://my-bucket.s3.ap-northeast-1.amazonaws.com/outputs/video_123/master.m3u8",
  "output_type": "hls",
  "files_uploaded": 15
}
```

### 注意事項

#### CORS設定（S3）

HLSやDASHをブラウザで再生する場合、S3バケットにCORS設定が必要：

```json
[
  {
    "AllowedHeaders": ["*"],
    "AllowedMethods": ["GET", "HEAD"],
    "AllowedOrigins": ["*"],
    "ExposeHeaders": ["ETag"]
  }
]
```

#### キャッシュ制御

CDN経由で配信する場合、適切なCache-Controlヘッダーを設定：

- マスタープレイリスト/マニフェスト: `Cache-Control: no-cache`（頻繁に更新される場合）
- セグメント: `Cache-Control: public, max-age=31536000`（不変）

#### ストレージコスト

HLS/DASHは大量の小さなファイルを生成するため、単一ファイルよりもストレージコストが高くなる可能性があります。

**例**: 10分の動画、6秒セグメント、3品質
- セグメント数: 約300ファイル（100セグメント × 3品質）
- S3リクエストコスト: GETリクエスト数が増加

### 実装の段階的アプローチ

#### Step 1: 基本HLS対応（単一品質）
- シンプルなHLSプリセット実装
- ディレクトリアップロード機能追加
- 既存の単一ファイル出力と共存

#### Step 2: ABR（Adaptive Bitrate）対応
- 複数品質バリアントのHLSプリセット
- マスタープレイリスト生成

#### Step 3: DASH対応
- DASHプリセット追加
- マニフェスト生成

#### Step 4: 最適化
- 並列アップロード（複数ファイルを同時にアップロード）
- S3 Transfer Acceleration対応
- 進捗報告の改善（ファイルごとの進捗）

### 互換性の維持

既存の単一ファイル出力との互換性を維持するため：

1. **デフォルト動作**: `output.type`が指定されていない場合は`"single"`として扱う
2. **プリセット判定**: プリセット名から自動判定も可能（`hls_*`で始まるプリセットはHLS等）
3. **段階的移行**: 既存のAPIは変更せず、新しいフィールドを追加

---

## 今後の拡張案

1. **HLS/DASH マルチファイル出力**: アダプティブビットレートストリーミング対応（設計完了）
2. **サービスディスカバリ**: Workerの動的登録・削除
3. **ジョブキュー**: Redis/SQS等を導入し、Control Planeをさらにステートレスに
4. **Webhook通知**: ジョブ完了時にコールバックURL呼び出し
5. **認証強化**: OAuth 2.0、JWT対応
6. **管理UI**: ジョブ一覧、Worker監視ダッシュボード
7. **優先度制御**: ジョブに優先度を設定し、高優先度ジョブを優先処理
8. **マルチテナント**: テナントごとのリソース分離

## 開発フェーズ案

### Phase 1: MVP（最小構成）
- [ ] Protobuf定義
- [ ] Worker: gRPCサーバー + ffmpeg実行 + S3アップロード + **自動停止機能**
- [ ] Control Plane: REST API + Worker選択ロジック（ラウンドロビン） + SSE
- [ ] Worker起動待ち対応（WORKER_STARTUP_TIMEOUT）
- [ ] 1つのプリセット実装（720p_h264）

### Phase 2: 本番対応
- [ ] 認証ミドルウェア
- [ ] エラーハンドリング・リトライ
- [ ] ロギング・メトリクス
- [ ] Docker化

### Phase 3: 拡張
- [ ] 複数プリセット対応
- [ ] FTPアップローダー実装
- [ ] Webhook通知
- [ ] 管理用API（Worker状態確認等）

---

## 補足・設計判断の根拠

- **DBを使わない理由**: シンプルさ重視。Workerが状態を持ち、Control Planeは中継に徹する
- **gRPC採用理由**: 型安全、双方向ストリーミング、高性能
- **SSE採用理由**: クライアントへの一方向プッシュに最適、WebSocketより軽量
- **プリセット方式**: セキュリティリスク低減、運用しやすい
- **Worker選択アルゴリズム**: 最もシンプルな負荷分散、将来的により高度なアルゴリズムに変更可能
