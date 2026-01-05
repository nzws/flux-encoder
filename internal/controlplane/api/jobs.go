package api

import (
	"sync"

	workerv1 "github.com/nzws/flux-encoder/proto/worker/v1"
)

// JobManager はジョブの進捗を管理する
type JobManager struct {
	jobs  map[string]chan *workerv1.JobProgress
	mutex sync.RWMutex
}

// NewJobManager は新しい JobManager を作成する
func NewJobManager() *JobManager {
	return &JobManager{
		jobs: make(map[string]chan *workerv1.JobProgress),
	}
}

// CreateProgressChannel は新しい進捗チャネルを作成する
func (jm *JobManager) CreateProgressChannel(jobID string) chan *workerv1.JobProgress {
	jm.mutex.Lock()
	defer jm.mutex.Unlock()

	ch := make(chan *workerv1.JobProgress, 100)
	jm.jobs[jobID] = ch
	return ch
}

// GetProgressChannel は進捗チャネルを取得する
func (jm *JobManager) GetProgressChannel(jobID string) (chan *workerv1.JobProgress, bool) {
	jm.mutex.RLock()
	defer jm.mutex.RUnlock()

	ch, exists := jm.jobs[jobID]
	return ch, exists
}

// CloseProgressChannel は進捗チャネルを閉じて削除する
func (jm *JobManager) CloseProgressChannel(jobID string) {
	jm.mutex.Lock()
	defer jm.mutex.Unlock()

	if ch, exists := jm.jobs[jobID]; exists {
		close(ch)
		delete(jm.jobs, jobID)
	}
}
