package recorder

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// SyncJobStatus represents the current state of a sync operation.
type SyncJobStatus struct {
	JobID           string      `json:"job_id"`
	Status          string      `json:"status"` // "idle", "running", "completed", "failed", "cancelled"
	OutputDir       string      `json:"output_dir"`
	Total           int         `json:"total"`
	Processed       int         `json:"processed"`
	Downloaded      int         `json:"downloaded"`
	Skipped         int         `json:"skipped"`
	Failed          int         `json:"failed"`
	CurrentItem     string      `json:"current_item,omitempty"`
	StartTime       time.Time   `json:"start_time,omitempty"`
	EndTime         time.Time   `json:"end_time,omitempty"`
	DurationSeconds float64     `json:"duration_seconds,omitempty"`
	Errors          []string    `json:"errors,omitempty"`
	Report          *SyncReport `json:"report,omitempty"`
}

// SyncManager manages concurrent or background synchronization jobs.
type SyncManager struct {
	client *Client
	mu     sync.RWMutex
	active *activeJob
	last   *SyncJobStatus
}

type activeJob struct {
	status SyncJobStatus
	cancel context.CancelFunc
}

// NewSyncManager creates a manager for orchestrating sync jobs.
func NewSyncManager(client *Client) *SyncManager {
	return &SyncManager{
		client: client,
		last: &SyncJobStatus{
			Status: "idle",
		},
	}
}

// StartAsync initiates an asynchronous background synchronization job.
func (m *SyncManager) StartAsync(parentCtx context.Context, opts SyncOptions) (string, error) {
	m.mu.Lock()
	if m.active != nil && m.active.status.Status == "running" {
		m.mu.Unlock()
		return "", errors.New("a synchronization job is already running")
	}

	jobID := fmt.Sprintf("sync_%d", time.Now().Unix())
	ctx, cancel := context.WithCancel(context.Background())

	job := &activeJob{
		status: SyncJobStatus{
			JobID:     jobID,
			Status:    "running",
			OutputDir: opts.OutputDir,
			StartTime: time.Now(),
		},
		cancel: cancel,
	}
	m.active = job
	m.mu.Unlock()

	go func() {
		report, err := m.client.Sync(ctx, opts, func(curr, total int, item *SyncItem) {
			m.mu.Lock()
			if m.active != nil && m.active.status.JobID == jobID {
				m.active.status.Total = total
				m.active.status.Processed = curr
				m.active.status.CurrentItem = item.Title
				if item.Status == "downloaded" {
					m.active.status.Downloaded++
				} else if item.Status == "skipped" {
					m.active.status.Skipped++
				} else {
					m.active.status.Failed++
				}
			}
			m.mu.Unlock()
		})

		m.mu.Lock()
		defer m.mu.Unlock()

		endTime := time.Now()
		finalStatus := "completed"
		if errors.Is(ctx.Err(), context.Canceled) {
			finalStatus = "cancelled"
		} else if err != nil {
			finalStatus = "failed"
		}

		statusCopy := m.active.status
		statusCopy.Status = finalStatus
		statusCopy.EndTime = endTime
		statusCopy.DurationSeconds = endTime.Sub(statusCopy.StartTime).Seconds()
		statusCopy.Report = report
		if err != nil {
			statusCopy.Errors = append(statusCopy.Errors, err.Error())
		}

		m.last = &statusCopy
		m.active = nil
	}()

	return jobID, nil
}

// RunSync synchronously executes a sync operation and blocks until completion.
func (m *SyncManager) RunSync(ctx context.Context, opts SyncOptions) (*SyncReport, error) {
	return m.client.Sync(ctx, opts, nil)
}

// GetStatus returns the status of the currently running job or the most recent job.
func (m *SyncManager) GetStatus() SyncJobStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.active != nil {
		return m.active.status
	}
	if m.last != nil {
		return *m.last
	}
	return SyncJobStatus{Status: "idle"}
}

// Cancel terminates any currently running synchronization job.
func (m *SyncManager) Cancel() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active != nil && m.active.cancel != nil {
		m.active.cancel()
		m.active.status.Status = "cancelling"
		return true
	}
	return false
}
