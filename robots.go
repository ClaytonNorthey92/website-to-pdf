package websitetopdf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/temoto/robotstxt"
)

// RobotsUserAgent is the user agent the crawler matches against robots.txt rules.
// Sites can address it with "User-agent: websitetopdf"; otherwise the "*" rules apply.
const RobotsUserAgent = "websitetopdf"

// ErrDisallowed is returned when robots.txt disallows crawling the starting page.
var ErrDisallowed = errors.New("disallowed by robots.txt")

// maxRobotsSize is how much of a robots.txt is read; RFC 9309 requires at least 500 KiB.
const maxRobotsSize = 500 << 10

// robotsRules fetches each site's robots.txt once and answers whether URLs on it may be crawled.
type robotsRules struct {
	sites map[string]robotsResult // keyed by scheme://host
}

// robotsResult is a site's parsed robots.txt, or why it couldn't be fetched or parsed.
type robotsResult struct {
	data *robotstxt.RobotsData
	err  error
}

func newRobotsRules() *robotsRules {
	return &robotsRules{sites: map[string]robotsResult{}}
}

// check returns nil if robots.txt lets the crawler fetch link, an error wrapping
// ErrDisallowed if it doesn't, or the error that stopped robots.txt being read.
func (r *robotsRules) check(ctx context.Context, link *url.URL) error {
	data, err := r.site(ctx, link)
	if err != nil {
		return err
	}
	// TestAgent, unlike FindGroup, honors the full disallow of a robots.txt that returned a 5xx
	if !data.TestAgent(link.RequestURI(), RobotsUserAgent) {
		return fmt.Errorf("%w: %s", ErrDisallowed, link)
	}
	return nil
}

// crawlDelay returns the Crawl-delay robots.txt asks for on link's site, or 0 if there is none.
func (r *robotsRules) crawlDelay(ctx context.Context, link *url.URL) (time.Duration, error) {
	data, err := r.site(ctx, link)
	if err != nil {
		return 0, err
	}
	return data.FindGroup(RobotsUserAgent).CrawlDelay, nil
}

// site returns the parsed robots.txt for link's site, fetching it the first time.
// Failures are remembered too, so an unreachable site isn't asked again.
func (r *robotsRules) site(ctx context.Context, link *url.URL) (*robotstxt.RobotsData, error) {
	key := link.Scheme + "://" + link.Host
	result, ok := r.sites[key]
	if !ok {
		result.data, result.err = fetchRobots(ctx, key+"/robots.txt")
		r.sites[key] = result
	}
	return result.data, result.err
}

// fetchRobots fetches and parses the robots.txt at robotsUrl. A missing robots.txt
// (any 4xx) allows everything, and a server error (5xx) disallows everything, as
// RFC 9309 requires.
func fetchRobots(ctx context.Context, robotsUrl string) (*robotstxt.RobotsData, error) {

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsUrl, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", robotsUrl, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRobotsSize))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", robotsUrl, err)
	}

	data, err := robotstxt.FromStatusAndBytes(resp.StatusCode, body)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", robotsUrl, err)
	}

	return data, nil
}
