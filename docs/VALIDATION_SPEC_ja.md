# エンコード出力検証機能 仕様書

## 概要

ffmpegによるエンコード処理後、生成されたメディアファイル（HLS、MP4等）の品質と整合性を機械的に検証する機能。エンコード失敗やデータ破損を早期に検出し、不正なファイルがS3にアップロードされることを防ぐ。

## 背景と目的

### 課題
- エンコード処理が正常終了（exit code 0）しても、出力ファイルが不完全な場合がある
- 破損したメディアファイルがアップロードされると、クライアント側で再生エラーが発生する
- 問題の発見が遅れると、再エンコードのコストが増大する

### 目的
1. **品質保証**: 出力ファイルが再生可能で仕様を満たしていることを保証
2. **早期検出**: エンコード直後に問題を検出し、即座に再試行または通知
3. **運用コスト削減**: 不正なファイルのアップロードとストレージコストを削減
4. **ユーザー体験向上**: クライアントでの再生エラーを未然に防止

## 検証項目

### 1. ファイル存在性
- 出力ファイル/ディレクトリの存在確認
- HLSの場合、マスタープレイリスト、メディアプレイリスト、全セグメントファイルの存在確認
- 空ファイル（0バイト）の検出

### 2. メディアストリーム基本検証
**映像ストリーム:**
- コーデック（H.264, H.265等）の確認
- 解像度（width, height）の確認
- フレームレート（fps）の確認
- ピクセルフォーマット（yuv420p等）の確認
- アスペクト比の妥当性

**音声ストリーム:**
- コーデック（AAC, MP3等）の確認
- サンプルレート（44100Hz, 48000Hz等）の確認
- チャンネル数（mono, stereo等）の確認
- 音声トラックの存在確認（音声が期待される場合）

### 3. メディア品質検証
- デュレーション（再生時間）の妥当性
  - 期待値との差異が一定範囲内（例: ±2秒）
  - 極端に短い/長いファイルの検出
- ビットレート
  - 指定レンジ内に収まっているか
  - 異常に低い/高いビットレートの検出
- ファイルサイズ
  - デュレーションとビットレートから期待されるサイズとの比較
  - 異常に小さい/大きいファイルの検出

### 4. HLS固有の検証
**プレイリスト検証:**
- m3u8ファイルの構文チェック
  - 必須タグ（#EXTM3U, #EXT-X-VERSION等）の存在
  - タグの順序と構文の正当性
- セグメント情報の整合性
  - `#EXTINF`で宣言された時間長と実際のセグメントファイルの長さの比較
  - `#EXT-X-TARGETDURATION`の妥当性
- マスタープレイリストとメディアプレイリストの関連性
  - メディアプレイリストへの参照が正しいか
  - BANDWIDTH、RESOLUTION等のメタデータが実際のストリームと一致するか

**セグメントファイル検証:**
- 全セグメントファイル（.ts）の存在確認
- 各セグメントのデコード可能性チェック
- セグメント間の連続性（タイムスタンプの連続性）
- キーフレーム配置
  - セグメント境界にキーフレームが存在するか
  - GOPサイズが適切か

### 5. デコード整合性検証
- ffmpegによる完全デコードテスト
  - `-f null -` を使用してデコードのみ実行
  - エラーログの検出
- フレームドロップやデコードエラーの検出
- 音声と映像の同期（A/V sync）チェック

## アーキテクチャ設計

### コンポーネント構成

```
internal/worker/
├── validator/
│   ├── validator.go          # 検証オーケストレーター
│   ├── file_validator.go     # ファイル存在性チェック
│   ├── media_validator.go    # メディアストリーム検証
│   ├── hls_validator.go      # HLS固有の検証
│   ├── decode_validator.go   # デコード整合性検証
│   └── validator_test.go     # テスト
```

### 処理フロー

```
Encoder (ffmpegラッパー)
    ↓
    エンコード完了
    ↓
Validator
    ↓
    ├─→ ファイル存在性チェック
    ├─→ ffprobeによる基本情報取得
    ├─→ メディアストリーム検証
    ├─→ HLS固有検証（該当する場合）
    ├─→ デコード整合性検証
    ↓
ValidationResult
    ↓
    ├─→ OK: Uploader へ
    └─→ NG: エラー返却 & クリーンアップ
```

### 実行タイミング
1. エンコード完了直後、アップロード前
2. Worker の gRPC `EncodeVideo` レスポンス前
3. 検証失敗時は自動的にジョブ失敗として処理

## API設計

### ValidationResult 構造体

