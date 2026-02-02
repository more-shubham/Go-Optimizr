package analytics

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Collector handles all metrics collection and analysis
type Collector struct {
	// Timing metrics
	StartTime time.Time
	EndTime   time.Time

	// File metrics (atomic for thread safety)
	TotalFiles     int64
	ProcessedFiles int64
	FailedFiles    int64

	// Size metrics (in bytes)
	TotalInputBytes  int64
	TotalOutputBytes int64

	// Processing time tracking (nanoseconds)
	TotalProcessingTime int64
	MinProcessingTime   int64
	MaxProcessingTime   int64

	// Memory tracking
	PeakMemoryAlloc uint64
	PeakMemorySys   uint64
	MemSnapshots    []MemorySnapshot

	// GC tracking
	GCPauses      []time.Duration
	InitialGCRuns uint32

	// Control
	stopChan chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex

	// Configuration
	OutputDir string
	Verbose   bool
}

// MemorySnapshot captures memory state at a point in time
type MemorySnapshot struct {
	Timestamp  time.Time `json:"timestamp"`
	AllocMB    float64   `json:"alloc_mb"`
	TotalAlloc float64   `json:"total_alloc_mb"`
	SysMB      float64   `json:"sys_mb"`
	HeapInUse  float64   `json:"heap_in_use_mb"`
	NumGC      uint32    `json:"num_gc"`
	GCPauseMs  float64   `json:"gc_pause_ms"`
}

// Summary is the final JSON export structure
type Summary struct {
	// Run info
	StartTime       string  `json:"start_time"`
	EndTime         string  `json:"end_time"`
	Duration        string  `json:"duration"`
	DurationSeconds float64 `json:"duration_seconds"`

	// File stats
	TotalFiles     int64   `json:"total_files"`
	ProcessedFiles int64   `json:"processed_files"`
	FailedFiles    int64   `json:"failed_files"`
	ErrorRate      float64 `json:"error_rate_percent"`

	// Size stats
	TotalInputGB     float64 `json:"total_input_gb"`
	TotalOutputGB    float64 `json:"total_output_gb"`
	SpaceSavedGB     float64 `json:"space_saved_gb"`
	SpaceSavingPct   float64 `json:"space_saving_percent"`
	CompressionRatio float64 `json:"compression_ratio"`

	// Performance stats
	AvgTimePerImageMs float64 `json:"avg_time_per_image_ms"`
	MinTimePerImageMs float64 `json:"min_time_per_image_ms"`
	MaxTimePerImageMs float64 `json:"max_time_per_image_ms"`
	ThroughputMBps    float64 `json:"throughput_mb_per_sec"`
	ImagesPerSecond   float64 `json:"images_per_second"`

	// Memory stats
	PeakMemoryMB float64 `json:"peak_memory_mb"`
	PeakSysMemMB float64 `json:"peak_sys_memory_mb"`
	MemoryStable bool    `json:"memory_stable"`
	MemoryRange  string  `json:"memory_range"`

	// GC stats
	TotalGCRuns  uint32  `json:"total_gc_runs"`
	AvgGCPauseMs float64 `json:"avg_gc_pause_ms"`
	MaxGCPauseMs float64 `json:"max_gc_pause_ms"`

	// Analysis
	CPUBoundAnalysis string   `json:"cpu_bound_analysis"`
	Recommendations  []string `json:"recommendations"`

	// Raw snapshots for graphing
	MemorySnapshots []MemorySnapshot `json:"memory_snapshots"`
}

// NewCollector creates a new analytics collector
func NewCollector(outputDir string, verbose bool) *Collector {
	return &Collector{
		MinProcessingTime: int64(^uint64(0) >> 1), // Max int64
		stopChan:          make(chan struct{}),
		OutputDir:         outputDir,
		Verbose:           verbose,
		MemSnapshots:      make([]MemorySnapshot, 0, 200),
	}
}

