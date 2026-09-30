// Command stackchan-pet is a Tamagotchi for Stack-chan robots in Embody Mode.
//
// Robots connect to ws://<host>/api/workers/connect with a bearer token (by
// default the same token file as stackchan-server, so that server can offer
// this one to its robots). Kids open http://<host>/ after scanning the robot's
// QR code; parents open /parent and set a PIN.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mj41/stackchan-pet/internal/app"
)

func main() {
	var (
		listen    = flag.String("listen", ":8770", "HTTP listen address for robots and browsers")
		publicURL = flag.String("public-url", "", "base URL browsers use to reach this server (default: http://<LAN IP>:<port>)")
		tokenFile = flag.String("token-file", defaultConfigFile("stackchan-server", "robot-token"), "file with the robot bearer token; generated if missing")
		stateFile = flag.String("state-file", defaultStateFile(), "JSON file with pets and pairings (\"\" keeps them in memory only)")
		tz        = flag.String("tz", "", "the family's time zone for the schedule, e.g. Europe/Prague (default: this machine's)")
		uiDir     = flag.String("ui-dir", "", "development: serve the pages from this directory (e.g. internal/app/ui), so edits need only a reload")
		debug     = flag.Bool("debug", false, "debug logging")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	fail := func(msg string, err error) {
		log.Error(msg, "err", err)
		os.Exit(1)
	}

	token, err := loadOrCreateToken(*tokenFile, log)
	if err != nil {
		fail("robot token", err)
	}
	if *publicURL == "" {
		if *publicURL, err = defaultPublicURL(*listen); err != nil {
			fail("public URL", err)
		}
	}
	loc := time.Local
	if *tz != "" {
		if loc, err = time.LoadLocation(*tz); err != nil {
			fail("time zone", err)
		}
	}

	a := app.New(app.Config{
		RobotToken: token,
		PublicURL:  strings.TrimRight(*publicURL, "/"),
		StateFile:  *stateFile,
		UIDir:      *uiDir,
		Location:   loc,
		Log:        log,
	})
	srv := &http.Server{Addr: *listen, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go a.Run(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Info("stackchan-pet listening", "listen", *listen, "public_url", *publicURL, "state_file", *stateFile, "tz", loc.String())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fail("server", err)
	}
	if err := a.Save(); err != nil {
		log.Warn("state not saved", "err", err)
	}
}

// defaultStateFile follows the XDG base directory spec: $XDG_STATE_HOME or ~/.local/state.
func defaultStateFile() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "stackchan-pet", "state.json")
}

func defaultConfigFile(app, name string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return name
	}
	return filepath.Join(dir, app, name)
}

// loadOrCreateToken reads the robot token, generating a random one on first run.
func loadOrCreateToken(path string, log *slog.Logger) (string, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(b))
		if token == "" {
			return "", fmt.Errorf("%s is empty", path)
		}
		return token, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	token := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	log.Info("generated new robot token", "path", path)
	return token, nil
}

// defaultPublicURL builds http://<LAN IP>:<port> so a phone on the same network can open the QR link.
func defaultPublicURL(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("parse -listen %q: %w", listen, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = lanIP()
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

// lanIP returns the address of the interface used for outbound traffic (no packets are sent).
func lanIP() string {
	if c, err := net.Dial("udp4", "192.0.2.1:80"); err == nil {
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).IP.String()
	}
	return "127.0.0.1"
}