```go
type ValidationResult struct {
    // 検証全体の成否
    Valid bool

    // エラーリスト（致命的な問題）
    Errors []ValidationError

    // 警告リスト（非致命的な問題）
    Warnings []ValidationWarning

    // 取得したメディア情報
    MediaInfo *MediaInfo

    // 検証実行時間
    ValidationDuration time.Duration
}

type ValidationError struct {
    Code    string  // エラーコード（例: "FILE_NOT_FOUND", "CODEC_MISMATCH"）
    Message string  // 人間が読めるエラーメッセージ
    Field   string  // 問題のあるフィールド（オプション）
    Details map[string]interface{}  // 追加の詳細情報
}

type ValidationWarning struct {
    Code    string
    Message string
    Field   string
}

type MediaInfo struct {
    // 基本情報
    Format         string    // フォーマット（hls, mp4等）
    Duration       float64   // 秒
    Size           int64     // バイト
    Bitrate        int64     // bps

    // 映像ストリーム
    VideoStreams   []VideoStreamInfo

    // 音声ストリーム
    AudioStreams   []AudioStreamInfo

    // HLS固有情報（該当する場合）
    HLSInfo        *HLSInfo
}

type VideoStreamInfo struct {
    Codec          string
    Profile        string
    Width          int
    Height         int
    FrameRate      float64
    PixelFormat    string
    Bitrate        int64
}

type AudioStreamInfo struct {
    Codec          string
    SampleRate     int
    Channels       int
    ChannelLayout  string
    Bitrate        int64
}

type HLSInfo struct {
    MasterPlaylist string              // マスタープレイリストパス
    Playlists      []PlaylistInfo      // メディアプレイリスト情報
    TotalSegments  int                 // 総セグメント数
    TargetDuration float64             // セグメントの目標時間長
}

type PlaylistInfo struct {
    Path           string
    Bandwidth      int64
    Resolution     string
    Codecs         string
    SegmentCount   int
    Segments       []SegmentInfo
}

type SegmentInfo struct {
    Path           string
    Duration       float64
    Size           int64
}
```

### Validator インターフェース

```go
type Validator interface {
    // メディアファイルを検証
    Validate(ctx context.Context, outputPath string, options *ValidationOptions) (*ValidationResult, error)
}

type ValidationOptions struct {
    // 検証レベル
    Level ValidationLevel

    // 期待されるメディア情報（プリセットから取得）
    Expected *ExpectedMediaInfo

    // タイムアウト設定
    Timeout time.Duration

    // デコードテストの実行有無（重い処理なのでオプション）
    SkipDecodeTest bool

    // HLS検証の詳細レベル
    HLSValidationDepth HLSValidationDepth
}

type ValidationLevel int

const (
    // 最小限の検証（ファイル存在性とffprobe基本情報のみ）
    ValidationLevelMinimal ValidationLevel = iota

    // 標準検証（メディアストリーム情報の詳細チェック）
    ValidationLevelStandard

    // 厳密な検証（デコードテストを含む完全チェック）
    ValidationLevelStrict
)

type HLSValidationDepth int

const (
    // プレイリストの構文チェックのみ
    HLSValidationDepthBasic HLSValidationDepth = iota

    // 全セグメントの存在確認
    HLSValidationDepthMedium

    // 各セグメントの内容検証
    HLSValidationDepthFull
)

type ExpectedMediaInfo struct {
    // プリセットから取得した期待値
    VideoCodec     string
    Width          int
    Height         int
    AudioCodec     string
    MinDuration    float64  // 最小デュレーション
    MaxDuration    float64  // 最大デュレーション
    MinBitrate     int64
    MaxBitrate     int64
}
```

## 実装方針

### 1. ffprobe ラッパー

```go
// internal/worker/validator/ffprobe.go

type FFProbe struct {
    execPath string
}

func (f *FFProbe) GetMediaInfo(ctx context.Context, filePath string) (*MediaInfo, error) {
    // ffprobe -v error -print_format json -show_format -show_streams
    // JSON出力をパースして MediaInfo に変換
}

func (f *FFProbe) ValidatePlaylist(ctx context.Context, playlistPath string) error {
    // ffprobe で m3u8 の構文チェック
}

func (f *FFProbe) GetSegmentInfo(ctx context.Context, segmentPath string) (*SegmentInfo, error) {
    // 個別セグメントの情報取得
}
```

### 2. HLS プレイリストパーサー

```go
// internal/worker/validator/hls_parser.go

type HLSParser struct{}

func (p *HLSParser) ParseMasterPlaylist(path string) (*MasterPlaylist, error) {
    // m3u8ファイルをパースして構造体に変換
}

func (p *HLSParser) ParseMediaPlaylist(path string) (*MediaPlaylist, error) {
    // メディアプレイリストのパース
}

func (p *HLSParser) ValidatePlaylistSyntax(content string) []ValidationError {
    // 構文エラーのチェック
}
```

