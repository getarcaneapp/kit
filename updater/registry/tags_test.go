package registry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
)

func tagsResponseInternal(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}
}

func TestFetchTagsPaginationAndAuthentication(t *testing.T) {
	var tokenCalls, pageCalls int
	pages := map[string]struct{ body, link string }{
		"":      {body: `{"name":"team/app","tags":["1.0.0"]}`, link: `</v2/team/app/tags/list?last=1.0.0&n=1000>; rel="next"`},
		"1.0.0": {body: `{"name":"team/app","tags":["1.0.1"]}`, link: `</v2/team/app/tags/list?last=1.0.1&n=1000>; rel="next"`},
		"1.0.1": {body: `{"name":"team/app","tags":["1.1.0"]}`},
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/token" {
			tokenCalls++
			user, password, _ := r.BasicAuth()
			if user != "user" || password != "password" || r.URL.Query().Get("scope") != "repository:team/app:pull" {
				t.Error("token request did not preserve credentials and scope")
			}
			return tagsResponseInternal(r, http.StatusOK, `{"token":"private-token"}`), nil
		}
		if r.Header.Get("Authorization") != "Bearer private-token" {
			resp := tagsResponseInternal(r, http.StatusUnauthorized, "")
			resp.Header.Set("WWW-Authenticate", `Bearer realm="https://registry.test/token",service="registry"`)
			return resp, nil
		}
		if r.URL.Path == "/v2/" {
			return tagsResponseInternal(r, http.StatusOK, "{}"), nil
		}
		pageCalls++
		if r.URL.Query().Get("n") != "1000" {
			t.Errorf("page %q requested without explicit page size: %s", r.URL.Query().Get("last"), r.URL.RawQuery)
		}
		page := pages[r.URL.Query().Get("last")]
		resp := tagsResponseInternal(r, http.StatusOK, page.body)
		if page.link != "" {
			resp.Header.Set("Link", page.link)
		}
		return resp, nil
	})}
	tags, err := FetchTags(t.Context(), "registry.test", "team/app", &authn.AuthConfig{Username: "user", Password: "password"}, client)
	if err != nil || !reflect.DeepEqual(tags, []string{"1.0.0", "1.0.1", "1.1.0"}) {
		t.Fatalf("FetchTags = %v, %v", tags, err)
	}
	if tokenCalls != 1 || pageCalls != 3 {
		t.Fatalf("token calls = %d, page calls = %d", tokenCalls, pageCalls)
	}
}

func TestFetchTagsRequestsChallengeScope(t *testing.T) {
	// ACR challenges tags/list for metadata_read and refuses pull-only tokens there.
	const metadataScope = "repository:team/app:metadata_read"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/oauth2/token" {
			if user, password, _ := r.BasicAuth(); user != "user" || password != "password" {
				return tagsResponseInternal(r, http.StatusUnauthorized, ""), nil
			}
			if slices.Contains(r.URL.Query()["scope"], metadataScope) {
				return tagsResponseInternal(r, http.StatusOK, `{"access_token":"metadata-token"}`), nil
			}
			return tagsResponseInternal(r, http.StatusOK, `{"access_token":"pull-token"}`), nil
		}
		challenge := `Bearer realm="https://registry.test/oauth2/token",service="registry"`
		switch {
		case r.URL.Path == "/v2/" && r.Header.Get("Authorization") != "":
			return tagsResponseInternal(r, http.StatusOK, "{}"), nil
		case r.URL.Path != "/v2/" && r.Header.Get("Authorization") == "Bearer metadata-token":
			return tagsResponseInternal(r, http.StatusOK, `{"name":"team/app","tags":["1.1.1-1"]}`), nil
		case r.URL.Path != "/v2/":
			challenge += `,scope="` + metadataScope + `"`
		}
		resp := tagsResponseInternal(r, http.StatusUnauthorized, "")
		resp.Header.Set("WWW-Authenticate", challenge)
		return resp, nil
	})}
	tags, err := FetchTags(t.Context(), "registry.test", "team/app", &authn.AuthConfig{Username: "user", Password: "password"}, client)
	if err != nil || !reflect.DeepEqual(tags, []string{"1.1.1-1"}) {
		t.Fatalf("FetchTags = %v, %v", tags, err)
	}
}

func TestFetchTagsFailures(t *testing.T) {
	for _, tt := range []struct {
		name, body, link string
		status           int
	}{
		{name: "malformed JSON", body: `{"tags":[`},
		{name: "not found", status: http.StatusNotFound},
		{name: "unauthorized", status: http.StatusUnauthorized},
		{name: "foreign host", body: `{"tags":["1.0.0"]}`, link: `<https://other.test/v2/team/app/tags/list>; rel="next"`},
		{name: "malformed link", body: `{"tags":["1.0.0"]}`, link: `invalid`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/v2/" {
					return tagsResponseInternal(r, http.StatusOK, "{}"), nil
				}
				status := tt.status
				if status == 0 {
					status = http.StatusOK
				}
				resp := tagsResponseInternal(r, status, tt.body)
				if tt.link != "" {
					resp.Header.Set("Link", tt.link)
				}
				return resp, nil
			})}
			tags, err := FetchTags(t.Context(), "registry.test", "team/app", nil, client)
			if err == nil || tags != nil {
				t.Fatalf("expected failure without partial tags, got %v, %v", tags, err)
			}
		})
	}
}