// Start begins the analytics collection
func (c *Collector) Start() {
	c.StartTime = time.Now()

	// Capture initial GC count
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	c.InitialGCRuns = memStats.NumGC

	// Start memory monitoring goroutine
	c.wg.Add(1)
	go c.memoryMonitor()

	log.Println("[Analytics] Collection started")
}

// memoryMonitor logs memory stats every 30 seconds
func (c *Collector) memoryMonitor() {
	defer c.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Take initial snapshot
	c.captureMemorySnapshot()

	for {
		select {
		case <-ticker.C:
			c.captureMemorySnapshot()
		case <-c.stopChan:
			// Final snapshot
			c.captureMemorySnapshot()
			return
		}
	}
}

// captureMemorySnapshot records current memory state
func (c *Collector) captureMemorySnapshot() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	snapshot := MemorySnapshot{
		Timestamp:  time.Now(),
		AllocMB:    float64(memStats.Alloc) / (1024 * 1024),
		TotalAlloc: float64(memStats.TotalAlloc) / (1024 * 1024),
		SysMB:      float64(memStats.Sys) / (1024 * 1024),
		HeapInUse:  float64(memStats.HeapInuse) / (1024 * 1024),
		NumGC:      memStats.NumGC,
		GCPauseMs:  float64(memStats.PauseNs[(memStats.NumGC+255)%256]) / 1e6,
	}

	c.mu.Lock()
	c.MemSnapshots = append(c.MemSnapshots, snapshot)

	// Track peak memory
	if memStats.Alloc > c.PeakMemoryAlloc {
		c.PeakMemoryAlloc = memStats.Alloc
	}
	if memStats.Sys > c.PeakMemorySys {
		c.PeakMemorySys = memStats.Sys
	}
	c.mu.Unlock()

	if c.Verbose {
		log.Printf("[Memory] Alloc: %.2f MB | TotalAlloc: %.2f MB | Sys: %.2f MB | NumGC: %d",
			snapshot.AllocMB, snapshot.TotalAlloc, snapshot.SysMB, snapshot.NumGC)
	}
}

// SetTotalFiles sets the total number of files to process
func (c *Collector) SetTotalFiles(count int64) {
	atomic.StoreInt64(&c.TotalFiles, count)
}

// RecordSuccess records a successful conversion
func (c *Collector) RecordSuccess(inputBytes, outputBytes int64, processingTime time.Duration) {
	atomic.AddInt64(&c.ProcessedFiles, 1)
	atomic.AddInt64(&c.TotalInputBytes, inputBytes)
	atomic.AddInt64(&c.TotalOutputBytes, outputBytes)

	procNanos := processingTime.Nanoseconds()
	atomic.AddInt64(&c.TotalProcessingTime, procNanos)

	// Update min/max (using CAS for thread safety)
	for {
		oldMin := atomic.LoadInt64(&c.MinProcessingTime)
		if procNanos >= oldMin {
			break
		}
		if atomic.CompareAndSwapInt64(&c.MinProcessingTime, oldMin, procNanos) {
			break
		}
	}

	for {
		oldMax := atomic.LoadInt64(&c.MaxProcessingTime)
		if procNanos <= oldMax {
			break
		}
		if atomic.CompareAndSwapInt64(&c.MaxProcessingTime, oldMax, procNanos) {
			break
		}
	}
}

// RecordFailure records a failed conversion
func (c *Collector) RecordFailure() {
	atomic.AddInt64(&c.FailedFiles, 1)
}

// Stop ends collection and generates the summary
func (c *Collector) Stop() *Summary {
	c.EndTime = time.Now()

	// Stop memory monitor
	close(c.stopChan)
	c.wg.Wait()

	log.Println("[Analytics] Collection stopped, generating summary...")

	return c.generateSummary()
}

