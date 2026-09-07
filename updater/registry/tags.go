package registry

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	kitregistry "go.getarcane.app/kit/pkg/registry"
)

// FetchTags lists all repository tags, including paginated results. A partial
// listing is never returned on failure. The entire lookup has a 30-second limit.
func FetchTags(
	ctx context.Context,
	registryHost, repository string,
	credential *Credentials,
	httpClient *http.Client,
) ([]string, error) {
	if httpClient == nil {
		httpClient = NewHTTPClient()
	}
	// Copy the client so callers' redirect policies remain unchanged. Registry
	// and token endpoint redirects must not forward credentials to another URL.
	client := *httpClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	registryHost = kitregistry.Normalize(registryHost)
	if registryHost == "docker.io" {
		registryHost = defaultRegistryHost
	}
	repository = strings.Trim(repository, "/")
	endpoint := &url.URL{Scheme: "https", Host: registryHost, Path: "/v2/" + repository + "/tags/list"}
	next := endpoint
	seen := map[string]bool{}
	tags := []string{}
	authHeader := basicAuthHeaderForCredential(credential)
	for next != nil {
		key := *next
		key.RawQuery = next.Query().Encode()
		if seen[key.String()] {
			return nil, errors.New("registry tag pagination cycle")
		}
		seen[key.String()] = true
		resp, err := tagsRequestInternal(requestCtx, &client, next, authHeader)
		if err != nil {
			return nil, fmt.Errorf("list registry tags: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			realm, service := parseWWWAuth(resp.Header.Get("WWW-Authenticate"))
			if err := resp.Body.Close(); err != nil {
				return nil, fmt.Errorf("close registry authentication response: %w", err)
			}
			if err := validateAuthRealm(registryHost, realm); err != nil {
				return nil, err
			}
			authHeader, err = fetchRegistryToken(requestCtx, &client, realm, service, repository, credential)
			if err != nil {
				return nil, fmt.Errorf("authorize registry tags: %w", err)
			}
			resp, err = tagsRequestInternal(requestCtx, &client, next, authHeader)
			if err != nil {
				return nil, fmt.Errorf("list authorized registry tags: %w", err)
			}
		}
		page, pageErr := readTagsPageInternal(resp, repository)
		if closeErr := resp.Body.Close(); closeErr != nil {
			pageErr = errors.Join(pageErr, fmt.Errorf("close registry tag response: %w", closeErr))
		}
		if pageErr != nil {
			return nil, pageErr
		}
		tags = append(tags, page...)
		next, err = nextTagsPageInternal(resp.Header.Values("Link"), next, endpoint)
		if err != nil {
			return nil, err
		}
	}
	return tags, nil
}

func tagsRequestInternal(ctx context.Context, client *http.Client, page *url.URL, authorization string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, page.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	return client.Do(req)
}

func readTagsPageInternal(resp *http.Response, repository string) ([]string, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry tag request failed with status: %d", resp.StatusCode)
	}
	var body struct {
		Name string         `json:"name"`
		Tags jsontext.Value `json:"tags"`
	}
	// Bound each page while requiring a complete JSON document.
	data, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read registry tags: %w", err)
	}
	if len(data) > 16<<20 {
		return nil, errors.New("registry tag page exceeds 16 MiB")
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("decode registry tags: %w", err)
	}
	if body.Name != "" && body.Name != repository {
		return nil, errors.New("registry tag response names a different repository")
	}
	if len(body.Tags) == 0 {
		return nil, errors.New("registry tag response is missing tags")
	}
	tags := []string{}
	if err := json.Unmarshal(body.Tags, &tags); err != nil {
		return nil, fmt.Errorf("decode registry tag list: %w", err)
	}
	return tags, nil
}

func nextTagsPageInternal(headers []string, current, endpoint *url.URL) (*url.URL, error) {
	var next *url.URL
	for _, header := range headers {
		// A comma inside an angle-bracketed URI is part of the URI.
		for strings.TrimSpace(header) != "" {
			header = strings.TrimSpace(header)
			if !strings.HasPrefix(header, "<") {
				return nil, errors.New("malformed registry pagination link")
			}
			uri, tail, ok := strings.Cut(header[1:], ">")
			if !ok {
				return nil, errors.New("malformed registry pagination link")
			}
			params, rest, _ := strings.Cut(tail, ",")
			header = rest
			isNext, hasRelation := tagsLinkRelationInternal(params)
			if !hasRelation {
				return nil, errors.New("registry pagination link has no relation")
			}
			if !isNext {
				continue
			}
			if next != nil {
				return nil, errors.New("registry pagination has multiple next links")
			}
			ref, err := url.Parse(uri)
			if err != nil {
				return nil, fmt.Errorf("parse registry pagination link: %w", err)
			}
			next = current.ResolveReference(ref)
			if next.Scheme != endpoint.Scheme || next.Host != endpoint.Host || next.Path != endpoint.Path || next.User != nil || next.Fragment != "" {
				return nil, errors.New("registry pagination link leaves the repository endpoint")
			}
		}
	}
	return next, nil
}

func tagsLinkRelationInternal(params string) (isNext, hasRelation bool) {
	for param := range strings.SplitSeq(params, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(param), "=")
		if !ok || !strings.EqualFold(key, "rel") {
			continue
		}
		for relation := range strings.FieldsSeq(strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"`))) {
			hasRelation = true
			if relation == "next" {
				isNext = true
			}
		}
	}
	return isNext, hasRelation
}
