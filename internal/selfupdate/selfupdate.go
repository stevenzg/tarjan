// Package selfupdate checks GitHub Releases for a newer tarjan and replaces the
// running binary in place. It backs the `tarjan upgrade` command and the
// "update available" notice `tarjan up` prints on startup.
//
// tarjan is distributed from a private repository, so every GitHub request is
// authenticated with a token resolved from GH_TOKEN / GITHUB_TOKEN, falling
// back to the gh CLI (`gh auth token`). Release assets are fetched through the
// API asset endpoint (not the public download URL, which 404s for private
// repos); GitHub redirects that to a signed CDN URL, and net/http drops the
// Authorization header on the cross-domain hop so the CDN accepts it.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo is the GitHub owner/name releases are pulled from.
const Repo = "stevenzg/tarjan"

// maxDownload caps how much we read from a release asset, a sanity bound well
// above any real tarjan archive.
const maxDownload = 64 << 20 // 64 MiB

// ErrNoToken indicates no GitHub credential was found for this private repo.
var ErrNoToken = fmt.Errorf("no GitHub token found: set GH_TOKEN or GITHUB_TOKEN, or run `gh auth login`")

var (
	tokenOnce  sync.Once
	tokenCache string
)

// authToken resolves a GitHub token from the environment, falling back to the
// gh CLI. Empty means unauthenticated (which 404s against a private repo). The
// result is memoized for the process: a single `tarjan upgrade` calls both
// Latest and Apply, and the credential does not change between them, so the
// (potentially subprocess-spawning) gh lookup runs at most once.
func authToken(ctx context.Context) string {
	tokenOnce.Do(func() { tokenCache = resolveToken(ctx) })
	return tokenCache
}

func resolveToken(ctx context.Context) string {
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(tctx, "gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func newRequest(ctx context.Context, url, accept, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// notFoundHint turns GitHub's opaque 404 (returned for a private repo when the
// caller can't see it) into actionable guidance.
func notFoundHint(status string, token string) error {
	if token == "" {
		return fmt.Errorf("%s — tarjan is a private repo: %w", status, ErrNoToken)
	}
	return fmt.Errorf("%s — the token cannot access %s (check its repo scope)", status, Repo)
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"` // API asset URL: .../releases/assets/{id}
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

// Latest returns the latest published release tag (e.g. "v0.4.0").
func Latest(ctx context.Context) (string, error) {
	token := authToken(ctx)
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", Repo)
	rel, err := fetchRelease(ctx, url, token)
	if err != nil {
		return "", err
	}
	return rel.TagName, nil
}

func fetchRelease(ctx context.Context, url, token string) (*ghRelease, error) {
	req, err := newRequest(ctx, url, "application/vnd.github+json", token)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, notFoundHint(resp.Status, token)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github returned %s", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("no tag_name in release response")
	}
	return &rel, nil
}

// IsNewer reports whether latest is a higher semantic version than current. A
// current that is a dev build or otherwise unparseable returns false, so local
// builds are never told they are out of date.
func IsNewer(current, latest string) bool {
	c, ok := parseSemver(current)
	if !ok {
		return false
	}
	l, ok := parseSemver(latest)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// Parseable reports whether v looks like a release version (not "dev").
func Parseable(v string) bool {
	_, ok := parseSemver(v)
	return ok
}

// parseSemver parses "vX.Y.Z" / "X.Y.Z" (ignoring any -prerelease/+build suffix)
// into its numeric components.
func parseSemver(s string) ([3]int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

// assetName is the release archive for the running platform, matching the
// goreleaser name_template (windows ships a .zip, everything else a .tar.gz).
func assetName(version string) (name string, isZip bool) {
	ver := strings.TrimPrefix(version, "v")
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("tarjan_%s_%s_%s.zip", ver, runtime.GOOS, runtime.GOARCH), true
	}
	return fmt.Sprintf("tarjan_%s_%s_%s.tar.gz", ver, runtime.GOOS, runtime.GOARCH), false
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "tarjan.exe"
	}
	return "tarjan"
}

// Apply downloads the given tag's binary for this platform, verifies its
// checksum, and atomically replaces the running executable. It returns the path
// that was replaced.
func Apply(ctx context.Context, tag string) (string, error) {
	token := authToken(ctx)
	asset, isZip := assetName(tag)

	relURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", Repo, tag)
	rel, err := fetchRelease(ctx, relURL, token)
	if err != nil {
		return "", err
	}
	assetURL := findAsset(rel, asset)
	if assetURL == "" {
		return "", fmt.Errorf("release %s has no asset %s", tag, asset)
	}
	sumsURL := findAsset(rel, "checksums.txt")
	if sumsURL == "" {
		return "", fmt.Errorf("release %s has no checksums.txt", tag)
	}

	archive, err := downloadAsset(ctx, assetURL, token)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", asset, err)
	}
	sums, err := downloadAsset(ctx, sumsURL, token)
	if err != nil {
		return "", fmt.Errorf("downloading checksums: %w", err)
	}
	want := checksumFor(string(sums), asset)
	if want == "" {
		return "", fmt.Errorf("no checksum listed for %s", asset)
	}
	if got := sha256hex(archive); !strings.EqualFold(got, want) {
		return "", fmt.Errorf("checksum mismatch for %s (want %s, got %s)", asset, want, got)
	}

	bin, err := extractBinary(archive, isZip)
	if err != nil {
		return "", err
	}

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := replaceExecutable(exe, bin); err != nil {
		return "", err
	}
	return exe, nil
}

func findAsset(rel *ghRelease, name string) string {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

// downloadAsset fetches a release asset's bytes through the API asset endpoint.
// Accept: application/octet-stream makes GitHub redirect to a signed CDN URL;
// net/http strips the Authorization header on that cross-domain hop, which the
// CDN requires.
func downloadAsset(ctx context.Context, apiURL, token string) ([]byte, error) {
	req, err := newRequest(ctx, apiURL, "application/octet-stream", token)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("returned %s", resp.Status)
	}
	return readCapped(resp.Body)
}

// readCapped reads all of r but refuses to silently truncate: it reads up to
// maxDownload+1 bytes and returns an explicit error if the limit is exceeded,
// rather than returning a truncated blob that later surfaces as a misleading
// "checksum mismatch" — or, past the checksum, an installed corrupt binary.
func readCapped(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxDownload {
		return nil, fmt.Errorf("asset exceeds size limit of %d bytes", maxDownload)
	}
	return b, nil
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// checksumFor returns the hex sum listed for asset in a checksums.txt body
// (lines of "<sha256>  <filename>").
func checksumFor(sums, asset string) string {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			return fields[0]
		}
	}
	return ""
}

// extractBinary pulls the tarjan binary out of a release archive held in memory.
func extractBinary(archive []byte, isZip bool) ([]byte, error) {
	want := binaryName()
	if isZip {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) != want {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = rc.Close() }()
			return readCapped(rc)
		}
		return nil, fmt.Errorf("%s not found in archive", want)
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == want {
			return readCapped(tr)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", want)
}
