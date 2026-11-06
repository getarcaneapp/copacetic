package patch

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/containerd/platforms"
	"github.com/distribution/reference"
	buildkitclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/progress/progressui"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	log "github.com/sirupsen/logrus"

	"getarcane.dev/copacetic/internal/buildkit"
	"getarcane.dev/copacetic/internal/common"
	"getarcane.dev/copacetic/internal/report"
	"getarcane.dev/copacetic/internal/types"
	"getarcane.dev/copacetic/internal/types/unversioned"
	"getarcane.dev/copacetic/internal/utils"
)

// for testing.
var (
	bkNewClient = buildkit.NewClient
	listWorkers = func(ctx context.Context, client *buildkitclient.Client) ([]*buildkitclient.WorkerInfo, error) {
		return client.ListWorkers(ctx)
	}
)

// Patch command applies package updates to an OCI image given a vulnerability report for a given set of options.
func Patch(ctx context.Context, opts *types.Options) error {
	allowedProgressModes := map[string]struct{}{
		"auto":    {},
		"plain":   {},
		"tty":     {},
		"quiet":   {},
		"rawjson": {},
	}
	if _, ok := allowedProgressModes[string(opts.Progress)]; !ok {
		log.Warnf("Invalid value for --progress: %q. Allowed values are 'auto', 'plain' 'tty', 'quiet' or 'rawjson'. Defaulting to 'auto'.", string(opts.Progress))
		opts.Progress = progressui.DisplayMode("auto")
	}
	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	ch := make(chan error, 1)

	go func() {
		ch <- patchWithContext(timeoutCtx, opts)
		close(ch)
	}()
	select {
	case err := <-ch:
		return err
	case <-timeoutCtx.Done():
		<-time.After(1 * time.Second)

		// Check if this was a cancellation or an actual timeout.
		if ctx.Err() == context.Canceled {
			return context.Canceled
		}
		return fmt.Errorf("patch exceeded timeout %v", opts.Timeout)
	}
}

// patchWithContext orchestrates the main patching workflow.
func patchWithContext(ctx context.Context, opts *types.Options) error {
	// Configure EOL API if provided
	if opts.EOLAPIBaseURL != "" {
		utils.SetEOLAPIBaseURL(opts.EOLAPIBaseURL)
		log.Debugf("Configured EOL API base URL: %s", opts.EOLAPIBaseURL)
	}

	image := opts.Image
	reportPath := opts.Report
	targetPlatforms := opts.Platforms
	pkgTypes := opts.PkgTypes

	// Parse and validate package types early
	pkgTypesList, err := parsePkgTypes(pkgTypes)
	if err != nil {
		return fmt.Errorf("invalid package types: %w", err)
	}

	// Validate that library package types require a scanner report
	reportProvided := reportPath != ""
	if err := validateLibraryPkgTypesRequireReport(pkgTypesList, reportProvided); err != nil {
		return err
	}

	// Handle empty report path - check if image is manifest list or single platform
	if reportPath == "" {
		// Discover platforms from the image reference to determine if it's multi-platform
		discoveredPlatforms, err := buildkit.DiscoverPlatformsFromReference(image)
		if err != nil {
			// Failed to discover platforms - treat as single-platform image
			log.Warnf("Failed to discover platforms for image %s (treating as single-platform): %v", image, err)
			if len(targetPlatforms) > 0 {
				log.Info("Platform flag ignored when platform discovery fails")
			}

			// Fallback to default platform
			defaultPlatform := common.GetDefaultLinuxPlatform()
			patchPlatform := types.PatchPlatform{
				Platform:       defaultPlatform,
				ReportFile:     "",
				ShouldPreserve: false,
			}

			displaySingleArchPlan(opts, &patchPlatform)
			result, err := patchSingleArchImage(ctx, opts, patchPlatform, false, nil)
			if result != nil {
				logPatchSummary(result.Summary)
			}
			if err == nil && result != nil && result.PatchedRef != nil {
				log.Infof("Patched image (%s): %s\n", patchPlatform.OS+"/"+patchPlatform.Architecture, result.PatchedRef)
			}
			return err
		}

		if len(discoveredPlatforms) <= 1 {
			// Single-platform image or multi-platform with only one valid platform
			log.Debugf("Detected single-platform image or multi-platform with single valid platform")
			if len(targetPlatforms) > 0 {
				log.Info("Platform flag ignored for single-platform image")
			}

			var patchPlatform types.PatchPlatform
			if len(discoveredPlatforms) == 1 {
				// Use the discovered platform from the manifest
				patchPlatform = discoveredPlatforms[0]
				log.Debugf("Using discovered platform from manifest: %s/%s", patchPlatform.OS, patchPlatform.Architecture)
			} else {
				// No platforms discovered, use default
				defaultPlatform := common.GetDefaultLinuxPlatform()
				patchPlatform = types.PatchPlatform{
					Platform:       defaultPlatform,
					ReportFile:     "",
					ShouldPreserve: false,
				}
			}

			displaySingleArchPlan(opts, &patchPlatform)
			result, err := patchSingleArchImage(ctx, opts, patchPlatform, false, nil)
			if result != nil {
				logPatchSummary(result.Summary)
			}
			if err == nil && result != nil && result.PatchedRef != nil {
				log.Infof("Patched image (%s): %s\n", patchPlatform.OS+"/"+patchPlatform.Architecture, result.PatchedRef)
			}
			return err
		}

		log.Debugf("Detected multi-platform image with %d platforms", len(discoveredPlatforms))
		return patchMultiPlatformImage(ctx, opts, discoveredPlatforms)
	}

	// Check if reportPath exists
	if _, err := os.Stat(reportPath); os.IsNotExist(err) {
		return fmt.Errorf("report path %s does not exist", reportPath)
	}

	// Get file info to determine if it's a file or directory
	f, err := os.Stat(reportPath)
	if err != nil {
		return fmt.Errorf("failed to stat report path %s: %w", reportPath, err)
	}

	if f.IsDir() {
		// Handle directory - multi-platform patching
		log.Debugf("Using report directory: %s", reportPath)
		if len(targetPlatforms) > 0 {
			log.Info("Platform flag ignored when report directory is provided")
		}
		// For report directory, we pass nil as discoveredPlatforms - the function will discover them internally
		return patchMultiPlatformImage(ctx, opts, nil)
	}
	// Handle file - single-platform patching
	log.Debugf("Using report file: %s", reportPath)
	var parsedUpdates *unversioned.UpdateManifest
	if len(targetPlatforms) == 0 {
		parsedUpdates, err = report.TryParseScanReport(reportPath, opts.Scanner, pkgTypes, opts.LibraryPatchLevel)
		if err != nil {
			return err
		}
	}
	var patchPlatform types.PatchPlatform
	if parsedUpdates == nil {
		patchPlatform, err = resolveSingleReportPlatform(targetPlatforms)
	} else {
		patchPlatform, err = resolveSingleReportPlatformWithUpdates(targetPlatforms, parsedUpdates)
	}
	if err != nil {
		return err
	}
	displaySingleArchPlan(opts, &patchPlatform)
	result, err := patchSingleArchImageWithUpdates(ctx, opts, patchPlatform, false, nil, parsedUpdates)
	if result != nil {
		logPatchSummary(result.Summary)
	}
	if err == nil && result != nil {
		log.Infof("Patched image (%s): %s\n", patchPlatform.OS+"/"+patchPlatform.Architecture, result.PatchedRef.String())
	}
	return err
}

