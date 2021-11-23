package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/calyptia/cloud"
	cloudclient "github.com/calyptia/cloud/client"
	"github.com/hako/durafmt"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
)

func newCmdGetAgents(config *config) *cobra.Command {
	var format string
	var projectKey string
	var last uint64
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Display latest agents from a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID := projectKey
			if !validUUID(projectID) {
				pp, err := config.cloud.Projects(config.ctx, 0)
				if err != nil {
					return err
				}

				p, ok := findProjectByName(pp, projectKey)
				if !ok {
					return fmt.Errorf("could not find project %q", projectKey)
				}

				projectID = p.ID
			}

			aa, err := config.cloud.Agents(config.ctx, projectID, last)
			if err != nil {
				return fmt.Errorf("could not fetch your agents: %w", err)
			}

			switch format {
			case "table":
				tw := table.NewWriter()
				tw.AppendHeader(table.Row{"ID", "Name", "Type", "Version", "Status", "Created at"})
				tw.Style().Options = table.OptionsNoBordersAndSeparators
				if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
					tw.SetAllowedRowLength(w)
				}

				for _, a := range aa {
					tw.AppendRow(table.Row{a.ID, a.Name, a.Type, a.Version, agentStatus(a.LastMetricsAddedAt, time.Minute*-5), a.CreatedAt.Local()})
				}
				fmt.Println(tw.Render())
			case "json":
				err := json.NewEncoder(os.Stdout).Encode(aa)
				if err != nil {
					return fmt.Errorf("could not json encode your agents: %w", err)
				}
			default:
				return fmt.Errorf("unknown output format %q", format)
			}
			return nil
		},
	}

	fs := cmd.Flags()
	fs.StringVarP(&format, "output-format", "o", "table", "Output format. Allowed: table, json")
	fs.StringVar(&projectKey, "project", "", "Parent project ID or name")
	fs.Uint64VarP(&last, "last", "l", 0, "Last `N` agents. 0 means no limit")

	_ = cmd.RegisterFlagCompletionFunc("output-format", config.completeOutputFormat)
	_ = cmd.RegisterFlagCompletionFunc("project", config.completeProjects)

	_ = cmd.MarkFlagRequired("project") // TODO: use default project ID from config cmd.

	return cmd
}

func (config *config) completeAgents(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	aa, err := config.fetchAllAgents()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	if len(aa) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return agentsKeys(aa), cobra.ShellCompDirectiveNoFileComp
}

func (config *config) fetchAllAgents() ([]cloud.Agent, error) {
	return fetchAllAgents(config.cloud, config.ctx)
}

func fetchAllAgents(client *cloudclient.Client, ctx context.Context) ([]cloud.Agent, error) {
	pp, err := client.Projects(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("could not prefetch projects: %w", err)
	}

	if len(pp) == 0 {
		return nil, nil
	}

	var aa []cloud.Agent
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for _, p := range pp {
		p := p
		g.Go(func() error {
			got, err := client.Agents(gctx, p.ID, 0)
			if err != nil {
				return fmt.Errorf("could not fetch agents from project: %w", err)
			}

			mu.Lock()
			aa = append(aa, got...)
			mu.Unlock()

			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("could not fetch projects agents: %w", err)
	}

	var uniqueAgents []cloud.Agent
	agentsIDs := map[string]struct{}{}
	for _, a := range aa {
		if _, ok := agentsIDs[a.ID]; !ok {
			uniqueAgents = append(uniqueAgents, a)
			agentsIDs[a.ID] = struct{}{}
		}
	}

	return uniqueAgents, nil
}

// agentsKeys returns unique agent names first and then IDs.
func agentsKeys(aa []cloud.Agent) []string {
	namesCount := map[string]int{}
	for _, a := range aa {
		if _, ok := namesCount[a.Name]; ok {
			namesCount[a.Name] += 1
			continue
		}

		namesCount[a.Name] = 1
	}

	var out []string

	for _, a := range aa {
		var nameIsUnique bool
		for name, count := range namesCount {
			if a.Name == name && count == 1 {
				nameIsUnique = true
				break
			}
		}
		if nameIsUnique {
			out = append(out, a.Name)
			continue
		}

		out = append(out, a.ID)
	}

	return out
}

func findAgentByName(aa []cloud.Agent, name string) (cloud.Agent, bool) {
	for _, a := range aa {
		if a.Name == name {
			return a, true
		}
	}
	return cloud.Agent{}, false
}

func agentStatus(lastMetricsAddedAt time.Time, start time.Duration) string {
	var status string
	if lastMetricsAddedAt.IsZero() {
		status = "inactive"
	} else if lastMetricsAddedAt.Before(time.Now().Add(start)) {
		status = fmt.Sprintf("inactive for %s", durafmt.ParseShort(time.Since(lastMetricsAddedAt)).LimitFirstN(1))
	} else {
		status = "active"
	}
	return status
}
