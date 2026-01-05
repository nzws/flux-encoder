package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func Test初回で成功した場合はリトライせずに成功を返す(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		return nil
	}

	config := Config{
		MaxAttempts: 3,
		InitialWait: 10 * time.Millisecond,
		MaxWait:     100 * time.Millisecond,
		Multiplier:  2.0,
	}

	err := Do(context.Background(), config, fn)
	if err != nil {
		t.Errorf("初回成功なのにエラーが返された: %v", err)
	}
	if callCount != 1 {
		t.Errorf("初回成功なのに関数が %d 回呼ばれた（期待値: 1）", callCount)
	}
}

func Test最大試行回数までリトライして失敗する(t *testing.T) {
	callCount := 0
	expectedErr := errors.New("常に失敗")

	fn := func() error {
		callCount++
		return expectedErr
	}

	config := Config{
		MaxAttempts: 3,
		InitialWait: 1 * time.Millisecond,
		MaxWait:     100 * time.Millisecond,
		Multiplier:  2.0,
	}

	err := Do(context.Background(), config, fn)
	if err == nil {
		t.Fatal("エラーが返されるべきだが nil だった")
	}
	if callCount != 3 {
		t.Errorf("関数が %d 回呼ばれた（期待値: 3）", callCount)
	}
}

func Test2回目で成功した場合はリトライが機能する(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		if callCount < 2 {
			return errors.New("1回目は失敗")
		}
		return nil
	}

	config := Config{
		MaxAttempts: 3,
		InitialWait: 1 * time.Millisecond,
		MaxWait:     100 * time.Millisecond,
		Multiplier:  2.0,
	}

	err := Do(context.Background(), config, fn)
	if err != nil {
		t.Errorf("2回目で成功すべきだがエラーが返された: %v", err)
	}
	if callCount != 2 {
		t.Errorf("関数が %d 回呼ばれた（期待値: 2）", callCount)
	}
}

func Test待機時間が指数関数的に増加する(t *testing.T) {
	callCount := 0
	callTimes := []time.Time{}

	fn := func() error {
		callTimes = append(callTimes, time.Now())
		callCount++
		if callCount < 3 {
			return errors.New("失敗")
		}
		return nil
	}

	config := Config{
		MaxAttempts: 3,
		InitialWait: 50 * time.Millisecond,
		MaxWait:     500 * time.Millisecond,
		Multiplier:  2.0,
	}

	err := Do(context.Background(), config, fn)
	if err != nil {
		t.Errorf("3回目で成功すべきだがエラーが返された: %v", err)
	}

	// 待機時間の確認（1回目→2回目は約50ms、2回目→3回目は約100ms）
	if len(callTimes) != 3 {
		t.Fatalf("関数が %d 回呼ばれた（期待値: 3）", len(callTimes))
	}

	// 1回目→2回目の待機時間
	firstWait := callTimes[1].Sub(callTimes[0])
	if firstWait < 40*time.Millisecond || firstWait > 80*time.Millisecond {
		t.Errorf("1回目の待機時間が期待値（約50ms）と異なる: %v", firstWait)
	}

	// 2回目→3回目の待機時間（2倍になっているはず）
	secondWait := callTimes[2].Sub(callTimes[1])
	if secondWait < 80*time.Millisecond || secondWait > 150*time.Millisecond {
		t.Errorf("2回目の待機時間が期待値（約100ms）と異なる: %v", secondWait)
	}
}

func Test待機時間がMaxWaitを超えない(t *testing.T) {
	callCount := 0
	callTimes := []time.Time{}

	fn := func() error {
		callTimes = append(callTimes, time.Now())
		callCount++
		return errors.New("常に失敗")
	}

	config := Config{
		MaxAttempts: 5,
		InitialWait: 50 * time.Millisecond,
		MaxWait:     100 * time.Millisecond,
		Multiplier:  2.0,
	}

	if err := Do(context.Background(), config, fn); err == nil {
		t.Error("常に失敗する関数のためエラーが返されるべき")
	}

	// 3回目以降の待機時間は MaxWait (100ms) でキャップされるはず
	// 1回目→2回目: 50ms
	// 2回目→3回目: 100ms (50*2)
	// 3回目→4回目: 100ms (100ms cap)
	// 4回目→5回目: 100ms (100ms cap)

	if len(callTimes) != 5 {
		t.Fatalf("関数が %d 回呼ばれた（期待値: 5）", len(callTimes))
	}

	// 3回目→4回目の待機時間が MaxWait (100ms) でキャップされているか確認
	thirdWait := callTimes[3].Sub(callTimes[2])
	if thirdWait > 150*time.Millisecond {
		t.Errorf("3回目の待機時間が MaxWait を超えている: %v", thirdWait)
	}
}

func Testコンテキストがキャンセルされたら即座に中止する(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		return errors.New("失敗")
	}

	ctx, cancel := context.WithCancel(context.Background())

	config := Config{
		MaxAttempts: 10,
		InitialWait: 100 * time.Millisecond,
		MaxWait:     1 * time.Second,
		Multiplier:  2.0,
	}

	// 別のgoroutineで50msでキャンセル
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	startTime := time.Now()
	err := Do(ctx, config, fn)
	elapsed := time.Since(startTime)

	if err == nil {
		t.Fatal("コンテキストキャンセル時はエラーが返されるべき")
	}

	// コンテキストキャンセルによるエラーかチェック
	if !errors.Is(err, context.Canceled) {
		t.Errorf("コンテキストキャンセルエラーが返されるべきだが別のエラーが返された: %v", err)
	}

	// 1回目の実行 + 50ms待機でキャンセルされるので、150ms以内に完了するはず
	if elapsed > 200*time.Millisecond {
		t.Errorf("コンテキストキャンセル後も実行が続いている: %v", elapsed)
	}

	// 2回目の実行前にキャンセルされるはず
	if callCount > 2 {
		t.Errorf("コンテキストキャンセル後も関数が %d 回呼ばれた", callCount)
	}
}

func TestDefaultConfigが適切に設定されている(t *testing.T) {
	if DefaultConfig.MaxAttempts != 3 {
		t.Errorf("DefaultConfig.MaxAttempts = %d, 期待値: 3", DefaultConfig.MaxAttempts)
	}
	if DefaultConfig.InitialWait != 1*time.Second {
		t.Errorf("DefaultConfig.InitialWait = %v, 期待値: 1s", DefaultConfig.InitialWait)
	}
	if DefaultConfig.MaxWait != 30*time.Second {
		t.Errorf("DefaultConfig.MaxWait = %v, 期待値: 30s", DefaultConfig.MaxWait)
	}
	if DefaultConfig.Multiplier != 2.0 {
		t.Errorf("DefaultConfig.Multiplier = %f, 期待値: 2.0", DefaultConfig.Multiplier)
	}
}

func Test最大試行回数が1の場合はリトライしない(t *testing.T) {
	callCount := 0
	fn := func() error {
		callCount++
		return errors.New("失敗")
	}

	config := Config{
		MaxAttempts: 1,
		InitialWait: 10 * time.Millisecond,
		MaxWait:     100 * time.Millisecond,
		Multiplier:  2.0,
	}

	err := Do(context.Background(), config, fn)
	if err == nil {
		t.Fatal("エラーが返されるべきだが nil だった")
	}
	if callCount != 1 {
		t.Errorf("MaxAttempts=1 なのに関数が %d 回呼ばれた", callCount)
	}
}
