//go:build browser && distributed

// Independent distributed browser acceptance entry (B-4).
//
// "Distributed" means the running product stack reached through the Edge
// Gateway: the three nginx-served Vite artifacts proxy /api, /openapi.json and
// /mcp to gateway:8080. This entry never starts an embedded httpapi handler, a
// test store, or a Vite dev server, and it imports nothing from internal/.
//
// It reuses web/e2e/c1b-journey.ts — the same journey the embedded Chromium
// acceptance runs — so the release gate keeps exactly one user journey. The
// browser follows the existing runner convention: CHROMIUM_BIN or
// /usr/bin/chromium, headless, Chromium (no other browser is selected here).
//
// Run (one owner serialises the shared environment):
//
//	go test -tags "browser distributed" -count=1 -timeout=900s ./e2e -run '^TestBrowserDistributed' -v
//	make browser-distributed          # Makefile target owned by B
//
// Environment:
//
//	E2E_WEB               client site,        default http://127.0.0.1:18080
//	E2E_ADMIN             admin site,         default http://127.0.0.1:18081/admin/
//	E2E_PORTAL            portal site,        default http://127.0.0.1:18082/portal/
//	E2E_TELECOM_DATASET   frozen dataset,     default ../testdata/telecom-c1b-v1.json
//	E2E_TELECOM_SCENARIO  scenario id,        default conflict
//
// E2E_TELECOM_DATASET_ID  exact expected fixture ID, default telecom-c1b-v1
//
//	E2E_USERNAME/E2E_PASSWORD  seeded identity used by the journey
//	CHROMIUM_BIN          Chromium executable (optional)
package e2e_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const distributedSiteTimeout = 20 * time.Second

// TestBrowserDistributedClientArtifact verifies the client release artifact is
// actually served (not the dev server), that deep links, static assets, and API
// routing work through nginx, and then runs the shared C1-B journey.
func TestBrowserDistributedClientArtifact(t *testing.T) {
	web := siteURL("E2E_WEB", "http://127.0.0.1:18080")
	client := &http.Client{Timeout: distributedSiteTimeout}

	index := artifactIndex(t, client, web, "Client")
	reference := assetReference(t, web, index)
	assertArtifactPrefix(t, "Client", reference, "/assets/")
	assertAssetServed(t, client, absoluteAssetURL(t, web, reference), reference)
	for _, deepLink := range []string{"/login", "/cases/00000000-0000-0000-0000-000000000000"} {
		assertDeepLink(t, client, web+deepLink, reference)
	}
	assertGatewayRouted(t, client, web, "client", "")

	runDistributedJourney(t, web)
}

// TestBrowserDistributedArtifactsRoutes verifies that the admin and portal
// containers serve their own audience artifact with deep links, static assets,
// API routing, and an exact root redirect that never falls back to the client
// bundle.
func TestBrowserDistributedArtifactsRoutes(t *testing.T) {
	client := &http.Client{Timeout: distributedSiteTimeout}
	admin := siteURL("E2E_ADMIN", "http://127.0.0.1:18081/admin/")
	portal := siteURL("E2E_PORTAL", "http://127.0.0.1:18082/portal/")

	adminOrigin := originOf(t, admin)
	adminIndex := artifactIndex(t, client, admin, "Admin")
	adminReference := assetReference(t, admin, adminIndex)
	assertArtifactPrefix(t, "Admin", adminReference, "/admin/assets/")
	assertAssetServed(t, client, absoluteAssetURL(t, admin, adminReference), adminReference)
	assertDeepLink(t, client, adminOrigin+"/admin/settings", adminReference)
	assertRootRedirect(t, client, adminOrigin+"/", "/admin/")
	assertGatewayRouted(t, client, adminOrigin, "admin", "?audience=admin")

	portalOrigin := originOf(t, portal)
	portalIndex := artifactIndex(t, client, portal, "Portal")
	portalReference := assetReference(t, portal, portalIndex)
	assertArtifactPrefix(t, "Portal", portalReference, "/portal/assets/")
	assertAssetServed(t, client, absoluteAssetURL(t, portal, portalReference), portalReference)
	assertDeepLink(t, client, portalOrigin+"/portal/reference/deep/link", portalReference)
	assertRootRedirect(t, client, portalOrigin+"/", "/portal/")
	assertGatewayRouted(t, client, portalOrigin, "portal", "")
}

