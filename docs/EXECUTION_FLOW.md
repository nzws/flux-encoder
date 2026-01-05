# 実行フロー詳細

このドキュメントでは、flux-encoderの動作順序とコードの実行フローを詳しく説明します。

## 概要

flux-encoderは2層アーキテクチャで構成されています:

1. **Control Plane**: REST API経由でジョブを受け付け、Workerに配信
2. **Worker Node**: gRPC経由でジョブを受け取り、ffmpegでエンコーディング実行

## 1. 起動シーケンス

### 1.1 Control Plane 起動 (cmd/controlplane/main.go)

```
main()
├─ logger.Init() (34-40行目)
│  └─ ログシステム初期化
│
├─ 環境変数読み込み (44-62行目)
│  ├─ PORT: HTTPサーバーポート (デフォルト: 8080)
│  ├─ WORKER_NODES: Worker アドレスリスト (カンマ区切り)
│  └─ WORKER_STARTUP_TIMEOUT: Workerの起動待機時間 (秒)
│
├─ balancer.New() (65行目)
│  └─ internal/controlplane/balancer/balancer.go:24-31
│     └─ Balancer インスタンス作成 (Worker負荷分散管理)
│
├─ api.NewHandler() (68行目)
│  └─ internal/controlplane/api/handler.go:25-30
│     ├─ Handler インスタンス作成
│     └─ JobManager 初期化 (ジョブの進捗管理)
│
└─ ginサーバー起動 (74-102行目)
   ├─ ルート設定
   │  ├─ POST /api/v1/jobs → CreateJob (ジョブ作成)
   │  ├─ GET /api/v1/jobs/:id/stream → StreamJobProgress (SSE)
   │  └─ GET /api/v1/workers/status → GetWorkerStatus
   ├─ ミドルウェア
   │  └─ auth.APIKeyMiddleware() (Bearer認証)
   ├─ /health → ヘルスチェック
   ├─ /metrics → Prometheusメトリクス
   └─ /swagger → Swagger UI
```

### 1.2 Worker Node 起動 (cmd/worker/main.go)

```
main()
├─ logger.Init() (26-31行目)
│  └─ ログシステム初期化
│
├─ 環境変数読み込み (35-48行目)
│  ├─ GRPC_PORT: gRPCポート (デフォルト: 50051)
│  ├─ MAX_CONCURRENT_JOBS: 最大同時実行ジョブ数 (デフォルト: 2)
│  ├─ WORK_DIR: 作業ディレクトリ (デフォルト: /tmp/ffmpeg-jobs)
│  ├─ STORAGE_TYPE: ストレージタイプ (s3/local)
│  └─ WORKER_ID: Worker識別子
│
├─ os.MkdirAll(workDir) (51-56行目)
│  └─ 作業ディレクトリ作成
│
├─ encoder.New() (59行目)
│  └─ internal/worker/encoder/encoder.go:37-42
│     └─ Encoder インスタンス作成
│
├─ uploader.NewUploader() (63-69行目)
│  └─ internal/worker/uploader/uploader.go
│     ├─ S3アップローダー or ローカルアップローダー初期化
│     └─ AWS SDK v2セットアップ (S3の場合)
│
├─ workergrpc.NewServer() (73行目)
│  └─ internal/worker/grpc/server.go:38-54
│     └─ gRPCサーバーインスタンス作成
│
└─ grpcServer.Serve() (101行目)
   └─ gRPCサーバー起動 (ポート: 50051)
```

## 2. ジョブ実行フロー

### 2.1 ジョブリクエスト受信

```
クライアント → POST /api/v1/jobs (JSON)
{
  "input_url": "https://example.com/video.mp4",
  "preset": "720p_h264",
  "output": {
    "storage": "s3",
    "path": "output/video.mp4"
  }
}
```

### 2.2 CreateJob ハンドラー (internal/controlplane/api/handler.go:70-154)

```
CreateJob()
├─ リクエストバリデーション (72-75行目)
│  └─ JSON形式のチェック
│
├─ uuid.New() (78行目)
│  └─ ジョブID生成 (例: "550e8400-e29b-41d4-a716-446655440000")
│
├─ balancer.SelectWorker() (87行目)
│  └─ 空いているWorkerを選択 (後述)
│
├─ jobManager.CreateProgressChannel() (95行目)
│  └─ internal/controlplane/api/jobs.go:23-30
│     └─ バッファ付きチャネル作成 (サイズ: 100)
│
├─ goroutine起動 (98-146行目)
│  └─ Workerへのジョブ送信と進捗受信 (非同期)
│
└─ レスポンス返却 (149-153行目)
   └─ 202 Accepted
      {
        "job_id": "550e8400-...",
        "status": "accepted",
        "stream_url": "/api/v1/jobs/550e8400-.../stream"
      }
```

