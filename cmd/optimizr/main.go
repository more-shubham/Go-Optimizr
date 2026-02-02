package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/more-shubham/go-optimizr/internal/processor"
)

const (
	// DefaultWorkers is optimized for 1 CPU core to minimize context switching
	DefaultWorkers = 3
	// DefaultQuality is the WebP quality setting (0-100)
	DefaultQuality = 80.0
)

var (
	version = "1.0.0"
)

func main() {
	// Parse command line flags
	inputDir := flag.String("input", "./images", "Input directory containing JPEG/PNG images")
	outputDir := flag.String("output", "./output", "Output directory for WebP images")
	maxWorkers := flag.Int("workers", DefaultWorkers, "Number of concurrent workers (default 3 for 1 CPU)")
	quality := flag.Float64("quality", DefaultQuality, "WebP quality (0-100, higher = better quality)")
	verbose := flag.Bool("verbose", false, "Enable verbose logging with per-image metrics")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	// Show version and exit
	if *showVersion {
		fmt.Printf("go-optimizr v%s\n", version)
		fmt.Println("High-performance JPEG/PNG to WebP converter")
		fmt.Println("Optimized for constrained environments (1 CPU, 8GB RAM)")
		os.Exit(0)
	}

	// Configure logging
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	// Print banner
	printBanner()

	// Validate input directory exists
	if _, err := os.Stat(*inputDir); os.IsNotExist(err) {
		log.Fatalf("Input directory does not exist: %s", *inputDir)
	}

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	// Log configuration
	log.Printf("Configuration:")
	log.Printf("  Input:   %s", *inputDir)
	log.Printf("  Output:  %s", *outputDir)
	log.Printf("  Workers: %d", *maxWorkers)
	log.Printf("  Quality: %.0f%%", *quality)
	log.Printf("  Verbose: %v", *verbose)

	// Setup graceful shutdown handler
	ctx := setupGracefulShutdown()

	// Create processor configuration
	config := &processor.Config{
		InputDir:  *inputDir,
		OutputDir: *outputDir,
		Quality:   float32(*quality),
		Verbose:   *verbose,
	}

	// Create and run the conversion engine
	engine := processor.NewEngine(*maxWorkers, config, ctx)
	engine.Run()

	log.Println("Conversion complete. Exiting.")
}

// setupGracefulShutdown configures SIGINT/SIGTERM handling
func setupGracefulShutdown() <-chan struct{} {
	ctx := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("\nReceived %v signal", sig)
		log.Println("Finishing current images and shutting down gracefully...")
		log.Println("(Press Ctrl+C again to force quit)")
		close(ctx)

		// If another signal is received, exit immediately
		sig = <-sigChan
		log.Printf("Received %v signal again. Force quitting.", sig)
		os.Exit(1)
	}()

	return ctx
}

// printBanner displays the application banner
func printBanner() {
	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════════════════════╗")
	fmt.Println("  ║            GO-OPTIMIZR v" + version + "                    ║")
	fmt.Println("  ║     High-Performance Image to WebP Converter      ║")
	fmt.Println("  ╚═══════════════════════════════════════════════════╝")
	fmt.Println()
}
