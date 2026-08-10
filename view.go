package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/AlvinKuruvilla/clockdiff/internal/report"
)

// The viewer, built from ./viewer and committed so that `go install` — which
// builds from the module proxy, and so sees only what is checked in — still
// produces a binary that can serve it.
//
//go:embed all:viewer/dist
var viewerAssets embed.FS

// runFileRoute is where the viewer fetches its run. The built assets contain a
// fixture at this same path, from the dev server's public directory, so this
// route has to be registered explicitly: ServeMux prefers the exact pattern
// over the "/" subtree, and that precedence is what stops the fixture being
// served in place of the user's run.
const runFileRoute = "/run.json"

func newViewCommand() *cobra.Command {
	var (
		addr        string
		openBrowser bool
	)

	cmd := &cobra.Command{
		Use:   "view <run.json>",
		Short: "Open a run file in the timeline viewer",
		Long: "Open a run file in the timeline viewer.\n\n" +
			"Reads the file written by `clockdiff up --export-json`. The run is\n" +
			"re-read on each request, so re-running the stack and refreshing the\n" +
			"page shows the new measurements.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			// Fail on an unreadable or wrong-version file before binding a
			// port and opening a browser onto an error page.
			if err := checkRunFile(path); err != nil {
				return err
			}

			assets, err := fs.Sub(viewerAssets, "viewer/dist")
			if err != nil {
				return err
			}

			mux := http.NewServeMux()
			mux.Handle("/", http.FileServer(http.FS(assets)))
			mux.HandleFunc(runFileRoute, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				// No-store, because the point of refreshing is to pick up a
				// newer run, and a cached copy of the old one defeats that.
				w.Header().Set("Cache-Control", "no-store")
				http.ServeFile(w, r, path)
			})

			listener, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}

			url := fmt.Sprintf("http://%s/", listener.Addr().String())
			fmt.Fprintf(cmd.OutOrStdout(), "serving %s at %s\n", path, url)
			fmt.Fprintln(cmd.OutOrStdout(), "press ctrl-c to stop")

			if openBrowser {
				// A browser that will not open is not a reason to fail: the
				// URL is already printed and can be pasted.
				if err := launchBrowser(url); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"could not open a browser (%v); open the URL above\n", err)
				}
			}

			return serveUntilCancelled(cmd.Context(), listener, mux)
		},
	}

	cmd.Flags().StringVar(&addr, "http", "localhost:0",
		"address to serve on; port 0 picks a free one")
	cmd.Flags().BoolVar(&openBrowser, "browser", true,
		"open the viewer in a browser")

	return cmd
}

// serveUntilCancelled runs the server until the command's context ends, which
// is what turns ctrl-c into a clean shutdown rather than a killed process.
func serveUntilCancelled(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{
		Handler: handler,
		// The viewer fetches one small file; anything slower than this is a
		// stuck client rather than a slow one.
		ReadHeaderTimeout: 10 * time.Second,
	}

	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()

	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

// checkRunFile reports why a file cannot be viewed, before anything is served.
//
// Only the envelope is checked. A file that decodes and carries a version this
// viewer reads is the viewer's problem from here; the point is to catch the
// wrong path and the wrong format, which are the two mistakes that otherwise
// surface as a blank page.
func checkRunFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var doc struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s is not a run file: %w", path, err)
	}
	if doc.Version != report.FormatVersion {
		return fmt.Errorf(
			"%s is format version %d, and this viewer reads version %d",
			path, doc.Version, report.FormatVersion)
	}
	return nil
}

// launchBrowser opens url with whatever the platform uses for the job.
func launchBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
