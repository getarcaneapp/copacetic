package types

import (
	"time"

	"github.com/moby/buildkit/util/progress/progressui"

	"getarcane.dev/copacetic/internal/ocilayout"
)

// Options contains common copacetic options.
type Options struct {
	// Core single image patch configuration
	Image         string
	Report        string
	PatchedTag    string
	Suffix        string
	ChiselRelease string

	// Working environment
	WorkingFolder string
	Timeout       time.Duration

	// Scanner and output
	Scanner     string
	IgnoreError bool

	// Output configuration
	Format   string
	Output   string
	Progress progressui.DisplayMode

	// Buildkit connection options
	BkAddr       string
	BkCACertPath string
	BkCertPath   string
	BkKeyPath    string

	// Platform and push
	Push             bool
	Platforms        []string
	Loader           string
	InputOCILayout   string
	OCIDir           string
	Compression      string
	ForceCompression bool
	OCISource        *ocilayout.Source

	// Package types and library patch level
	PkgTypes          string
	LibraryPatchLevel string // empty when --library-patch-level isn't set

	// Toolchain patch level (e.g., Go stdlib upgrade)
	ToolchainPatchLevel string
	GoVCSURL            string

	// EOL configuration
	EOLAPIBaseURL string
	ExitOnEOL     bool
}
