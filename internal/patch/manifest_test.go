package patch

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/distribution/reference"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	v1types "github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"getarcane.dev/copacetic/internal/types"
)

const stalePlatformTimestamp = "2020-01-01T00:00:00Z"

// pushPlatformImage pushes a random image with the given media type to
// repo:tag and returns its descriptor, as copa records it after patching.
func pushPlatformImage(t *testing.T, repo, tag string, mt v1types.MediaType, arch string, withPlatform bool) (reference.Named, ocispec.Descriptor) {
	t.Helper()

	img, err := random.Image(256, 1)
	require.NoError(t, err)
	img, err = mutate.ConfigFile(mutate.MediaType(img, mt), &v1.ConfigFile{OS: "linux", Architecture: arch})
	require.NoError(t, err)

	ref, err := name.ParseReference(repo + ":" + tag)
	require.NoError(t, err)
	require.NoError(t, remote.Write(ref, img))

	hash, err := img.Digest()
	require.NoError(t, err)
	size, err := img.Size()
	require.NoError(t, err)
	named, err := reference.ParseNamed(repo + ":" + tag)
	require.NoError(t, err)

	if !withPlatform {
		return named, ocispec.Descriptor{
			Digest:      digest.Digest(hash.String()),
			Size:        size,
			Annotations: map[string]string{annotationCreated: stalePlatformTimestamp},
		}
	}
	return named, ocispec.Descriptor{
		MediaType:   string(mt),
		Digest:      digest.Digest(hash.String()),
		Size:        size,
		Platform:    &ocispec.Platform{OS: "linux", Architecture: arch},
		Annotations: map[string]string{annotationCreated: stalePlatformTimestamp},
	}
}

func fetchIndex(t *testing.T, ref string) (v1types.MediaType, *v1.IndexManifest) {
	t.Helper()

	parsed, err := name.ParseReference(ref)
	require.NoError(t, err)
	desc, err := remote.Get(parsed)
	require.NoError(t, err)
	idx, err := desc.ImageIndex()
	require.NoError(t, err)
	manifest, err := idx.IndexManifest()
	require.NoError(t, err)
	return desc.MediaType, manifest
}

func TestCreateMultiPlatformManifest(t *testing.T) {
	tests := []struct {
		name         string
		mediaType    v1types.MediaType
		withPlatform bool
		wantIndex    v1types.MediaType
		wantAnnotate bool
	}{
		{
			name:         "OCI manifests produce an annotated OCI index",
			mediaType:    v1types.OCIManifestSchema1,
			withPlatform: true,
			wantIndex:    v1types.OCIImageIndex,
			wantAnnotate: true,
		},
		{
			name:         "Docker manifests produce a Docker manifest list without annotations",
			mediaType:    v1types.DockerManifestSchema2,
			withPlatform: true,
			wantIndex:    v1types.DockerManifestList,
		},
		{
			name:         "missing media type and platform are read from the registry",
			mediaType:    v1types.OCIManifestSchema1,
			wantIndex:    v1types.OCIImageIndex,
			wantAnnotate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(registry.New())
			t.Cleanup(server.Close)
			repo := strings.TrimPrefix(server.URL, "http://") + "/library/app"

			// The original multi-platform image, whose index annotations carry over.
			original, ok := mutate.Annotations(empty.Index, map[string]string{
				annotationVersion:  "1.0.0",
				"org.example.kept": "yes",
			}).(v1.ImageIndex)
			require.True(t, ok)
			originalRef, err := name.ParseReference(repo + ":1.0.0")
			require.NoError(t, err)
			require.NoError(t, remote.WriteIndex(originalRef, original))
			originalNamed, err := reference.ParseNamed(repo + ":1.0.0")
			require.NoError(t, err)

			patchedAMD64, amd64Desc := pushPlatformImage(t, repo, "1.0.0-patched-amd64", tt.mediaType, "amd64", tt.withPlatform)
			preservedARM64, arm64Desc := pushPlatformImage(t, repo, "1.0.0-arm64", tt.mediaType, "arm64", tt.withPlatform)

			target, err := reference.ParseNamed(repo + ":1.0.0-patched")
			require.NoError(t, err)
			targetTagged, ok := target.(reference.NamedTagged)
			require.True(t, ok)

			err = createMultiPlatformManifest(context.Background(), targetTagged, []types.PatchResult{
				{OriginalRef: originalNamed, PatchedRef: patchedAMD64, PatchedDesc: &amd64Desc},
				{OriginalRef: preservedARM64, PatchedRef: preservedARM64, PatchedDesc: &arm64Desc},
				// A duplicate digest must collapse into one index entry.
				{OriginalRef: preservedARM64, PatchedRef: preservedARM64, PatchedDesc: &arm64Desc},
			}, repo+":1.0.0")
			require.NoError(t, err)

			mt, index := fetchIndex(t, repo+":1.0.0-patched")
			assert.Equal(t, tt.wantIndex, mt)
			require.Len(t, index.Manifests, 2)

			byArch := map[string]v1.Descriptor{}
			for _, m := range index.Manifests {
				require.NotNil(t, m.Platform)
				assert.Equal(t, tt.mediaType, m.MediaType)
				byArch[m.Platform.Architecture] = m
			}
			assert.Equal(t, amd64Desc.Digest.String(), byArch["amd64"].Digest.String())
			assert.Equal(t, arm64Desc.Digest.String(), byArch["arm64"].Digest.String())

			// Preserved platforms always keep their original timestamp.
			assert.Equal(t, stalePlatformTimestamp, byArch["arm64"].Annotations[annotationCreated])

			if !tt.wantAnnotate {
				assert.Empty(t, index.Annotations)
				assert.Equal(t, stalePlatformTimestamp, byArch["amd64"].Annotations[annotationCreated])
				return
			}
			assert.Equal(t, "1.0.0-patched", index.Annotations[annotationVersion])
			assert.Equal(t, "yes", index.Annotations["org.example.kept"])
			assert.NotEmpty(t, index.Annotations[copaAnnotationKeyPrefix+".patched"])
			assert.NotEmpty(t, index.Annotations[annotationCreated])
			assert.NotEqual(t, stalePlatformTimestamp, byArch["amd64"].Annotations[annotationCreated])
		})
	}
}
