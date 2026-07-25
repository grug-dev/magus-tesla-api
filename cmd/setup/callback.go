// callback.go is the one-shot Gin server that catches the Tesla OAuth redirect
// during setup. It covers Step 6b of docs/post-registration-setup.md — receive
// the authorization code from Tesla's redirect.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// freePort kills whatever process currently holds :8080 so the one-shot callback
// server can bind it. This is intentionally aggressive: on a dev machine :8080 is
// almost certainly the magus web server (`cmd/web`) the user left running, and if
// it stays up it intercepts Tesla's redirect — its `TeslaCallback` handler has no
// session `tesla_state` (setup is a separate process) and rejects the callback
// with "invalid oauth state". freePort makes `go run ./cmd/setup` self-sufficient:
// no need to manually stop the web server first. It logs what it killed. If lsof
// is absent or the port is free, it does nothing.
func freePort(port string) {
	out, err := exec.Command("lsof", "-ti", ":"+port).Output()
	if err != nil {
		// lsof returns exit 1 when nothing holds the port; not an error for us.
		return
	}
	pids := strings.Fields(strings.TrimSpace(string(out)))
	if len(pids) == 0 {
		return
	}
	fmt.Printf("[setup] freeing :%s — killing process(es) holding the port: %s\n", port, strings.Join(pids, " "))
	for _, pid := range pids {
		// kill is portable on macOS/Linux; on Windows lsof is absent, so we never reach here.
		_ = exec.Command("kill", pid).Run()
	}
	// Give the OS a moment to release the socket so the subsequent ListenAndServe
	// does not race the killed process's teardown.
	time.Sleep(200 * time.Millisecond)
}

// startCallbackServer starts a Gin server on :8080 and listens for the OAuth
// callback. It sends the authorization code to codeCh and shuts itself down
// afterward. Returns the *http.Server so the caller can shut it down gracefully.
func startCallbackServer(codeCh chan<- string) *http.Server {
	freePort("8080")
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	srv := &http.Server{
		Addr:    ":8080",
		Handler: router,
	}

	// Step 6b — Tesla redirects here after the user approves the consent screen.
	// The path must match the redirect_uri registered in internal/config
	// (http://localhost:8080/connect/tesla/callback), otherwise Tesla's
	// redirect 404s and the code never reaches the channel.
	router.GET("/connect/tesla/callback", func(c *gin.Context) {
		code := c.Query("code")
		if code == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing authorization code"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Authorization successful! You can close this tab."})

		// Send the code to main and let it handle shutdown.
		go func() { codeCh <- code }()
	})

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()

	return srv
}

// shutdown gracefully stops the callback server.
func shutdown(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
