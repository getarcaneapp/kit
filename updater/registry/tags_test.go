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
)

func TestFetchTagsPaginationAndAuthentication(t *testing.T) {
	var server *httptest.Server
	var tokenCalls, pageCalls int
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
		if r.URL.Query().Get("last") == "1.0.0" {
			_, _ = io.WriteString(w, `{"name":"team/app","tags":["1.0.1"]}`)
			return
		}
		w.Header().Set("Link", `</v2/team/app/tags/list?last=1.0.0&n=1>; rel="next"`)
		_, _ = io.WriteString(w, `{"name":"team/app","tags":["1.0.0"]}`)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	tags, err := FetchTags(t.Context(), u.Host, "team/app", &Credentials{Username: "user", Token: "password"}, server.Client())
	if err != nil || !reflect.DeepEqual(tags, []string{"1.0.0", "1.0.1"}) {
		t.Fatalf("FetchTags = %v, %v", tags, err)
	}
	if tokenCalls != 1 || pageCalls != 2 {
		t.Fatalf("token calls = %d, page calls = %d", tokenCalls, pageCalls)
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
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := FetchTags(ctx, "registry.test", "team/app", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want canceled", err)
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

func TestFetchTagsDiscardsPartialListing(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Link", `<?last=1.0.0>; rel="next"`)
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
				if r.URL.RawQuery == "" {
					links := []string{`<?first=1>; rel="first"`, `<?last=1.0.0>; rel="next"`, `<?prev=1>; rel="prev"`}
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
