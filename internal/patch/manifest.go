package patch

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	v1types "github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	log "github.com/sirupsen/logrus"

	"getarcane.dev/copacetic/internal/types"
	"getarcane.dev/copacetic/internal/utils"
)

const (
	copaAnnotationKeyPrefix = "sh.copa"

	annotationCreated = "org.opencontainers.image.created"
	annotationVersion = "org.opencontainers.image.version"
)

// createMultiPlatformManifest assembles a multi-platform index from the
// per-platform results and pushes it to imageName. It builds the same index as
// `docker buildx imagetools create --tag … img@sha256:d1 img@sha256:d2 …`, and
// like that command it expects every referenced manifest to already exist in
// imageName's repository.
func createMultiPlatformManifest(
	ctx context.Context,
	imageName reference.NamedTagged,
	items []types.PatchResult,
	originalImage string,
) error {
	now := time.Now().UTC().Format(time.RFC3339)

	descs, err := indexDescriptors(ctx, items)
	if err != nil {
		return err
	}

	// Annotations are only allowed on OCI indexes, so an index of pure Docker
	// manifests becomes a Docker manifest list without them (as buildx does).
	index := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: string(v1types.DockerManifestList),
		Manifests: descs,
	}
	if !allDockerManifests(descs) {
		index = ocispec.Index{
			Versioned:   index.Versioned,
			MediaType:   ocispec.MediaTypeImageIndex,
			Manifests:   stampPatchedDescriptors(descs, items, now),
			Annotations: buildIndexAnnotations(ctx, originalImage, imageName.Tag(), now),
		}
	}

	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest list: %w", err)
	}

	log.Infof("Creating manifest list with %d annotations and %d sources", len(index.Annotations), len(index.Manifests))
	for k, v := range index.Annotations {
		log.Debugf("Index annotation: %s = %s", k, v)
	}

	ref, err := name.ParseReference(imageName.String())
	if err != nil {
		return fmt.Errorf("failed to parse manifest list reference %s: %w", imageName.String(), err)
	}
	hash, size, err := v1.SHA256(strings.NewReader(string(raw)))
	if err != nil {
		return fmt.Errorf("failed to digest manifest list: %w", err)
	}

	log.Infof("Successfully created manifest list, pushing to %s", imageName.String())
	err = remote.Put(ref, &remote.Descriptor{
		Descriptor: v1.Descriptor{
			MediaType: v1types.MediaType(index.MediaType),
			Size:      size,
			Digest:    hash,
		},
		Manifest: raw,
	}, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to push multi-platform manifest list: %w", err)
	}

	log.Infof("Successfully pushed multi-platform manifest list to %s", imageName.String())
	return nil
}

// buildIndexAnnotations carries the original index annotations over to the
// patched index, refreshing the creation time, the version, and Copa's marker.
func buildIndexAnnotations(ctx context.Context, originalImage, patchedTag, now string) map[string]string {
	// This lookup is safe here (after per-platform patches have been built and
	// pushed) because per-platform pushes go to architecture-suffixed tags such
	// as `image:tag-amd64` (see archTag in pkg/patch/single.go), not to the
	// source `originalImage` tag. The source tag is not overwritten until the
	// index push, by which point the annotations have been read.
	original, err := utils.GetIndexManifestAnnotations(ctx, originalImage)
	switch {
	case err != nil:
		log.Warnf("Failed to get original image annotations: %v", err)
	case len(original) == 0:
		log.Info("No annotations found in original image, adding Copa annotations only")
	default:
		log.Infof("Retrieved %d annotations from original image %s", len(original), originalImage)
	}

	annotations := maps.Clone(original)
	if annotations == nil {
		annotations = map[string]string{}
	}
	if version, ok := annotations[annotationVersion]; ok {
		annotations[annotationVersion] = rewriteVersionAnnotation(version, patchedTag)
	}
	annotations[annotationCreated] = now
	annotations[copaAnnotationKeyPrefix+".patched"] = now
	return annotations
}

