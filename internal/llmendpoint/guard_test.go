package llmendpoint

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestForbiddenIPReasonClassifiesDangerousAddresses(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"loopback-v4", "127.0.0.1", "loopback address"},
		{"loopback-v6", "::1", "loopback address"},
		{"loopback-v4-in-v6", "::ffff:127.0.0.1", "loopback address"},
		{"private-v4", "192.168.1.10", "private address"},
		{"private-v6-ula", "fd00::1", "private address"},
		{"metadata-link-local", "169.254.169.254", "link-local address"},
		{"unspecified-v4", "0.0.0.0", "unspecified address"},
		{"unspecified-v6", "::", "unspecified address"},
		{"multicast", "224.0.0.1", "multicast address"},
		{"broadcast", "255.255.255.255", "broadcast address"},
		{"cgnat-metadata", "100.100.100.200", "shared address space"},
		{"public", "93.184.216.34", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := forbiddenIPReason(net.ParseIP(tc.raw)); got != tc.want {
				t.Fatalf("forbiddenIPReason(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestHostAllowlistMatchesExactHostsOnly(t *testing.T) {
	t.Setenv(AllowedHostsEnv, " vllm.internal, 127.0.0.1:9200, LLM.Corp.LOCAL. ")
	if !hostAllowlisted("vllm.internal", "8000") {
		t.Fatal("bare host entry must match any port")
	}
	if !hostAllowlisted("llm.corp.local", "443") {
		t.Fatal("entries are case-insensitive and ignore a trailing dot")
	}
	if !hostAllowlisted("127.0.0.1", "9200") {
		t.Fatal("host and port entry must match")
	}
	if hostAllowlisted("127.0.0.1", "11434") {
		t.Fatal("host and port entry must not match another port")
	}
	if hostAllowlisted("vllm.internal.attacker.example", "8000") {
		t.Fatal("entries must not match subdomains")
	}
	if hostAllowlisted("*.internal", "8000") {
		t.Fatal("wildcards never match")
	}
	if hostAllowlisted("", "8000") {
		t.Fatal("empty hosts never match")
	}
}

func TestCheckBaseURLPolicyRejectsForbiddenLiterals(t *testing.T) {
	if _, err := Normalize("网关", "", "http://169.254.169.254/v1", []string{"m"}, "", ""); !errors.Is(err, ErrEndpointForbidden) {
		t.Fatalf("save accepted a metadata endpoint: %v", err)
	}
	t.Setenv(AllowedHostsEnv, "127.0.0.1")
	config, err := Normalize("网关", "", "http://127.0.0.1:11434/v1", []string{"m"}, "", "")
	if err != nil || !strings.HasPrefix(config.BaseURL, "http://127.0.0.1:11434") {
		t.Fatalf("allowlisted loopback endpoint = %+v, %v", config, err)
	}
	if err := CheckBaseURLPolicy("https://llm.example.com/v1"); err != nil {
		t.Fatalf("public hostname endpoint rejected at save: %v", err)
	}
}

func TestGuardedClientDialsVerifiedAddressesAndRefusesLoopback(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer server.Close()

	if _, err := Probe(context.Background(), ProtocolOpenAICompatible, server.URL, ""); !errors.Is(err, ErrEndpointForbidden) {
		t.Fatalf("probe reached a loopback endpoint without an allowlist entry: %v", err)
	}
	if hits != 0 {
		t.Fatalf("rejected probe reached the endpoint %d times", hits)
	}
	t.Setenv(AllowedHostsEnv, "127.0.0.1")
	if _, err := Probe(context.Background(), ProtocolOpenAICompatible, server.URL, "sk-provider"); err != nil {
		t.Fatalf("allowlisted loopback probe failed: %v", err)
	}
	if hits != 1 {
		t.Fatalf("probe hits = %d, want 1", hits)
	}
}

func TestGuardedClientNeverFollowsRedirects(t *testing.T) {
	redirected := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected <- r.Header.Get("Authorization")
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL+"/stolen")
		w.WriteHeader(http.StatusFound)
	}))
	defer source.Close()

	t.Setenv(AllowedHostsEnv, "127.0.0.1")
	response, err := GuardedClient(5 * time.Second).Get(source.URL + "/models")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("redirect response status = %d, want the unmodified 302", response.StatusCode)
	}
	select {
	case authorization := <-redirected:
		t.Fatalf("redirect forwarded the credential: %q", authorization)
	default:
	}
}