### 2.3 Worker選択アルゴリズム (internal/controlplane/balancer/balancer.go:34-77)

```
SelectWorker()
├─ Round-Robin選択 (38行目)
│  └─ 前回選択したWorkerの次から順番にチェック
│
├─ 各Workerに対してループ (40-74行目)
│  ├─ getWorkerStatus() (50行目)
│  │  └─ internal/controlplane/balancer/balancer.go:80-105
│  │     ├─ grpc.NewClient() (86行目)
│  │     │  └─ gRPC接続確立 (タイムアウト: WORKER_STARTUP_TIMEOUT)
│  │     └─ client.GetStatus() (96行目)
│  │        └─ Workerの状態取得 (現在のジョブ数/最大ジョブ数)
│  │
│  └─ 空きチェック (60行目)
│     └─ CurrentJobs < MaxConcurrentJobs なら選択
│
└─ 全Workerが満杯の場合 (76行目)
   └─ 503 Service Unavailable エラー
```

### 2.4 Worker側: ジョブ受信 (internal/worker/grpc/server.go:62-244)

```
SubmitJob()
├─ 同時実行数チェック (72-75行目)
│  └─ activeJobs >= maxConcurrent なら ResourceExhausted エラー
│
├─ ジョブカウント増加 (78行目)
│  └─ atomic.AddInt32(&s.activeJobs, 1)
│
├─ キャンセル可能なコンテキスト作成 (81-84行目)
│  └─ context.WithCancel()
│
├─ defer: ジョブ終了処理 (86-112行目)
│  ├─ ジョブカウント減少
│  ├─ encoder.Cleanup() → 作業ディレクトリ削除
│  └─ 自動シャットダウンチェック (104-111行目)
│     └─ activeJobs == 0 なら gracefulShutdown()
│
├─ "QUEUED" ステータス送信 (115-123行目)
│
├─ encoder.Encode() (137-158行目)
│  └─ エンコード実行 (後述)
│
├─ "UPLOADING" ステータス送信 (177-185行目)
│
├─ uploader.Upload() (189-213行目)
│  └─ S3またはローカルにアップロード
│
└─ "COMPLETED" ステータス送信 (236-243行目)
   └─ output_url を含む完了通知
```

### 2.5 エンコード実行 (internal/worker/encoder/encoder.go:45-133)

```
Encode()
├─ preset.Get() (53行目)
│  └─ internal/worker/preset/preset.go
│     └─ プリセット定義取得 (例: 720p_h264)
│        ├─ FFmpegArgs: ["-c:v", "libx264", "-preset", "medium", ...]
│        ├─ Extension: "mp4"
│        └─ OutputType: "" (通常ファイル) or "hls"/"dash"
│
├─ os.MkdirAll() (60行目)
│  └─ ジョブディレクトリ作成 (/tmp/ffmpeg-jobs/{jobID}/)
│
├─ buildFFmpegArgs() (71行目)
│  └─ 166-175行目
│     └─ ffmpeg コマンド引数構築
│        ["-i", inputURL, "-progress", "pipe:2", "-y", ...preset.FFmpegArgs, outputFile]
│
├─ exec.CommandContext() (81行目)
│  └─ ffmpegプロセス起動
│
├─ getDuration() (98行目)
│  └─ 344-363行目
│     └─ ffprobe で入力動画の長さ取得 (進捗計算用)
│
├─ readFFmpegProgress() (104行目)
│  └─ 183-224行目
│     ├─ ffmpegのstderrから進捗情報をパース
│     │  └─ "out_time_ms=123456" から進捗率計算
│     └─ callback() で進捗を通知
│        └─ gRPCサーバー経由でControl Planeへ
│
├─ cmd.Wait() (113行目)
│  └─ ffmpeg完了待機
│
└─ validateOutput() (128行目)
   └─ 248-292行目
      └─ internal/worker/validator/validator.go
         ├─ ffprobeで出力ファイル検証
         ├─ デコードテスト実行
         ├─ HLS/DASH形式の場合はマニフェスト解析
         └─ コーデック/解像度/ビットレートチェック
```