### 3. デコードテスト

```go
// internal/worker/validator/decode_validator.go

type DecodeValidator struct {
    ffmpegPath string
}

func (d *DecodeValidator) TestDecode(ctx context.Context, filePath string) error {
    // ffmpeg -v error -i input -f null -
    // stderr を監視してエラーを検出
}
```

### 4. メイン Validator 実装

```go
// internal/worker/validator/validator.go

type DefaultValidator struct {
    ffprobe          *FFProbe
    hlsParser        *HLSParser
    decodeValidator  *DecodeValidator
    logger           *zap.Logger
}

func NewValidator(logger *zap.Logger) Validator {
    return &DefaultValidator{
        ffprobe:         NewFFProbe(),
        hlsParser:       NewHLSParser(),
        decodeValidator: NewDecodeValidator(),
        logger:          logger,
    }
}

func (v *DefaultValidator) Validate(ctx context.Context, outputPath string, options *ValidationOptions) (*ValidationResult, error) {
    result := &ValidationResult{
        Valid: true,
    }

    // 1. ファイル存在性チェック
    if err := v.validateFileExists(outputPath); err != nil {
        result.AddError("FILE_NOT_FOUND", err.Error())
        return result, nil
    }

    // 2. ffprobe でメディア情報取得
    mediaInfo, err := v.ffprobe.GetMediaInfo(ctx, outputPath)
    if err != nil {
        result.AddError("FFPROBE_FAILED", err.Error())
        return result, nil
    }
    result.MediaInfo = mediaInfo

    // 3. フォーマット別検証
    switch mediaInfo.Format {
    case "hls":
        v.validateHLS(ctx, outputPath, options, result)
    case "mp4", "mov":
        v.validateMP4(ctx, outputPath, options, result)
    }

    // 4. メディアストリーム検証
    v.validateMediaStreams(mediaInfo, options.Expected, result)

    // 5. デコードテスト（オプション）
    if !options.SkipDecodeTest && options.Level >= ValidationLevelStrict {
        if err := v.decodeValidator.TestDecode(ctx, outputPath); err != nil {
            result.AddError("DECODE_FAILED", err.Error())
        }
    }

    return result, nil
}
```

## Encoder との統合

```go
// internal/worker/encoder/encoder.go

type Encoder struct {
    // ... 既存フィールド
    validator validator.Validator
}

func (e *Encoder) Encode(ctx context.Context, req *EncodeRequest) (*EncodeResult, error) {
    // ... 既存のエンコード処理

    // エンコード完了後に検証
    validationOpts := &validator.ValidationOptions{
        Level:              validator.ValidationLevelStandard,
        Expected:           e.getExpectedInfoFromPreset(req.Preset),
        Timeout:            30 * time.Second,
        SkipDecodeTest:     false,
        HLSValidationDepth: validator.HLSValidationDepthMedium,
    }

    validationResult, err := e.validator.Validate(ctx, outputPath, validationOpts)
    if err != nil {
        return nil, fmt.Errorf("validation error: %w", err)
    }

    if !validationResult.Valid {
        e.logger.Error("validation failed",
            zap.Strings("errors", validationResult.GetErrorMessages()),
        )
        return nil, fmt.Errorf("output validation failed: %v", validationResult.Errors)
    }

    // 警告があればログ出力
    if len(validationResult.Warnings) > 0 {
        e.logger.Warn("validation warnings",
            zap.Strings("warnings", validationResult.GetWarningMessages()),
        )
    }

    // 検証成功 → アップロード処理へ
    return result, nil
}
```

## エラーハンドリング

### エラーコード一覧

| コード | 説明 | 対応 |
|--------|------|------|
| `FILE_NOT_FOUND` | 出力ファイルが存在しない | エンコード失敗として扱う |
| `FILE_EMPTY` | ファイルサイズが0バイト | エンコード失敗として扱う |
| `FFPROBE_FAILED` | ffprobeの実行失敗 | エンコード失敗として扱う |
| `CODEC_MISMATCH` | コーデックが期待値と異なる | エンコード失敗として扱う |
| `RESOLUTION_MISMATCH` | 解像度が期待値と異なる | エンコード失敗として扱う |
| `DURATION_TOO_SHORT` | デュレーションが短すぎる | エンコード失敗として扱う |
| `DURATION_TOO_LONG` | デュレーションが長すぎる | 警告（許容する場合あり） |
| `BITRATE_ABNORMAL` | ビットレートが異常 | 警告または失敗 |
| `NO_VIDEO_STREAM` | 映像ストリームがない | エンコード失敗として扱う |
| `NO_AUDIO_STREAM` | 音声ストリームがない | 警告（音声なし動画の場合は正常） |
| `HLS_PLAYLIST_SYNTAX_ERROR` | プレイリスト構文エラー | エンコード失敗として扱う |
| `HLS_SEGMENT_MISSING` | セグメントファイル欠損 | エンコード失敗として扱う |
| `HLS_DURATION_MISMATCH` | セグメント時間長不一致 | 警告または失敗 |
| `DECODE_ERROR` | デコードエラー発生 | エンコード失敗として扱う |
| `AV_SYNC_ERROR` | 音声映像同期エラー | 警告または失敗 |