func resolveSingleReportPlatform(targetPlatforms []string) (types.PatchPlatform, error) {
	return resolveSingleReportPlatformWithUpdates(targetPlatforms, nil)
}

func resolveSingleReportPlatformWithUpdates(targetPlatforms []string, updates *unversioned.UpdateManifest) (types.PatchPlatform, error) {
	if len(targetPlatforms) > 1 {
		return types.PatchPlatform{}, fmt.Errorf("a single report file can target only one platform; got %d: %s", len(targetPlatforms), strings.Join(targetPlatforms, ", "))
	}

	platform := common.GetDefaultLinuxPlatform()
	if len(targetPlatforms) == 1 {
		target := targetPlatforms[0]
		parsed, err := platforms.Parse(target)
		if err != nil {
			return types.PatchPlatform{}, fmt.Errorf("parse platform %q: %w", target, err)
		}
		platform = platforms.Normalize(parsed)
		if !isSupportedPatchPlatform(&platform) {
			return types.PatchPlatform{}, fmt.Errorf("unsupported platform %q; valid platforms: %s", target, strings.Join(validPlatforms, ", "))
		}
	} else if updates != nil {
		reportArch := strings.TrimSpace(updates.Metadata.Config.Arch)
		if reportArch != "" {
			platform = platforms.Normalize(ocispec.Platform{
				OS:           LINUX,
				Architecture: reportArch,
				Variant:      strings.TrimSpace(updates.Metadata.Config.Variant),
			})
			if !isSupportedPatchPlatform(&platform) {
				return types.PatchPlatform{}, fmt.Errorf("unsupported scan report platform %q; valid platforms: %s", platforms.Format(platform), strings.Join(validPlatforms, ", "))
			}
		}
	}
	if platform.OS != LINUX {
		platform.OS = LINUX
	}

	return types.PatchPlatform{Platform: platform}, nil
}

// logPatchSummary prints the patch summary if available.
func logPatchSummary(summary *unversioned.PatchSummary) {
	if summary == nil {
		return
	}
	log.Infof("Patch Summary: %d total, %d patched, %d skipped", summary.Total, summary.Patched, summary.Skipped)
}

// displaySingleArchPlan shows a patching plan for single-arch images.
func displaySingleArchPlan(opts *types.Options, platform *types.PatchPlatform) {
	// Use the same resolution logic as the actual patching to get accurate name
	patchedName := opts.Image + "-patched" // fallback
	if ref, err := reference.ParseNormalizedNamed(opts.Image); err == nil {
		if imageName, tag, err := common.ResolvePatchedImageName(ref, opts.PatchedTag, opts.Suffix); err == nil {
			patchedName = fmt.Sprintf("%s:%s", imageName, tag)
		}
	}

	log.Infof("Patching plan: platform=%s patched=%s", platform.String(), patchedName)
}
