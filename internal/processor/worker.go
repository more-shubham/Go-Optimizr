package processor

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/chai2010/webp"
	"github.com/more-shubham/go-optimizr/internal/analytics"
	"github.com/more-shubham/go-optimizr/internal/tracker"
)

// Job represents a single image conversion task
type Job struct {
	InputPath  string
	OutputPath string
}

// Result represents the outcome of a conversion job
type Result struct {
	InputPath   string
	InputBytes  int64
	OutputBytes int64
	BytesSaved  int64
	Duration    time.Duration
	Error       error
}

// WorkerPool manages a pool of workers for concurrent image processing
type WorkerPool struct {
	NumWorkers  int
	JobsChan    chan Job
	ResultsChan chan Result
	Stats       *tracker.Stats
	Analytics   *analytics.Collector
	Config      *Config
	wg          sync.WaitGroup
	ctx         <-chan struct{}
	verbose     bool
}

// Config holds worker pool configuration
type Config struct {
	InputDir  string
	OutputDir string
	Quality   float32
	Verbose   bool
}

const (
	// MaxAllocMB is the memory threshold (70% of 8GB) before triggering FreeOSMemory
	MaxAllocMB = 5600
)

// NewWorkerPool creates a new worker pool
func NewWorkerPool(numWorkers int, config *Config, stats *tracker.Stats, analyticsCollector *analytics.Collector, ctx <-chan struct{}) *WorkerPool {
	// Buffer size prevents memory spikes by limiting queued jobs
	bufferSize := numWorkers * 2

	return &WorkerPool{
		NumWorkers:  numWorkers,
		JobsChan:    make(chan Job, bufferSize),
		ResultsChan: make(chan Result, bufferSize),
		Stats:       stats,
		Analytics:   analyticsCollector,
		Config:      config,
		ctx:         ctx,
		verbose:     config.Verbose,
	}
}

// Start launches all workers in the pool
func (wp *WorkerPool) Start() {
	for i := 0; i < wp.NumWorkers; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
	log.Printf("Started %d workers", wp.NumWorkers)
}

// Submit sends a job to the worker pool
func (wp *WorkerPool) Submit(job Job) bool {
	select {
	case wp.JobsChan <- job:
		return true
	case <-wp.ctx:
		return false
	}
}

// Close closes the jobs channel and waits for workers to finish
func (wp *WorkerPool) Close() {
	close(wp.JobsChan)
	wp.wg.Wait()
	close(wp.ResultsChan)
}

// Results returns the results channel for consumption
func (wp *WorkerPool) Results() <-chan Result {
	return wp.ResultsChan
}

// worker processes jobs from the jobs channel
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()

	for job := range wp.JobsChan {
		// Check for shutdown signal before processing
		select {
		case <-wp.ctx:
			if wp.verbose {
				log.Printf("[Worker %d] Shutdown signal received, stopping", id)
			}
			return
		default:
		}

		result := wp.processImage(job)

		if wp.verbose {
			if result.Error != nil {
				log.Printf("[Worker %d] FAILED: %s - %v",
					id, filepath.Base(job.InputPath), result.Error)
			} else {
				log.Printf("[Worker %d] OK: %s in %v (saved %.2f KB)",
					id, filepath.Base(job.InputPath),
					result.Duration.Round(time.Millisecond),
					float64(result.BytesSaved)/1024)
			}
		}

		wp.ResultsChan <- result

		// Memory management: check and free memory if needed
		wp.checkAndFreeMemory()
	}
}

// processImage converts a single image to WebP format
func (wp *WorkerPool) processImage(job Job) Result {
	start := time.Now()
	result := Result{InputPath: job.InputPath}

	// Get input file info for size comparison
	inputInfo, err := os.Stat(job.InputPath)
	if err != nil {
		result.Error = fmt.Errorf("stat input: %w", err)
		return result
	}
	inputSize := inputInfo.Size()
	result.InputBytes = inputSize

	// Open input file
	inputFile, err := os.Open(job.InputPath)
	if err != nil {
		result.Error = fmt.Errorf("open input: %w", err)
		return result
	}
	defer inputFile.Close()

	// Decode image based on format
	var img image.Image
	ext := strings.ToLower(filepath.Ext(job.InputPath))

	switch ext {
	case ".jpg", ".jpeg":
		img, err = jpeg.Decode(inputFile)
	case ".png":
		img, err = png.Decode(inputFile)
	default:
		result.Error = fmt.Errorf("unsupported format: %s", ext)
		return result
	}

	if err != nil {
		result.Error = fmt.Errorf("decode image: %w", err)
		return result
	}

	// Create output directory structure (preserve directory hierarchy)
	relPath, _ := filepath.Rel(wp.Config.InputDir, job.InputPath)
	outputPath := filepath.Join(wp.Config.OutputDir, relPath)
	outputPath = strings.TrimSuffix(outputPath, ext) + ".webp"

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		result.Error = fmt.Errorf("create output dir: %w", err)
		return result
	}

	// Create output file
	outputFile, err := os.Create(outputPath)
	if err != nil {
		result.Error = fmt.Errorf("create output: %w", err)
		return result
	}
	defer outputFile.Close()

	// Encode to WebP
	options := &webp.Options{
		Lossless: false,
		Quality:  wp.Config.Quality,
	}

	if err := webp.Encode(outputFile, img, options); err != nil {
		result.Error = fmt.Errorf("encode webp: %w", err)
		return result
	}

	// Clear image buffer to help GC - important for memory management
	img = nil

	// Get output file size for savings calculation
	outputInfo, err := os.Stat(outputPath)
	if err != nil {
		result.Error = fmt.Errorf("stat output: %w", err)
		return result
	}
	outputSize := outputInfo.Size()

	result.OutputBytes = outputSize
	result.BytesSaved = inputSize - outputSize
	result.Duration = time.Since(start)

	return result
}

// checkAndFreeMemory monitors RAM usage and triggers GC if threshold exceeded
func (wp *WorkerPool) checkAndFreeMemory() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Calculate allocated memory in MB
	allocMB := float64(memStats.Alloc) / (1024 * 1024)

	// Trigger GC and FreeOSMemory at 70% of threshold (~4GB for 8GB system)
	if allocMB > float64(MaxAllocMB)*0.7 {
		runtime.GC()
		debug.FreeOSMemory()
		if wp.verbose {
			log.Printf("Memory freed. Allocation was: %.2f MB", allocMB)
		}
	}
}