// generateSummary creates the final analytics summary
func (c *Collector) generateSummary() *Summary {
	duration := c.EndTime.Sub(c.StartTime)
	processed := atomic.LoadInt64(&c.ProcessedFiles)
	failed := atomic.LoadInt64(&c.FailedFiles)
	total := atomic.LoadInt64(&c.TotalFiles)
	inputBytes := atomic.LoadInt64(&c.TotalInputBytes)
	outputBytes := atomic.LoadInt64(&c.TotalOutputBytes)
	totalProcTime := atomic.LoadInt64(&c.TotalProcessingTime)

	// Calculate metrics
	inputGB := float64(inputBytes) / (1024 * 1024 * 1024)
	outputGB := float64(outputBytes) / (1024 * 1024 * 1024)
	savedGB := inputGB - outputGB

	var savingPct, compressionRatio float64
	if inputBytes > 0 {
		savingPct = (1 - float64(outputBytes)/float64(inputBytes)) * 100
		compressionRatio = float64(inputBytes) / float64(outputBytes)
	}

	var errorRate float64
	if total > 0 {
		errorRate = float64(failed) / float64(total) * 100
	}

	var avgTimeMs, minTimeMs, maxTimeMs float64
	if processed > 0 {
		avgTimeMs = float64(totalProcTime) / float64(processed) / 1e6
		minTimeMs = float64(atomic.LoadInt64(&c.MinProcessingTime)) / 1e6
		maxTimeMs = float64(atomic.LoadInt64(&c.MaxProcessingTime)) / 1e6
	}

	var throughputMBps, imagesPerSec float64
	if duration.Seconds() > 0 {
		throughputMBps = (float64(inputBytes) / (1024 * 1024)) / duration.Seconds()
		imagesPerSec = float64(processed) / duration.Seconds()
	}

	// Memory analysis
	peakMemMB := float64(c.PeakMemoryAlloc) / (1024 * 1024)
	peakSysMB := float64(c.PeakMemorySys) / (1024 * 1024)

	memStable, memRange := c.analyzeMemoryStability()

	// GC analysis
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	totalGCRuns := memStats.NumGC - c.InitialGCRuns
	avgGCPause, maxGCPause := c.analyzeGCPauses()

	// CPU bound analysis
	cpuAnalysis := c.analyzeCPUBound(avgTimeMs, maxTimeMs, throughputMBps)

	// Generate recommendations
	recommendations := c.generateRecommendations(
		memStable, avgTimeMs, maxTimeMs, errorRate, throughputMBps,
	)

	summary := &Summary{
		StartTime:       c.StartTime.Format(time.RFC3339),
		EndTime:         c.EndTime.Format(time.RFC3339),
		Duration:        duration.Round(time.Second).String(),
		DurationSeconds: duration.Seconds(),

		TotalFiles:     total,
		ProcessedFiles: processed,
		FailedFiles:    failed,
		ErrorRate:      errorRate,

		TotalInputGB:     inputGB,
		TotalOutputGB:    outputGB,
		SpaceSavedGB:     savedGB,
		SpaceSavingPct:   savingPct,
		CompressionRatio: compressionRatio,

		AvgTimePerImageMs: avgTimeMs,
		MinTimePerImageMs: minTimeMs,
		MaxTimePerImageMs: maxTimeMs,
		ThroughputMBps:    throughputMBps,
		ImagesPerSecond:   imagesPerSec,

		PeakMemoryMB: peakMemMB,
		PeakSysMemMB: peakSysMB,
		MemoryStable: memStable,
		MemoryRange:  memRange,

		TotalGCRuns:  totalGCRuns,
		AvgGCPauseMs: avgGCPause,
		MaxGCPauseMs: maxGCPause,

		CPUBoundAnalysis: cpuAnalysis,
		Recommendations:  recommendations,
		MemorySnapshots:  c.MemSnapshots,
	}

	return summary
}

// analyzeMemoryStability checks if memory usage was stable
func (c *Collector) analyzeMemoryStability() (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.MemSnapshots) < 2 {
		return true, "N/A"
	}

	var minAlloc, maxAlloc float64 = c.MemSnapshots[0].AllocMB, c.MemSnapshots[0].AllocMB

	for _, snap := range c.MemSnapshots {
		if snap.AllocMB < minAlloc {
			minAlloc = snap.AllocMB
		}
		if snap.AllocMB > maxAlloc {
			maxAlloc = snap.AllocMB
		}
	}

	memRange := maxAlloc - minAlloc
	rangeStr := fmt.Sprintf("%.2f MB - %.2f MB (range: %.2f MB)", minAlloc, maxAlloc, memRange)

	// Memory is stable if range is within 500MB
	isStable := memRange < 500

	return isStable, rangeStr
}

