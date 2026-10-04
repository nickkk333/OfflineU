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
	capCheck := flags.Bool("cap-check", false, "report whether the read capability (CAP_DAC_OVERRIDE) is active, then exit")
	flags.BoolVar(checkWeb, "check-templates", false, "alias of --check-web (kept for compatibility)")
	flags.Usage = func() { printUsage(flags) }

	if err := flags.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *capCheck {
		// /entrypoint.sh probes the copy of the binary that carries the file
		// capability before it starts it: a container whose bounding set lacks
		// CAP_DAC_OVERRIDE refuses that exec with EPERM, and the entrypoint then
		// falls back to the plain copy instead of dying on startup.
		readable, known := offlineu.ReadCapability()
		switch {
		case !known:
			fmt.Fprintln(os.Stderr, "Read capability: /proc/self/status is missing (not a Linux container)")
			return 1
		case readable:
			fmt.Println("Read capability: CAP_DAC_OVERRIDE is active")
			return 0
		default:
			fmt.Fprintln(os.Stderr, "Read capability: CAP_DAC_OVERRIDE is not active")
			return 1
		}
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
	if len(cfg.Roots) > 0 {
		fmt.Println("Course folders:")
		for _, status := range cfg.RootStatuses() {
			fmt.Printf("  %s -> %s\n", status.Path, status.Summary())
		}
	} else if raw := strings.TrimSpace(os.Getenv(offlineu.EnvRoots)); raw != "" {
		fmt.Printf("Warning: %s=%q resolved to no existing folder, so browsing is unrestricted.\n", offlineu.EnvRoots, raw)
	}
	// Say which copy of the binary is running, so a "mapped, but not readable"
	// report can be traced back: /app/offlineu-cap carries CAP_DAC_OVERRIDE and
	// reads folders that belong to root, the plain copy does not.
	if readable, known := offlineu.ReadCapability(); known {
		if readable {
			fmt.Println("Read access: CAP_DAC_OVERRIDE is active - a mapped folder is readable even when it belongs to root.")
		} else {
			fmt.Println("Read access: no CAP_DAC_OVERRIDE in this container (dropped, or no-new-privileges); a folder only root may read is reported as unreadable.")
		}
	}
	switch cfg.MountIssue() {
	case offlineu.MountIssueVolume:
		fmt.Println("\nWarning: a Docker managed volume is mounted instead of your folder.")
		fmt.Println("Map a host folder onto the container path (host folder -> " + strings.Join(cfg.Roots, " / ") + ") and recreate the container.")
		fmt.Println(offlineu.MountHint)
	case offlineu.MountIssueNotMounted:
		fmt.Println("\nWarning: nothing is mounted on the configured folder(s).")
		fmt.Println("Check that the mapping's container path is exactly " + strings.Join(cfg.Roots, string(os.PathListSeparator)) + ".")
		fmt.Println(offlineu.MountHint)
	case offlineu.MountIssueEmpty:
		fmt.Println("\nWarning: the folder(s) above are mapped but still empty.")
		fmt.Println("Copy your course files into the host folder and reload the page - no restart is needed.")
	case offlineu.MountIssueUnreadable:
		fmt.Println("\nWarning: the folder(s) above are mapped but cannot be read by " + offlineu.RunAsUser() + ".")
		fmt.Println("The image normally reads any mapped folder through the CAP_DAC_OVERRIDE of /app/offlineu-cap;")
		fmt.Println("this container does not provide it (no-new-privileges, --cap-drop DAC_OVERRIDE/ALL, or a")
		fmt.Println("network share that judges permissions itself). Fix the host folder permissions")
		fmt.Println("(chmod -R a+rX <host folder>), run the container as the folder's owner")
		fmt.Println("(user: \"1000:1000\"), or recreate it with --user 0:0 to run as root. The user can only be set when")
		fmt.Println("the container is created - nothing inside it can become root.")
	case offlineu.MountIssueMissing:
		fmt.Println("\nWarning: the configured folder(s) do not exist inside the container.")
		fmt.Println("Check that the mapping's container path is exactly " + strings.Join(cfg.Roots, string(os.PathListSeparator)) + ".")
		fmt.Println(offlineu.MountHint)
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
		"  OFFLINEU_ROOTS_LABEL    friendly name shown in the UI instead of the mounted folder path\n"+
		"  OFFLINEU_PROGRESS_DIR   store progress files and the course list in this folder\n"+
		"  OFFLINEU_DLNA           set to off to hide casting (DLNA/UPnP) entirely\n"+
		"  OFFLINEU_FFMPEG         path of ffmpeg (casting converts a file a device would refuse)\n"+
		"  OFFLINEU_FFPROBE        path of ffprobe (defaults to the one next to ffmpeg)\n"+
		"  AUTO_LOAD_COURSE        load this course at startup when no path is given\n")
}
