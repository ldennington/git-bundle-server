package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/git-ecosystem/git-bundle-server/internal/bundles"
	"github.com/git-ecosystem/git-bundle-server/internal/cmd"
	"github.com/git-ecosystem/git-bundle-server/internal/common"
	"github.com/git-ecosystem/git-bundle-server/internal/core"
	"github.com/git-ecosystem/git-bundle-server/internal/git"
	"github.com/git-ecosystem/git-bundle-server/internal/log"
)

type bundleWebServer struct {
	logger             log.TraceLogger
	server             *http.Server
	serverWaitGroup    *sync.WaitGroup
	listenAndServeFunc func() error
}

func NewBundleWebServer(logger log.TraceLogger,
	port string,
	certFile string, keyFile string,
	tlsMinVersion uint16,
	clientCAFile string,
) (*bundleWebServer, error) {
	bundleServer := &bundleWebServer{
		logger:          logger,
		serverWaitGroup: &sync.WaitGroup{},
	}

	// Configure the http.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/", bundleServer.serve)
	bundleServer.server = &http.Server{
		Handler: mux,
		Addr:    ":" + port,
	}

	// No TLS configuration to be done, return
	if certFile == "" {
		bundleServer.listenAndServeFunc = func() error { return bundleServer.server.ListenAndServe() }
		return bundleServer, nil
	}

	// Configure for TLS
	tlsConfig := &tls.Config{
		MinVersion: tlsMinVersion,
	}
	bundleServer.server.TLSConfig = tlsConfig
	bundleServer.listenAndServeFunc = func() error { return bundleServer.server.ListenAndServeTLS(certFile, keyFile) }

	if clientCAFile != "" {
		caBytes, err := os.ReadFile(clientCAFile)
		if err != nil {
			return nil, err
		}
		certPool := x509.NewCertPool()
		certPool.AppendCertsFromPEM(caBytes)
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		tlsConfig.ClientCAs = certPool
	}

	return bundleServer, nil
}

func (b *bundleWebServer) parseRoute(ctx context.Context, path string) (string, string, string, error) {
	elements := strings.FieldsFunc(path, func(char rune) bool { return char == '/' })
	switch len(elements) {
	case 0:
		return "", "", "", fmt.Errorf("empty route")
	case 1:
		return "", "", "", fmt.Errorf("route has owner, but no repo")
	case 2:
		return elements[0], elements[1], "", nil
	case 3:
		return elements[0], elements[1], elements[2], nil
	default:
		return "", "", "", fmt.Errorf("path has depth exceeding three")
	}
}

func (b *bundleWebServer) serve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ctx, exitRegion := b.logger.Region(ctx, "http", "serve")
	defer exitRegion()

	path := r.URL.Path
	owner, repo, filename, err := b.parseRoute(ctx, path)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Printf("Failed to parse route: %s\n", err)
		return
	}

	route := owner + "/" + repo

	userProvider := common.NewUserProvider()
	fileSystem := common.NewFileSystem()
	commandExecutor := cmd.NewCommandExecutor(b.logger)
	gitHelper := git.NewGitHelper(b.logger, commandExecutor)
	repoProvider := core.NewRepositoryProvider(b.logger, userProvider, fileSystem, gitHelper)

	repos, err := repoProvider.GetRepositories(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Printf("Failed to load routes\n")
		return
	}

	repository, contains := repos[route]
	if !contains {
		w.WriteHeader(http.StatusNotFound)
		fmt.Printf("Failed to get route out of repos\n")
		return
	}

	var fileToServe string
	if filename == "" {
		if path[len(path)-1] == '/' {
			// Trailing slash, so the bundle URIs should be relative to the
			// request's URL as if it were a directory
			fileToServe = filepath.Join(repository.WebDir, bundles.BundleListFilename)
		} else {
			// No trailing slash, so the bundle URIs should be relative to the
			// request's URL as if it were a file
			fileToServe = filepath.Join(repository.WebDir, bundles.RepoBundleListFilename)
		}
	} else if filename == bundles.BundleListFilename || filename == bundles.RepoBundleListFilename {
		// If the request identifies a non-bundle "reserved" file, return 404
		w.WriteHeader(http.StatusNotFound)
		fmt.Printf("Failed to open file\n")
		return
	} else {
		fileToServe = filepath.Join(repository.WebDir, filename)
	}

	file, err := os.OpenFile(fileToServe, os.O_RDONLY, 0)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Printf("Failed to open file\n")
		return
	}

	fmt.Printf("Successfully serving content for %s/%s\n", route, filename)
	http.ServeContent(w, r, filename, time.UnixMicro(0), file)
}

func (b *bundleWebServer) StartServerAsync(ctx context.Context) {
	// Add to wait group
	b.serverWaitGroup.Add(1)

	go func(ctx context.Context) {
		defer b.serverWaitGroup.Done()

		// Return error unless it indicates graceful shutdown
		err := b.listenAndServeFunc()
		if err != nil && err != http.ErrServerClosed {
			b.logger.Fatal(ctx, err)
		}
	}(ctx)

	fmt.Println("Server is running at address " + b.server.Addr)
}

func (b *bundleWebServer) HandleSignalsAsync(ctx context.Context) {
	// Intercept interrupt signals
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	go func(ctx context.Context) {
		<-c
		fmt.Println("Starting graceful server shutdown...")
		b.server.Shutdown(ctx)
	}(ctx)
}

func (b *bundleWebServer) Wait() {
	b.serverWaitGroup.Wait()
}
