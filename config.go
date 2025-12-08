// config.go

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"time"

	// infisical
	infisical "github.com/infisical/go-sdk"
	"github.com/infisical/go-sdk/packages/models"

	// others
	"github.com/tailscale/hujson"
)

// struct for configuration
type config struct {
	// configurations
	AllowedIDs      []string `json:"allowed_ids"`
	MonitorInterval int      `json:"monitor_interval"`

	// d2 rendering style
	ThemeID int64 `json:"theme_id,omitempty"` // NOTE: pick `ID` from https://github.com/terrastruct/d2/tree/master/d2themes/d2themescatalog
	Sketch  bool  `json:"sketch,omitempty"`

	// logging
	IsVerbose bool `json:"is_verbose,omitempty"`

	// Bot API token
	BotToken string `json:"bot_token,omitempty"`

	// or Infisical settings
	Infisical *struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`

		ProjectID   string `json:"project_id"`
		Environment string `json:"environment"`
		SecretType  string `json:"secret_type"`

		BotTokenKeyPath string `json:"bot_token_key_path"`
	} `json:"infisical,omitempty"`
}

// read config file
func loadConfig(
	ctx context.Context,
	filepath string,
) (conf config, err error) {
	var bytes []byte
	if bytes, err = os.ReadFile(filepath); err == nil {
		if bytes, err = standardizeJSON(bytes); err == nil {
			if err = json.Unmarshal(bytes, &conf); err == nil {
				if conf.BotToken == "" && conf.Infisical != nil {
					ctxInfisical, cancelInfisical := context.WithTimeout(ctx, requestTimeoutSeconds*time.Second)
					defer cancelInfisical()

					// read bot token from infisical
					client := infisical.NewInfisicalClient(
						ctxInfisical,
						infisical.Config{
							SiteUrl: "https://app.infisical.com",
						},
					)

					_, err = client.Auth().UniversalAuthLogin(
						conf.Infisical.ClientID,
						conf.Infisical.ClientSecret,
					)
					if err != nil {
						return config{}, fmt.Errorf("failed to authenticate with Infisical: %s", err)
					}

					keyPath := conf.Infisical.BotTokenKeyPath

					var secret models.Secret
					secret, err = client.Secrets().Retrieve(infisical.RetrieveSecretOptions{
						ProjectID:   conf.Infisical.ProjectID,
						Type:        conf.Infisical.SecretType,
						Environment: conf.Infisical.Environment,
						SecretPath:  path.Dir(keyPath),
						SecretKey:   path.Base(keyPath),
					})
					if err != nil {
						return config{}, fmt.Errorf("failed to retrieve telegram bot token from Infisical: %s", err)
					}

					conf.BotToken = secret.SecretValue
				}
			}
		}
	}

	return conf, err
}

// standardize given JSON (JWCC) bytes
func standardizeJSON(b []byte) ([]byte, error) {
	ast, err := hujson.Parse(b)
	if err != nil {
		return b, err
	}
	ast.Standardize()

	return ast.Pack(), nil
}
