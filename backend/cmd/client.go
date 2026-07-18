package cmd

import (
	cliintegration "github.com/msgmate-io/open-chat-go-golang-client/cliintegration"
	"github.com/urfave/cli/v3"
)

func GetClientCmd(action string) *cli.Command {
	return cliintegration.GetClientCmd(action)
}

func ClientCli() *cli.Command {
	return cliintegration.ClientCli()
}
