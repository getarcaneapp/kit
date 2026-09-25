package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFetchTagsPaginationAndAuthentication(t *testing.T) {
	var server *httptest.Server
	var tokenCalls, pageCalls int
	pages := map[string]struct{ body, link string }{
		"":      {body: `{"name":"team/app","tags":["1.0.0"]}`, link: `</v2/team/app/tags/list?last=1.0.0&n=1000>; rel="next"`},
		"1.0.0": {body: `{"name":"team/app","tags":["1.0.1"]}`, link: `</v2/team/app/tags/list?last=1.0.1&n=1000>; rel="next"`},
		"1.0.1": {body: `{"name":"team/app","tags":["1.1.0"]}`},
	}
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenCalls++
			user, password, _ := r.BasicAuth()
			if user != "user" || password != "password" || r.URL.Query().Get("scope") != "repository:team/app:pull" {
				t.Error("token request did not preserve credentials and scope")
			}
			_, _ = io.WriteString(w, `{"token":"private-token"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer private-token" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="registry"`, server.URL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		pageCalls++
		if r.URL.Query().Get("n") != "1000" {
			t.Errorf("page %q requested without explicit page size: %s", r.URL.Query().Get("last"), r.URL.RawQuery)
		}
		page := pages[r.URL.Query().Get("last")]
		if page.link != "" {
			w.Header().Set("Link", page.link)
		}
		_, _ = io.WriteString(w, page.body)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	tags, err := FetchTags(t.Context(), u.Host, "team/app", &Credentials{Username: "user", Token: "password"}, server.Client())
	if err != nil || !reflect.DeepEqual(tags, []string{"1.0.0", "1.0.1", "1.1.0"}) {
		t.Fatalf("FetchTags = %v, %v", tags, err)
	}
	if tokenCalls != 1 || pageCalls != 3 {
		t.Fatalf("token calls = %d, page calls = %d", tokenCalls, pageCalls)
	}
}

func TestFetchTagsRequestsChallengeScope(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			user, password, _ := r.BasicAuth()
			if user != "user" || password != "password" || r.URL.Query().Get("scope") != "repository:team/app:metadata_read" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"metadata-token"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer metadata-token" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/oauth2/token",service="registry",scope="repository:team/app:metadata_read"`, server.URL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"name":"team/app","tags":["1.1.1-1"]}`)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	tags, err := FetchTags(t.Context(), u.Host, "team/app", &Credentials{Username: "user", Token: "password"}, server.Client())
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
		{name: "trailing JSON", body: `{"tags":[]} {}`},
		{name: "missing tags", body: `{}`},
		{name: "wrong tags type", body: `{"tags":"1.0.0"}`},
		{name: "wrong repository", body: `{"name":"other","tags":[]}`},
		{name: "rate limit", status: http.StatusTooManyRequests},
		{name: "unauthorized", status: http.StatusUnauthorized},
		{name: "cycle", body: `{"tags":["1.0.0"]}`, link: `</v2/team/app/tags/list>; rel="next"`},
		{name: "foreign host", body: `{"tags":[]}`, link: `<https://other.test/v2/team/app/tags/list>; rel="next"`},
		{name: "foreign endpoint", body: `{"tags":[]}`, link: `</v2/other/tags/list>; rel="next"`},
		{name: "malformed link", body: `{"tags":[]}`, link: `invalid`},
		{name: "missing relation", body: `{"tags":[]}`, link: `</v2/team/app/tags/list?last=1>`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.link != "" {
					w.Header().Set("Link", tt.link)
				}
				if tt.status != 0 {
					w.WriteHeader(tt.status)
				}
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			tags, err := FetchTags(t.Context(), u.Host, "team/app", nil, server.Client())
			if err == nil || tags != nil {
				t.Fatalf("expected failure without partial tags, got %v, %v", tags, err)
			}
		})
	}
}