func siteURL(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		value = fallback
	}
	return strings.TrimRight(value, "/")
}

func originOf(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		t.Fatalf("%s is not an absolute site URL", raw)
	}
	return parsed.Scheme + "://" + parsed.Host
}

func fetch(t *testing.T, client *http.Client, url string) (int, string, http.Header) {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s failed: %v\nStart the distributed stack with `make infra-up && make apps-up`, or point the E2E_* URLs at a running environment.", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		t.Fatalf("reading %s failed: %v", url, err)
	}
	return response.StatusCode, string(body), response.Header
}

// artifactIndex requires the release artifact: a bundle reference that the
// dev server cannot produce, and no Vite dev-server markers. A dev server
// answering on the same URL must fail.
func artifactIndex(t *testing.T, client *http.Client, base, label string) string {
	t.Helper()
	status, body, _ := fetch(t, client, base+"/")
	if status != http.StatusOK {
		t.Fatalf("%s site %s returned HTTP %d", label, base, status)
	}
	if strings.Contains(body, "@vite/client") || strings.Contains(body, "/src/main.tsx") {
		t.Fatalf("%s site at %s is served by the Vite dev server, not by a release artifact", label, base)
	}
	if !strings.Contains(body, "/assets/") {
		t.Fatalf("%s site at %s did not reference a built asset directory", label, base)
	}
	return body
}

var assetPattern = regexp.MustCompile(`(?:src|href)="(/[^"]*assets/[^"]+\.(?:js|css))"`)

// assetReference returns the root-relative bundle reference exactly as the
// served document declares it (for example /admin/assets/index-abc.js).
func assetReference(t *testing.T, base, index string) string {
	t.Helper()
	match := assetPattern.FindStringSubmatch(index)
	if match == nil {
		t.Fatalf("no built asset reference found in the index served by %s", base)
	}
	return match[1]
}

// absoluteAssetURL resolves the root-relative reference against the site URL,
// so a site served below a path prefix cannot produce a double prefix.
func absoluteAssetURL(t *testing.T, base, reference string) *url.URL {
	t.Helper()
	baseURL, err := url.Parse(base)
	if err != nil || baseURL.Host == "" {
		t.Fatalf("%s is not an absolute site URL", base)
	}
	relative, err := url.Parse(reference)
	if err != nil {
		t.Fatalf("asset reference %q is not a valid URL reference: %v", reference, err)
	}
	resolved := baseURL.ResolveReference(relative)
	if resolved.Path != reference || resolved.Host != baseURL.Host {
		t.Fatalf("asset reference %q resolved to %s instead of staying under %s", reference, resolved, baseURL)
	}
	return resolved
}

// assertArtifactPrefix is the audience identity check: each site must publish
// its own base path bundle, so the admin or portal site can never present the
// client bundle (or the other site's bundle).
func assertArtifactPrefix(t *testing.T, label, reference, prefix string) {
	t.Helper()
	if !strings.HasPrefix(reference, prefix) {
		t.Fatalf("%s site served bundle %q instead of a %s bundle", label, reference, prefix)
	}
}

func assertAssetServed(t *testing.T, client *http.Client, assetURL *url.URL, reference string) {
	t.Helper()
	status, _, header := fetch(t, client, assetURL.String())
	if status != http.StatusOK {
		t.Fatalf("static asset %s returned HTTP %d", assetURL, status)
	}
	contentType := header.Get("Content-Type")
	want := "javascript"
	if strings.HasSuffix(reference, ".css") {
		want = "text/css"
	}
	if !strings.Contains(contentType, want) {
		t.Fatalf("static asset %s returned Content-Type %q; an SPA fallback answered instead of the %s file", assetURL, contentType, want)
	}
}

