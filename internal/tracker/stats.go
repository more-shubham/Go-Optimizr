package tracker

import (
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

// Stats tracks processing statistics using atomic operations for thread safety
type Stats struct {
	ProcessedFiles  int64
	TotalBytesSaved int64
	FailedFiles     int64
	TotalFiles      int64
	StartTime       time.Time
}

// NewStats creates a new Stats instance
func NewStats() *Stats {
	return &Stats{
		StartTime: time.Now(),
	}
}

// SetTotalFiles sets the total number of files to process
func (s *Stats) SetTotalFiles(count int) {
	atomic.StoreInt64(&s.TotalFiles, int64(count))
}

// IncrementProcessed atomically increments the processed files counter
func (s *Stats) IncrementProcessed() int64 {
	return atomic.AddInt64(&s.ProcessedFiles, 1)
}

// IncrementFailed atomically increments the failed files counter
func (s *Stats) IncrementFailed() int64 {
	return atomic.AddInt64(&s.FailedFiles, 1)
}

// AddBytesSaved atomically adds to the total bytes saved
func (s *Stats) AddBytesSaved(bytes int64) int64 {
	return atomic.AddInt64(&s.TotalBytesSaved, bytes)
}

// GetProcessed returns the current processed files count
func (s *Stats) GetProcessed() int64 {
	return atomic.LoadInt64(&s.ProcessedFiles)
}

// GetFailed returns the current failed files count
func (s *Stats) GetFailed() int64 {
	return atomic.LoadInt64(&s.FailedFiles)
}

// GetBytesSaved returns the total bytes saved
func (s *Stats) GetBytesSaved() int64 {
	return atomic.LoadInt64(&s.TotalBytesSaved)
}

// GetTotal returns the total files count
func (s *Stats) GetTotal() int64 {
	return atomic.LoadInt64(&s.TotalFiles)
}

// GetMBSaved returns the total megabytes saved
func (s *Stats) GetMBSaved() float64 {
	return float64(s.GetBytesSaved()) / (1024 * 1024)
}

// GetGBSaved returns the total gigabytes saved
func (s *Stats) GetGBSaved() float64 {
	return s.GetMBSaved() / 1024
}

// GetElapsed returns the elapsed time since start
func (s *Stats) GetElapsed() time.Duration {
	return time.Since(s.StartTime)
}

// LogProgress logs progress every N files
func (s *Stats) LogProgress(interval int64) {
	processed := s.GetProcessed()
	if processed%interval == 0 && processed > 0 {
		savedMB := s.GetMBSaved()
		total := s.GetTotal()
		percent := float64(processed) / float64(total) * 100
		log.Printf("Progress: %d/%d (%.1f%%) files processed, %.2f MB saved",
			processed, total, percent, savedMB)
	}
}

// PrintFinalReport outputs the final processing statistics
func (s *Stats) PrintFinalReport() {
	elapsed := s.GetElapsed()
	processed := s.GetProcessed()
	failed := s.GetFailed()
	total := s.GetTotal()
	savedMB := s.GetMBSaved()
	savedGB := s.GetGBSaved()

	fmt.Println()
	fmt.Println(strings.Repeat("=", 55))
	fmt.Println("              GO-OPTIMIZR CONVERSION COMPLETE")
	fmt.Println(strings.Repeat("=", 55))
	fmt.Printf("  Total files found:      %d\n", total)
	fmt.Printf("  Successfully converted: %d\n", processed)
	fmt.Printf("  Failed:                 %d\n", failed)
	fmt.Println(strings.Repeat("-", 55))
	fmt.Printf("  Total space saved:      %.2f MB (%.2f GB)\n", savedMB, savedGB)
	fmt.Printf("  Total time:             %v\n", elapsed.Round(time.Millisecond))

	if processed > 0 {
		avgTime := elapsed / time.Duration(processed)
		throughput := float64(processed) / elapsed.Seconds()
		fmt.Println(strings.Repeat("-", 55))
		fmt.Printf("  Average time per image: %v\n", avgTime.Round(time.Millisecond))
		fmt.Printf("  Throughput:             %.2f images/sec\n", throughput)
	}

	fmt.Println(strings.Repeat("=", 55))
}

// Summary returns a summary string for logging
func (s *Stats) Summary() string {
	return fmt.Sprintf("Processed: %d, Failed: %d, Saved: %.2f MB",
		s.GetProcessed(), s.GetFailed(), s.GetMBSaved())
}
