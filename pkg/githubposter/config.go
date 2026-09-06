// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubposter

import (
	"context"
	"os"
	"path/filepath"

	errors "github.com/bborbe/errors"

	"github.com/bborbe/maintainer/maintainerconfig"
)

// ReadAutoApprove reads `.maintainer.yaml` from workDir and returns the
// prReviewer.autoApprove value. A missing file is NOT an error — returns
// false. Malformed YAML surfaces as a wrapped error (NOT silently false) so
// the ai_review step fails loudly rather than masking an operator typo.
// Note: the poster reads this for operator tooling but does not gate the
// posted event on it — the verdict alone decides (spec 060).
func ReadAutoApprove(ctx context.Context, workDir string) (bool, error) {
	path := filepath.Join(workDir, ".maintainer.yaml")
	data, err := os.ReadFile(
		path,
	) // #nosec G304 -- workDir is an internal trusted path, not user-controlled input
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, errors.Wrapf(ctx, err, "read .maintainer.yaml at %s", path)
	}
	cfg, err := maintainerconfig.Parse(ctx, data)
	if err != nil {
		return false, errors.Wrapf(ctx, err, "parse .maintainer.yaml at %s", path)
	}
	return cfg.PrReviewer.AutoApprove, nil
}