func TestFetchTagsDockerHubAndEmpty(t *testing.T) {
	for _, body := range []string{`{"tags":[]}`, `{"tags":null}`} {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "registry-1.docker.io" || r.URL.Path != "/v2/library/alpine/tags/list" {
				t.Errorf("unexpected URL: %s", r.URL)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		})}
		tags, err := FetchTags(t.Context(), "docker.io", "library/alpine", nil, client)
		if err != nil || len(tags) != 0 {
			t.Fatalf("expected empty list: %v, %v", tags, err)
		}
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
			pages := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				pages++
				header := http.Header{}
				if pages == 1 {
					header.Set("Link", `<?last=1.0.0&n=1000>; rel="next"`)
				} else {
					tt.secondPage(cancel)
				}
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tags":["1.0.0"]}`)), Header: header}, nil
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
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tags":["1.0.0"]}`)), Header: http.Header{}}, nil
			})}
			if _, err := FetchTags(ctx, "registry.test", "team/app", nil, client); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFetchTagsRejectsCredentialRedirects(t *testing.T) {
	for _, tokenRedirect := range []bool{false, true} {
		t.Run(fmt.Sprint(tokenRedirect), func(t *testing.T) {
			var server *httptest.Server
			var leaked bool
			server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/leak" {
					leaked = true
					return
				}
				if tokenRedirect && r.URL.Path != "/token" {
					w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token"`, server.URL))
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				http.Redirect(w, r, "/leak", http.StatusTemporaryRedirect)
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			_, err := FetchTags(t.Context(), u.Host, "team/app", &Credentials{Username: "user", Token: "secret"}, server.Client())
			if err == nil || leaked {
				t.Fatalf("redirect allowed: leaked=%v, err=%v", leaked, err)
			}
		})
	}
}

func TestFetchTagsFollowsAnonymousRedirects(t *testing.T) {
	// registry.k8s.io redirects tag listings to a regional mirror whose
	// response names the mirrored repository.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/team/app/tags/list":
			http.Redirect(w, r, "/v2/mirror/team/app/tags/list?rid=1", http.StatusTemporaryRedirect)
		case "/v2/mirror/team/app/tags/list":
			_, _ = io.WriteString(w, `{"name":"mirror/team/app","tags":["1.0.0","1.1.0"]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	tags, err := FetchTags(t.Context(), u.Host, "team/app", nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"1.0.0", "1.1.0"}; !reflect.DeepEqual(tags, want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
}

func TestFetchTagsRejectsInsecureRedirects(t *testing.T) {
	var leaked bool
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		leaked = true
	}))
	defer plain.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/v2/team/app/tags/list", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	_, err := FetchTags(t.Context(), u.Host, "team/app", nil, server.Client())
	if err == nil || leaked {
		t.Fatalf("insecure redirect allowed: leaked=%v, err=%v", leaked, err)
	}
}

func TestFetchTagsDiscardsPartialListing(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("last") {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Link", `<?last=1.0.0&n=1000>; rel="next"`)
		_, _ = io.WriteString(w, `{"tags":["1.0.0"]}`)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	tags, err := FetchTags(t.Context(), u.Host, "team/app", nil, server.Client())
	if err == nil || tags != nil {
		t.Fatalf("partial listing escaped: %v, %v", tags, err)
	}
}

func TestFetchTagsMixedRelationsInternal(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprint(separate), func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				header := http.Header{}
				body := `{"tags":["1.0.0"]}`
				if !r.URL.Query().Has("last") {
					links := []string{`<?first=1>; rel="first"`, `<?last=1.0.0&n=1000>; rel="next"`, `<?prev=1>; rel="prev"`}
					if separate {
						for _, link := range links {
							header.Add("Link", link)
						}
					} else {
						header.Set("Link", strings.Join(links, ", "))
					}
				} else {
					body = `{"tags":["1.0.1"]}`
					header.Set("Link", `<?prev=1>; rel="prev"`)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: header}, nil
			})}
			tags, err := FetchTags(t.Context(), "registry.test", "team/app", nil, client)
			if err != nil || !reflect.DeepEqual(tags, []string{"1.0.0", "1.0.1"}) || calls != 2 {
				t.Fatalf("tags=%v err=%v calls=%d", tags, err, calls)
			}
		})
	}
}

type failingTagsBodyInternal struct {
	io.Reader
	closeErr error
	closed   bool
}

func (b *failingTagsBodyInternal) Close() error { b.closed = true; return b.closeErr }

func TestFetchTagsCloseErrorsInternal(t *testing.T) {
	for _, tt := range []struct {
		name, body  string
		status      int
		readFailure bool
	}{
		{name: "successful page", body: `{"tags":["1.0.0"]}`, status: 200},
		{name: "invalid page", body: `{"tags":[`, status: 200, readFailure: true},
		{name: "authentication challenge", status: 401},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sentinel := errors.New("close failed")
			body := &failingTagsBodyInternal{Reader: strings.NewReader(tt.body), closeErr: sentinel}
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tt.status, Body: body, Header: http.Header{"Www-Authenticate": {`Bearer realm="https://registry.test/token"`}}}, nil
			})}
			tags, err := FetchTags(t.Context(), "registry.test", "team/app", nil, client)
			if !errors.Is(err, sentinel) || tags != nil || !body.closed || calls != 1 {
				t.Fatalf("tags=%v err=%v closed=%v calls=%d", tags, err, body.closed, calls)
			}
			if tt.readFailure && !strings.Contains(err.Error(), "decode registry tags") {
				t.Fatalf("decode error lost: %v", err)
			}
		})
	}
}