func TestFetchTagsDockerHubAndEmpty(t *testing.T) {
	for _, body := range []string{`{"tags":[]}`, `{"tags":null}`} {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "registry-1.docker.io" {
				t.Errorf("unexpected URL: %s", r.URL)
			}
			if r.URL.Path == "/v2/" {
				return tagsResponseInternal(r, http.StatusOK, "{}"), nil
			}
			if r.URL.Path != "/v2/library/alpine/tags/list" {
				t.Errorf("unexpected URL: %s", r.URL)
			}
			return tagsResponseInternal(r, http.StatusOK, body), nil
		})}
		tags, err := FetchTags(t.Context(), "docker.io", "library/alpine", nil, client)
		if err != nil || len(tags) != 0 {
			t.Fatalf("expected empty list: %v, %v", tags, err)
		}
	}
}

func TestFetchTagsDiscardsPartialListing(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/v2/":
			return tagsResponseInternal(r, http.StatusOK, "{}"), nil
		case r.URL.Query().Has("last"):
			return tagsResponseInternal(r, http.StatusNotFound, ""), nil
		}
		resp := tagsResponseInternal(r, http.StatusOK, `{"tags":["1.0.0"]}`)
		resp.Header.Set("Link", `<?last=1.0.0&n=1000>; rel="next"`)
		return resp, nil
	})}
	tags, err := FetchTags(t.Context(), "registry.test", "team/app", nil, client)
	if err == nil || tags != nil {
		t.Fatalf("partial listing escaped: %v, %v", tags, err)
	}
}

func TestFetchTagsCancellation(t *testing.T) {
	for _, tt := range []struct {
		name         string
		timeout      time.Duration
		cancelBefore bool
		secondPage   func(cancel context.CancelFunc)
		want         error
	}{
		{name: "before first page", cancelBefore: true, want: context.Canceled},
		{name: "caller deadline expires during pagination", timeout: 100 * time.Millisecond, secondPage: func(context.CancelFunc) { time.Sleep(200 * time.Millisecond) }, want: context.DeadlineExceeded},
		{name: "cancelled during pagination", secondPage: func(cancel context.CancelFunc) { cancel() }, want: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.timeout > 0 {
				var cancelTimeout context.CancelFunc
				ctx, cancelTimeout = context.WithTimeout(ctx, tt.timeout)
				defer cancelTimeout()
			}
			if tt.cancelBefore {
				cancel()
			}
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Has("last") {
					tt.secondPage(cancel)
				}
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				resp := tagsResponseInternal(r, http.StatusOK, `{"tags":["1.0.0"]}`)
				if r.URL.Path != "/v2/" && !r.URL.Query().Has("last") {
					resp.Header.Set("Link", `<?last=1.0.0&n=1000>; rel="next"`)
				}
				return resp, nil
			})}
			tags, err := FetchTags(ctx, "registry.test", "team/app", nil, client)
			if !errors.Is(err, tt.want) || tags != nil {
				t.Fatalf("tags = %v, err = %v, want %v", tags, err, tt.want)
			}
		})
	}
}

func TestFetchTagsHonorsCallerDeadlineInternal(t *testing.T) {
	for _, tt := range []struct {
		name     string
		timeout  time.Duration
		min, max time.Duration
	}{
		{name: "caller deadline longer than 30 seconds", timeout: 5 * time.Minute, min: 4 * time.Minute, max: 5 * time.Minute},
		{name: "no caller deadline falls back to 120 seconds", min: 115 * time.Second, max: 120 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if tt.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.timeout)
				defer cancel()
			}
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("request has no deadline")
				}
				if remaining := time.Until(deadline); remaining < tt.min || remaining > tt.max {
					t.Fatalf("request deadline %s remaining, want between %s and %s", remaining, tt.min, tt.max)
				}
				return tagsResponseInternal(r, http.StatusOK, `{"tags":["1.0.0"]}`), nil
			})}
			if _, err := FetchTags(ctx, "registry.test", "team/app", nil, client); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFetchTagsFollowsRedirectsWithoutForwardingCredentials(t *testing.T) {
	// registry.k8s.io redirects tag listings to a regional mirror.
	for _, credential := range []*authn.AuthConfig{nil, {Username: "user", Password: "secret"}} {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.URL.Host == "mirror.test":
				if r.Header.Get("Authorization") != "" {
					t.Error("credentials forwarded to redirect target")
				}
				return tagsResponseInternal(r, http.StatusOK, `{"name":"mirror/team/app","tags":["1.0.0","1.1.0"]}`), nil
			case r.URL.Path == "/v2/":
				return tagsResponseInternal(r, http.StatusOK, "{}"), nil
			}
			resp := tagsResponseInternal(r, http.StatusTemporaryRedirect, "")
			resp.Header.Set("Location", "https://mirror.test/v2/mirror/team/app/tags/list")
			return resp, nil
		})}
		tags, err := FetchTags(t.Context(), "registry.test", "team/app", credential, client)
		if err != nil || !reflect.DeepEqual(tags, []string{"1.0.0", "1.1.0"}) {
			t.Fatalf("credential=%v: FetchTags = %v, %v", credential != nil, tags, err)
		}
	}
}