// analyzeGCPauses analyzes garbage collection pauses
func (c *Collector) analyzeGCPauses() (avgMs, maxMs float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.MemSnapshots) == 0 {
		return 0, 0
	}

	var total float64
	for _, snap := range c.MemSnapshots {
		total += snap.GCPauseMs
		if snap.GCPauseMs > maxMs {
			maxMs = snap.GCPauseMs
		}
	}

	avgMs = total / float64(len(c.MemSnapshots))
	return avgMs, maxMs
}

// analyzeCPUBound determines if the workload is CPU or I/O bound
func (c *Collector) analyzeCPUBound(avgTimeMs, maxTimeMs, throughputMBps float64) string {
	// If max time is significantly higher than avg, GC pauses are likely
	gcImpact := maxTimeMs / avgTimeMs

	var analysis string

	if gcImpact > 5 {
		analysis = fmt.Sprintf("GC Impact Detected: Max processing time (%.0fms) is %.1fx higher than average (%.0fms). "+
			"Consider reducing concurrent memory allocation.", maxTimeMs, gcImpact, avgTimeMs)
	} else if throughputMBps < 10 {
		analysis = "I/O Bound: Low throughput suggests disk I/O is the bottleneck. " +
			"Consider using faster storage (SSD/NVMe) or increasing workers if CPU < 100%."
	} else if throughputMBps > 50 {
		analysis = "CPU Bound: High throughput with 1 CPU indicates efficient processing. " +
			"Adding workers may cause context switching overhead."
	} else {
		analysis = "Balanced: System shows good balance between CPU and I/O. " +
			"Current worker count appears optimal."
	}

	return analysis
}

// generateRecommendations creates actionable recommendations
func (c *Collector) generateRecommendations(memStable bool, avgTimeMs, maxTimeMs, errorRate, throughputMBps float64) []string {
	var recs []string

	if !memStable {
		recs = append(recs, "Memory instability detected. Consider reducing buffer sizes or worker count.")
	}

	if maxTimeMs > avgTimeMs*3 {
		recs = append(recs, fmt.Sprintf("Processing time variance is high (avg: %.0fms, max: %.0fms). "+
			"This may indicate GC pauses or I/O contention.", avgTimeMs, maxTimeMs))
	}

	if errorRate > 1 {
		recs = append(recs, fmt.Sprintf("Error rate of %.2f%% detected. Review failed files for corruption.", errorRate))
	}

	if throughputMBps < 5 {
		recs = append(recs, "Low throughput detected. Consider profiling I/O operations.")
	}

	if len(recs) == 0 {
		recs = append(recs, "System performing optimally. No changes recommended.")
	}

	return recs
}

// ExportJSON writes the summary to a JSON file
func (c *Collector) ExportJSON(summary *Summary) error {
	filename := fmt.Sprintf("%s/summary.json", c.OutputDir)

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal summary: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write summary: %w", err)
	}

	log.Printf("[Analytics] Summary exported to %s", filename)
	return nil
}

