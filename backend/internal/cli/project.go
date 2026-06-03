package cli

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type projectAddOptions struct {
	path string
	id   string
	name string
}

type projectListOptions struct {
	json bool
}

type projectGetOptions struct {
	json bool
}

type projectRemoveOptions struct {
	json bool
	yes  bool
}

// addProjectRequest mirrors the daemon's project AddInput body for
// POST /api/v1/projects. projectId and name are optional (pointers omit them).
type addProjectRequest struct {
	Path      string  `json:"path"`
	ProjectID *string `json:"projectId,omitempty"`
	Name      *string `json:"name,omitempty"`
}

type projectSummary struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	SessionPrefix string `json:"sessionPrefix"`
	ResolveError  string `json:"resolveError,omitempty"`
}

type projectDetails struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Path           string         `json:"path"`
	Repo           string         `json:"repo"`
	DefaultBranch  string         `json:"defaultBranch"`
	DefaultHarness string         `json:"agent,omitempty"`
	Tracker        map[string]any `json:"tracker,omitempty"`
	SCM            map[string]any `json:"scm,omitempty"`
	ResolveError   string         `json:"resolveError,omitempty"`
}

type projectListResult struct {
	Projects []projectSummary `json:"projects"`
}

type projectGetResult struct {
	Status  string         `json:"status"`
	Project projectDetails `json:"project"`
}

type projectResult struct {
	Project projectDetails `json:"project"`
}

type projectRemoveResult struct {
	OK                bool   `json:"ok,omitempty"`
	ID                string `json:"id,omitempty"`
	ProjectID         string `json:"projectId,omitempty"`
	RemovedStorageDir *bool  `json:"removedStorageDir,omitempty"`
}

func newProjectCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
	}
	cmd.AddCommand(newProjectListCommand(ctx))
	cmd.AddCommand(newProjectGetCommand(ctx))
	cmd.AddCommand(newProjectAddCommand(ctx))
	cmd.AddCommand(newProjectRemoveCommand(ctx))
	return cmd
}

func newProjectListCommand(ctx *commandContext) *cobra.Command {
	var opts projectListOptions
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List registered projects",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var res projectListResult
			if err := ctx.getJSON(cmd.Context(), "projects", &res); err != nil {
				return err
			}
			sort.Slice(res.Projects, func(i, j int) bool {
				return res.Projects[i].ID < res.Projects[j].ID
			})
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeProjectList(cmd, res.Projects)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output projects as JSON")
	return cmd
}

func newProjectGetCommand(ctx *commandContext) *cobra.Command {
	var opts projectGetOptions
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Fetch one registered project",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return usageError{err}
			}
			if strings.TrimSpace(args[0]) == "" {
				return usageError{errors.New("usage: project id is required")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			var res projectGetResult
			if err := ctx.getJSON(cmd.Context(), "projects/"+url.PathEscape(id), &res); err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeProjectDetails(cmd, res)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output project as JSON")
	return cmd
}

func newProjectAddCommand(ctx *commandContext) *cobra.Command {
	var opts projectAddOptions
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Register a local git repo as a project",
		Long: "Register a local git repo as a project so sessions can be spawned in it.\n\n" +
			"The path must be an existing git repository on disk.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.path == "" {
				return usageError{fmt.Errorf("--path is required")}
			}
			req := addProjectRequest{Path: opts.path}
			if opts.id != "" {
				req.ProjectID = &opts.id
			}
			if opts.name != "" {
				req.Name = &opts.name
			}
			var res projectResult
			if err := ctx.postJSON(cmd.Context(), "projects", req, &res); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "registered project %s at %s\n", res.Project.ID, res.Project.Path)
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.path, "path", "", "Absolute path to the local git repo (required)")
	f.StringVar(&opts.id, "id", "", "Project id (default: derived by the daemon from the path)")
	f.StringVar(&opts.name, "name", "", "Display name")
	return cmd
}

func newProjectRemoveCommand(ctx *commandContext) *cobra.Command {
	var opts projectRemoveOptions
	cmd := &cobra.Command{
		Use:     "rm <id>",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove a registered project",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return usageError{err}
			}
			if strings.TrimSpace(args[0]) == "" {
				return usageError{errors.New("usage: project id is required")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if !opts.yes {
				confirmed, err := confirmProjectRemoval(cmd, id)
				if err != nil {
					return err
				}
				if !confirmed {
					_, err := fmt.Fprintln(cmd.OutOrStdout(), "aborted")
					return err
				}
			}
			var res projectRemoveResult
			if err := ctx.deleteJSON(cmd.Context(), "projects/"+url.PathEscape(id), &res); err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			removedID := res.ProjectID
			if removedID == "" {
				removedID = res.ID
			}
			if removedID == "" {
				removedID = id
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "removed project %s\n", removedID)
			return err
		},
	}
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output removal result as JSON")
	return cmd
}

func writeProjectList(cmd *cobra.Command, projects []projectSummary) error {
	out := cmd.OutOrStdout()
	if len(projects) == 0 {
		if _, err := fmt.Fprintln(out, "No projects registered."); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Run `ao project add --path <path>` to register one.")
		return err
	}

	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tNAME\tSESSION PREFIX\tSTATUS"); err != nil {
		return err
	}
	for _, p := range projects {
		status := "ok"
		if p.ResolveError != "" {
			status = "degraded: " + p.ResolveError
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, p.Name, p.SessionPrefix, status); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeProjectDetails(cmd *cobra.Command, res projectGetResult) error {
	out := cmd.OutOrStdout()
	p := res.Project
	if _, err := fmt.Fprintf(out, "Project %s (%s)\n", p.ID, res.Status); err != nil {
		return err
	}
	fields := []struct {
		label string
		value string
	}{
		{label: "name", value: p.Name},
		{label: "path", value: p.Path},
		{label: "repo", value: p.Repo},
		{label: "default branch", value: p.DefaultBranch},
		{label: "default harness", value: p.DefaultHarness},
		{label: "resolve error", value: p.ResolveError},
	}
	for _, f := range fields {
		if f.value == "" {
			continue
		}
		if _, err := fmt.Fprintf(out, "  %s: %s\n", f.label, f.value); err != nil {
			return err
		}
	}
	return nil
}

func confirmProjectRemoval(cmd *cobra.Command, id string) (bool, error) {
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Remove project %q? Type the project id to confirm: ", id); err != nil {
		return false, err
	}
	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	return strings.TrimSpace(line) == id, nil
}
