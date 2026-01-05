package api

import (
	"encoding/json"
	"sync"
	"testing"

	workerv1 "github.com/nzws/flux-encoder/proto/worker/v1"
)

func Test進捗チャネルを作成できる(t *testing.T) {
	jm := NewJobManager()
	jobID := "test-job-123"

	ch := jm.CreateProgressChannel(jobID)
	if ch == nil {
		t.Fatal("チャネルが作成されなかった")
	}

	// 作成したチャネルが取得できるか確認
	retrievedCh, exists := jm.GetProgressChannel(jobID)
	if !exists {
		t.Fatal("作成したチャネルが取得できない")
	}
	if retrievedCh != ch {
		t.Error("取得したチャネルが作成したチャネルと異なる")
	}
}

func Test存在しないジョブIDで取得するとfalseが返る(t *testing.T) {
	jm := NewJobManager()

	_, exists := jm.GetProgressChannel("存在しないジョブID")
	if exists {
		t.Error("存在しないジョブIDで exists が true になった")
	}
}

func Test進捗チャネルをクローズして削除できる(t *testing.T) {
	jm := NewJobManager()
	jobID := "test-job-456"

	ch := jm.CreateProgressChannel(jobID)

	// クローズ前は取得できる
	_, exists := jm.GetProgressChannel(jobID)
	if !exists {
		t.Fatal("チャネルが存在しない")
	}

	// クローズ
	jm.CloseProgressChannel(jobID)

	// クローズ後は取得できない
	_, exists = jm.GetProgressChannel(jobID)
	if exists {
		t.Error("クローズ後もチャネルが存在する")
	}

	// チャネルがクローズされているか確認
	_, ok := <-ch
	if ok {
		t.Error("チャネルがクローズされていない")
	}
}

func Test存在しないジョブIDをクローズしてもエラーにならない(t *testing.T) {
	jm := NewJobManager()

	// パニックが発生しないことを確認
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("存在しないジョブIDのクローズでパニックが発生: %v", r)
		}
	}()

	jm.CloseProgressChannel("存在しないジョブID")
}

func Testチャネルのバッファ容量が100である(t *testing.T) {
	jm := NewJobManager()
	jobID := "test-job-buffer"

	ch := jm.CreateProgressChannel(jobID)

	// バッファ容量を確認（100個のメッセージを送信してもブロックしないはず）
	for i := 0; i < 100; i++ {
		select {
		case ch <- &workerv1.JobProgress{Progress: float32(i)}:
			// OK
		default:
			t.Fatalf("バッファが %d 個目でいっぱいになった（期待: 100）", i)
		}
	}

	// 101個目を送信しようとするとブロックするはず
	select {
	case ch <- &workerv1.JobProgress{Progress: 100}:
		t.Error("バッファが101個目を受け入れた（期待: ブロック）")
	default:
		// ブロックされた（期待通り）
	}
}

func Test並行処理で複数のジョブを作成できる(t *testing.T) {
	jm := NewJobManager()
	numJobs := 100
	var wg sync.WaitGroup

	// 並行して複数のジョブを作成
	for i := 0; i < numJobs; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			jobID := string(rune('A'+id%26)) + string(rune('0'+id%10))
			jm.CreateProgressChannel(jobID)
		}(i)
	}

	wg.Wait()

	// すべてのジョブが作成されたか確認（重複を考慮）
	jm.mutex.RLock()
	jobCount := len(jm.jobs)
	jm.mutex.RUnlock()

	if jobCount == 0 {
		t.Error("ジョブが1つも作成されていない")
	}
}

func Test並行処理で読み書きが競合しない(t *testing.T) {
	jm := NewJobManager()
	numOperations := 1000
	var wg sync.WaitGroup

	// 並行して作成・取得・削除を行う
	for i := 0; i < numOperations; i++ {
		wg.Add(3)

		// 作成
		go func(id int) {
			defer wg.Done()
			jobID := string(rune('J' + id%10))
			jm.CreateProgressChannel(jobID)
		}(i)

		// 取得
		go func(id int) {
			defer wg.Done()
			jobID := string(rune('J' + id%10))
			jm.GetProgressChannel(jobID)
		}(i)

		// 削除
		go func(id int) {
			defer wg.Done()
			jobID := string(rune('J' + id%10))
			jm.CloseProgressChannel(jobID)
		}(i)
	}

	wg.Wait()

	// データ競合が発生しなければ成功
	// go test -race で実行することで競合を検出できる
}

