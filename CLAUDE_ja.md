# flux-encoder

ffmpegを使用した分散ビデオエンコーディングサービス。Control PlaneとWorker Nodeの2層アーキテクチャ。

## アーキテクチャ

- **Control Plane**: REST APIでジョブを受け付け、gRPC経由でWorkerに配信
- **Worker Node**: gRPCサーバーとしてffmpegエンコーディングを実行し、結果をS3にアップロード
- **通信**: Control Plane <-> Worker (gRPC), Client <-> Control Plane (REST API + SSE)

## 主な特徴

- Worker間の自動負荷分散
- ジョブ完了後のWorker自動停止（コスト最適化）
- プリセット方式によるセキュアなエンコード設定
- S3アップロード対応
- 型安全なgRPC通信

## 技術スタック

- **言語**: Go 1.25.5
- **Webフレームワーク**: gin-gonic/gin
- **RPC**: gRPC (google.golang.org/grpc)
- **クラウドストレージ**: AWS SDK v2 (S3)
- **ロギング**: uber-go/zap (構造化ログ)
- **メトリクス**: Prometheus
- **タスクランナー**: Task (Taskfile.yml)

## プロジェクト構造

```
cmd/
├── controlplane/     # Control Planeエントリーポイント
└── worker/           # Workerエントリーポイント

internal/
├── controlplane/     # Control Planeロジック
│   ├── api/          # REST APIハンドラー
│   ├── auth/         # 認証ミドルウェア
│   └── balancer/     # Worker負荷分散
├── worker/           # Workerロジック
│   ├── grpc/         # gRPCサーバー
│   ├── encoder/      # ffmpegラッパー
│   ├── uploader/     # S3/localアップローダー
│   └── preset/       # エンコードプリセット
└── shared/           # 共通ユーティリティ
    ├── logger/
    ├── metrics/
    └── retry/

proto/worker/v1/      # Protobuf定義
docs/                 # ドキュメント
```

## 開発ワークフロー

### よく使うコマンド

```bash
# コードフォーマット
task fmt

# リント実行
task lint

# テスト実行
task test

# ビルド
task build

# CI用チェック（すべて実行）
task ci

# Protobufコード生成
task proto

# ビルド成果物削除
task clean
```

### 開発モード

```bash
# Control Plane起動
task dev:controlplane

# Worker起動
task dev:worker
```

## コーディングガイドライン

### Go標準

- Go標準規約に従う（gofmt、goimports）
- golangci-lintでコード品質を確保
- エラーは必ずチェック: `if err != nil { return err }`
- zapで構造化ログを使用
- テーブル駆動テストを優先

### テスト

- `*_test.go`ファイルにテストを記述
- `go test`と`go test -race`で競合検出
- 意味のあるカバレッジを目指す（100%である必要はない）
- 外部依存（S3、gRPC呼び出し）はモック化

### 並行処理

- `docs/GOROUTINE_GUIDE.md`でgoroutineのベストプラクティスを確認
- context.Contextでキャンセルとタイムアウトを管理
- 共有状態はsync.Mutexまたはチャネルで保護
- goroutineリークを避ける（goroutineが確実に終了できるようにする）

## 重要なファイル

- `Taskfile.yml`: よく使う操作のタスク定義
- `.golangci.yml`: リンター設定
- `proto/worker/v1/worker.proto`: gRPCサービス定義
- `docs/DESIGN.md`: 詳細なシステム設計
- `docs/GOLANG_STARTER_GUIDE.md`: Go開発ガイド
- `docs/GOROUTINE_GUIDE.md`: Goroutineベストプラクティス

## 環境変数

### Control Plane
- `PORT`: HTTPサーバーポート（デフォルト: 8080）
- `WORKER_NODES`: Workerアドレス（カンマ区切り）
- `WORKER_STARTUP_TIMEOUT`: Worker起動待ち時間（秒）
- `ENV`: 環境（development/production）
- `LOG_LEVEL`: ログレベル（debug/info/warn/error）

### Worker Node
- `GRPC_PORT`: gRPCサーバーポート（デフォルト: 50051）
- `MAX_CONCURRENT_JOBS`: 最大同時実行ジョブ数
- `WORK_DIR`: ジョブの作業ディレクトリ
- `STORAGE_TYPE`: ストレージタイプ（s3/local）
- `S3_BUCKET`: S3バケット名
- `S3_REGION`: S3リージョン
- `WORKER_ID`: Worker識別子

## 重要な概念

### プリセット

エンコードプリセットは`internal/worker/preset/preset.go`で定義されています。プリセットは、生のコマンド実行を公開せずにffmpeg引数を定義する安全な方法を提供します。

利用可能なプリセット:
- `720p_h264`: H.264でHD 720p
- `1080p_h264`: H.264でフルHD 1080p
- `480p_h264`: H.264でSD 480p

### Worker選択

Control Planeはラウンドロビン + 最初に利用可能な戦略を使用:
1. 前回選択したWorkerの次から開始
2. 各Workerの状態を`GetStatus()` RPCで確認
3. 利用可能な容量がある最初のWorkerを選択
4. 停止中のWorkerが起動するまで`WORKER_STARTUP_TIMEOUT`まで待機
5. すべてのWorkerが満杯の場合は503を返す

### Worker自動停止

Workerは実行中のジョブがなくなると自動的に終了し、クラウドプラットフォームの自動停止をトリガーします（例: Fly.io Machines）。これにより、必要なときだけWorkerを実行してコストを削減します。

## Docker

イメージビルド:
```bash
docker build -f deployments/Dockerfile.controlplane -t flux-encoder-control .
docker build -f deployments/Dockerfile.worker -t flux-encoder-worker .
```

## デプロイ

自動起動機能を持つサーバーレス/コンテナプラットフォーム向けに設計:
- Fly.io Machines（推奨）
- Google Cloud Run
- AWS App Runner
- Azure Container Apps

詳細なデプロイ戦略については`docs/DESIGN.md`を参照してください。

## AI アシスタントへの注意事項

- コード変更を提案する前に必ず`task fmt`と`task lint`を実行
- 変更する前に既存のコードを読む
- コードベースで確立されたパターンに従う
- 新機能を追加する際は、それに応じてテストも更新
- 関数は小さく焦点を絞る（高い循環的複雑度を避ける）
- 既存のエラーハンドリングパターンを使用
- 継承よりも合成を優先
- エクスポートされた関数と型はドキュメント化する
