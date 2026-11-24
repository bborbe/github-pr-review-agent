// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package security

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bborbe/errors"
)

// OSV endpoints. The batch endpoint returns only ids; the per-vuln endpoint
// carries the summary and aliases the review needs to cite.
const (
	osvQueryBatchURL = "https://api.osv.dev/v1/querybatch"
	osvVulnURLFormat = "https://api.osv.dev/v1/vulns/%s"
	// osvBatchLimit is OSV's documented maximum queries per batch request.
	osvBatchLimit = 1000
	// defaultTimeout bounds one HTTP round trip.
	defaultTimeout = 30 * time.Second
)

// newOSVClient returns the production client. The transport is built explicitly
// so the proxy comes from the environment: the review Job runs in a default-deny
// sandbox and reaches api.osv.dev only through egress-proxy, whose allowlist
// carries .osv.dev.
func newOSVClient(timeout time.Duration) OSVClient {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &osvClient{
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
			},
		},
		baseURL: "",
	}
}

type osvClient struct {
	httpClient *http.Client
	// baseURL overrides the API root in tests; empty means the public API.
	baseURL string
}

type osvQuery struct {
	Package osvPackage `json:"package"`
	Version string     `json:"version"`
}

type osvPackage struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
}

type osvBatchRequest struct {
	Queries []osvQuery `json:"queries"`
}

type osvBatchResponse struct {
	Results []struct {
		Vulns []struct {
			ID string `json:"id"`
		} `json:"vulns"`
	} `json:"results"`
}

type osvVuln struct {
	ID      string   `json:"id"`
	Aliases []string `json:"aliases"`
	Summary string   `json:"summary"`
}

// Findings queries OSV for every module and returns one Finding per
// (module, advisory) pair. Any transport, status or decode failure is returned
// as an error so the caller can render the scan as failed rather than clean.
func (c *osvClient) Findings(
	ctx context.Context,
	modules []ModuleVersion,
) ([]Finding, error) {
	idsByModule, err := c.queryBatch(ctx, modules)
	if err != nil {
		return nil, err
	}
	details, err := c.vulnDetails(ctx, idsByModule)
	if err != nil {
		return nil, err
	}
	findings := make([]Finding, 0, len(modules))
	for _, mod := range modules {
		for _, id := range idsByModule[modKey(mod)] {
			detail := details[id]
			findings = append(findings, Finding{
				Tool:     "osv",
				Package:  mod.Path,
				Version:  mod.Version,
				Advisory: id,
				Aliases:  detail.Aliases,
				Message:  detail.Summary,
			})
		}
	}
	return findings, nil
}

func modKey(mod ModuleVersion) string {
	return mod.Path + "@" + mod.Version
}

// queryBatch posts the modules in OSV's batch limit and maps advisory ids back
// onto the module they belong to.
func (c *osvClient) queryBatch(
	ctx context.Context,
	modules []ModuleVersion,
) (map[string][]string, error) {
	result := map[string][]string{}
	for start := 0; start < len(modules); start += osvBatchLimit {
		end := start + osvBatchLimit
		if end > len(modules) {
			end = len(modules)
		}
		chunk := modules[start:end]
		response, err := c.postBatch(ctx, chunk)
		if err != nil {
			return nil, err
		}
		if len(response.Results) != len(chunk) {
			return nil, errors.Errorf(
				ctx,
				"osv querybatch returned %d results for %d queries",
				len(response.Results), len(chunk),
			)
		}
		for i, item := range response.Results {
			ids := make([]string, 0, len(item.Vulns))
			for _, vuln := range item.Vulns {
				ids = append(ids, vuln.ID)
			}
			result[modKey(chunk[i])] = ids
		}
	}
	return result, nil
}

func (c *osvClient) postBatch(
	ctx context.Context,
	modules []ModuleVersion,
) (osvBatchResponse, error) {
	queries := make([]osvQuery, 0, len(modules))
	for _, mod := range modules {
		queries = append(queries, osvQuery{
			Package: osvPackage{Name: mod.Path, Ecosystem: "Go"},
			Version: mod.Version,
		})
	}
	body, err := json.Marshal(osvBatchRequest{Queries: queries})
	if err != nil {
		return osvBatchResponse{}, errors.Wrapf(ctx, err, "marshal osv batch request failed")
	}
	var response osvBatchResponse
	if err := c.doJSON(ctx, http.MethodPost, c.url(osvQueryBatchURL), body, &response); err != nil {
		return osvBatchResponse{}, err
	}
	return response, nil
}

// vulnDetails fetches each unique advisory once and returns it keyed by id. A
// missing summary is not an error — the review can still cite the id.
func (c *osvClient) vulnDetails(
	ctx context.Context,
	idsByModule map[string][]string,
) (map[string]osvVuln, error) {
	details := map[string]osvVuln{}
	for _, ids := range idsByModule {
		for _, id := range ids {
			if _, ok := details[id]; ok {
				continue
			}
			var vuln osvVuln
			url := c.url(fmt.Sprintf(osvVulnURLFormat, id))
			if err := c.doJSON(ctx, http.MethodGet, url, nil, &vuln); err != nil {
				return nil, err
			}
			details[id] = vuln
		}
	}
	return details, nil
}

func (c *osvClient) url(absolute string) string {
	if c.baseURL == "" {
		return absolute
	}
	return c.baseURL + absolute[len("https://api.osv.dev"):]
}

func (c *osvClient) doJSON(
	ctx context.Context,
	method string,
	url string,
	body []byte,
	target interface{},
) error {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return errors.Wrapf(ctx, err, "build osv request failed")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return errors.Wrapf(ctx, err, "osv request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return errors.Errorf(ctx, "osv returned status %d for %s", response.StatusCode, url)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return errors.Wrapf(ctx, err, "decode osv response failed")
	}
	return nil
}
