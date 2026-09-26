# websitetopdf

websitetopdf archives a website as files. Starting from one page, it crawls every
page on the same site, saves each page as a PDF, and saves each image the pages
display as-is.

It renders pages in headless Chromium, so links and content added by JavaScript
are captured just as a visitor would see them.

## How it works

- **Stays on the site.** Only links to the starting page's host are followed; links
  to other websites are not. Images are saved wherever they are hosted (a CDN, for
  example), since they are part of the page.
- **Saves pages as PDFs** with their backgrounds printed.
- **Saves images unchanged**, including images that are linked to directly.
- **Skips error pages.** A page that responds with an HTTP error status (a 404, for
  example) is not saved, and its links and images are not followed. If the
  *starting* page is an error, the crawl stops with an error.
- **Names files after a SHA-256 hash of their URL**, so any URL makes a safe,
  fixed-length file name. Pages get a `.pdf` extension; images keep the extension
  from their URL, e.g. `3f2a…9c1e.png`. Each saved file is logged along with its
  URL, so you can map names back to pages:

  ```
  INFO saved file file=cbe05d8f…3564ae.pdf url=https://example.com/about
  ```

- **Obeys `robots.txt`.** See [robots.txt](#robotstxt) below.
- **Crawls politely.** By default it waits a random 0–10 seconds between pages, so a
  large site can take a while. Use `-delay` to change this. If the site's
  `robots.txt` sets a `Crawl-delay`, the wait is never shorter than that.

## robots.txt

Before crawling, websitetopdf reads the site's `/robots.txt` and follows it as
described in [RFC 9309](https://www.rfc-editor.org/rfc/rfc9309):

- **Disallowed pages are never requested**, so they aren't saved and their links
  aren't followed. The most specific rule wins, so `Allow: /private/open` overrides
  `Disallow: /private` for that path.
- **Disallowed images aren't saved.** Each image host's own `robots.txt` applies,
  including a CDN's. Chromium still loads them to render the page, like a visitor's
  browser would, so they appear inside the page PDFs.
- **Rules for `User-agent: websitetopdf` take precedence** over the `*` rules, so a
  site can give this crawler its own rules.
- **`Crawl-delay`** on the starting page's site sets the minimum wait between pages,
  even with `-delay 0`. A large `Crawl-delay` makes a crawl slow; it's what the site asked for.
- **If the starting page is disallowed**, the crawl stops with the error
  `disallowed by robots.txt`, and nothing is saved.
- **A missing `robots.txt`** (any 4xx response) allows everything.
- **A server error** (5xx) for `robots.txt` disallows the whole site.
- **If `robots.txt` can't be fetched at all** (a network error, for example), the
  crawl stops with that error.

## Usage

```
websitetopdf [-delay duration] <initial page> <asset directory>
```

| Argument | Description |
| --- | --- |
| `initial page` | The URL to start crawling from. |
| `asset directory` | Where to save PDFs and images. It must already exist. |
| `-delay` | Maximum random delay between page requests, e.g. `5s` or `1m`. Default `10s`; `0` disables it. |

If the crawl fails, the error is printed and the program exits with status 1.

You need [Go](https://go.dev/dl/) and Chromium or Google Chrome installed.
Build the CLI from the project root with:

```sh
go build ./cmd/websitetopdf
```

## Example: archiving the City of Madison website with Docker

The Dockerfile provides Go and Chromium, so there's nothing else to install. It
doesn't contain the source code; you mount the project into the container at `/git`.

From the project root:

```sh
# build the image
docker build -t websitetopdf .

# create the output directory; the crawler doesn't create it
mkdir -p cityofmadison

# crawl the site
docker run --rm \
  --shm-size=1g \
  -v "$PWD":/git \
  websitetopdf \
  go run ./cmd/websitetopdf https://www.cityofmadison.com/ /git/cityofmadison
```

`--shm-size=1g` gives Chromium more shared memory than Docker's 64 MB default,
which it can run out of on large pages.

The PDFs and images end up in `cityofmadison/` on your machine. Press Ctrl+C to
stop the crawl early; files saved so far are kept.

A government site can have thousands of pages. Keep the default delay (or a longer
one) so you don't overload the server, and check the site's terms of use before
archiving it. Pages the site's `robots.txt` disallows are skipped and logged:

```
INFO skipping page url=https://www.cityofmadison.com/… reason="disallowed by robots.txt: https://www.cityofmadison.com/…"
```

## Using it as a Go library

```go
import "websitetopdf"

// optional: change the maximum delay between pages (default 10s)
websitetopdf.CrawlDelay = 5 * time.Second

err := websitetopdf.SaveAllPDFableAssets(ctx, "https://example.com/", "out")
switch {
case errors.Is(err, websitetopdf.ErrErrorPage):
	// the starting page responded with an HTTP error status
case errors.Is(err, websitetopdf.ErrDisallowed):
	// robots.txt disallows the starting page
}
```

## Development

Run the tests with:

```sh
go test ./...
```

The tests need Chromium and run both the library and the CLI against local test
websites. They compare the saved PDFs byte for byte with the expected PDFs in
`testdata/`, ignoring the parts that change on every run (timestamps, the Chromium
version and the test server's port). Those PDFs were made with the Chromium in the
Dockerfile's image (version 152). A different Chromium version or different fonts
can change the output, so run the tests in that image:

```sh
docker run --rm -v "$PWD":/git websitetopdf go test ./...
```

## License

[MIT](LICENSE) © 2026 Clayton Northey
