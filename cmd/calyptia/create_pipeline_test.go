package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calyptia/api/types"
)

func Test_newCmdCreatePipeline(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	configFile := setupFile(t, "fluent-bit-*.conf", []byte(`TEST CONFIG`))
	sharedFile := setupFile(t, "shared-*.conf", []byte(`TEST FILE`))
	secretFile := setupFile(t, "secrets-*.env", []byte(`FOO=BAR`))

	got := &bytes.Buffer{}
	mock := &ClientMock{
		AggregatorsFunc: func(ctx context.Context, projectID string, params types.AggregatorsParams) (types.Aggregators, error) {
			return types.Aggregators{
				Items: []types.Aggregator{{
					ID: "want_aggregator",
				}},
			}, nil
		},
		CreatePipelineFunc: func(ctx context.Context, aggregatorID string, payload types.CreatePipeline) (types.CreatedPipeline, error) {
			return types.CreatedPipeline{
				ID:        "want_pipeline_id",
				Name:      "want_name",
				CreatedAt: now,
			}, nil
		},
	}
	cmd := newCmdCreatePipeline(configWithMock(mock))
	cmd.SetOut(got)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--aggregator", "want_aggregator",
		"--name", "want_name",
		"--replicas", "33",
		"--config-file", configFile.Name(),
		"--file", sharedFile.Name(),
		"--secrets-file", secretFile.Name(),
		"--metadata", "foo:bar",
	})

	wantEq(t, nil, cmd.Execute())
	wantEq(t, ""+
		"ID               NAME      AGE\n"+
		"want_pipeline_id want_name Just now\n", got.String())

	calls := mock.CreatePipelineCalls()
	wantEq(t, 1, len(calls))

	call := calls[0]
	wantEq(t, "want_aggregator", call.AggregatorID)
	wantEq(t, "want_name", call.Payload.Name)
	wantEq(t, uint64(33), call.Payload.ReplicasCount)
	wantEq(t, 1, len(call.Payload.Files))
	wantEq(t, "TEST CONFIG", call.Payload.RawConfig)
	wantEq(t, strings.TrimSuffix(filepath.Base(sharedFile.Name()), filepath.Ext(sharedFile.Name())), call.Payload.Files[0].Name)
	wantEq(t, []byte(`TEST FILE`), call.Payload.Files[0].Contents)
	wantEq(t, 1, len(call.Payload.Secrets))
	wantEq(t, "FOO", call.Payload.Secrets[0].Key)
	wantEq(t, []byte("BAR"), call.Payload.Secrets[0].Value)
	wantEq(t, json.RawMessage([]byte(`{"foo":"bar"}`)), *call.Payload.Metadata)
}
