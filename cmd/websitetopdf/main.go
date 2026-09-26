// Command websitetopdf saves every page of a website as a PDF, and every image as-is,
// into an asset directory.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"websitetopdf"
)

func main() {
	delay := flag.Duration("delay", websitetopdf.CrawlDelay, "maximum random delay between page requests; 0 disables it")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [-delay duration] <initial page> <asset directory>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}
	initialPage, assetDir := flag.Arg(0), flag.Arg(1)
	websitetopdf.CrawlDelay = *delay

	// stop crawling on ctrl+c
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := websitetopdf.SaveAllPDFableAssets(ctx, initialPage, assetDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		stop()
		os.Exit(1)
	}
}
