// Command offlineu is a self-hosted, offline course viewer and progress tracker.
//
// It turns any folder of offline video, audio, document and quiz material into a
// browsable course dashboard with automatic progress tracking. Everything stays
// on your machine: no cloud, no accounts, no telemetry.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nickkk333/offlineu/internal/offlineu"
)

// embeddedWeb carries the compiled Vue frontend inside the binary. The
// web/dist directory always exists in the repository (kept by a tracked
// placeholder), so the build works even before "npm run build" was executed.
//
//go:embed all:web/dist
var embeddedWeb embed.FS

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	cfg := offlineu.ConfigFromEnv()

	flags := flag.NewFlagSet("offlineu", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	host := flags.String("host", cfg.Host, "host to bind to (default: 127.0.0.1, env OFFLINEU_HOST)")
	port := flags.Int("port", cfg.Port, "port to bind to (default: 5000, env OFFLINEU_PORT)")
	debug := flags.Bool("debug", false, "enable verbose logging")
	webDir := flags.String("web-dir", "", "serve the frontend from this directory instead of the embedded build")
	checkWeb := flags.Bool("check-web", false, "verify that the bundled frontend assets are present")
	flags.BoolVar(checkWeb, "check-templates", false, "alias of --check-web (kept for compatibility)")
	flags.Usage = func() { printUsage(flags) }

	if err := flags.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	cfg.Host = *host
	cfg.Port = *port
	cfg.Debug = *debug
	cfg.RefreshRoots()

	webFS, err := resolveWeb(*webDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	_, bundleErr := fs.Stat(webFS, "index.html")

	if *checkWeb {
		if bundleErr != nil {
			fmt.Fprintln(os.Stderr, `Missing frontend bundle: run "cd web && npm install && npm run build".`)
			return 1
		}
		fmt.Println("Frontend assets OK: index.html found")
		if len(flags.Args()) == 0 {
			return 0
		}
	}

	app := offlineu.NewApp(cfg)

	if len(cfg.Roots) > 0 {
		fmt.Println("Browsing confined to OFFLINEU_ROOTS: " + strings.Join(cfg.Roots, string(os.PathListSeparator)))
	}
	if cfg.ProgressDir != "" {
		fmt.Println("Progress files go to " + cfg.ProgressDir)
	}

	coursePath := ""
	if args := flags.Args(); len(args) > 0 {
		coursePath = args[0]
	} else {
		coursePath = strings.TrimSpace(os.Getenv(offlineu.EnvAutoCourse))
	}
	return finishStartup(app, cfg, webFS, coursePath, bundleErr)
}

// finishStartup loads (or restores) a course and then runs the HTTP server.
func finishStartup(app *offlineu.App, cfg offlineu.Config, webFS fs.FS, coursePath string, bundleErr error) int {
	if coursePath != "" {
		candidate := filepath.Clean(offlineu.ExpandHome(coursePath))
		if info, err := os.Stat(candidate); err != nil || !info.IsDir() {
			fmt.Fprintf(os.Stderr, "Error: course path does not exist: %s\n", coursePath)
			return 1
		}
		if len(cfg.Roots) > 0 && !cfg.InsideRoots(candidate) {
			fmt.Fprintln(os.Stderr, "Warning: the course is outside OFFLINEU_ROOTS; its files will not be served.")
		}
		course, err := cfg.ScanCourse(candidate)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading course: %v\n", err)
			return 1
		}
		app.Store.Set(course)
		fmt.Printf("Auto-loaded course: %s (%d top-level folders)\n", course.Name, len(course.Root.Children))
	} else {
		// No course on the command line: bring back the one from last time, so a
		// restart does not look like "my course was deleted".
		if restored := app.Store.Restore(); restored != nil {
			fmt.Printf("Restored last course: %s (%s)\n", restored.Name, restored.Path)
		}
		if remembered := app.Store.RecentCourses(); len(remembered) > 0 {
			fmt.Printf("%d remembered course(s) available in the picker\n", len(remembered))
		}
	}

	fmt.Println("Course bookkeeping file: " + app.Store.StateFile())
	if bundleErr != nil {
		fmt.Println(`Note: the frontend bundle is missing; only the API is served. Run "cd web && npm run build".`)
	}

	return serve(app, cfg, webFS)
}

func serve(app *offlineu.App, cfg offlineu.Config, webFS fs.FS) int {
	server := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler:           app.Handler(webFS),
		ReadHeaderTimeout: 15 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	failed := make(chan error, 1)
	go func() {
		fmt.Printf("Starting OfflineU on http://%s:%d\n", cfg.Host, cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
	}()

	select {
	case err := <-failed:
		fmt.Fprintf(os.Stderr, "Error starting server: %v\n", err)
		return 1
	case <-ctx.Done():
		fmt.Println("\nShutting down OfflineU...")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	return 0
}

// resolveWeb decides where the frontend is loaded from: an explicit directory
// (--web-dir, useful during development) or the embedded build.
func resolveWeb(webDir string) (fs.FS, error) {
	if webDir != "" {
		info, err := os.Stat(webDir)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("web directory not found: %s", webDir)
		}
		return os.DirFS(webDir), nil
	}
	return fs.Sub(embeddedWeb, "web/dist")
}

func printUsage(flags *flag.FlagSet) {
	fmt.Fprint(os.Stderr, "OfflineU "+offlineu.Version+" - self-hosted offline course viewer & progress tracker\n\n"+
		"Usage:\n  offlineu [options] [course_path]\n\nOptions:\n")
	flags.PrintDefaults()
	fmt.Fprint(os.Stderr, "\nEnvironment:\n"+
		"  OFFLINEU_HOST           default value for --host\n"+
		"  OFFLINEU_PORT           default value for --port\n"+
		"  OFFLINEU_ROOTS          path-list separated allow-list of folders OfflineU may browse and serve\n"+
		"  OFFLINEU_PROGRESS_DIR   store progress files and the course list in this folder\n"+
		"  AUTO_LOAD_COURSE        load this course at startup when no path is given\n")
}
