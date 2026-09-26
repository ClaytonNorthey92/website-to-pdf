package websitetopdf

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"websitetopdf/internal/sitetest"
)

func TestMain(m *testing.M) {
	CrawlDelay = 0
	os.Exit(m.Run())
}

// assertSavedPDFs checks the saved PDFs against testdata/<test name>/<page>.pdf.
func assertSavedPDFs(t *testing.T, baseURL, outDir string, paths ...string) {
	t.Helper()
	sitetest.AssertSavedPDFs(t, baseURL, outDir, filepath.Join("testdata", t.Name()), paths...)
}

func TestSaveAllPDFableAssets_SinglePage(t *testing.T) {
	srv := sitetest.NewSite(t, map[string]string{
		"/": `<html><body>
			<a href="/a">A</a>
			<a href="/b">B</a>
			<a href="/c">C</a>
		</body></html>`,
		"/a": `<html><body>A</body></html>`,
		"/b": `<html><body>B</body></html>`,
		"/c": `<html><body>C</body></html>`,
	})

	outDir := t.TempDir()

	if err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSavedPDFs(t, srv.URL, outDir, "/", "/a", "/b", "/c")
}

func TestSaveAllPDFableAssets_ThreeDeep(t *testing.T) {
	srv := sitetest.NewSite(t, map[string]string{
		"/": `<html><body>
			<a href="/level1">Level 1</a>
			<a href="http://example.com/x">External</a>
		</body></html>`,
		"/level1": `<html><body><a href="/level2">Level 2</a></body></html>`,
		"/level2": `<html><body><a href="/level3">Level 3</a></body></html>`,
		"/level3": `<html><body><a href="/">Home</a></body></html>`,
	})

	outDir := t.TempDir()

	if err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// the external link is not saved
	assertSavedPDFs(t, srv.URL, outDir, "/", "/level1", "/level2", "/level3")
}

func TestSaveAllPDFableAssets_NoLinks(t *testing.T) {
	srv := sitetest.NewSite(t, map[string]string{
		"/": `<html><body><p>Nothing to see here.</p></body></html>`,
	})

	outDir := t.TempDir()

	if err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSavedPDFs(t, srv.URL, outDir, "/")
}

func TestSaveAllPDFableAssets_JavaScriptRenderedLinks(t *testing.T) {
	srv := sitetest.NewSite(t, map[string]string{
		"/": `<html><body>
			<div id="nav"></div>
			<script>
				const a = document.createElement('a');
				a.href = '/dynamic';
				a.textContent = 'Dynamic';
				document.getElementById('nav').appendChild(a);
			</script>
		</body></html>`,
		"/dynamic": `<html><body>Rendered by JS</body></html>`,
	})

	outDir := t.TempDir()

	if err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSavedPDFs(t, srv.URL, outDir, "/", "/dynamic")
}

func TestSaveAllPDFableAssets_SavesPDFsAndImages(t *testing.T) {
	var logo bytes.Buffer
	if err := png.Encode(&logo, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encoding test image: %v", err)
	}

	srv := sitetest.NewSite(t, map[string]string{
		"/": `<html><body>
			<img src="/logo.png">
			<a href="/about">About</a>
		</body></html>`,
		"/about":    `<html><body>About</body></html>`,
		"/logo.png": logo.String(),
	})
	outDir := t.TempDir()

	if err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, path := range []string{"/", "/about"} {
		data, err := os.ReadFile(filepath.Join(outDir, sitetest.HashedName(srv.URL+path, ".pdf")))
		if err != nil {
			t.Errorf("missing PDF for %s: %v", path, err)
			continue
		}
		if !bytes.HasPrefix(data, []byte("%PDF")) {
			t.Errorf("file for %s is not a PDF", path)
		}
	}

	img, err := os.ReadFile(filepath.Join(outDir, sitetest.HashedName(srv.URL+"/logo.png", ".png")))
	if err != nil {
		t.Fatalf("missing image: %v", err)
	}
	if !bytes.Equal(img, logo.Bytes()) {
		t.Errorf("saved image differs from the served image")
	}
}

func TestSaveAllPDFableAssets_SavesImageAsLink(t *testing.T) {
	var logo bytes.Buffer
	if err := png.Encode(&logo, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encoding test image: %v", err)
	}

	srv := sitetest.NewSite(t, map[string]string{
		"/": `<html><body>
			<a href="/logo.png">Logo Here!</a>
		</body></html>`,
		"/logo.png": logo.String(),
	})
	outDir := t.TempDir()

	if err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	img, err := os.ReadFile(filepath.Join(outDir, sitetest.HashedName(srv.URL+"/logo.png", ".png")))
	if err != nil {
		t.Fatalf("missing image: %v", err)
	}
	if !bytes.Equal(img, logo.Bytes()) {
		t.Errorf("saved image differs from the served image")
	}
}

func TestSaveAllPDFableAssets_SkipsErrorPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`<html><body><a href="/missing">Missing</a></body></html>`))
		case "/missing":
			// an error page with its own content, which must be ignored
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`<html><body>
				<img src="/error.png">
				<a href="/hidden">Hidden</a>
			</body></html>`))
		default:
			w.Write([]byte(`<html><body>should not be crawled</body></html>`))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	outDir := t.TempDir()

	if err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// only the start page is saved: no PDF of the error page, none of its images,
	// and none of the pages it links to
	assertSavedPDFs(t, srv.URL, outDir, "/")
}

