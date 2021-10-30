package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/calyptia/cloud-cli/auth0"
	cloudclient "github.com/calyptia/cloud-cli/cloud"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		cloudURLStr   = env("CALYPTIA_CLOUD_URL", "https://cloud-api.calyptia.com")
		auth0Domain   = env("AUTH0_DOMAIN", "sso.calyptia.com")
		auth0ClientID = os.Getenv("AUTH0_CLIENT_ID")
		auth0Audience = env("AUTH0_AUDIENCE", "https://config.calyptia.com")
	)

	fs := flag.NewFlagSet("calyptia", flag.ExitOnError)
	fs.StringVar(&cloudURLStr, "calyptia-cloud-url", cloudURLStr, "Calyptia Cloud URL origin")
	fs.StringVar(&auth0Domain, "auth0-domain", auth0Domain, "Auth0 domain")
	fs.StringVar(&auth0ClientID, "auth0-client-id", auth0ClientID, "Auth0 client ID") // TODO: setup auth0 at build time.
	fs.StringVar(&auth0Audience, "auth0-audience", auth0Audience, "Auth0 audiience")
	err := fs.Parse(os.Args[1:])
	if err != nil {
		return fmt.Errorf("could not parse flags: %w", err)
	}

	cloudURL, err := url.Parse(cloudURLStr)
	if err != nil {
		return fmt.Errorf("could not parse calyptia cloud url: %w", err)
	}

	if cloudURL.Scheme != "https" && cloudURL.Scheme != "http" {
		return fmt.Errorf("invalid calyptia cloud url scheme: %q", cloudURL.Scheme)
	}

	accessToken, err := localAccessToken()
	if err != nil {
		return err
	}

	refreshToken, err := localRefreshToken()
	if err != nil {
		return err
	}

	m := model{
		ctx:     context.Background(),
		keys:    keys,
		help:    help.NewModel(),
		spinner: spinner.NewModel(),

		auth0: &auth0.Client{
			HTTPClient: http.DefaultClient,
			Domain:     auth0Domain,
			ClientID:   auth0ClientID,
			Audience:   auth0Audience,
		},
		cloud: &cloudclient.Client{
			HTTPClient:  http.DefaultClient,
			BaseURL:     cloudURL,
			AccessToken: accessToken,
		},
		refreshToken: refreshToken,
	}
	m.spinner.Spinner = spinner.Dot
	m.spinner.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	if accessToken != "" {
		m.keys.Logout.SetEnabled(true)
		m.refreshToken = refreshToken
		m.fetchingProjects = true
	} else {
		m.requestingDeviceCode = true
	}

	p := tea.NewProgram(m)
	p.EnterAltScreen()
	return p.Start()
}

func env(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}