package report

import v1 "github.com/google/go-containerregistry/pkg/v1"

// The types below mirror the subset of Trivy's JSON report format
// (github.com/aquasecurity/trivy/pkg/types.Report) that the parser reads.
// Keeping them local avoids pulling the Trivy module into the build; field
// names match Trivy's so its JSON output decodes unchanged.

// trivyClassOSPkg is the result class Trivy uses for OS package findings.
const trivyClassOSPkg = "os-pkgs"

type trivyReport struct {
	SchemaVersion int           `json:",omitempty"`
	ArtifactName  string        `json:",omitempty"`
	Metadata      trivyMetadata `json:",omitzero"`
	Results       []trivyResult `json:",omitempty"`
}

type trivyMetadata struct {
	OS          *trivyOS      `json:",omitempty"`
	ImageConfig v1.ConfigFile `json:",omitzero"`
}

type trivyOS struct {
	Family string
	Name   string
}

type trivyResult struct {
	Target          string
	Class           string               `json:",omitempty"`
	Type            string               `json:",omitempty"`
	Vulnerabilities []trivyVulnerability `json:",omitempty"`
}

type trivyVulnerability struct {
	VulnerabilityID  string
	PkgID            string `json:",omitempty"`
	PkgName          string
	PkgPath          string `json:",omitempty"`
	InstalledVersion string `json:",omitempty"`
	FixedVersion     string `json:",omitempty"`
}