### リトライポリシー
- 検証失敗時は基本的にリトライしない（エンコード自体の問題の可能性が高い）
- `FFPROBE_FAILED` など一時的なエラーの場合のみリトライを検討
- 最大リトライ回数: 1回
- リトライ間隔: 5秒

## パフォーマンス考慮事項

### 検証実行時間の目安
- **Minimal**: 1〜3秒（ファイル確認とffprobe基本情報のみ）
- **Standard**: 5〜15秒（メディアストリーム詳細検証）
- **Strict**: 15〜60秒（完全デコードテストを含む）

※ ファイルサイズとHLSセグメント数に依存

### 最適化戦略

1. **並列処理**
   - HLSの場合、複数セグメントの検証を並列実行
   - Goroutineプールを使用（同時実行数制限: 4〜8）

2. **段階的検証**
   - 軽量なチェックを先に実行
   - 早期失敗（fail-fast）により無駄な処理を削減

3. **キャッシング**
   - ffprobeの出力結果をメモリにキャッシュ
   - 同じファイルへの重複呼び出しを回避

4. **選択的検証**
   - HLSの場合、全セグメントではなくサンプリング検証も可能
   - 本番環境では `HLSValidationDepthMedium` を推奨

5. **タイムアウト**
   - 各検証ステップにタイムアウトを設定
   - デフォルト: 30秒、調整可能

## メトリクス

Prometheusメトリクスとして以下を記録：

```go
// 検証実行回数
validation_total{result="success|failure", level="minimal|standard|strict"}

// 検証実行時間
validation_duration_seconds{level="minimal|standard|strict"}

// エラー種別ごとのカウント
validation_errors_total{code="FILE_NOT_FOUND|CODEC_MISMATCH|..."}

// 警告種別ごとのカウント
validation_warnings_total{code="DURATION_TOO_LONG|..."}
```

## 設定

### 環境変数

```bash
# 検証レベル (minimal/standard/strict)
VALIDATION_LEVEL=standard

# デコードテストの有効化
VALIDATION_DECODE_TEST=false

# HLS検証の深さ (basic/medium/full)
VALIDATION_HLS_DEPTH=medium

# 検証タイムアウト（秒）
VALIDATION_TIMEOUT=30

# ffprobeパス（デフォルト: ffprobe）
FFPROBE_PATH=/usr/bin/ffprobe

# ffmpegパス（デフォルト: ffmpeg）
FFMPEG_PATH=/usr/bin/ffmpeg
```

### プリセットごとの期待値設定

```go
// internal/worker/preset/preset.go

type Preset struct {
    // ... 既存フィールド

    // 検証用の期待値
    Validation ValidationExpectation
}

type ValidationExpectation struct {
    VideoCodec     string
    Width          int
    Height         int
    AudioCodec     string
    DurationDelta  float64  // デュレーション許容誤差（秒）
    BitrateDelta   float64  // ビットレート許容誤差（割合: 0.1 = ±10%）
}
```

## 今後の拡張性

### フェーズ1（初期実装）
- ファイル存在性チェック
- ffprobeによる基本メディア情報検証
- HLSプレイリスト構文チェック
- 簡易的なストリーム情報検証

### フェーズ2（機能拡張）
- デコードテストの実装
- HLS全セグメント検証
- 詳細なA/V同期チェック
- プリセット別の期待値検証

### フェーズ3（高度な検証）
- 映像品質スコアリング（VMAF、SSIM等）
- サムネイル生成と目視確認用プレビュー
- 音量レベリング検証
- 字幕/キャプションの検証

### 将来的な機能
- AI/MLを使った異常検出
- リアルタイムストリーミング検証
- CDN配信前の E2E テスト
- クライアントプレイヤーエミュレーション

## 参考資料

- [ffmpeg Documentation](https://ffmpeg.org/documentation.html)
- [ffprobe Documentation](https://ffmpeg.org/ffprobe.html)
- [HLS Specification (RFC 8216)](https://datatracker.ietf.org/doc/html/rfc8216)
- [Apple HLS Authoring Specification](https://developer.apple.com/documentation/http_live_streaming/hls_authoring_specification_for_apple_devices)
