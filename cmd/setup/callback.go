// callback.go is the one-shot Gin server that catches the Tesla OAuth redirect
// during setup. It covers Step 6b of docs/post-registration-setup.md — receive
// the authorization code from Tesla's redirect.
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// startCallbackServer starts a Gin server on :8080 and listens for the OAuth
// callback. It sends the authorization code to codeCh and shuts itself down
// afterward. Returns the *http.Server so the caller can shut it down gracefully.
func startCallbackServer(codeCh chan<- string) *http.Server {
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
