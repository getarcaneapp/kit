package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestIsFallbackEligibleDaemonError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "unauthorized", err: errors.New("unauthorized: authentication required"), want: false},
		{name: "certificate", err: errors.New("x509: certificate signed by unknown authority"), want: false},
		{name: "not found", err: errors.New("manifest unknown: status 404"), want: true},
		{name: "forbidden", err: errors.New("status: 403 forbidden by administrative rules"), want: true},
		{name: "proxy", err: errors.New("proxy" + "connect tcp: connection refused"), want: true},
		{name: "unsupported", err: errors.New("distribution api not implemented"), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsFallbackEligibleDaemonError(tt.err); got != tt.want {
				t.Fatalf("IsFallbackEligibleDaemonError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

const testDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// fakeRegistryInternal serves registry.test with bearer auth from auth.test and
// passes authorized requests to handle.
func fakeRegistryInternal(t *testing.T, realm string, checkToken func(*http.Request), handle func(*http.Request) *http.Response) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "auth.test" {
			checkToken(r)
			return tagsResponseInternal(r, http.StatusOK, `{"token":"registry-token"}`), nil
		}
		if r.Header.Get("Authorization") != "Bearer registry-token" {
			resp := tagsResponseInternal(r, http.StatusUnauthorized, "")
			resp.Header.Set("WWW-Authenticate", `Bearer realm="`+realm+`",service="registry.test"`)
			return resp, nil
		}
		return handle(r), nil
	})}
}

func manifestResponseInternal(r *http.Request, body, digest string) *http.Response {
	resp := tagsResponseInternal(r, http.StatusOK, body)
	if r.Method == http.MethodHead {
		resp.Body = http.NoBody
	}
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
	if digest != "" {
		resp.Header.Set("Docker-Content-Digest", digest)
	}
	return resp
}

func TestFetchDigestUsesHeadWithTokenAuth(t *testing.T) {
	for _, tt := range []struct {
		name       string
		credential *authn.AuthConfig
		checkToken func(*testing.T, *http.Request)
	}{
		{name: "anonymous", checkToken: func(t *testing.T, r *http.Request) {
			if _, _, ok := r.BasicAuth(); ok {
				t.Error("anonymous token request sent credentials")
			}
		}},
		{name: "basic", credential: &authn.AuthConfig{Username: "user", Password: "secret"}, checkToken: func(t *testing.T, r *http.Request) {
			if user, password, _ := r.BasicAuth(); user != "user" || password != "secret" {
				t.Errorf("token credentials = %q/%q", user, password)
			}
		}},
		{name: "identity token", credential: &authn.AuthConfig{Username: "user", IdentityToken: "refresh"}, checkToken: func(t *testing.T, r *http.Request) {
			if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "refresh" {
				t.Errorf("identity token not exchanged as refresh token: %v %v", r.PostForm, err)
			}
		}},
		{name: "registry token", credential: &authn.AuthConfig{RegistryToken: "registry-token"}, checkToken: func(t *testing.T, _ *http.Request) {
			t.Error("registry token should be used without a token request")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := fakeRegistryInternal(t, "https://auth.test/token", func(r *http.Request) {
				if r.URL.Query().Get("scope") != "repository:team/app:pull" && r.FormValue("scope") != "repository:team/app:pull" {
					t.Errorf("token request missing pull scope: %s", r.URL)
				}
				tt.checkToken(t, r)
			}, func(r *http.Request) *http.Response {
				if r.Method != http.MethodHead || r.URL.Path != "/v2/team/app/manifests/1.2.3" {
					t.Errorf("unexpected manifest request: %s %s", r.Method, r.URL)
				}
				for _, mediaType := range []string{"application/vnd.docker.distribution.manifest.list.v2+json", "application/vnd.oci.image.index.v1+json"} {
					if !strings.Contains(r.Header.Get("Accept"), mediaType) {
						t.Errorf("Accept = %q, want %s", r.Header.Get("Accept"), mediaType)
					}
				}
				return manifestResponseInternal(r, "{}", testDigest)
			})
			digest, err := FetchDigest(t.Context(), "registry.test", "team/app", "1.2.3", tt.credential, client)
			if err != nil || digest != testDigest {
				t.Fatalf("FetchDigest = %q, %v", digest, err)
			}
		})
	}
}

func TestFetchDigestFallsBackToGetWithoutDigestHeader(t *testing.T) {
	body := `{"schemaVersion":2}`
	sum := sha256.Sum256([]byte(body))
	client := fakeRegistryInternal(t, "https://auth.test/token", func(*http.Request) {}, func(r *http.Request) *http.Response {
		return manifestResponseInternal(r, body, "")
	})
	digest, err := FetchDigest(t.Context(), "registry.test", "team/app", "1.2.3", nil, client)
	if want := "sha256:" + hex.EncodeToString(sum[:]); err != nil || digest != want {
		t.Fatalf("FetchDigest = %q, %v; want %q", digest, err, want)
	}
}

func TestFetchDigestDoesNotFallBackOnRegistryError(t *testing.T) {
	client := fakeRegistryInternal(t, "https://auth.test/token", func(*http.Request) {}, func(r *http.Request) *http.Response {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected %s after registry error", r.Method)
		}
		return tagsResponseInternal(r, http.StatusNotFound, "")
	})
	if _, err := FetchDigest(t.Context(), "registry.test", "team/app", "1.2.3", nil, client); err == nil {
		t.Fatal("FetchDigest returned nil error")
	}
}

func TestFetchDigestRejectsNonHTTPSAuthRealm(t *testing.T) {
	client := fakeRegistryInternal(t, "http://auth.test/token", func(*http.Request) {
		t.Error("token requested from non-HTTPS realm")
	}, func(r *http.Request) *http.Response {
		return manifestResponseInternal(r, "{}", testDigest)
	})
	if _, err := FetchDigest(t.Context(), "registry.test", "team/app", "1.2.3", nil, client); err == nil {
		t.Fatal("FetchDigest returned nil error")
	}
}

func TestFetchRegistryRateLimitUsesHead(t *testing.T) {
	for _, credential := range []*authn.AuthConfig{nil, {Username: "user", Password: "secret"}} {
		client := fakeRegistryInternal(t, "https://auth.test/token", func(r *http.Request) {
			if user, _, _ := r.BasicAuth(); credential != nil && user != "user" {
				t.Errorf("token user = %q, want user", user)
			}
		}, func(r *http.Request) *http.Response {
			if r.Method != http.MethodHead {
				t.Errorf("manifest method = %s, want HEAD", r.Method)
			}
			resp := manifestResponseInternal(r, "{}", testDigest)
			resp.Header.Set("RateLimit-Limit", "100;w=21600")
			resp.Header.Set("RateLimit-Remaining", "90;w=21600")
			return resp
		})
		limit, err := FetchRegistryRateLimit(t.Context(), "registry.test", "team/app", "1.2.3", credential, client)
		if err != nil {
			t.Fatal(err)
		}
		if limit.Limit == nil || *limit.Limit != 100 || limit.Remaining == nil || *limit.Remaining != 90 || limit.WindowSeconds == nil || *limit.WindowSeconds != 21600 {
			t.Fatalf("rate limit = %+v", limit)
		}
	}
}