### 2.6 進捗ストリーミング (SSE)

クライアントは並行して `GET /api/v1/jobs/{id}/stream` にアクセス可能:

```
StreamJobProgress() (internal/controlplane/api/handler.go:166-240)
├─ SSEヘッダー設定 (172-176行目)
│  └─ Content-Type: text/event-stream
│
├─ jobManager.GetProgressChannel() (179行目)
│  └─ 対応するジョブの進捗チャネル取得
│
└─ ループ: チャネルから進捗を読み取り (199-239行目)
   ├─ progress受信 (201行目)
   │  └─ Worker → Control Planeのgoloutineが送信
   │
   ├─ JSON変換 (209-226行目)
   │  └─ data: {"job_id": "...", "status": "JOB_STATUS_PROCESSING", "progress": 45.2, ...}
   │
   └─ SSE送信 (228-232行目)
      └─ fmt.Fprintf(c.Writer, "data: %s\n\n", jsonData)
```

## 3. 完了とクリーンアップ

### 3.1 ジョブ完了

```
Worker → Control Plane
└─ JobProgress {
     Status: JOB_STATUS_COMPLETED,
     OutputUrl: "s3://bucket/output/video.mp4"
   }

Control Plane → Client (SSE)
└─ data: {"status":"JOB_STATUS_COMPLETED","output_url":"s3://..."}

Worker
└─ encoder.Cleanup()
   └─ os.RemoveAll(jobDir)
```

### 3.2 Worker自動停止 (internal/worker/grpc/server.go:289-304)

```
gracefulShutdown()
├─ time.Sleep(1s) (292行目)
│  └─ 新しいジョブが来る可能性を待つ
│
├─ activeJobs == 0 確認 (295行目)
│
└─ os.Exit(0) (302行目)
   └─ Workerプロセス終了
      └─ Fly.io Machines等が自動停止
```

## 4. エラーハンドリング

### 4.1 Worker接続エラー

```
balancer.SelectWorker() でエラー
└─ Control Plane: 503 Service Unavailable
   └─ {"error": "no available workers"}
```

### 4.2 エンコードエラー

```
encoder.Encode() でエラー
└─ Worker: JobProgress {Status: JOB_STATUS_FAILED, Error: "..."}
   └─ Control Plane → Client (SSE)
      └─ data: {"status":"JOB_STATUS_FAILED","error":"..."}
```

### 4.3 検証エラー

```
validator.Validate() でエラー
└─ encoder.Encode() が error返却
   └─ Worker: JobProgress {Status: JOB_STATUS_FAILED, Error: "validation failed"}
```

### 4.4 アップロードエラー

```
uploader.Upload() でエラー
└─ Worker: JobProgress {Status: JOB_STATUS_FAILED, Error: "upload failed"}
   └─ Control Plane → Client (SSE)
```

## 5. 並行処理とスレッド安全性

### 5.1 Control Plane

- **JobManager** (internal/controlplane/api/jobs.go)
  - `sync.RWMutex` でチャネルマップを保護 (12行目)
  - 複数のCreateJob/StreamJobProgress呼び出しに対応

### 5.2 Worker

- **activeJobs** カウンター
  - `atomic.LoadInt32` / `atomic.AddInt32` で原子的操作 (server.go:72,78,88,103)

- **activeJobIDs** マップ
  - `sync.RWMutex` で保護 (server.go:30,82,90,248,266)

- **Encoder**
  - 各ジョブは独立したディレクトリで動作
  - goroutineセーフ

## 6. タイムアウトとキャンセル

### 6.1 Worker選択タイムアウト

```go
// balancer.go:82
ctx, cancel := context.WithTimeout(ctx, b.timeout)
// WORKER_STARTUP_TIMEOUT 秒で接続タイムアウト
```

### 6.2 ジョブキャンセル

```go
// server.go:81
jobCtx, cancel := context.WithCancel(ctx)
// encoder.go:81
cmd := exec.CommandContext(ctx, "ffmpeg", args...)
// キャンセル時にffmpegプロセスも停止
```

### 6.3 検証タイムアウト

```go
// encoder.go:257
Timeout: 30 * time.Second
// 検証処理は30秒でタイムアウト
```

## 7. データフロー図