func TestSaveAllPDFableAssets_ErrorStartPage(t *testing.T) {
	mux := http.NewServeMux()
	// no robots.txt: a 5xx there would disallow the whole site before the page is loaded
	mux.HandleFunc("/robots.txt", http.NotFound)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`<html><body><a href="/other">Other</a></body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	outDir := t.TempDir()

	err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir)
	if !errors.Is(err, ErrErrorPage) {
		t.Fatalf("expected ErrErrorPage, got: %v", err)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("reading output dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no saved files, got %d", len(entries))
	}
}

func TestSaveAllPDFableAssets_RobotsDisallowedPages(t *testing.T) {
	srv := sitetest.NewSite(t, map[string]string{
		"/robots.txt": "User-agent: *\nDisallow: /private\nAllow: /private/open\n",
		"/": `<html><body>
			<a href="/public">Public</a>
			<a href="/private">Private</a>
			<a href="/private/secret">Secret</a>
			<a href="/private/open">Open</a>
		</body></html>`,
		"/public":         `<html><body>Public</body></html>`,
		"/private":        `<html><body>Private</body></html>`,
		"/private/secret": `<html><body>Secret</body></html>`,
		"/private/open":   `<html><body>Open</body></html>`,
	})
	outDir := t.TempDir()

	if err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// the more specific Allow overrides the Disallow for /private/open
	assertSavedPDFs(t, srv.URL, outDir, "/", "/public", "/private/open")

	for _, path := range []string{"/private", "/private/secret"} {
		if srv.Requested(path) {
			t.Errorf("disallowed page %s was requested", path)
		}
	}
}

func TestSaveAllPDFableAssets_RobotsUserAgentGroup(t *testing.T) {
	srv := sitetest.NewSite(t, map[string]string{
		// the websitetopdf group applies instead of the * group, which would disallow everything
		"/robots.txt": "User-agent: *\nDisallow: /\n\nUser-agent: websitetopdf\nDisallow: /not-for-websitetopdf\n",
		"/": `<html><body>
			<a href="/a">A</a>
			<a href="/not-for-websitetopdf">Not for websitetopdf</a>
		</body></html>`,
		"/a":                    `<html><body>A</body></html>`,
		"/not-for-websitetopdf": `<html><body>Not for websitetopdf</body></html>`,
	})
	outDir := t.TempDir()

	if err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSavedPDFs(t, srv.URL, outDir, "/", "/a")

	if srv.Requested("/not-for-websitetopdf") {
		t.Errorf("disallowed page was requested")
	}
}

func TestSaveAllPDFableAssets_RobotsDisallowedImages(t *testing.T) {
	var logo bytes.Buffer
	if err := png.Encode(&logo, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encoding test image: %v", err)
	}

	srv := sitetest.NewSite(t, map[string]string{
		"/robots.txt": "User-agent: *\nDisallow: /private-images/\n",
		"/": `<html><body>
			<img src="/logo.png">
			<img src="/private-images/hidden.png">
		</body></html>`,
		"/logo.png":                  logo.String(),
		"/private-images/hidden.png": logo.String(),
	})
	outDir := t.TempDir()

	if err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// the page is saved with the allowed image, but not the disallowed one
	sitetest.AssertSavedFiles(t, outDir,
		sitetest.HashedName(srv.URL+"/", ".pdf"),
		sitetest.HashedName(srv.URL+"/logo.png", ".png"),
	)
}

func TestSaveAllPDFableAssets_RobotsDisallowedStartPage(t *testing.T) {
	srv := sitetest.NewSite(t, map[string]string{
		"/robots.txt": "User-agent: websitetopdf\nDisallow: /\n",
		"/":           `<html><body><a href="/a">A</a></body></html>`,
		"/a":          `<html><body>A</body></html>`,
	})
	outDir := t.TempDir()

	err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir)
	if !errors.Is(err, ErrDisallowed) {
		t.Fatalf("expected ErrDisallowed, got: %v", err)
	}

	sitetest.AssertSavedFiles(t, outDir)

	if requests := srv.Requests(); len(requests) != 1 || requests[0].Path != "/robots.txt" {
		t.Errorf("expected only robots.txt to be requested, got: %v", requests)
	}
}

func TestSaveAllPDFableAssets_RobotsServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>Home</body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	outDir := t.TempDir()

	// a server error for robots.txt disallows the whole site
	err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir)
	if !errors.Is(err, ErrDisallowed) {
		t.Fatalf("expected ErrDisallowed, got: %v", err)
	}

	sitetest.AssertSavedFiles(t, outDir)
}

func TestSaveAllPDFableAssets_RobotsCrawlDelay(t *testing.T) {
	srv := sitetest.NewSite(t, map[string]string{
		"/robots.txt": "User-agent: *\nCrawl-delay: 1\n",
		"/":           `<html><body><a href="/a">A</a></body></html>`,
		"/a":          `<html><body>A</body></html>`,
	})
	outDir := t.TempDir()

	if err := SaveAllPDFableAssets(context.Background(), srv.URL+"/", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sitetest.AssertCrawlDelay(t, srv, time.Second, "/", "/a")
}