// PrintSummary prints a formatted summary to stdout
func (c *Collector) PrintSummary(s *Summary) {
	fmt.Println()
	fmt.Println("╔═══════════════════════════════════════════════════════════════╗")
	fmt.Println("║              DEEP ANALYTICS REPORT                            ║")
	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")

	fmt.Println("║ EXECUTION                                                     ║")
	fmt.Printf("║   Duration:              %-38s║\n", s.Duration)
	fmt.Printf("║   Start:                 %-38s║\n", c.StartTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("║   End:                   %-38s║\n", c.EndTime.Format("2006-01-02 15:04:05"))

	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
	fmt.Println("║ FILE STATISTICS                                               ║")
	fmt.Printf("║   Total Files:           %-38d║\n", s.TotalFiles)
	fmt.Printf("║   Processed:             %-38d║\n", s.ProcessedFiles)
	fmt.Printf("║   Failed:                %-38d║\n", s.FailedFiles)
	fmt.Printf("║   Error Rate:            %-37.2f%%║\n", s.ErrorRate)

	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
	fmt.Println("║ SIZE METRICS                                                  ║")
	fmt.Printf("║   Input Size:            %-35.2f GB║\n", s.TotalInputGB)
	fmt.Printf("║   Output Size:           %-35.2f GB║\n", s.TotalOutputGB)
	fmt.Printf("║   Space Saved:           %-35.2f GB║\n", s.SpaceSavedGB)
	fmt.Printf("║   Compression:           %-37.1f%%║\n", s.SpaceSavingPct)
	fmt.Printf("║   Compression Ratio:     %-36.1fx║\n", s.CompressionRatio)

	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
	fmt.Println("║ PERFORMANCE METRICS                                           ║")
	fmt.Printf("║   Avg Time/Image:        %-35.2f ms║\n", s.AvgTimePerImageMs)
	fmt.Printf("║   Min Time/Image:        %-35.2f ms║\n", s.MinTimePerImageMs)
	fmt.Printf("║   Max Time/Image:        %-35.2f ms║\n", s.MaxTimePerImageMs)
	fmt.Printf("║   Throughput:            %-33.2f MB/s║\n", s.ThroughputMBps)
	fmt.Printf("║   Images/Second:         %-36.2f║\n", s.ImagesPerSecond)

	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
	fmt.Println("║ MEMORY ANALYSIS                                               ║")
	fmt.Printf("║   Peak Heap:             %-35.2f MB║\n", s.PeakMemoryMB)
	fmt.Printf("║   Peak System:           %-35.2f MB║\n", s.PeakSysMemMB)
	fmt.Printf("║   Memory Stable:         %-38v║\n", s.MemoryStable)
	fmt.Printf("║   Range:                 %-38s║\n", truncateString(s.MemoryRange, 38))

	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
	fmt.Println("║ GC ANALYSIS                                                   ║")
	fmt.Printf("║   Total GC Runs:         %-38d║\n", s.TotalGCRuns)
	fmt.Printf("║   Avg GC Pause:          %-35.2f ms║\n", s.AvgGCPauseMs)
	fmt.Printf("║   Max GC Pause:          %-35.2f ms║\n", s.MaxGCPauseMs)

	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
	fmt.Println("║ CPU/IO ANALYSIS                                               ║")
	fmt.Println("╠───────────────────────────────────────────────────────────────╣")
	printWrapped(s.CPUBoundAnalysis, 61, "║   ")

	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
	fmt.Println("║ RECOMMENDATIONS                                               ║")
	fmt.Println("╠───────────────────────────────────────────────────────────────╣")
	for i, rec := range s.Recommendations {
		printWrapped(fmt.Sprintf("%d. %s", i+1, rec), 61, "║   ")
	}

	fmt.Println("╚═══════════════════════════════════════════════════════════════╝")
}

// truncateString truncates a string to maxLen
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// printWrapped prints text wrapped to fit within the box
func printWrapped(text string, maxWidth int, prefix string) {
	words := splitWords(text)
	line := ""

	for _, word := range words {
		if len(line)+len(word)+1 > maxWidth {
			fmt.Printf("%s%-*s║\n", prefix, maxWidth, line)
			line = word
		} else {
			if line == "" {
				line = word
			} else {
				line += " " + word
			}
		}
	}

	if line != "" {
		fmt.Printf("%s%-*s║\n", prefix, maxWidth, line)
	}
}

// splitWords splits text into words
func splitWords(text string) []string {
	var words []string
	word := ""
	for _, r := range text {
		if r == ' ' || r == '\n' {
			if word != "" {
				words = append(words, word)
				word = ""
			}
		} else {
			word += string(r)
		}
	}
	if word != "" {
		words = append(words, word)
	}
	return words
}
