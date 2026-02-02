package processor

import (
	"io/fs"
	"log"
	"path/filepath"
	"strings"

	"github.com/more-shubham/go-optimizr/internal/analytics"
	"github.com/more-shubham/go-optimizr/internal/tracker"
)

// Engine orchestrates the image conversion process
type Engine struct {
	Pool      *WorkerPool
	Stats     *tracker.Stats
	Analytics *analytics.Collector
	Config    *Config
	ctx       <-chan struct{}
	done      chan struct{}
}

// NewEngine creates a new conversion engine
func NewEngine(numWorkers int, config *Config, ctx <-chan struct{}) *Engine {
	stats := tracker.NewStats()
	analyticsCollector := analytics.NewCollector(config.OutputDir, config.Verbose)
	pool := NewWorkerPool(numWorkers, config, stats, analyticsCollector, ctx)

	return &Engine{
		Pool:      pool,
		Stats:     stats,
		Analytics: analyticsCollector,
		Config:    config,
		ctx:       ctx,
		done:      make(chan struct{}),
	}
}

// Run executes the full conversion pipeline
func (e *Engine) Run() {
	// Start analytics collection
	e.Analytics.Start()

	// Start the worker pool
	e.Pool.Start()

	// Start result collector in background
	go e.collectResults()

	// Walk directory and submit jobs
	e.walkAndSubmitJobs()

	// Close the pool (waits for workers to finish)
	e.Pool.Close()

	// Wait for result collector to finish
	<-e.done

	// Stop analytics and get summary
	summary := e.Analytics.Stop()

	// Print reports
	e.Stats.PrintFinalReport()
	e.Analytics.PrintSummary(summary)

	// Export JSON summary
	if err := e.Analytics.ExportJSON(summary); err != nil {
		log.Printf("Warning: Failed to export summary: %v", err)
	}
}

// walkAndSubmitJobs walks the input directory and submits jobs to the worker pool
func (e *Engine) walkAndSubmitJobs() {
	var count int

	err := filepath.WalkDir(e.Config.InputDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Printf("Error accessing path %s: %v", path, err)
			return nil // Continue walking despite errors
		}

		// Check for shutdown signal
		select {
		case <-e.ctx:
			log.Println("Shutdown signal received during directory walk")
			return filepath.SkipAll
		default:
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Check if it's a supported image format
		ext := strings.ToLower(filepath.Ext(path))
		if !isSupportedFormat(ext) {
			return nil
		}

		// Submit job to worker pool
		job := Job{InputPath: path}
		if e.Pool.Submit(job) {
			count++
		} else {
			// Shutdown was triggered
			return filepath.SkipAll
		}

		return nil
	})

	if err != nil {
		log.Printf("Error walking directory: %v", err)
	}

	e.Stats.SetTotalFiles(count)
	e.Analytics.SetTotalFiles(int64(count))
	log.Printf("Found %d images to process", count)
}

// collectResults aggregates results from workers and updates statistics
func (e *Engine) collectResults() {
	defer close(e.done)

	progressInterval := int64(100) // Log every 100 files

	for result := range e.Pool.Results() {
		if result.Error != nil {
			e.Stats.IncrementFailed()
			e.Analytics.RecordFailure()
		} else {
			e.Stats.IncrementProcessed()
			e.Stats.AddBytesSaved(result.BytesSaved)
			e.Analytics.RecordSuccess(result.InputBytes, result.OutputBytes, result.Duration)
		}

		// Log progress periodically
		e.Stats.LogProgress(progressInterval)
	}
}

// isSupportedFormat checks if the file extension is a supported image format
func isSupportedFormat(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png":
		return true
	default:
		return false
	}
}

// SupportedFormats returns a list of supported input formats
func SupportedFormats() []string {
	return []string{".jpg", ".jpeg", ".png"}
}
