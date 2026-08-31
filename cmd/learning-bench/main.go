// learning-bench is the offline validation tool for the learning layer
// (topic 4, stage 5). Two modes, no external services:
//
//	simulate  — synthetic persona scripts → assert mastery curve shapes,
//	            hysteresis absorption, and reconcile migration losslessness.
//	replay    — 80/20 time split of an event stream → HitRate@K / MRR
//	            against random and popularity baselines.
//
// Both modes exit non-zero on failure.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	mode := flag.String("mode", "", "simulate | replay | verify | walk")
	flag.StringVar(&weightOverrides, "w", "", "replay: weight overrides K=V,K=V (offline tuning sweep only)")
	flag.BoolVar(&refold, "refold", false, "replay: recompute weights from current vars instead of frozen row values (tuning mode)")
	verifyOut := flag.String("verify-out", "", "verify: write JSON artifact to this path")
	script := flag.String("script", "", "simulate: path to persona JSON script")
	export := flag.String("export", "", "replay: path to exported profile JSON file")
	topK := flag.String("topk", "5,10", "replay: comma-separated K values (default 5,10)")
	verbose := flag.Bool("verbose", false, "print per-step details")
	flag.Parse()

	switch *mode {
	case "simulate":
		if *script == "" {
			fatal("simulate mode requires -script=<path>")
		}
		if err := runSimulate(*script, *verbose); err != nil {
			fatal(fmt.Sprintf("simulate failed: %v", err))
		}
		fmt.Println("✓ simulate: all assertions passed")
	case "replay":
		if *export == "" {
			fatal("replay mode requires -export=<path>")
		}
		if err := runReplay(*export, *topK); err != nil {
			fatal(fmt.Sprintf("replay failed: %v", err))
		}
	case "verify":
		if err := runVerify(*verifyOut); err != nil {
			fatal(fmt.Sprintf("verify failed: %v", err))
		}
		fmt.Println("✓ verify: all layer checks passed")
	case "walk":
		if err := runWalk(); err != nil {
			fatal(fmt.Sprintf("walk failed: %v", err))
		}
		fmt.Println("✓ walk: simulated learner completed the full chain")
	default:
		flag.Usage()
		os.Exit(1)
	}
}

func fatal(msg string) {
	fmt.Fprintf(os.Stderr, "learning-bench: %s\n", msg)
	os.Exit(1)
}