// indexDescriptors returns one descriptor per distinct manifest digest,
// filling in a missing media type or platform from the registry.
func indexDescriptors(ctx context.Context, items []types.PatchResult) ([]ocispec.Descriptor, error) {
	descs := make([]ocispec.Descriptor, 0, len(items))
	positions := make(map[digest.Digest]int, len(items))

	for _, it := range items {
		if it.PatchedDesc == nil {
			return nil, fmt.Errorf("patched descriptor is nil for %s", it.OriginalRef.String())
		}

		desc, err := completeDescriptor(ctx, it.PatchedRef, it.PatchedDesc)
		if err != nil {
			return nil, err
		}

		pos, seen := positions[desc.Digest]
		if !seen {
			positions[desc.Digest] = len(descs)
			descs = append(descs, desc)
			continue
		}

		prev := descs[pos]
		merged := maps.Clone(prev.Annotations)
		if merged == nil {
			merged = map[string]string{}
		}
		maps.Copy(merged, desc.Annotations)
		descs[pos] = ocispec.Descriptor{
			MediaType:   cmp.Or(prev.MediaType, desc.MediaType),
			Digest:      prev.Digest,
			Size:        prev.Size,
			Platform:    cmp.Or(desc.Platform, prev.Platform),
			Annotations: merged,
		}
	}

	return descs, nil
}

// completeDescriptor fetches the manifest when the descriptor lacks a media
// type or platform, both of which an index entry must carry.
func completeDescriptor(ctx context.Context, src reference.Named, desc *ocispec.Descriptor) (ocispec.Descriptor, error) {
	hasPlatform := desc.Platform != nil && desc.Platform.OS != "" && desc.Platform.Architecture != ""
	if desc.MediaType != "" && hasPlatform {
		return *desc, nil
	}

	srcRef, err := name.ParseReference(src.String())
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to parse source reference %s: %w", src.String(), err)
	}

	remoteDesc, err := remote.Get(srcRef.Context().Digest(desc.Digest.String()),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithContext(ctx),
	)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("failed to fetch manifest %s@%s: %w", src.Name(), desc.Digest, err)
	}
	switch {
	case remoteDesc.MediaType.IsIndex():
		return ocispec.Descriptor{}, fmt.Errorf("source %s@%s is an index, expected a platform manifest", src.Name(), desc.Digest)
	case remoteDesc.MediaType.IsSchema1():
		return ocispec.Descriptor{}, fmt.Errorf("schema1 manifests are not allowed in manifest lists")
	}

	platform := desc.Platform
	if !hasPlatform {
		img, err := remoteDesc.Image()
		if err != nil {
			return ocispec.Descriptor{}, fmt.Errorf("failed to read image %s@%s: %w", src.Name(), desc.Digest, err)
		}
		cfg, err := img.ConfigFile()
		if err != nil {
			return ocispec.Descriptor{}, fmt.Errorf("failed to read image config for %s@%s: %w", src.Name(), desc.Digest, err)
		}
		platform = &ocispec.Platform{
			OS:           cfg.OS,
			Architecture: cfg.Architecture,
			Variant:      cfg.Variant,
			OSVersion:    cfg.OSVersion,
			OSFeatures:   cfg.OSFeatures,
		}
	}

	return ocispec.Descriptor{
		MediaType:   cmp.Or(desc.MediaType, string(remoteDesc.MediaType)),
		Digest:      desc.Digest,
		Size:        desc.Size,
		Platform:    platform,
		Annotations: desc.Annotations,
	}, nil
}

// stampPatchedDescriptors refreshes the creation-time annotation on the
// descriptors of platforms that were actually patched; preserved platforms
// keep their original timestamps.
func stampPatchedDescriptors(descs []ocispec.Descriptor, items []types.PatchResult, now string) []ocispec.Descriptor {
	patched := make(map[digest.Digest]bool, len(items))
	for _, it := range items {
		if it.PatchedDesc != nil && it.PatchedRef != it.OriginalRef {
			patched[it.PatchedDesc.Digest] = true
		}
	}

	stamped := make([]ocispec.Descriptor, len(descs))
	for i, d := range descs {
		stamped[i] = d
		if _, ok := d.Annotations[annotationCreated]; !ok || !patched[d.Digest] {
			continue
		}
		stamped[i].Annotations = maps.Clone(d.Annotations)
		stamped[i].Annotations[annotationCreated] = now
		log.Debugf("Refreshed %s for patched platform %s/%s", annotationCreated, d.Platform.OS, d.Platform.Architecture)
	}
	return stamped
}

// allDockerManifests reports whether every descriptor uses a Docker media type.
func allDockerManifests(descs []ocispec.Descriptor) bool {
	for _, d := range descs {
		if !strings.HasPrefix(d.MediaType, "application/vnd.docker.") {
			return false
		}
	}
	return true
}