func assertDeepLink(t *testing.T, client *http.Client, url, reference string) {
	t.Helper()
	status, body, _ := fetch(t, client, url)
	if status != http.StatusOK {
		t.Fatalf("deep link %s returned HTTP %d", url, status)
	}
	if !strings.Contains(body, reference) {
		t.Fatalf("deep link %s did not fall back to the single-page bundle %s", url, reference)
	}
}

// assertRootRedirect requires the exact site-root redirect. A prefix match
// would also accept an unintended origin or the container port.
func assertRootRedirect(t *testing.T, client *http.Client, url, wantLocation string) {
	t.Helper()
	noRedirect := &http.Client{
		Timeout:       distributedSiteTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := noRedirect.Get(url)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusPermanentRedirect && response.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("%s returned HTTP %d instead of a redirect", url, response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != wantLocation {
		t.Fatalf("%s redirected to %q instead of the exact %q", url, location, wantLocation)
	}
}

// assertGatewayRouted proves the nginx site proxies the real API instead of
// answering locally: the audience-scoped session route must be reachable and
// authenticated reads must be refused with 401/403 rather than 404/502.
// /openapi.json must return the contract.
func assertGatewayRouted(t *testing.T, client *http.Client, origin, label, sessionQuery string) {
	t.Helper()
	status, _, _ := fetch(t, client, origin+"/api/session"+sessionQuery)
	if status != http.StatusOK && status != http.StatusUnauthorized {
		t.Fatalf("%s /api/session%s returned HTTP %d; the site is not proxying the Gateway", label, sessionQuery, status)
	}
	status, _, _ = fetch(t, client, origin+"/api/cases")
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("%s /api/cases returned HTTP %d without a session; the Gateway authorisation path is not in front", label, status)
	}
	status, body, _ := fetch(t, client, origin+"/openapi.json")
	if status != http.StatusOK || !strings.Contains(body, `"openapi"`) {
		t.Fatalf("%s /openapi.json returned HTTP %d without an OpenAPI document", label, status)
	}
}

// runDistributedJourney drives the shared Playwright journey against the
// running environment. It starts only the journey process — no handler, store,
// or Vite server is created here.
func runDistributedJourney(t *testing.T, web string) {
	t.Helper()
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("Bun is unavailable; install Bun 1.4.3 and run `make setup` in web/")
	}
	webRoot, err := filepath.Abs(filepath.Join("..", "web"))
	if err != nil {
		t.Fatal(err)
	}
	dataset := strings.TrimSpace(os.Getenv("E2E_TELECOM_DATASET"))
	if dataset == "" {
		dataset = filepath.Join("..", "testdata", "telecom-c1b-v1.json")
	}
	dataset, err = filepath.Abs(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dataset); err != nil {
		t.Fatalf("frozen dataset unavailable: %v", err)
	}
	scenario := strings.TrimSpace(os.Getenv("E2E_TELECOM_SCENARIO"))
	if scenario == "" {
		scenario = "conflict"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()
	script := exec.CommandContext(ctx, bun, "e2e/c1b-journey.ts")
	script.Dir = webRoot
	script.Env = append(os.Environ(),
		"E2E_WEB="+web,
		"E2E_TELECOM_DATASET="+dataset,
		"E2E_TELECOM_SCENARIO="+scenario,
	)
	out, err := script.CombinedOutput()
	t.Logf("distributed journey output:\n%s", out)
	if err != nil {
		if ctx.Err() != nil {
			t.Fatalf("distributed C1-B journey timed out: %s", out)
		}
		t.Fatalf("distributed C1-B journey failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "C1-B journey passed") {
		t.Fatalf("distributed journey did not report success:\n%s", out)
	}
}