```
┌─────────┐                     ┌───────────────┐                    ┌────────┐
│ Client  │                     │ Control Plane │                    │ Worker │
└────┬────┘                     └───────┬───────┘                    └───┬────┘
     │                                  │                                │
     │ POST /api/v1/jobs                │                                │
     ├─────────────────────────────────>│                                │
     │                                  │                                │
     │                                  │ SelectWorker()                 │
     │                                  ├───────────────────────────────>│
     │                                  │ GetStatus() (gRPC)             │
     │                                  │<───────────────────────────────┤
     │                                  │ {CurrentJobs, MaxJobs}         │
     │                                  │                                │
     │ 202 Accepted                     │                                │
     │ {job_id, stream_url}             │                                │
     │<─────────────────────────────────┤                                │
     │                                  │                                │
     │ GET /api/v1/jobs/{id}/stream     │                                │
     ├─────────────────────────────────>│                                │
     │ (SSE接続確立)                    │                                │
     │                                  │                                │
     │                                  │ SubmitJob() (gRPC stream)      │
     │                                  ├───────────────────────────────>│
     │                                  │                                │
     │                                  │<───────────────────────────────┤
     │                                  │ QUEUED                         │
     │<─────────────────────────────────┤                                │
     │ data: {"status":"QUEUED"}        │                                │
     │                                  │                                │
     │                                  │<───────────────────────────────┤
     │                                  │ PROCESSING (progress: 0%)      │
     │<─────────────────────────────────┤                                │
     │ data: {"status":"PROCESSING"...} │                                │
     │                                  │                                │
     │                                  │<───────────────────────────────┤
     │                                  │ PROCESSING (progress: 25%)     │
     │<─────────────────────────────────┤                                │
     │                                  │                                │
     │                                  │<───────────────────────────────┤
     │                                  │ PROCESSING (progress: 50%)     │
     │<─────────────────────────────────┤                                │
     │                                  │                                │
     │                                  │<───────────────────────────────┤
     │                                  │ UPLOADING                      │
     │<─────────────────────────────────┤                                │
     │                                  │                                │
     │                                  │<───────────────────────────────┤
     │                                  │ COMPLETED {output_url}         │
     │<─────────────────────────────────┤                                │
     │ data: {"status":"COMPLETED"...}  │                                │
     │                                  │                                │
     │                                  │                                │ (activeJobs == 0)
     │                                  │                                ├─> gracefulShutdown()
     │                                  │                                │   └─> os.Exit(0)
```

## 8. ファイルごとの責務まとめ

| ファイルパス | 責務 | 主要な関数 |
|-------------|------|-----------|
| `cmd/controlplane/main.go` | Control Plane起動 | `main()` |
| `cmd/worker/main.go` | Worker起動 | `main()` |
| `internal/controlplane/api/handler.go` | REST APIハンドラー | `CreateJob()`, `StreamJobProgress()` |
| `internal/controlplane/api/jobs.go` | ジョブ進捗管理 | `CreateProgressChannel()`, `GetProgressChannel()` |
| `internal/controlplane/balancer/balancer.go` | Worker負荷分散 | `SelectWorker()`, `getWorkerStatus()` |
| `internal/controlplane/auth/middleware.go` | 認証ミドルウェア | `APIKeyMiddleware()` |
| `internal/worker/grpc/server.go` | gRPCサーバー | `SubmitJob()`, `GetStatus()`, `CancelJob()` |
| `internal/worker/encoder/encoder.go` | ffmpegラッパー | `Encode()`, `validateOutput()` |
| `internal/worker/preset/preset.go` | エンコードプリセット | `Get()` |
| `internal/worker/uploader/s3.go` | S3アップローダー | `Upload()`, `UploadDirectory()` |
| `internal/worker/uploader/uploader.go` | アップローダーインターフェース | `NewUploader()` |
| `internal/worker/validator/validator.go` | 出力検証 | `Validate()` |
| `internal/worker/validator/ffprobe.go` | ffprobe統合 | `GetMediaInfo()` |
| `internal/worker/validator/hls_parser.go` | HLS/DASHパーサー | `ParseHLS()` |
| `internal/shared/logger/logger.go` | ロギング | `Init()`, `Info()`, `Error()` |
| `internal/shared/metrics/metrics.go` | Prometheusメトリクス | (未実装) |
| `internal/shared/retry/retry.go` | リトライ処理 | `Do()` |

## 9. 主要な環境変数と設定

### Control Plane

| 変数名 | デフォルト | 説明 | 参照箇所 |
|--------|-----------|------|----------|
| `ENV` | - | development/production | main.go:35 |
| `PORT` | 8080 | HTTPサーバーポート | main.go:45 |
| `WORKER_NODES` | (必須) | Workerアドレスリスト | main.go:46 |
| `WORKER_STARTUP_TIMEOUT` | 60 | Worker起動待機秒数 | main.go:56 |

