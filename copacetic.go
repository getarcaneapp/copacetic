// Package copacetic patches vulnerable OS and library packages in existing
// container images with BuildKit, usually driven by a Trivy JSON report.
//
// It is a library-only fork of Project Copacetic. This package is the whole
// public API; the patching engine lives under internal/.
package copacetic

import (
	"context"

	"github.com/distribution/reference"

	"getarcane.dev/copacetic/internal/common"
	"getarcane.dev/copacetic/internal/patch"
	"getarcane.dev/copacetic/internal/types"
)

// Options configures a patch run.
type Options = types.Options

// ErrNoUpdatesFound is returned by Patch when the image has no package
// updates to apply.
var ErrNoUpdatesFound = types.ErrNoUpdatesFound

// Patch applies package updates to opts.Image and produces the patched image.
func Patch(ctx context.Context, opts *Options) error {
	return patch.Patch(ctx, opts)
}

// ResolvePatchedImageName returns the image name and tag Patch writes for
// imageRef: explicitTag when set, otherwise the source tag joined with suffix.
func ResolvePatchedImageName(imageRef reference.Named, explicitTag, suffix string) (imageName, patchTag string, err error) {
	return common.ResolvePatchedImageName(imageRef, explicitTag, suffix)
}
