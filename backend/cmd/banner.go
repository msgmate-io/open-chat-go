package cmd

import (
	"fmt"

	"backend/api/metrics"

	ufcli "github.com/urfave/cli/v3"
)

// bannerArt is the large ASCII wordmark printed at the top of the CLI help and
// version output.
const bannerArt = `  ██████╗ ██████╗ ███████╗███╗   ██╗     ██████╗██╗  ██╗ █████╗ ████████╗
 ██╔═══██╗██╔══██╗██╔════╝████╗  ██║    ██╔════╝██║  ██║██╔══██╗╚══██╔══╝
 ██║   ██║██████╔╝█████╗  ██╔██╗ ██║    ██║     ███████║███████║   ██║
 ██║   ██║██╔═══╝ ██╔══╝  ██║╚██╗██║    ██║     ██╔══██║██╔══██║   ██║
 ╚██████╔╝██║     ███████╗██║ ╚████║    ╚██████╗██║  ██║██║  ██║   ██║
  ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═══╝     ╚═════╝╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝`

// BuildVersion is the canonical Open-Chat version embedded in the binary.
func BuildVersion() string {
	return metrics.VERSION
}

// Banner returns the CLI banner: the ASCII wordmark followed by the command
// description and the compiled integration profile and version.
func Banner() string {
	return fmt.Sprintf("%s\n\n  Open Chat command line interface\n  profile: %s    version: %s\n\n", bannerArt, BuildProfile, BuildVersion())
}

// RootHelpTemplate returns the root help template with the banner prepended so
// `open-chat` and `open-chat --help` open on the branded header.
func RootHelpTemplate() string {
	return Banner() + ufcli.RootCommandHelpTemplate
}

// PrintVersion renders the banner for `open-chat --version`.
func PrintVersion(c *ufcli.Command) {
	writer := c.Root().Writer
	if writer == nil {
		writer = c.Writer
	}
	_, _ = fmt.Fprint(writer, Banner())
}