func Test複数のジョブを同時に管理できる(t *testing.T) {
	jm := NewJobManager()

	// 複数のジョブを作成
	jobIDs := []string{"job1", "job2", "job3"}
	channels := make(map[string]chan *workerv1.JobProgress)

	for _, jobID := range jobIDs {
		ch := jm.CreateProgressChannel(jobID)
		channels[jobID] = ch
	}

	// すべてのジョブが取得できることを確認
	for _, jobID := range jobIDs {
		ch, exists := jm.GetProgressChannel(jobID)
		if !exists {
			t.Errorf("ジョブ %s が取得できない", jobID)
		}
		if ch != channels[jobID] {
			t.Errorf("ジョブ %s のチャネルが一致しない", jobID)
		}
	}

	// 1つのジョブをクローズ
	jm.CloseProgressChannel("job2")

	// job2 は取得できない
	_, exists := jm.GetProgressChannel("job2")
	if exists {
		t.Error("クローズしたジョブ job2 が取得できた")
	}

	// 他のジョブはまだ取得できる
	for _, jobID := range []string{"job1", "job3"} {
		_, exists := jm.GetProgressChannel(jobID)
		if !exists {
			t.Errorf("ジョブ %s が取得できない（クローズしていないはず）", jobID)
		}
	}
}

func Testチャネルに進捗情報を送受信できる(t *testing.T) {
	jm := NewJobManager()
	jobID := "test-job-progress"

	ch := jm.CreateProgressChannel(jobID)

	// 進捗情報を送信
	progress := &workerv1.JobProgress{
		JobId:    jobID,
		Status:   workerv1.JobStatus_JOB_STATUS_PROCESSING,
		Progress: 50.0,
		Message:  "エンコード中",
	}

	ch <- progress

	// 進捗情報を受信
	received := <-ch
	if received.JobId != jobID {
		t.Errorf("JobId が一致しない: 期待値 %s, 取得値 %s", jobID, received.JobId)
	}
	if received.Progress != 50.0 {
		t.Errorf("Progress が一致しない: 期待値 50.0, 取得値 %f", received.Progress)
	}
	if received.Message != "エンコード中" {
		t.Errorf("Message が一致しない: 期待値 'エンコード中', 取得値 '%s'", received.Message)
	}
}

func TestNewJobManagerが初期化された状態を返す(t *testing.T) {
	jm := NewJobManager()

	if jm == nil {
		t.Fatal("NewJobManager が nil を返した")
	}

	if jm.jobs == nil {
		t.Error("jobs マップが初期化されていない")
	}

	jm.mutex.RLock()
	jobCount := len(jm.jobs)
	jm.mutex.RUnlock()

	if jobCount != 0 {
		t.Errorf("初期状態でジョブが存在する: %d 個", jobCount)
	}
}

func Test特殊文字を含むメッセージがJSON化できる(t *testing.T) {
	testCases := []struct {
		name    string
		message string
		error   string
	}{
		{
			name:    "ダブルクォート",
			message: `メッセージに"ダブルクォート"が含まれる`,
			error:   "",
		},
		{
			name:    "改行",
			message: "メッセージに\n改行が含まれる",
			error:   "",
		},
		{
			name:    "バックスラッシュ",
			message: `メッセージに\バックスラッシュが含まれる`,
			error:   "",
		},
		{
			name:    "複数の特殊文字",
			message: "メッセージに\"改行\nタブ\t\"が含まれる",
			error:   `エラー: "失敗"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// SSEで送信されるJSON構造をテスト
			data := map[string]interface{}{
				"job_id":   "test-job-123",
				"status":   "PROCESSING",
				"progress": 50.0,
				"message":  tc.message,
			}
			if tc.error != "" {
				data["error"] = tc.error
			}

			// json.Marshalが成功することを確認
			jsonData, err := json.Marshal(data)
			if err != nil {
				t.Fatalf("JSON化に失敗: %v", err)
			}

			// JSONが有効であることを確認（unmarshalできる）
			var decoded map[string]interface{}
			if err := json.Unmarshal(jsonData, &decoded); err != nil {
				t.Fatalf("JSONのデコードに失敗: %v (JSON: %s)", err, string(jsonData))
			}

			// メッセージが正しくデコードされることを確認
			if decoded["message"] != tc.message {
				t.Errorf("メッセージが一致しない: 期待値 %q, 取得値 %q", tc.message, decoded["message"])
			}
		})
	}
}
