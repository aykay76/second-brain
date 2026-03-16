package vision

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"pa/internal/ingestion"
)

// JobManager processes vision ingestion jobs asynchronously.
type JobManager struct {
	syncer   *FilesystemSyncer
	jobQueue chan *Job
	done     chan struct{}
	wg       sync.WaitGroup
	active   sync.Map // map[string]*Job - tracks active jobs by ID
}

// Job represents a single vision ingestion task.
type Job struct {
	ID        string
	StartedAt time.Time
	Result    *ingestion.SyncResult
	Error     error
	Done      bool
	mu        sync.Mutex
}

// NewJobManager creates a new async vision job manager.
func NewJobManager(syncer *FilesystemSyncer, workers int) *JobManager {
	if workers <= 0 {
		workers = 1
	}

	jm := &JobManager{
		syncer:   syncer,
		jobQueue: make(chan *Job, 100),
		done:     make(chan struct{}),
	}

	// Start worker goroutines
	jm.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go jm.worker(i)
	}

	return jm
}

// Submit queues a new vision ingestion job.
func (jm *JobManager) Submit(ctx context.Context) (*Job, error) {
	job := &Job{
		ID:        time.Now().Format("20060102150405"),
		StartedAt: time.Now(),
	}

	select {
	case jm.jobQueue <- job:
		jm.active.Store(job.ID, job)
		slog.Info("vision job queued", "job_id", job.ID)
		return job, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Get retrieves a job by ID.
func (jm *JobManager) Get(id string) *Job {
	if v, ok := jm.active.Load(id); ok {
		return v.(*Job)
	}
	return nil
}

// Shutdown gracefully stops the job manager.
func (jm *JobManager) Shutdown(ctx context.Context) error {
	close(jm.done)

	// Wait for workers to finish or context to timeout
	done := make(chan struct{})
	go func() {
		jm.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// worker processes jobs from the queue.
func (jm *JobManager) worker(id int) {
	defer jm.wg.Done()

	for {
		select {
		case job := <-jm.jobQueue:
			if job == nil {
				return
			}
			jm.processJob(job)
		case <-jm.done:
			return
		}
	}
}

// processJob runs the actual sync operation.
func (jm *JobManager) processJob(job *Job) {
	slog.Info("vision job started", "job_id", job.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()

	result, err := jm.syncer.Sync(ctx)

	job.mu.Lock()
	job.Result = result
	job.Error = err
	job.Done = true
	job.mu.Unlock()

	if err != nil {
		slog.Error("vision job failed", "job_id", job.ID, "error", err)
	} else {
		slog.Info("vision job completed",
			"job_id", job.ID,
			"ingested", result.Ingested,
			"skipped", result.Skipped,
			"errors", result.Errors,
		)
	}

	// Keep job in active map for a while so clients can retrieve results
	go func() {
		time.Sleep(5 * time.Minute)
		jm.active.Delete(job.ID)
	}()
}

// Status returns the current status of a job.
func (j *Job) Status() map[string]interface{} {
	j.mu.Lock()
	defer j.mu.Unlock()

	status := map[string]interface{}{
		"id":         j.ID,
		"started_at": j.StartedAt,
		"done":       j.Done,
	}

	if j.Done {
		elapsed := time.Since(j.StartedAt)
		status["elapsed_seconds"] = int(elapsed.Seconds())
		if j.Error != nil {
			status["error"] = j.Error.Error()
		} else if j.Result != nil {
			status["ingested"] = j.Result.Ingested
			status["skipped"] = j.Result.Skipped
			status["errors"] = j.Result.Errors
		}
	} else {
		status["elapsed_seconds"] = int(time.Since(j.StartedAt).Seconds())
	}

	return status
}
