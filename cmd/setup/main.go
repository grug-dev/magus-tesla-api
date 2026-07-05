// Command setup runs the one-time Tesla Fleet API OAuth flow.
// It covers Steps 1 and 6 (a, b, c) from docs/post-registration-setup.md.
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/auth"
	"github.com/cristianpena/magus-tesla-api/internal/config"
	"github.com/cristianpena/magus-tesla-api/internal/server"
)

func main() {
	// Step 1 — Load TESLA_CLIENT_ID and TESLA_CLIENT_SECRET from .env.
	fmt.Println("[Step 1] Loading credentials from .env...")
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed: %v", err)
	}
	fmt.Println("[Step 1] Credentials loaded.")

	// Step 6a — Build the Tesla OAuth URL and open it in the browser.
	fmt.Println("[Step 6a] Opening Tesla authorization page in your browser...")
	authURL := auth.BuildAuthURL(cfg.ClientID, cfg.RedirectURI)
	fmt.Printf("If the browser does not open automatically, paste this URL:\n%s\n\n", authURL)

	if err := auth.OpenBrowser(authURL); err != nil {
		fmt.Printf("Could not open browser automatically: %v\n", err)
	}

	// Step 6b — Start the Gin callback server and wait for Tesla to redirect with the code.
	fmt.Println("[Step 6b] Waiting for Tesla callback on http://localhost:8080/callback ...")
	codeCh := make(chan string, 1)
	srv := server.StartCallbackServer(codeCh)

	var code string
	select {
	case code = <-codeCh:
		fmt.Println("[Step 6b] Authorization code received.")
	case <-time.After(5 * time.Minute):
		log.Fatal("Timed out waiting for authorization code. Re-run setup to try again.")
	}

	server.Shutdown(srv)

	// Step 6c — Exchange the authorization code for an access token and refresh token.
	fmt.Println("[Step 6c] Exchanging authorization code for tokens...")
	tokens, err := auth.ExchangeCode(cfg.ClientID, cfg.ClientSecret, code, cfg.RedirectURI)
	if err != nil {
		log.Fatalf("Failed: %v", err)
	}

	if err := config.SaveTokens(tokens.AccessToken, tokens.RefreshToken); err != nil {
		log.Fatalf("Failed to save tokens to .env: %v", err)
	}

	fmt.Println("[Step 6c] Tokens saved to .env.")
	fmt.Printf("\nSetup complete! Access token expires in %d seconds.\n", tokens.ExpiresIn)
	fmt.Println("Your refresh token has been saved — it will be used automatically on future runs.")
}
