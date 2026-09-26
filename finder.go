package websitetopdf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// CrawlDelay is the maximum random delay between page requests. 0 disables it.
var CrawlDelay = 10 * time.Second

// ErrErrorPage is returned when the starting page responds with an HTTP error status.
var ErrErrorPage = errors.New("page responded with an error status")

// collectLinksJS returns the absolute href of every anchor after scripts have run.
const collectLinksJS = `Array.from(document.querySelectorAll('a[href]'), a => a.href)`

// collectImagesJS returns the absolute URL of every image the page loaded.
const collectImagesJS = `Array.from(document.images, img => img.currentSrc || img.src).filter(Boolean)`

// renderedPage is what a single page load in the browser produces.
type renderedPage struct {
	links  []string
	images []string
	pdf    []byte
}

// SaveAllPDFableAssets crawls every page on initialPage's site, saving each
// page as a PDF and each image as-is into outDir. Files are named after the
// SHA-256 hash of their URL: pages get a .pdf extension, and images keep the
// extension from their URL's path.
//
// It obeys each site's robots.txt for RobotsUserAgent: disallowed pages and images
// are skipped, and a Crawl-delay on initialPage's site is the minimum wait between
// pages. If initialPage itself is disallowed, it returns an error wrapping ErrDisallowed.
func SaveAllPDFableAssets(ctx context.Context, initialPage, outDir string) error {
	pageUrl, err := url.Parse(initialPage)
	if err != nil {
		return err
	}
	pageUrl.Fragment = ""
	start := pageUrl.String()

	// render pages in headless chrome so links added by javascript are found
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, chromedp.DefaultExecAllocatorOptions[:]...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	robots := newRobotsRules()

	// robots.txt may ask for a minimum delay between requests
	robotsDelay, err := robots.crawlDelay(ctx, pageUrl)
	if err != nil {
		return err
	}

	visited := map[string]bool{}
	savedImages := map[string]bool{}
	queue := []string{start}

	for len(queue) > 0 {
		pageLink := queue[0]
		queue = queue[1:]

		if visited[pageLink] {
			continue
		}
		visited[pageLink] = true

		linkUrl, err := url.Parse(pageLink)
		if err != nil {
			return err
		}
		if err := robots.check(ctx, linkUrl); err != nil {
			if pageLink == start {
				return err
			}
			slog.Info("skipping page", "url", pageLink, "reason", err)
			continue
		}

		delay := robotsDelay
		if CrawlDelay > 0 {
			delay = max(delay, rand.N(CrawlDelay))
		}
		if len(visited) > 1 && delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		slog.Debug("visiting", "url", pageLink)

		rendered, err := renderPage(browserCtx, pageLink)
		if err != nil {
			if pageLink == start {
				return err
			}
			if errors.Is(err, ErrErrorPage) { // nothing to save or follow
				slog.Debug("skipping error page", "url", pageLink, "error", err)
			} else {
				slog.Warn("failed to visit page", "url", pageLink, "error", err)
			}
			continue
		}

		pdfName := assetFileName(pageLink, ".pdf")
		if err := os.WriteFile(filepath.Join(outDir, pdfName), rendered.pdf, 0o644); err != nil {
			return err
		}
		slog.Info("saved file", "file", pdfName, "url", pageLink)

		// images may come from other domains (a cdn, for example)
		for _, src := range rendered.images {
			srcUrl, err := url.Parse(src)
			if err != nil { // srcUrl is nil
				slog.Warn("could not parse image url", "url", src, "error", err)
				continue
			}
			if (srcUrl.Scheme != "http" && srcUrl.Scheme != "https") || savedImages[src] {
				slog.Warn("could not parse image url", "scheme", srcUrl.Scheme, "already found", savedImages[src])
				continue
			}
			if err := robots.check(ctx, srcUrl); err != nil {
				slog.Info("skipping image", "url", src, "reason", err)
				savedImages[src] = true
				continue
			}
			if err := saveImage(ctx, src, outDir); err != nil {
				slog.Warn("failed to save image", "url", src, "error", err)
			}

			savedImages[src] = true
		}

		for _, href := range rendered.links {
			linkUrl, err := url.Parse(href)
			if err != nil || linkUrl.Host != pageUrl.Host { // do not leave website
				continue
			}
			linkUrl.Fragment = ""
			link := linkUrl.String()

			if !visited[link] {
				queue = append(queue, link)
			}
		}
	}

	return nil
}

// renderPage loads pageLink in the browser, collects its links and images, and prints it to PDF.
// It returns an error wrapping ErrErrorPage if the server responded with an HTTP error status.
func renderPage(ctx context.Context, pageLink string) (*renderedPage, error) {
	resp, err := chromedp.RunResponse(ctx, chromedp.Navigate(pageLink))
	if err != nil {
		return nil, err
	}
	if resp != nil && resp.Status >= 400 {
		return nil, fmt.Errorf("%w: %s returned %d", ErrErrorPage, pageLink, resp.Status)
	}

	rendered := &renderedPage{}

	err = chromedp.Run(ctx,
		chromedp.Evaluate(collectLinksJS, &rendered.links),
		chromedp.Evaluate(collectImagesJS, &rendered.images),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			rendered.pdf, _, err = page.PrintToPDF().
				WithPrintBackground(true). // background may include disposal information
				Do(ctx)
			return err
		}),
	)

	return rendered, err
}

// saveImage downloads src into outDir without converting it.
func saveImage(ctx context.Context, src, outDir string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}

	name := assetFileName(src, urlExt(src))
	f, err := os.Create(filepath.Join(outDir, name))
	if err != nil {
		return err
	}

	_, err = io.Copy(f, resp.Body)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	slog.Info("saved file", "file", name, "url", src)

	return nil
}

// assetFileName names an asset after the hex-encoded SHA-256 hash of its URL,
// followed by ext.
func assetFileName(link, ext string) string {
	sum := sha256.Sum256([]byte(link))
	return hex.EncodeToString(sum[:]) + ext
}

// urlExt returns the file extension of link's path, or "" if it has none.
func urlExt(link string) string {
	linkUrl, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return path.Ext(linkUrl.Path)
}
