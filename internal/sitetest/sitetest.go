// Package sitetest holds helpers shared by the library and CLI tests: a test
// website server and checks on the files a crawl saves.
package sitetest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Site is a test website that records the requests it receives.
type Site struct {
	*httptest.Server

	mtx      sync.Mutex
	requests []Request
}

// Request is one request a Site received.
type Request struct {
	Path string
	At   time.Time
}

// Requests returns the requests the site has received, in order.
func (s *Site) Requests() []Request {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return slices.Clone(s.requests)
}

// Requested reports whether the site received a request for path.
func (s *Site) Requested(path string) bool {
	return slices.ContainsFunc(s.Requests(), func(r Request) bool { return r.Path == path })
}

// NewSite serves each path in pages with a sniffed content type and 404s everything else.
func NewSite(t *testing.T, pages map[string]string) *Site {
	t.Helper()

	site := &Site{}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		site.mtx.Lock()
		site.requests = append(site.requests, Request{Path: r.URL.Path, At: time.Now()})
		site.mtx.Unlock()

		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType([]byte(body)))
		w.Write([]byte(body))
	})

	site.Server = httptest.NewServer(mux)
	t.Cleanup(site.Close)

	return site
}

// HashedName is the file name an asset at link is saved under: its URL's
// hex-encoded SHA-256 hash followed by ext.
func HashedName(link, ext string) string {
	sum := sha256.Sum256([]byte(link))
	return hex.EncodeToString(sum[:]) + ext
}

// pdfVolatile matches the parts of a PDF that change between runs of the same page,
// each paired with the fixed text it is replaced with before comparing.
var pdfVolatile = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`/CreationDate \(D:[^)]*\)`), `/CreationDate ()`},
	{regexp.MustCompile(`/ModDate \(D:[^)]*\)`), `/ModDate ()`},
	// the chromium version; these strings may contain escaped parentheses
	{regexp.MustCompile(`/Producer \((?:\\.|[^\\)])*\)`), `/Producer ()`},
	{regexp.MustCompile(`/Creator \((?:\\.|[^\\)])*\)`), `/Creator ()`},
	// the httptest server's random port, in the title and link annotations
	{regexp.MustCompile(`127\.0\.0\.1:\d+`), `SERVER`},
	// the port's length can vary, shifting every byte offset
	{regexp.MustCompile(`(?s)\nxref\n.*?trailer`), "\nxref\ntrailer"},
	{regexp.MustCompile(`startxref\s+\d+`), `startxref`},
}

// normalizePDF strips the parts of a PDF that change between runs of the same page.
func normalizePDF(data []byte) []byte {
	for _, v := range pdfVolatile {
		data = v.re.ReplaceAll(data, []byte(v.repl))
	}
	return data
}

// AssertSavedFiles checks that outDir holds exactly the files named, and nothing else.
func AssertSavedFiles(t *testing.T, outDir string, names ...string) {
	t.Helper()

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("reading output dir: %v", err)
	}

	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}

	got = slices.Sorted(slices.Values(got))
	want := slices.Sorted(slices.Values(names))

	if !slices.Equal(got, want) {
		t.Fatalf("saved files mismatch\n got: %v\nwant: %v", got, want)
	}
}

// AssertCrawlDelay checks that the site's first request for to came at least delay after its
// first request for from.
func AssertCrawlDelay(t *testing.T, site *Site, delay time.Duration, from, to string) {
	t.Helper()

	requests := site.Requests()
	first := func(path string) time.Time {
		i := slices.IndexFunc(requests, func(r Request) bool { return r.Path == path })
		if i < 0 {
			t.Fatalf("%s was never requested", path)
		}
		return requests[i].At
	}

	if gap := first(to).Sub(first(from)); gap < delay {
		t.Errorf("%s was requested %v after %s, want at least %v", to, gap, from, delay)
	}
}

// AssertSavedPDFs checks that outDir holds exactly one PDF per page path on the site at
// baseURL, and that each matches expectedDir/<page>.pdf once volatile data is stripped from both.
func AssertSavedPDFs(t *testing.T, baseURL, outDir, expectedDir string, paths ...string) {
	t.Helper()

	var names []string
	for _, path := range paths {
		names = append(names, HashedName(baseURL+path, ".pdf"))
	}
	AssertSavedFiles(t, outDir, names...)

	for _, path := range paths {
		saved, err := os.ReadFile(filepath.Join(outDir, HashedName(baseURL+path, ".pdf")))
		if err != nil {
			t.Fatalf("reading PDF for %s: %v", path, err)
		}

		name := strings.Trim(path, "/")
		if name == "" {
			name = "index"
		}

		expected, err := os.ReadFile(filepath.Join(expectedDir, name+".pdf"))
		if err != nil {
			t.Fatalf("reading expected PDF: %v", err)
		}

		assertSameBytes(t, path, normalizePDF(saved), normalizePDF(expected))
	}
}

// assertSameBytes reports the first offset where got and want differ, with context around it.
func assertSameBytes(t *testing.T, path string, got, want []byte) {
	t.Helper()

	if bytes.Equal(got, want) {
		return
	}

	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}

	around := func(b []byte) []byte {
		return b[max(0, i-32):min(len(b), i+32)]
	}

	t.Errorf("PDF for %s differs at byte %d (got %d bytes, want %d)\n got: %q\nwant: %q",
		path, i, len(got), len(want), around(got), around(want))
}