### Worker

| 変数名 | デフォルト | 説明 | 参照箇所 |
|--------|-----------|------|----------|
| `ENV` | - | development/production | main.go:26 |
| `GRPC_PORT` | 50051 | gRPCポート | main.go:36 |
| `MAX_CONCURRENT_JOBS` | 2 | 最大同時実行数 | main.go:37 |
| `WORK_DIR` | /tmp/ffmpeg-jobs | 作業ディレクトリ | main.go:38 |
| `STORAGE_TYPE` | s3 | s3/local | main.go:39 |
| `WORKER_ID` | worker-1 | Worker識別子 | main.go:40 |
| `S3_BUCKET` | - | S3バケット名 | uploader/s3.go |
| `S3_REGION` | - | S3リージョン | uploader/s3.go |
| `DISABLE_AUTO_SHUTDOWN` | false | 自動停止無効化 | grpc/server.go:105 |

## 10. 重要な設計判断

### 10.1 なぜgoroutineで非同期実行?

`handler.go:98-146` でWorkerへのジョブ送信をgoroutineで実行している理由:

- CreateJob APIは即座に202 Acceptedを返す必要がある
- エンコード処理は数分～数時間かかる可能性がある
- クライアントは別途SSE接続で進捗を受信する

### 10.2 なぜWorkerは自動停止?

`server.go:104-111` でactiveJobs == 0時に自動停止する理由:

- Fly.io Machinesなどのサーバーレス環境でコスト最適化
- 停止中のMachineは課金されない
- 新しいジョブが来ると自動起動される (Fly.ioの機能)

### 10.3 なぜRound-Robin + First-Available?

`balancer.go:38-74` の選択アルゴリズム:

- 単純なRound-Robinではビジー状態のWorkerを選択してしまう
- First-Availableだけでは最初のWorkerに負荷が集中する
- 両方を組み合わせることで均等な負荷分散を実現

### 10.4 なぜプリセット方式?

`preset/preset.go` でffmpeg引数を事前定義している理由:

- セキュリティ: 任意のffmpegコマンド実行を防ぐ
- 安定性: テスト済みの設定のみを使用
- 保守性: エンコード設定を一元管理

## 11. パフォーマンス特性

### 11.1 スループット

- Control Plane: 単一インスタンスで数百リクエスト/秒
- Worker: MAX_CONCURRENT_JOBS × Worker数の並列処理
- ボトルネック: エンコード処理 (CPU/GPU)

### 11.2 メモリ使用量

- Control Plane: 基本 ~50MB + (進捗チャネル × 100 × JobProgress構造体サイズ)
- Worker: 基本 ~100MB + (ffmpegプロセス × ビデオサイズ)

### 11.3 ネットワーク

- Control Plane ↔ Worker: gRPC (HTTP/2)
  - 接続プール: grpc.NewClient()でコネクション再利用
  - ストリーミング: 双方向ストリーム for 進捗通知
- Worker ↔ S3: AWS SDK v2
  - チャンク分割アップロード対応

## 12. デバッグ方法

### 12.1 ログレベル

```bash
# Control Plane
ENV=development PORT=8080 WORKER_NODES=localhost:50051 ./controlplane

# Worker
ENV=development GRPC_PORT=50051 WORK_DIR=/tmp/test ./worker
```

development環境ではDEBUGレベルのログが出力される:

- ffmpegの全出力 (encoder.go:193-197)
- Worker選択の詳細 (balancer.go:44-47)

### 12.2 進捗確認

```bash
# SSEストリームを直接確認
curl -H "Authorization: Bearer xxx" \
  http://localhost:8080/api/v1/jobs/JOB_ID/stream
```

### 12.3 Workerステータス確認

```bash
# grpcurlでWorkerに直接アクセス
grpcurl -plaintext localhost:50051 worker.v1.WorkerService/GetStatus
```

## まとめ

flux-encoderは、Control PlaneとWorkerの分離により:

1. **スケーラビリティ**: Worker数を増やすだけで処理能力向上
2. **コスト効率**: Worker自動停止でアイドル時間のコスト削減
3. **信頼性**: Worker障害時も他のWorkerで継続可能
4. **セキュリティ**: プリセット方式で安全なエンコード実行

を実現しています。
