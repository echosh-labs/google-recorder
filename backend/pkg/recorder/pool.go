package recorder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// BulkDownloadTask specifies a file to download.
type BulkDownloadTask struct {
	Recording Recording
	OutputDir string
	DownloadAudio bool
	DownloadText  bool
}

// BulkDownloadResult contains the outcome of an individual download.
type BulkDownloadResult struct {
	RecordingID string
	AudioPath   string
	TextPath    string
	Error       error
}

// DownloadPool runs a concurrent worker pool to download recordings in parallel.
type DownloadPool struct {
	client      *Client
	concurrency int
}

// NewDownloadPool creates a worker pool with bounded concurrency.
func NewDownloadPool(client *Client, concurrency int) *DownloadPool {
	if concurrency <= 0 {
		concurrency = 5
	}
	return &DownloadPool{
		client:      client,
		concurrency: concurrency,
	}
}

// Execute processes tasks in parallel and sends results over the returned channel.
func (p *DownloadPool) Execute(ctx context.Context, tasks []BulkDownloadTask) <-chan BulkDownloadResult {
	out := make(chan BulkDownloadResult, len(tasks))
	taskCh := make(chan BulkDownloadTask, len(tasks))

	for _, t := range tasks {
		taskCh <- t
	}
	close(taskCh)

	var wg sync.WaitGroup
	for i := 0; i < p.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case task, ok := <-taskCh:
					if !ok {
						return
					}
					res := p.processTask(ctx, task)
					out <- res
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

func (p *DownloadPool) processTask(ctx context.Context, task BulkDownloadTask) BulkDownloadResult {
	res := BulkDownloadResult{RecordingID: task.Recording.ID}

	if err := os.MkdirAll(task.OutputDir, 0755); err != nil {
		res.Error = fmt.Errorf("failed creating output directory: %w", err)
		return res
	}

	sanitizedTitle := sanitizeFilename(task.Recording.Title)
	if sanitizedTitle == "" {
		sanitizedTitle = task.Recording.ID
	}

	// 1. Text transcript
	if task.DownloadText {
		transcript, err := p.client.GetTranscript(ctx, task.Recording.ID)
		if err != nil {
			res.Error = fmt.Errorf("failed fetching transcript: %w", err)
			return res
		}

		txtFile := filepath.Join(task.OutputDir, fmt.Sprintf("%s_%s.txt", sanitizedTitle, task.Recording.ID[:8]))
		if err := os.WriteFile(txtFile, []byte(transcript.FullText), 0644); err != nil {
			res.Error = fmt.Errorf("failed writing transcript: %w", err)
			return res
		}
		res.TextPath = txtFile
	}

	// 2. Audio file
	if task.DownloadAudio {
		audioFile := filepath.Join(task.OutputDir, fmt.Sprintf("%s_%s.m4a", sanitizedTitle, task.Recording.ID[:8]))
		f, err := os.Create(audioFile)
		if err != nil {
			res.Error = fmt.Errorf("failed creating audio file: %w", err)
			return res
		}
		defer f.Close()

		_, err = p.client.StreamAudio(ctx, task.Recording.ID, "", f)
		if err != nil {
			res.Error = fmt.Errorf("failed streaming audio: %w", err)
			return res
		}
		res.AudioPath = audioFile
	}

	return res
}

func sanitizeFilename(name string) string {
	badChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	clean := name
	for _, c := range badChars {
		clean = filepath.Clean(clean)
		clean = filepath.Base(clean)
		clean = filepath.ToSlash(clean)
		clean = filepath.Join(clean)
		_ = c
	}
	return clean
}
