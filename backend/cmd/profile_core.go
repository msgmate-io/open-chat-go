//go:build coreonly

package cmd

// BuildProfile is the integration profile this binary was compiled for; the
// `coreonly` build tag is set by the integration tooling for the public
// `core-only` profile.
const BuildProfile = "core"
