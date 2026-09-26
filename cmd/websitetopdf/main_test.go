package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"websitetopdf"
	"websitetopdf/internal/sitetest"
)

// binary is the CLI built by TestMain.
var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "websitetopdf-cli")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	binary = filepath.Join(dir, "websitetopdf")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building CLI: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// runCLI runs the CLI on initialPage and outDir without a crawl delay,
// returning its combined output and exit code.
func runCLI(t *testing.T, initialPage, outDir string) (string, int) {
	t.Helper()

	out, err := exec.Command(binary, "-delay", "0", initialPage, outDir).CombinedOutput()

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(out), exitErr.ExitCode()
	}
	if err != nil {
		t.Fatalf("running CLI: %v", err)
	}

	return string(out), 0
}

// runCLISuccessfully runs the CLI and fails the test unless it exits with 0.
func runCLISuccessfully(t *testing.T, initialPage, outDir string) {
	t.Helper()

	if out, code := runCLI(t, initialPage, outDir); code != 0 {
		t.Fatalf("CLI exited with %d:\n%s", code, out)
	}
}

// runCLIExpectingError runs the CLI and fails the test unless it exits with 1 and prints want.
func runCLIExpectingError(t *testing.T, initialPage, outDir string, want error) {
	t.Helper()

	out, code := runCLI(t, initialPage, outDir)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, want.Error()) {
		t.Errorf("expected %q to be printed, got:\n%s", want, out)
	}
}

// assertSavedPDFs checks the saved PDFs against the library tests' testdata/<test name>/<page>.pdf,
// as the CLI must save the same PDFs as the library.
func assertSavedPDFs(t *testing.T, baseURL, outDir, testName string, paths ...string) {
	t.Helper()
	sitetest.AssertSavedPDFs(t, baseURL, outDir, filepath.Join("..", "..", "testdata", testName), paths...)
}

func TestCLI_SinglePage(t *testing.T) {
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

	runCLISuccessfully(t, srv.URL+"/", outDir)

	assertSavedPDFs(t, srv.URL, outDir, "TestSaveAllPDFableAssets_SinglePage", "/", "/a", "/b", "/c")
}

func TestCLI_ThreeDeep(t *testing.T) {
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

	runCLISuccessfully(t, srv.URL+"/", outDir)

	// the external link is not saved
	assertSavedPDFs(t, srv.URL, outDir, "TestSaveAllPDFableAssets_ThreeDeep", "/", "/level1", "/level2", "/level3")
}

func TestCLI_NoLinks(t *testing.T) {
	srv := sitetest.NewSite(t, map[string]string{
		"/": `<html><body><p>Nothing to see here.</p></body></html>`,
	})

	outDir := t.TempDir()

	runCLISuccessfully(t, srv.URL+"/", outDir)

	assertSavedPDFs(t, srv.URL, outDir, "TestSaveAllPDFableAssets_NoLinks", "/")
}

func TestCLI_JavaScriptRenderedLinks(t *testing.T) {
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

	runCLISuccessfully(t, srv.URL+"/", outDir)

	assertSavedPDFs(t, srv.URL, outDir, "TestSaveAllPDFableAssets_JavaScriptRenderedLinks", "/", "/dynamic")
}

func TestCLI_SavesPDFsAndImages(t *testing.T) {
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

	runCLISuccessfully(t, srv.URL+"/", outDir)

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

func TestCLI_SavesImageAsLink(t *testing.T) {
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

	runCLISuccessfully(t, srv.URL+"/", outDir)

	img, err := os.ReadFile(filepath.Join(outDir, sitetest.HashedName(srv.URL+"/logo.png", ".png")))
	if err != nil {
		t.Fatalf("missing image: %v", err)
	}
	if !bytes.Equal(img, logo.Bytes()) {
		t.Errorf("saved image differs from the served image")
	}
}

func TestCLI_SkipsErrorPages(t *testing.T) {
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

	runCLISuccessfully(t, srv.URL+"/", outDir)

	// only the start page is saved: no PDF of the error page, none of its images,
	// and none of the pages it links to
	assertSavedPDFs(t, srv.URL, outDir, "TestSaveAllPDFableAssets_SkipsErrorPages", "/")
}

func TestCLI_ErrorStartPage(t *testing.T) {
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

	runCLIExpectingError(t, srv.URL+"/", outDir, websitetopdf.ErrErrorPage)

	sitetest.AssertSavedFiles(t, outDir)
}

func TestCLI_RobotsDisallowedPages(t *testing.T) {
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

	runCLISuccessfully(t, srv.URL+"/", outDir)

	// the more specific Allow overrides the Disallow for /private/open
	assertSavedPDFs(t, srv.URL, outDir, "TestSaveAllPDFableAssets_RobotsDisallowedPages", "/", "/public", "/private/open")

	for _, path := range []string{"/private", "/private/secret"} {
		if srv.Requested(path) {
			t.Errorf("disallowed page %s was requested", path)
		}
	}
}

func TestCLI_RobotsUserAgentGroup(t *testing.T) {
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

	runCLISuccessfully(t, srv.URL+"/", outDir)

	assertSavedPDFs(t, srv.URL, outDir, "TestSaveAllPDFableAssets_RobotsUserAgentGroup", "/", "/a")

	if srv.Requested("/not-for-websitetopdf") {
		t.Errorf("disallowed page was requested")
	}
}

func TestCLI_RobotsDisallowedImages(t *testing.T) {
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

	runCLISuccessfully(t, srv.URL+"/", outDir)

	// the page is saved with the allowed image, but not the disallowed one
	sitetest.AssertSavedFiles(t, outDir,
		sitetest.HashedName(srv.URL+"/", ".pdf"),
		sitetest.HashedName(srv.URL+"/logo.png", ".png"),
	)
}

func TestCLI_RobotsDisallowedStartPage(t *testing.T) {
	srv := sitetest.NewSite(t, map[string]string{
		"/robots.txt": "User-agent: websitetopdf\nDisallow: /\n",
		"/":           `<html><body><a href="/a">A</a></body></html>`,
		"/a":          `<html><body>A</body></html>`,
	})
	outDir := t.TempDir()

	runCLIExpectingError(t, srv.URL+"/", outDir, websitetopdf.ErrDisallowed)

	sitetest.AssertSavedFiles(t, outDir)

	if requests := srv.Requests(); len(requests) != 1 || requests[0].Path != "/robots.txt" {
		t.Errorf("expected only robots.txt to be requested, got: %v", requests)
	}
}

func TestCLI_RobotsServerError(t *testing.T) {
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
	runCLIExpectingError(t, srv.URL+"/", outDir, websitetopdf.ErrDisallowed)

	sitetest.AssertSavedFiles(t, outDir)
}

func TestCLI_RobotsCrawlDelay(t *testing.T) {
	srv := sitetest.NewSite(t, map[string]string{
		"/robots.txt": "User-agent: *\nCrawl-delay: 1\n",
		"/":           `<html><body><a href="/a">A</a></body></html>`,
		"/a":          `<html><body>A</body></html>`,
	})
	outDir := t.TempDir()

	// robots.txt's Crawl-delay applies even with -delay 0
	runCLISuccessfully(t, srv.URL+"/", outDir)

	sitetest.AssertCrawlDelay(t, srv, time.Second, "/", "/a")
}
