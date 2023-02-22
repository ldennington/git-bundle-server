package main

import (
	"context"

	"github.com/github/git-bundle-server/internal/argparse"
	"github.com/github/git-bundle-server/internal/core"
)

type stopCmd struct{}

func NewStopCommand() argparse.Subcommand {
	return &stopCmd{}
}

func (stopCmd) Name() string {
	return "stop"
}

func (stopCmd) Description() string {
	return `
Stop computing bundles or serving content for the repository at the
specified '<route>'.`
}

func (stopCmd) Run(ctx context.Context, args []string) error {
	parser := argparse.NewArgParser("git-bundle-server stop <route>")
	route := parser.PositionalString("route", "the route for which bundles should stop being generated")
	parser.Parse(ctx, args)

	return core.RemoveRoute(*route)
}
