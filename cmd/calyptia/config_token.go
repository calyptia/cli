package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/99designs/keyring"
	"github.com/spf13/cobra"
)

const (
	serviceName   = "cloud.calyptia.com"
	tokenFileName = "project_token"
)

var (
	errTokenNotFound = errors.New("token not found")
	errInvalidToken  = errors.New("invalid token")
)

func newCmdConfigSetToken(config *config) *cobra.Command {
	return &cobra.Command{
		Use:   "set_token TOKEN",
		Short: "Set the default project token so you don't have to specify it on all commands",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			token := args[0]
			_, err := decodeToken([]byte(token))
			if err != nil {
				return err
			}

			return config.saveToken(token)
		},
	}
}

func newCmdConfigCurrentToken(config *config) *cobra.Command {
	return &cobra.Command{
		Use:   "current_token",
		Short: "Get the current configured default project token",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), config.projectToken)
			return nil
		},
	}
}

func newCmdConfigUnsetToken(config *config) *cobra.Command {
	return &cobra.Command{
		Use:   "unset_token",
		Short: "Unset the current configured default project token",
		RunE: func(cmd *cobra.Command, args []string) error {
			return config.deleteSavedToken()
		},
	}
}

func (c *config) savedToken() (string, error) {
	v, err := c.ring.Get(tokenFileName)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return "", errTokenNotFound
	}

	if err != nil {
		return "", err
	}

	return string(v.Data), nil
}

func (c *config) saveToken(s string) error {
	return c.ring.Set(keyring.Item{
		Key:  tokenFileName,
		Data: []byte(s),
	})
}

func (c *config) deleteSavedToken() error {
	return c.ring.Remove(tokenFileName)
}

type projectTokenPayload struct {
	ProjectID string // no json tag
}

// decodeToken decodes a project token without verifying its signature
// and getting its inner project ID.
func decodeToken(token []byte) (string, error) {
	parts := bytes.Split(token, []byte("."))
	if len(parts) != 2 {
		return "", errInvalidToken
	}

	encodedPayload := parts[0]

	payload := make([]byte, base64.RawURLEncoding.DecodedLen(len(encodedPayload)))
	n, err := base64.RawURLEncoding.Decode(payload, encodedPayload)
	if err != nil {
		return "", errInvalidToken
	}

	payload = payload[:n]

	var out projectTokenPayload
	err = json.Unmarshal(payload, &out)
	if err != nil {
		return "", fmt.Errorf("could not json parse project token payload: %w", err)
	}

	return out.ProjectID, nil
}
