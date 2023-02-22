package main

import (
	"context"
	"fmt"

	"github.com/github/git-bundle-server/internal/argparse"
	"github.com/github/git-bundle-server/internal/bundles"
	"github.com/github/git-bundle-server/internal/core"
	"github.com/github/git-bundle-server/internal/git"
)

type initCmd struct{}

func NewInitCommand() argparse.Subcommand {
	return &initCmd{}
}

func (initCmd) Name() string {
	return "init"
}

func (initCmd) Description() string {
	return `
Initialize a repository by cloning a bare repo from '<url>', whose bundles
should be hosted at '<route>'.`
}

func (initCmd) Run(ctx context.Context, args []string) error {
	parser := argparse.NewArgParser("git-bundle-server init <url> <route>")
	url := parser.PositionalString("url", "the URL of a repository to clone")
	// TODO: allow parsing <route> out of <url>
	route := parser.PositionalString("route", "the route to host the specified repo")
	parser.Parse(ctx, args)

	repo, err := core.CreateRepository(*route)
	if err != nil {
		return err
	}

	fmt.Printf("Cloning repository from %s\n", *url)
	gitErr := git.GitCommand("clone", "--bare", *url, repo.RepoDir)

	if gitErr != nil {
		return fmt.Errorf("failed to clone repository: %w", gitErr)
	}

	gitErr = git.GitCommand("-C", repo.RepoDir, "config", "remote.origin.fetch", "+refs/heads/*:refs/heads/*")
	if gitErr != nil {
		return fmt.Errorf("failed to configure refspec: %w", gitErr)
	}

	gitErr = git.GitCommand("-C", repo.RepoDir, "fetch", "origin")
	if gitErr != nil {
		return fmt.Errorf("failed to fetch latest refs: %w", gitErr)
	}

	bundle := bundles.CreateInitialBundle(repo)
	fmt.Printf("Constructing base bundle file at %s\n", bundle.Filename)

	written, gitErr := git.CreateBundle(repo.RepoDir, bundle.Filename)
	if gitErr != nil {
		return fmt.Errorf("failed to create bundle: %w", gitErr)
	}
	if !written {
		return fmt.Errorf("refused to write empty bundle. Is the repo empty?")
	}

	list := bundles.CreateSingletonList(bundle)
	listErr := bundles.WriteBundleList(list, repo)
	if listErr != nil {
		return fmt.Errorf("failed to write bundle list: %w", listErr)
	}

	SetCronSchedule()

	return nil
}
